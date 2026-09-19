package probe

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// dnsReply is what the test server does with one query.
type dnsReply struct {
	rcode   dnsmessage.RCode
	answers []dnsmessage.Resource
	// truncated sets TC on UDP replies; TCP replies are never truncated.
	truncated bool
	// drop sends nothing, like a server that never answers.
	drop bool
	// delay postpones the reply.
	delay time.Duration
	// preface, when set, is sent (over UDP) before the real reply.
	preface func(id uint16) []byte
	// raw, when set, replaces the built reply.
	raw func(id uint16) []byte
}

// dnsTestServer is an in-process DNS server answering on one loopback port
// over both UDP and TCP, so tests exercise the real wire path without any
// external resolver.
type dnsTestServer struct {
	addr       string
	udpQueries atomic.Int32
	tcpQueries atomic.Int32
}

// startDNSServer serves handler on 127.0.0.1 until the test ends.
func startDNSServer(t *testing.T, handler func(q dnsmessage.Question, overTCP bool) dnsReply) *dnsTestServer {
	t.Helper()
	return startDNSServerOn(t, "127.0.0.1", handler)
}

// startDNSServerOn serves handler on the loopback address host until the
// test ends, skipping the test when that address family is unavailable.
func startDNSServerOn(t *testing.T, host string, handler func(q dnsmessage.Question, overTCP bool) dnsReply) *dnsTestServer {
	t.Helper()
	pc, ln := listenUDPAndTCP(t, host)
	srv := &dnsTestServer{addr: pc.LocalAddr().String()}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		_ = pc.Close()
		_ = ln.Close()
		wg.Wait()
	})

	wg.Go(func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			id, q, ok := parseQuery(buf[:n])
			if !ok {
				continue
			}
			srv.udpQueries.Add(1)
			reply := handler(q, false)
			if reply.drop {
				continue
			}
			time.Sleep(reply.delay)
			if reply.preface != nil {
				_, _ = pc.WriteTo(reply.preface(id), from)
			}
			_, _ = pc.WriteTo(buildReply(t, id, q, reply, false), from)
		}
	})
	wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer func() { _ = conn.Close() }()
				var length [2]byte
				if _, err := io.ReadFull(conn, length[:]); err != nil {
					return
				}
				msg := make([]byte, binary.BigEndian.Uint16(length[:]))
				if _, err := io.ReadFull(conn, msg); err != nil {
					return
				}
				id, q, ok := parseQuery(msg)
				if !ok {
					return
				}
				srv.tcpQueries.Add(1)
				reply := handler(q, true)
				if reply.drop {
					return
				}
				time.Sleep(reply.delay)
				out := buildReply(t, id, q, reply, true)
				_, _ = conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(out))), out...))
			})
		}
	})
	return srv
}

// listenUDPAndTCP opens a UDP socket and a TCP listener on the same port of
// host, retrying if the TCP side of a free UDP port happens to be taken.
func listenUDPAndTCP(t *testing.T, host string) (net.PacketConn, net.Listener) {
	t.Helper()
	for range 20 {
		pc, err := net.ListenPacket("udp", net.JoinHostPort(host, "0"))
		if err != nil {
			t.Skipf("cannot listen on udp %s: %v", host, err)
		}
		ln, err := net.Listen("tcp", pc.LocalAddr().String())
		if err == nil {
			return pc, ln
		}
		_ = pc.Close()
	}
	t.Fatalf("no port free for both udp and tcp on %s", host)
	return nil, nil
}

// parseQuery extracts the ID and question of a query message.
func parseQuery(b []byte) (uint16, dnsmessage.Question, bool) {
	var p dnsmessage.Parser
	h, err := p.Start(b)
	if err != nil || h.Response {
		return 0, dnsmessage.Question{}, false
	}
	q, err := p.Question()
	if err != nil {
		return 0, dnsmessage.Question{}, false
	}
	return h.ID, q, true
}

// buildReply packs reply as the answer to query id/q.
func buildReply(t *testing.T, id uint16, q dnsmessage.Question, reply dnsReply, overTCP bool) []byte {
	if reply.raw != nil {
		return reply.raw(id)
	}
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{
		ID: id, Response: true, Authoritative: true, RecursionDesired: true,
		RCode: reply.rcode, Truncated: reply.truncated && !overTCP,
	})
	must := func(err error) {
		if err != nil {
			t.Errorf("build reply: %v", err)
		}
	}
	must(b.StartQuestions())
	must(b.Question(q))
	must(b.StartAnswers())
	if !reply.truncated || overTCP {
		for _, rr := range reply.answers {
			switch body := rr.Body.(type) {
			case *dnsmessage.AResource:
				must(b.AResource(rr.Header, *body))
			case *dnsmessage.AAAAResource:
				must(b.AAAAResource(rr.Header, *body))
			case *dnsmessage.CNAMEResource:
				must(b.CNAMEResource(rr.Header, *body))
			case *dnsmessage.MXResource:
				must(b.MXResource(rr.Header, *body))
			case *dnsmessage.NSResource:
				must(b.NSResource(rr.Header, *body))
			case *dnsmessage.TXTResource:
				must(b.TXTResource(rr.Header, *body))
			case *dnsmessage.SOAResource:
				must(b.SOAResource(rr.Header, *body))
			default:
				t.Errorf("build reply: unsupported body %T", body)
			}
		}
	}
	out, err := b.Finish()
	must(err)
	return out
}

// record builds an IN-class answer owned by name.
func record(name string, body dnsmessage.ResourceBody) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name), Class: dnsmessage.ClassINET, TTL: 300},
		Body:   body,
	}
}

// answer replies NOERROR with answers to every query.
func answer(answers ...dnsmessage.Resource) func(dnsmessage.Question, bool) dnsReply {
	return func(dnsmessage.Question, bool) dnsReply { return dnsReply{answers: answers} }
}

// dnsMonitor builds a DNS monitor from cfg.
func dnsMonitor(t *testing.T, cfg monitor.DNSMonitorConfig, timeout time.Duration) monitor.Monitor {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return monitor.Monitor{ID: "dns-1", Type: monitor.MonitorTypeDNS, Interval: time.Minute, Timeout: timeout, Config: raw}
}

// runnerFor returns a DNSRunner whose system resolver is servers. Monitors
// with an explicit resolver ignore it.
func runnerFor(servers ...string) *DNSRunner {
	return &DNSRunner{systemServers: func() []string { return servers }}
}

// runDNS executes one check and decodes its Details, which every DNS result
// must carry (SPEC §15.3).
func runDNS(t *testing.T, ctx context.Context, r *DNSRunner, m monitor.Monitor) (Result, DNSDetails) {
	t.Helper()
	res, err := r.Run(ctx, m)
	if err != nil {
		t.Fatalf("Run: runner error %v, want a Result", err)
	}
	var d DNSDetails
	if err := json.Unmarshal(res.Details, &d); err != nil {
		t.Fatalf("decode DNS details %q: %v", res.Details, err)
	}
	return res, d
}

// TestDNSRunnerRecordTypes pins the canonical text of every supported record
// type: it is what expected values are written against and what the TUI
// shows, so a formatting change would silently break configured monitors.
func TestDNSRunnerRecordTypes(t *testing.T) {
	const owner = "example.com."
	for _, tc := range []struct {
		rtype   monitor.DNSRecordType
		answers []dnsmessage.Resource
		want    []string
	}{
		{monitor.DNSRecordA, []dnsmessage.Resource{
			record(owner, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}),
			record(owner, &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}),
		}, []string{"192.0.2.1", "192.0.2.2"}},
		{monitor.DNSRecordAAAA, []dnsmessage.Resource{
			record(owner, &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 0x01, 0x0d, 0xb8, 15: 1}}),
			// An IPv4-mapped address stays in IPv6 notation for AAAA.
			record(owner, &dnsmessage.AAAAResource{AAAA: [16]byte{10: 0xff, 11: 0xff, 12: 192, 13: 0, 14: 2, 15: 1}}),
		}, []string{"2001:db8::1", "::ffff:192.0.2.1"}},
		{monitor.DNSRecordCNAME, []dnsmessage.Resource{
			record(owner, &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("Target.Example.net.")}),
		}, []string{"Target.Example.net."}},
		{monitor.DNSRecordMX, []dnsmessage.Resource{
			record(owner, &dnsmessage.MXResource{Pref: 10, MX: dnsmessage.MustNewName("mail.example.com.")}),
			record(owner, &dnsmessage.MXResource{Pref: 20, MX: dnsmessage.MustNewName("mx2.example.com.")}),
		}, []string{"10 mail.example.com.", "20 mx2.example.com."}},
		{monitor.DNSRecordTXT, []dnsmessage.Resource{
			// Multiple character-strings are concatenated without quotes.
			record(owner, &dnsmessage.TXTResource{TXT: []string{"v=spf1 ", "include:_spf.example.com -all"}}),
			record(owner, &dnsmessage.TXTResource{TXT: []string{`quoted "value"`}}),
		}, []string{"v=spf1 include:_spf.example.com -all", `quoted "value"`}},
		{monitor.DNSRecordNS, []dnsmessage.Resource{
			record(owner, &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns1.example.com.")}),
			record(owner, &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns2.example.com.")}),
		}, []string{"ns1.example.com.", "ns2.example.com."}},
		{monitor.DNSRecordSOA, []dnsmessage.Resource{
			record(owner, &dnsmessage.SOAResource{
				NS: dnsmessage.MustNewName("ns1.example.com."), MBox: dnsmessage.MustNewName("hostmaster.example.com."),
				Serial: 2026091901, Refresh: 7200, Retry: 3600, Expire: 1209600, MinTTL: 3600,
			}),
		}, []string{"ns1.example.com. hostmaster.example.com. 2026091901 7200 3600 1209600 3600"}},
	} {
		t.Run(string(tc.rtype), func(t *testing.T) {
			var asked atomic.Value
			srv := startDNSServer(t, func(q dnsmessage.Question, _ bool) dnsReply {
				asked.Store(q)
				return dnsReply{answers: tc.answers}
			})
			m := dnsMonitor(t, monitor.DNSMonitorConfig{Name: "example.com", RecordType: tc.rtype, Resolver: srv.addr}, 2*time.Second)

			res, d := runDNS(t, context.Background(), runnerFor(), m)
			if !res.Success || res.Error != "" {
				t.Fatalf("Success = %v, Error = %q; want success", res.Success, res.Error)
			}
			if !slices.Equal(d.Records, tc.want) || d.AnswerCount != len(tc.want) {
				t.Errorf("records = %q (count %d), want %q", d.Records, d.AnswerCount, tc.want)
			}
			if d.RCode != "NOERROR" || d.Name != "example.com" || d.RecordType != string(tc.rtype) {
				t.Errorf("details = %+v, want NOERROR for example.com %s", d, tc.rtype)
			}
			q, _ := asked.Load().(dnsmessage.Question)
			if q.Name.String() != "example.com." || q.Type != dnsQueryTypes[tc.rtype] || q.Class != dnsmessage.ClassINET {
				t.Errorf("server was asked %v, want example.com. %s IN", q, tc.rtype)
			}
		})
	}
}

// TestDNSRunnerOnlyCountsRequestedType covers a CNAME chain in front of an A
// answer: only A records are the answer to an A query.
func TestDNSRunnerOnlyCountsRequestedType(t *testing.T) {
	srv := startDNSServer(t, answer(
		record("www.example.com.", &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName("web.example.net.")}),
		record("web.example.net.", &dnsmessage.AResource{A: [4]byte{198, 51, 100, 7}}),
	))
	m := dnsMonitor(t, monitor.DNSMonitorConfig{Name: "www.example.com", RecordType: monitor.DNSRecordA, Resolver: srv.addr}, 2*time.Second)
	res, d := runDNS(t, context.Background(), runnerFor(), m)
	if !res.Success || !slices.Equal(d.Records, []string{"198.51.100.7"}) || d.AnswerCount != 1 {
		t.Errorf("result %+v details %+v, want success with only the A record", res, d)
	}

	// Asking for AAAA gets the same chain but no AAAA record: a failure.
	m = dnsMonitor(t, monitor.DNSMonitorConfig{Name: "www.example.com", RecordType: monitor.DNSRecordAAAA, Resolver: srv.addr}, 2*time.Second)
	res, d = runDNS(t, context.Background(), runnerFor(), m)
	if res.Success || res.Error != "no AAAA records in answer" || d.AnswerCount != 0 || d.RCode != "NOERROR" {
		t.Errorf("result %+v details %+v, want failure: no AAAA records", res, d)
	}
}

// TestDNSRunnerResolverSelection checks both resolver modes: an explicit
// resolver is queried even when the system resolver would work, and without
// one the system nameservers are used and reported as "system".
func TestDNSRunnerResolverSelection(t *testing.T) {
	system := startDNSServer(t, answer(record("example.com.", &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}})))
	explicit := startDNSServer(t, answer(record("example.com.", &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}})))
	r := runnerFor(system.addr)

	res, d := runDNS(t, context.Background(), r, dnsMonitor(t, monitor.DNSMonitorConfig{
		Name: "example.com", RecordType: monitor.DNSRecordA, Resolver: explicit.addr,
	}, 2*time.Second))
	if !res.Success || !slices.Equal(d.Records, []string{"192.0.2.2"}) {
		t.Errorf("explicit resolver: result %+v details %+v, want the explicit server's answer", res, d)
	}
	if d.Resolver != explicit.addr || d.Server != explicit.addr {
		t.Errorf("explicit resolver: resolver/server = %q/%q, want %q", d.Resolver, d.Server, explicit.addr)
	}
	if system.udpQueries.Load() != 0 {
		t.Errorf("system resolver got %d queries, want 0 when a resolver is configured", system.udpQueries.Load())
	}

	res, d = runDNS(t, context.Background(), r, dnsMonitor(t, monitor.DNSMonitorConfig{
		Name: "example.com", RecordType: monitor.DNSRecordA,
	}, 2*time.Second))
	if !res.Success || !slices.Equal(d.Records, []string{"192.0.2.1"}) {
		t.Errorf("system resolver: result %+v details %+v, want the system server's answer", res, d)
	}
	if d.Resolver != "system" || d.Server != system.addr {
		t.Errorf("system resolver: resolver/server = %q/%q, want system/%q", d.Resolver, d.Server, system.addr)
	}
}

// TestDNSRunnerResolverWithoutPortUsesDefault checks a configured resolver
// without a port targets port 53. The check is cancelled up front so no
// query leaves the host, while the details still name the address used.
func TestDNSRunnerResolverWithoutPortUsesDefault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := dnsMonitor(t, monitor.DNSMonitorConfig{Name: "example.com", RecordType: monitor.DNSRecordSOA, Resolver: "192.0.2.53"}, time.Second)
	res, d := runDNS(t, ctx, runnerFor(), m)
	if d.Resolver != "192.0.2.53:53" {
		t.Errorf("resolver = %q, want 192.0.2.53:53", d.Resolver)
	}
	if res.Success || res.Error != "dns query: canceled" {
		t.Errorf("Success=%v Error=%q, want the cancelled check to fail", res.Success, res.Error)
	}
}

// TestDNSRunnerIPv6Resolver queries a resolver on the IPv6 loopback.
func TestDNSRunnerIPv6Resolver(t *testing.T) {
	srv := startDNSServerOn(t, "::1", answer(record("example.com.", &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 0x01, 0x0d, 0xb8, 15: 0x53}})))
	m := dnsMonitor(t, monitor.DNSMonitorConfig{Name: "example.com", RecordType: monitor.DNSRecordAAAA, Resolver: srv.addr}, 2*time.Second)
	res, d := runDNS(t, context.Background(), runnerFor(), m)
	if !res.Success || !slices.Equal(d.Records, []string{"2001:db8::53"}) || d.Server != srv.addr {
		t.Errorf("result %+v details %+v, want the AAAA answer from %s", res, d, srv.addr)
	}
}

// TestDNSRunnerTriesNextSystemServer checks the system resolver list is
// walked in order: a nameserver that refuses the query (nothing listening)
// does not fail the check while the next one answers.
func TestDNSRunnerTriesNextSystemServer(t *testing.T) {
	dead, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := dead.LocalAddr().String()
	_ = dead.Close()
	live := startDNSServer(t, answer(record("example.com.", &dnsmessage.AResource{A: [4]byte{192, 0, 2, 9}})))

	res, d := runDNS(t, context.Background(), runnerFor(deadAddr, live.addr),
		dnsMonitor(t, monitor.DNSMonitorConfig{Name: "example.com", RecordType: monitor.DNSRecordA}, 2*time.Second))
	if !res.Success || d.Server != live.addr {
		t.Errorf("result %+v details %+v, want success from the second server %s", res, d, live.addr)
	}
}

// TestDNSRunnerResponseCodeFailures covers DNS-level failures: each is a
// failed check carrying the response code, never success — a server that
// answers NXDOMAIN or SERVFAIL is not serving the zone.
func TestDNSRunnerResponseCodeFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply dnsReply
		want  string
		rcode string
	}{
		{"nxdomain", dnsReply{rcode: dnsmessage.RCodeNameError}, "response code NXDOMAIN", "NXDOMAIN"},
		{"servfail", dnsReply{rcode: dnsmessage.RCodeServerFailure}, "response code SERVFAIL", "SERVFAIL"},
		{"refused", dnsReply{rcode: dnsmessage.RCodeRefused}, "response code REFUSED", "REFUSED"},
		// An error rcode wins even if the server also sent records.
		{"servfail with answers", dnsReply{rcode: dnsmessage.RCodeServerFailure, answers: []dnsmessage.Resource{
			record("example.com.", &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}),
		}}, "response code SERVFAIL", "SERVFAIL"},
		{"empty answer", dnsReply{}, "no A records in answer", "NOERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startDNSServer(t, func(dnsmessage.Question, bool) dnsReply { return tc.reply })
			res, d := runDNS(t, context.Background(), runnerFor(),
				dnsMonitor(t, monitor.DNSMonitorConfig{Name: "example.com", RecordType: monitor.DNSRecordA, Resolver: srv.addr}, 2*time.Second))
			if res.Success || res.Error != tc.want || d.RCode != tc.rcode {
				t.Errorf("Success=%v Error=%q rcode=%q, want failure %q with rcode %s", res.Success, res.Error, d.RCode, tc.want, tc.rcode)
			}
		})
	}
}

// TestDNSRunnerTimeoutAndCancellation checks an unanswered query ends at the
// monitor timeout, and that cancelling the check (service shutdown) ends it
// immediately rather than after the timeout.
func TestDNSRunnerTimeoutAndCancellation(t *testing.T) {
	srv := startDNSServer(t, func(dnsmessage.Question, bool) dnsReply { return dnsReply{drop: true} })
	cfg := monitor.DNSMonitorConfig{Name: "example.com", RecordType: monitor.DNSRecordA, Resolver: srv.addr}

	start := time.Now()
	res, d := runDNS(t, context.Background(), runnerFor(), dnsMonitor(t, cfg, 200*time.Millisecond))
	if elapsed := time.Since(start); res.Success || res.Error != "dns query: timed out" || elapsed > 2*time.Second {
		t.Errorf("timeout: Success=%v Error=%q after %v, want %q near 200ms", res.Success, res.Error, elapsed, "dns query: timed out")
	}
	if d.Server != srv.addr || d.RCode != "" {
		t.Errorf("timeout details = %+v, want server %s and no rcode", d, srv.addr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start = time.Now()
	res, _ = runDNS(t, ctx, runnerFor(), dnsMonitor(t, cfg, 30*time.Second))
	if elapsed := time.Since(start); res.Success || res.Error != "dns query: canceled" || elapsed > 5*time.Second {
		t.Errorf("cancel: Success=%v Error=%q after %v, want %q promptly", res.Success, res.Error, elapsed, "dns query: canceled")
	}
}

// TestDNSRunnerMalformedResponse checks a reply to our query that does not
// parse is reported as malformed rather than waiting out the timeout.
func TestDNSRunnerMalformedResponse(t *testing.T) {
	srv := startDNSServer(t, func(dnsmessage.Question, bool) dnsReply {
		return dnsReply{raw: func(id uint16) []byte {
			// Header: our ID, QR set, one question — then a truncated name.
			return append(binary.BigEndian.AppendUint16(nil, id), 0x81, 0x80, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x')
		}}
	})
	start := time.Now()
	res, _ := runDNS(t, context.Background(), runnerFor(),
		dnsMonitor(t, monitor.DNSMonitorConfig{Name: "example.com", RecordType: monitor.DNSRecordA, Resolver: srv.addr}, 5*time.Second))
	if res.Success || res.Error != "dns query: malformed response" || time.Since(start) > 2*time.Second {
		t.Errorf("Success=%v Error=%q, want an immediate malformed-response failure", res.Success, res.Error)
	}
}

// TestDNSRunnerIgnoresForeignReplies checks UDP replies that do not answer
// our query — wrong ID, or right ID for a different question — are skipped
// like the Go resolver does, so a stray or spoofed packet cannot decide the
// check.
func TestDNSRunnerIgnoresForeignReplies(t *testing.T) {
	for _, tc := range []struct {
		name    string
		preface func(t *testing.T) func(id uint16) []byte
	}{
		{"wrong id", func(t *testing.T) func(uint16) []byte {
			return func(id uint16) []byte {
				return buildReply(t, id+1, dnsmessage.Question{Name: dnsmessage.MustNewName("example.com."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
					dnsReply{rcode: dnsmessage.RCodeNameError}, false)
			}
		}},
		{"wrong question", func(t *testing.T) func(uint16) []byte {
			return func(id uint16) []byte {
				return buildReply(t, id, dnsmessage.Question{Name: dnsmessage.MustNewName("evil.example."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
					dnsReply{rcode: dnsmessage.RCodeNameError}, false)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preface := tc.preface(t)
			srv := startDNSServer(t, func(dnsmessage.Question, bool) dnsReply {
				return dnsReply{preface: preface, answers: []dnsmessage.Resource{record("example.com.", &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}})}}
			})
			res, d := runDNS(t, context.Background(), runnerFor(),
				dnsMonitor(t, monitor.DNSMonitorConfig{Name: "example.com", RecordType: monitor.DNSRecordA, Resolver: srv.addr}, 2*time.Second))
			if !res.Success || d.RCode != "NOERROR" {
				t.Errorf("Success=%v Error=%q rcode=%q, want the genuine NOERROR reply", res.Success, res.Error, d.RCode)
			}
		})
	}
}

// TestDNSRunnerTruncationFallsBackToTCP checks a truncated UDP reply is
// retried over TCP and the TCP answer decides the check.
func TestDNSRunnerTruncationFallsBackToTCP(t *testing.T) {
	txt := record("example.com.", &dnsmessage.TXTResource{TXT: []string{strings.Repeat("x", 200)}})
	srv := startDNSServer(t, func(dnsmessage.Question, bool) dnsReply {
		return dnsReply{truncated: true, answers: []dnsmessage.Resource{txt, txt}}
	})
	res, d := runDNS(t, context.Background(), runnerFor(),
		dnsMonitor(t, monitor.DNSMonitorConfig{Name: "example.com", RecordType: monitor.DNSRecordTXT, Resolver: srv.addr}, 2*time.Second))
	if !res.Success || d.AnswerCount != 2 {
		t.Errorf("result %+v details %+v, want success with the 2 records from TCP", res, d)
	}
	if srv.udpQueries.Load() != 1 || srv.tcpQueries.Load() != 1 {
		t.Errorf("queries udp=%d tcp=%d, want 1 each", srv.udpQueries.Load(), srv.tcpQueries.Load())
	}
}

// TestDNSRunnerTCPFallbackSharesDeadline checks the TCP retry does not get a
// fresh timeout: a slow TCP answer fails the check at the original deadline.
func TestDNSRunnerTCPFallbackSharesDeadline(t *testing.T) {
	srv := startDNSServer(t, func(_ dnsmessage.Question, overTCP bool) dnsReply {
		if overTCP {
			return dnsReply{delay: time.Second}
		}
		return dnsReply{truncated: true, delay: 250 * time.Millisecond}
	})
	start := time.Now()
	res, _ := runDNS(t, context.Background(), runnerFor(),
		dnsMonitor(t, monitor.DNSMonitorConfig{Name: "example.com", RecordType: monitor.DNSRecordA, Resolver: srv.addr}, 400*time.Millisecond))
	elapsed := time.Since(start)
	if res.Success || res.Error != "dns query: tcp fallback: timed out" {
		t.Errorf("Success=%v Error=%q, want a tcp fallback timeout", res.Success, res.Error)
	}
	// A fresh timeout for the TCP leg would end at ~650ms (250ms UDP + 400ms).
	if elapsed > 600*time.Millisecond {
		t.Errorf("check took %v, want it bounded by the 400ms monitor timeout", elapsed)
	}
}

// TestDNSRunnerRecordCap checks Details keeps at most 10 records while the
// count and the expected-value check see every answer.
func TestDNSRunnerRecordCap(t *testing.T) {
	var answers []dnsmessage.Resource
	for i := range 12 {
		answers = append(answers, record("example.com.", &dnsmessage.AResource{A: [4]byte{192, 0, 2, byte(i + 1)}}))
	}
	srv := startDNSServer(t, answer(answers...))
	res, d := runDNS(t, context.Background(), runnerFor(), dnsMonitor(t, monitor.DNSMonitorConfig{
		Name: "example.com", RecordType: monitor.DNSRecordA, Resolver: srv.addr,
		ExpectedValue: &monitor.DNSExpectedValue{Condition: monitor.DNSCondEquals, Value: "192.0.2.12"},
	}, 2*time.Second))
	if !res.Success || d.AnswerCount != 12 || len(d.Records) != maxDNSDetailRecords {
		t.Errorf("result %+v, count %d, %d records; want success, 12, %d", res, d.AnswerCount, len(d.Records), maxDNSDetailRecords)
	}
}

// TestMatchExpected pins the SPEC §15.2.4 semantics over a multi-record
// answer: positive conditions need one matching record, negative conditions
// need none — "not_equals foo" must fail while any record equals foo.
func TestMatchExpected(t *testing.T) {
	values := []string{"foo", "bar"}
	for _, tc := range []struct {
		cond  monitor.DNSMatchCondition
		value string
		want  bool
	}{
		{monitor.DNSCondEquals, "foo", true},
		{monitor.DNSCondEquals, "bar", true},
		{monitor.DNSCondEquals, "baz", false},
		{monitor.DNSCondEquals, "Foo", false}, // case-sensitive
		{monitor.DNSCondNotEquals, "foo", false},
		{monitor.DNSCondNotEquals, "bar", false},
		{monitor.DNSCondNotEquals, "baz", true},
		{monitor.DNSCondNotEquals, "Foo", true},
		{monitor.DNSCondContains, "oo", true},
		{monitor.DNSCondContains, "OO", false},
		{monitor.DNSCondNotContains, "a", false}, // "bar" contains it
		{monitor.DNSCondNotContains, "z", true},
		{monitor.DNSCondStartsWith, "b", true},
		{monitor.DNSCondStartsWith, "o", false},
		{monitor.DNSCondNotStartsWith, "f", false},
		{monitor.DNSCondNotStartsWith, "x", true},
		{monitor.DNSCondEndsWith, "ar", true},
		{monitor.DNSCondEndsWith, "fo", false},
		{monitor.DNSCondNotEndsWith, "o", false},
		{monitor.DNSCondNotEndsWith, "q", true},
	} {
		ev := &monitor.DNSExpectedValue{Condition: tc.cond, Value: tc.value}
		if got := matchExpected(values, ev); got != tc.want {
			t.Errorf("%s %q over %q = %v, want %v", tc.cond, tc.value, values, got, tc.want)
		}
	}
	if matchExpected(values, &monitor.DNSExpectedValue{Condition: "matches", Value: "foo"}) {
		t.Error("unknown condition matched, want false")
	}
}

// TestDNSRunnerExpectedValue runs the expected-value check end to end for
// the motivating SOA case: the check passes only when the served SOA names
// the expected primary, and a negative condition fails when any record
// matches.
func TestDNSRunnerExpectedValue(t *testing.T) {
	srv := startDNSServer(t, answer(
		record("example.com.", &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns1.example.com.")}),
		record("example.com.", &dnsmessage.NSResource{NS: dnsmessage.MustNewName("ns2.example.com.")}),
	))
	for _, tc := range []struct {
		cond      monitor.DNSMatchCondition
		value     string
		wantOK    bool
		wantError string
	}{
		{monitor.DNSCondEquals, "ns2.example.com.", true, ""},
		{monitor.DNSCondEquals, "ns3.example.com.", false, `expected value check failed: equals "ns3.example.com."`},
		{monitor.DNSCondEquals, "ns1.example.com", false, `expected value check failed: equals "ns1.example.com"`},
		{monitor.DNSCondNotEquals, "ns1.example.com.", false, `expected value check failed: not_equals "ns1.example.com."`},
		{monitor.DNSCondNotContains, "ns3", true, ""},
		{monitor.DNSCondEndsWith, ".example.com.", true, ""},
	} {
		res, _ := runDNS(t, context.Background(), runnerFor(), dnsMonitor(t, monitor.DNSMonitorConfig{
			Name: "example.com", RecordType: monitor.DNSRecordNS, Resolver: srv.addr,
			ExpectedValue: &monitor.DNSExpectedValue{Condition: tc.cond, Value: tc.value},
		}, 2*time.Second))
		if res.Success != tc.wantOK || res.Error != tc.wantError {
			t.Errorf("%s %q: Success=%v Error=%q, want %v %q", tc.cond, tc.value, res.Success, res.Error, tc.wantOK, tc.wantError)
		}
	}
}

// TestDNSRunnerRejectsInvalidConfig checks configuration that escaped
// validation is a runner-level error (SPEC §15.4), not a "down" check.
func TestDNSRunnerRejectsInvalidConfig(t *testing.T) {
	for _, cfg := range []string{
		`{"name":"example.com","record_type":"ANY"}`,
		`{"name":"example.com","record_type":"AXFR"}`,
		`{"name":"example.com","record_type":"SRV"}`,
		`{"name":"bad name","record_type":"A"}`,
		`{"name":"example.com","record_type":"A","resolver":"bad resolver"}`,
		`not json`,
	} {
		m := monitor.Monitor{Type: monitor.MonitorTypeDNS, Timeout: time.Second, Config: json.RawMessage(cfg)}
		if _, err := runnerFor().Run(context.Background(), m); err == nil || !strings.Contains(err.Error(), "dns monitor config") {
			t.Errorf("config %s: err = %v, want a dns monitor config error", cfg, err)
		}
	}
}

// TestDNSQueryTypesCoverSupportedRecordTypes keeps the wire-type table in
// step with the validator: a record type that validates but has no wire
// type would be a runner error on every check.
func TestDNSQueryTypesCoverSupportedRecordTypes(t *testing.T) {
	for _, rt := range []monitor.DNSRecordType{
		monitor.DNSRecordA, monitor.DNSRecordAAAA, monitor.DNSRecordCNAME, monitor.DNSRecordMX,
		monitor.DNSRecordTXT, monitor.DNSRecordNS, monitor.DNSRecordSOA,
	} {
		if _, ok := dnsQueryTypes[rt]; !ok {
			t.Errorf("record type %s has no query type", rt)
		}
	}
}

// TestReadResolvConf checks the system nameserver list: nameserver lines in
// order (IPv6 bracketed, port 53), everything else ignored, and the Go
// resolver's localhost fallback when there are none.
func TestReadResolvConf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	conf := `# generated
search example.com
options edns0 trust-ad
nameserver 127.0.0.53
nameserver not-an-ip
; comment
nameserver 2001:db8::53
nameserver
`
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, want := readResolvConf(path), []string{"127.0.0.53:53", "[2001:db8::53]:53"}; !slices.Equal(got, want) {
		t.Errorf("readResolvConf = %q, want %q", got, want)
	}
	fallback := []string{"127.0.0.1:53", "[::1]:53"}
	if got := readResolvConf(filepath.Join(t.TempDir(), "missing")); !slices.Equal(got, fallback) {
		t.Errorf("missing file = %q, want %q", got, fallback)
	}
}

// TestDescribeDNSError checks the error causes an operator sees.
func TestDescribeDNSError(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errNoNameservers, "no nameservers configured"},
		{errMalformedResponse, "malformed response"},
		{fmt.Errorf("%w: %w", errTCPFallback, errMalformedResponse), "tcp fallback: malformed response"},
		{fmt.Errorf("%w: %w", errTCPFallback, io.EOF), "tcp fallback: connection closed"},
		{errors.New("mystery"), "network error"},
	} {
		if got := describeDNSError(ctx, tc.err); got != tc.want {
			t.Errorf("describeDNSError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
