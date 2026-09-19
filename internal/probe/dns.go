package probe

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// DNSRunner executes DNS monitor probes (SPEC §15.2.4). Each check sends one
// query for the configured name and record type to the configured resolver,
// or to the system nameservers when none is set, and classifies the response:
// success requires a NOERROR reply carrying at least one answer of the queried
// type and, when configured, a passing expected-value check.
//
// Queries go out over UDP; a truncated reply is retried over TCP against the
// same server. Every leg shares the single monitor deadline.
type DNSRunner struct {
	// systemServers lists the "ip:port" nameservers queried, in order, when a
	// monitor has no explicit resolver. Tests replace it to reach a local
	// server.
	systemServers func() []string
}

// NewDNSRunner returns a DNSRunner whose system resolver is the nameserver
// list in /etc/resolv.conf.
func NewDNSRunner() *DNSRunner {
	return &DNSRunner{systemServers: func() []string { return readResolvConf(resolvConfPath) }}
}

// Type reports that this runner handles DNS monitors.
func (r *DNSRunner) Type() monitor.MonitorType { return monitor.MonitorTypeDNS }

// dnsQueryTypes maps each supported record type to its wire type.
var dnsQueryTypes = map[monitor.DNSRecordType]dnsmessage.Type{
	monitor.DNSRecordA:     dnsmessage.TypeA,
	monitor.DNSRecordAAAA:  dnsmessage.TypeAAAA,
	monitor.DNSRecordCNAME: dnsmessage.TypeCNAME,
	monitor.DNSRecordMX:    dnsmessage.TypeMX,
	monitor.DNSRecordTXT:   dnsmessage.TypeTXT,
	monitor.DNSRecordNS:    dnsmessage.TypeNS,
	monitor.DNSRecordSOA:   dnsmessage.TypeSOA,
}

// ednsUDPSize is the UDP payload size advertised via EDNS(0), the value the
// Go resolver also uses; larger answers come back truncated and are retried
// over TCP.
const ednsUDPSize = 1232

var (
	// errMalformedResponse reports a reply to our query that cannot be
	// parsed, or a TCP reply that does not answer the question asked.
	errMalformedResponse = errors.New("malformed response")
	// errNoNameservers reports an empty system nameserver list.
	errNoNameservers = errors.New("no nameservers configured")
	// errTCPFallback marks a failure of the TCP retry after a truncated UDP
	// reply.
	errTCPFallback = errors.New("tcp fallback")
)

// Run executes one DNS check for m. Query failures and unsatisfied success
// criteria are failed Results; the error return is reserved for
// configuration that escaped validation (SPEC §15.4).
func (r *DNSRunner) Run(ctx context.Context, m monitor.Monitor) (Result, error) {
	var cfg monitor.DNSMonitorConfig
	if err := json.Unmarshal(m.Config, &cfg); err != nil {
		return Result{}, fmt.Errorf("decode dns monitor config: %w", err)
	}
	if err := monitor.ValidateDNSConfig(&cfg); err != nil {
		return Result{}, fmt.Errorf("validate dns monitor config: %w", err)
	}
	qtype, ok := dnsQueryTypes[cfg.RecordType]
	if !ok {
		return Result{}, fmt.Errorf("dns monitor: no query type for record type %q", cfg.RecordType)
	}
	qname, err := dnsmessage.NewName(fqdn(cfg.Name))
	if err != nil {
		return Result{}, fmt.Errorf("dns monitor: query name: %w", err)
	}
	q := dnsmessage.Question{Name: qname, Type: qtype, Class: dnsmessage.ClassINET}
	id := uint16(rand.Uint32())
	query, err := buildQuery(id, q)
	if err != nil {
		return Result{}, fmt.Errorf("dns monitor: build query: %w", err)
	}

	details := DNSDetails{Name: cfg.Name, RecordType: string(cfg.RecordType), Resolver: "system"}
	var servers []string
	if cfg.Resolver != "" {
		addr, err := monitor.ResolverAddress(cfg.Resolver)
		if err != nil {
			return Result{}, fmt.Errorf("dns monitor: resolver: %w", err)
		}
		details.Resolver = addr
		servers = []string{addr}
	} else {
		servers = r.systemServers()
	}

	runCtx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()

	started := time.Now()
	resp, server, err := queryServers(runCtx, servers, id, q, query)
	finished := time.Now()
	res := Result{
		StartedAt:  started,
		FinishedAt: finished,
		Duration:   finished.Sub(started),
	}
	details.Server = server
	if err != nil {
		res.Error = "dns query: " + describeDNSError(runCtx, err)
		res.Details = marshalDetails(details)
		return res, nil
	}

	values := answerValues(resp.Answers, qtype)
	details.RCode = rcodeName(resp.RCode)
	details.AnswerCount = len(values)
	details.Records = values[:min(len(values), maxDNSDetailRecords)]
	res.Details = marshalDetails(details)
	switch {
	case resp.RCode != dnsmessage.RCodeSuccess:
		res.Error = "response code " + details.RCode
	case len(values) == 0:
		res.Error = fmt.Sprintf("no %s records in answer", cfg.RecordType)
	case cfg.ExpectedValue != nil && !matchExpected(values, cfg.ExpectedValue):
		res.Error = fmt.Sprintf("expected value check failed: %s %q", cfg.ExpectedValue.Condition, cfg.ExpectedValue.Value)
	default:
		res.Success = true
	}
	return res, nil
}

// fqdn makes name absolute, which the wire format requires. Monitors always
// query the configured name itself; resolv.conf search domains never apply.
func fqdn(name string) string {
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

// buildQuery packs a recursion-desired query for q with an EDNS(0) record
// advertising ednsUDPSize.
func buildQuery(id uint16, q dnsmessage.Question) ([]byte, error) {
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: id, RecursionDesired: true})
	b.EnableCompression()
	if err := b.StartQuestions(); err != nil {
		return nil, err
	}
	if err := b.Question(q); err != nil {
		return nil, err
	}
	if err := b.StartAdditionals(); err != nil {
		return nil, err
	}
	var opt dnsmessage.ResourceHeader
	if err := opt.SetEDNS0(ednsUDPSize, dnsmessage.RCodeSuccess, false); err != nil {
		return nil, err
	}
	if err := b.OPTResource(opt, dnsmessage.OPTResource{}); err != nil {
		return nil, err
	}
	return b.Finish()
}

// queryServers sends the query to each server in turn until one replies. A
// reply with any response code ends the search. Each attempt gets an equal
// share of the time left before ctx's deadline — remaining time divided by
// the servers still to try — so a silent nameserver cannot use up the budget
// of those after it; the last attempt, and so the only attempt for an
// explicit resolver, gets all that is left. Shares are derived from ctx, so
// they never extend the monitor deadline and cancelling ctx ends any attempt
// at once. It returns the reply, the "ip:port" last queried, and the last
// error when no server replied.
func queryServers(ctx context.Context, servers []string, id uint16, q dnsmessage.Question, query []byte) (dnsmessage.Message, string, error) {
	if len(servers) == 0 {
		return dnsmessage.Message{}, "", errNoNameservers
	}
	var (
		lastAddr string
		lastErr  error
	)
	for i, server := range servers {
		attemptCtx, cancel := attemptContext(ctx, len(servers)-i)
		resp, addr, err := exchange(attemptCtx, server, id, q, query)
		cancel()
		if err == nil {
			return resp, addr, nil
		}
		lastAddr, lastErr = addr, err
		if ctx.Err() != nil {
			break
		}
	}
	return dnsmessage.Message{}, lastAddr, lastErr
}

// attemptContext bounds one server attempt when remaining servers, this one
// included, are left to try: it gets an equal share of the time until ctx's
// deadline. A lone remaining server keeps ctx's own deadline.
func attemptContext(ctx context.Context, remaining int) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok || remaining <= 1 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, time.Until(deadline)/time.Duration(remaining))
}

// exchange performs the query against one server over UDP and, when the
// reply is truncated, repeats it over TCP to the same address; both legs
// share ctx, the server's attempt budget. It returns the "ip:port" actually
// queried (empty if the server could not be dialled).
func exchange(ctx context.Context, server string, id uint16, q dnsmessage.Question, query []byte) (dnsmessage.Message, string, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", server)
	if err != nil {
		return dnsmessage.Message{}, "", err
	}
	addr := conn.RemoteAddr().String()
	resp, err := roundTripUDP(ctx, conn, id, q, query)
	_ = conn.Close()
	if err != nil || !resp.Truncated {
		return resp, addr, err
	}

	// Dial the resolved address rather than the configured name so the TCP
	// retry reaches the server that sent the truncated reply.
	conn, err = d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return dnsmessage.Message{}, addr, fmt.Errorf("%w: %w", errTCPFallback, err)
	}
	defer func() { _ = conn.Close() }()
	resp, err = roundTripTCP(ctx, conn, id, q, query)
	if err != nil {
		return dnsmessage.Message{}, addr, fmt.Errorf("%w: %w", errTCPFallback, err)
	}
	return resp, addr, nil
}

// roundTripUDP writes the query and reads datagrams until one answers it.
// Datagrams that are not a reply to this query (wrong ID or question, or not
// even a DNS header) are ignored as the Go resolver does, since they may be
// stale or forged; the read deadline bounds the wait.
func roundTripUDP(ctx context.Context, conn net.Conn, id uint16, q dnsmessage.Question, query []byte) (dnsmessage.Message, error) {
	stop := bindDeadline(ctx, conn)
	defer stop()
	if _, err := conn.Write(query); err != nil {
		return dnsmessage.Message{}, err
	}
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return dnsmessage.Message{}, err
		}
		resp, ok, err := parseResponse(buf[:n], id, q)
		if err != nil {
			return dnsmessage.Message{}, err
		}
		if ok {
			return resp, nil
		}
	}
}

// roundTripTCP writes the length-prefixed query and reads one
// length-prefixed reply (RFC 1035 §4.2.2). On a stream only the server can
// answer, so a reply that is not for this query is malformed.
func roundTripTCP(ctx context.Context, conn net.Conn, id uint16, q dnsmessage.Question, query []byte) (dnsmessage.Message, error) {
	stop := bindDeadline(ctx, conn)
	defer stop()
	msg := binary.BigEndian.AppendUint16(make([]byte, 0, 2+len(query)), uint16(len(query)))
	if _, err := conn.Write(append(msg, query...)); err != nil {
		return dnsmessage.Message{}, err
	}
	var length [2]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return dnsmessage.Message{}, err
	}
	buf := make([]byte, binary.BigEndian.Uint16(length[:]))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return dnsmessage.Message{}, err
	}
	resp, ok, err := parseResponse(buf, id, q)
	if err != nil {
		return dnsmessage.Message{}, err
	}
	if !ok {
		return dnsmessage.Message{}, errMalformedResponse
	}
	return resp, nil
}

// bindDeadline makes I/O on conn fail once ctx is done: the context deadline
// is applied directly, and cancellation (which has no deadline) moves the
// deadline into the past. The returned func releases the cancellation hook.
func bindDeadline(ctx context.Context, conn net.Conn) func() bool {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	return context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
}

// parseResponse decodes b as a reply to the query with the given ID and
// question. ok is false when b is not such a reply at all. A reply with the
// right ID whose body does not parse is errMalformedResponse. A reply with no
// question section is accepted, since some servers omit it from REFUSED and
// FORMERR replies. Answers of a truncated reply are not required to parse:
// the query is repeated over TCP.
func parseResponse(b []byte, id uint16, q dnsmessage.Question) (dnsmessage.Message, bool, error) {
	var p dnsmessage.Parser
	h, err := p.Start(b)
	if err != nil || !h.Response || h.ID != id {
		return dnsmessage.Message{}, false, nil
	}
	questions, err := p.AllQuestions()
	if err != nil {
		return dnsmessage.Message{}, false, errMalformedResponse
	}
	if len(questions) > 0 && !sameQuestion(questions[0], q) {
		return dnsmessage.Message{}, false, nil
	}
	resp := dnsmessage.Message{Header: h, Questions: questions}
	if resp.Answers, err = p.AllAnswers(); err != nil {
		if h.Truncated {
			return dnsmessage.Message{Header: h, Questions: questions}, true, nil
		}
		return dnsmessage.Message{}, false, errMalformedResponse
	}
	return resp, true, nil
}

// sameQuestion reports whether a reply's question matches the one asked.
// Names compare case-insensitively, as DNS names do.
func sameQuestion(a, b dnsmessage.Question) bool {
	return a.Type == b.Type && a.Class == b.Class && strings.EqualFold(a.Name.String(), b.Name.String())
}

// answerValues returns the canonical text of every answer of type t, in
// answer order. Records of other types, such as the CNAME chain in front of
// an A answer, are skipped.
func answerValues(answers []dnsmessage.Resource, t dnsmessage.Type) []string {
	var values []string
	for _, rr := range answers {
		if rr.Header.Type != t || rr.Header.Class != dnsmessage.ClassINET {
			continue
		}
		if v, ok := recordText(rr.Body); ok {
			values = append(values, v)
		}
	}
	return values
}

// recordText renders a record in the canonical text form that expected-value
// checks compare against and DNSDetails reports (SPEC §15.2.4): zone-file
// presentation with names fully qualified (trailing dot) and their case kept
// as received; IPv6 in RFC 5952 form; TXT as its character-strings
// concatenated without quotes or separators.
func recordText(body dnsmessage.ResourceBody) (string, bool) {
	switch rr := body.(type) {
	case *dnsmessage.AResource:
		return netip.AddrFrom4(rr.A).String(), true
	case *dnsmessage.AAAAResource:
		return netip.AddrFrom16(rr.AAAA).String(), true
	case *dnsmessage.CNAMEResource:
		return rr.CNAME.String(), true
	case *dnsmessage.NSResource:
		return rr.NS.String(), true
	case *dnsmessage.MXResource:
		return fmt.Sprintf("%d %s", rr.Pref, rr.MX.String()), true
	case *dnsmessage.TXTResource:
		return strings.Join(rr.TXT, ""), true
	case *dnsmessage.SOAResource:
		return fmt.Sprintf("%s %s %d %d %d %d %d",
			rr.NS.String(), rr.MBox.String(), rr.Serial, rr.Refresh, rr.Retry, rr.Expire, rr.MinTTL), true
	}
	return "", false
}

// matchExpected evaluates the expected-value check (SPEC §15.2.4) with
// case-sensitive byte comparisons. A positive condition passes when at least
// one value satisfies it; a negative condition passes only when no value
// satisfies its positive form.
func matchExpected(values []string, ev *monitor.DNSExpectedValue) bool {
	positive, negated := conditionTest(ev.Condition)
	if positive == nil {
		return false
	}
	matched := slices.ContainsFunc(values, func(v string) bool { return positive(v, ev.Value) })
	return matched != negated
}

// conditionTest returns the positive test behind a match condition and
// whether the condition negates it. The test is nil for an unknown condition.
func conditionTest(c monitor.DNSMatchCondition) (test func(value, want string) bool, negated bool) {
	switch c {
	case monitor.DNSCondEquals, monitor.DNSCondNotEquals:
		test = func(value, want string) bool { return value == want }
	case monitor.DNSCondContains, monitor.DNSCondNotContains:
		test = strings.Contains
	case monitor.DNSCondStartsWith, monitor.DNSCondNotStartsWith:
		test = strings.HasPrefix
	case monitor.DNSCondEndsWith, monitor.DNSCondNotEndsWith:
		test = strings.HasSuffix
	default:
		return nil, false
	}
	switch c {
	case monitor.DNSCondNotEquals, monitor.DNSCondNotContains, monitor.DNSCondNotStartsWith, monitor.DNSCondNotEndsWith:
		negated = true
	}
	return test, negated
}

// rcodeNames holds the RFC 1035/2136 mnemonics of the response codes a
// header can carry.
var rcodeNames = map[dnsmessage.RCode]string{
	0: "NOERROR", 1: "FORMERR", 2: "SERVFAIL", 3: "NXDOMAIN", 4: "NOTIMP", 5: "REFUSED",
	6: "YXDOMAIN", 7: "YXRRSET", 8: "NXRRSET", 9: "NOTAUTH", 10: "NOTZONE",
}

// rcodeName returns the mnemonic for c, or "RCODE<n>" for unassigned codes.
func rcodeName(c dnsmessage.RCode) string {
	if name, ok := rcodeNames[c]; ok {
		return name
	}
	return fmt.Sprintf("RCODE%d", c)
}

// describeDNSError reduces a query failure to a short cause for
// Result.Error.
func describeDNSError(ctx context.Context, err error) string {
	var cause string
	switch {
	case errors.Is(err, errNoNameservers):
		return "no nameservers configured"
	case errors.Is(err, errMalformedResponse):
		cause = "malformed response"
	default:
		cause = describeNetError(ctx, err)
	}
	if errors.Is(err, errTCPFallback) {
		return "tcp fallback: " + cause
	}
	return cause
}

// resolvConfPath is the system resolver configuration consulted when a DNS
// monitor has no explicit resolver.
const resolvConfPath = "/etc/resolv.conf"

// readResolvConf returns the nameservers listed in a resolv.conf file as
// "ip:port" addresses, in file order. Like the Go resolver it falls back to
// the local host when the file is missing or lists no nameserver. Search
// domains and options are ignored because monitors query absolute names.
func readResolvConf(path string) []string {
	var servers []string
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 || fields[0] != "nameserver" {
				continue
			}
			if _, err := netip.ParseAddr(fields[1]); err == nil {
				servers = append(servers, net.JoinHostPort(fields[1], "53"))
			}
		}
		_ = f.Close()
	}
	if len(servers) == 0 {
		return []string{"127.0.0.1:53", "[::1]:53"}
	}
	return servers
}
