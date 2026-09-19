package app_test

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/deicod/uptimemonitor/internal/app"
	"github.com/deicod/uptimemonitor/internal/config"
	"github.com/deicod/uptimemonitor/internal/ipc"
	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/notify/providers/fake"
)

// authServer is a minimal authoritative name server for one zone: it answers
// SOA queries over UDP, or SERVFAIL while failing is set.
type authServer struct {
	pc      net.PacketConn
	failing atomic.Bool
}

func startAuthServer(t *testing.T) *authServer {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	s := &authServer{pc: pc}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.serve()
	}()
	t.Cleanup(func() {
		_ = pc.Close()
		<-done
	})
	return s
}

func (s *authServer) serve() {
	buf := make([]byte, 512)
	for {
		n, from, err := s.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		var p dnsmessage.Parser
		h, err := p.Start(buf[:n])
		if err != nil {
			continue
		}
		q, err := p.Question()
		if err != nil {
			continue
		}
		rcode := dnsmessage.RCodeSuccess
		if s.failing.Load() {
			rcode = dnsmessage.RCodeServerFailure
		}
		b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RCode: rcode})
		_ = b.StartQuestions()
		_ = b.Question(q)
		_ = b.StartAnswers()
		if rcode == dnsmessage.RCodeSuccess && q.Type == dnsmessage.TypeSOA {
			_ = b.SOAResource(dnsmessage.ResourceHeader{Name: q.Name, Class: dnsmessage.ClassINET, TTL: 3600}, dnsmessage.SOAResource{
				NS: dnsmessage.MustNewName("ns1.dysv.de."), MBox: dnsmessage.MustNewName("hostmaster.dysv.de."),
				Serial: 2026091901, Refresh: 7200, Retry: 3600, Expire: 1209600, MinTTL: 3600,
			})
		}
		out, err := b.Finish()
		if err != nil {
			continue
		}
		_, _ = s.pc.WriteTo(out, from)
	}
}

// startTCPTarget listens on addr and hangs up on every connection, like an
// SSH daemon as far as a connect check can tell.
func startTCPTarget(t *testing.T, addr string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen tcp %s: %v", addr, err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// TestEndToEndTCPAndDNSServerReboot runs the motivating deployment against
// the real service: a name server's SSH port (TCP) and its authoritative SOA
// (DNS) are monitored; the server "reboots" (port closed, zone SERVFAIL),
// both monitors go down with incidents and notifications, and both recover
// once it is back. The configuration and check history survive a service
// restart.
func TestEndToEndTCPAndDNSServerReboot(t *testing.T) {
	cfg := testConfig(t)
	cfg.Notifications = config.NotificationConfig{
		Enabled: true, MaxAttempts: 3,
		InitialRetryDelay: 10 * time.Millisecond, MaxRetryDelay: 20 * time.Millisecond,
	}
	fakeProv := fake.New()
	stop := runService(t, cfg, app.WithProviders(fakeProv))
	client := ipc.NewClient(cfg.SocketPath)
	waitForStatus(t, client)
	ctx := context.Background()

	ssh := startTCPTarget(t, "127.0.0.1:0")
	sshAddr := ssh.Addr().String()
	_, sshPort, _ := net.SplitHostPort(sshAddr)
	dns := startAuthServer(t)

	if _, err := client.CreateNotificationTarget(ctx, ipc.CreateNotificationTargetRequest{Name: "Fake", Kind: "fake", Enabled: true}); err != nil {
		t.Fatalf("CreateNotificationTarget: %v", err)
	}
	create := func(name, typ, config string) string {
		t.Helper()
		m, err := client.CreateMonitor(ctx, ipc.CreateMonitorRequest{
			Name: name, Type: typ, Enabled: true, NotificationsEnabled: true,
			Interval: ipc.Duration(time.Hour), Timeout: ipc.Duration(2 * time.Second), Config: json.RawMessage(config),
		})
		if err != nil {
			t.Fatalf("CreateMonitor %s: %v", name, err)
		}
		return m.ID
	}
	sshID := create("ns1 SSH", "tcp", `{"host":"127.0.0.1","port":`+sshPort+`}`)
	dnsConfig := `{"name":"dysv.de","record_type":"SOA","resolver":"` + dns.pc.LocalAddr().String() + `",` +
		`"expected_value":{"condition":"starts_with","value":"ns1.dysv.de. "}}`
	dnsID := create("ns1 DNS authoritative", "dns", dnsConfig)

	checkBoth := func(want string) {
		t.Helper()
		for _, id := range []string{sshID, dnsID} {
			triggerManualCheck(t, client, id)
			waitForLatestCheck(t, client, id, want)
		}
	}

	// Healthy: both up, with type-specific details recorded.
	checkBoth("up")
	assertLatestDetails(t, client, sshID, `"remote_addr":"`+sshAddr+`"`)
	assertLatestDetails(t, client, dnsID, `"rcode":"NOERROR"`, `ns1.dysv.de. hostmaster.dysv.de. 2026091901 7200 3600 1209600 3600`)

	// Reboot: SSH refuses connections and the zone is not served yet.
	_ = ssh.Close()
	dns.failing.Store(true)
	checkBoth("down")
	for id, reason := range map[string]string{sshID: "connect: connection refused", dnsID: "response code SERVFAIL"} {
		incidents, err := client.ListMonitorIncidents(ctx, id)
		if err != nil || len(incidents) != 1 || incidents[0].ResolvedAt != nil || incidents[0].Reason != reason {
			t.Fatalf("incidents for %s = %+v (%v), want one open incident %q", id, incidents, err, reason)
		}
		waitForSend(t, fakeProv, notify.EventMonitorDown, id)
	}

	// Back up: both recover and their incidents close.
	startTCPTarget(t, sshAddr)
	dns.failing.Store(false)
	checkBoth("up")
	for _, id := range []string{sshID, dnsID} {
		incidents, err := client.ListMonitorIncidents(ctx, id)
		if err != nil || len(incidents) != 1 || incidents[0].ResolvedAt == nil {
			t.Fatalf("incidents for %s = %+v (%v), want the incident resolved", id, incidents, err)
		}
		waitForSend(t, fakeProv, notify.EventMonitorRecovered, id)

		// The outage is in the TSDB-backed history, not just SQLite.
		h, err := client.History(ctx, id, "1h")
		if err != nil {
			t.Fatalf("History %s: %v", id, err)
		}
		sawFailure := false
		for _, p := range h.Points {
			sawFailure = sawFailure || p.SuccessRatio < 1
		}
		if len(h.Points) == 0 || !sawFailure {
			t.Errorf("history for %s = %+v, want buckets recording the failed check", id, h.Points)
		}
	}

	// Restart: configuration and check history are read back intact.
	stop()
	stop = runService(t, cfg, app.WithProviders(fakeProv))
	defer stop()
	waitForStatus(t, client)
	got, err := client.GetMonitor(ctx, dnsID)
	if err != nil || got.Type != "dns" || !jsonEqual(t, got.Config, json.RawMessage(dnsConfig)) {
		t.Fatalf("after restart GetMonitor = %+v (%v), want the DNS config %s", got, err, dnsConfig)
	}
	checks, err := client.RecentChecks(ctx, dnsID, 10)
	if err != nil || len(checks) < 3 || checks[0].State != "up" {
		t.Fatalf("after restart RecentChecks = %d rows (%v), want the up/down/up history", len(checks), err)
	}
	assertLatestDetails(t, client, dnsID, `"record_type":"SOA"`)
}

// runService starts the service and returns a func that stops it and waits
// for a clean exit.
func runService(t *testing.T, cfg *config.Config, opts ...app.Option) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, cfg, opts...) }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("service exited with error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("service did not shut down within timeout")
		}
	}
	t.Cleanup(stop)
	return stop
}

// assertLatestDetails checks the newest check's details contain each want.
func assertLatestDetails(t *testing.T, c *ipc.Client, id string, wants ...string) {
	t.Helper()
	checks, err := c.RecentChecks(context.Background(), id, 1)
	if err != nil || len(checks) == 0 {
		t.Fatalf("RecentChecks(%s) = %v, %v", id, checks, err)
	}
	for _, want := range wants {
		if !strings.Contains(string(checks[0].Details), want) {
			t.Errorf("latest details for %s = %s, want %s", id, checks[0].Details, want)
		}
	}
}

// waitForSend waits until the fake provider delivered eventType for id.
func waitForSend(t *testing.T, p *fake.Provider, eventType, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range p.Sends() {
			if s.Message.EventType == eventType && s.Message.MonitorID == id {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %s notification for monitor %s (%d sends)", eventType, id, len(p.Sends()))
}

// jsonEqual reports whether a and b encode the same JSON value.
func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	ja, _ := json.Marshal(va)
	jb, _ := json.Marshal(vb)
	return string(ja) == string(jb)
}
