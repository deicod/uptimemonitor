package probe_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/probe"
)

// tcpMonitor builds a TCP monitor for addr ("host:port") with the given
// timeout.
func tcpMonitor(t *testing.T, addr string, timeout time.Duration) monitor.Monitor {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("port %q: %v", portStr, err)
	}
	cfg, err := json.Marshal(monitor.TCPMonitorConfig{Host: host, Port: port})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return monitor.Monitor{ID: "tcp-1", Type: monitor.MonitorTypeTCP, Timeout: timeout, Interval: time.Minute, Config: cfg}
}

// tcpDetails decodes a TCP result's Details, failing the test when the
// payload is missing: every TCP result must carry one (SPEC §15.3).
func tcpDetails(t *testing.T, res probe.Result) probe.TCPDetails {
	t.Helper()
	var d probe.TCPDetails
	if err := json.Unmarshal(res.Details, &d); err != nil {
		t.Fatalf("decode TCP details %q: %v", res.Details, err)
	}
	return d
}

// listen opens a TCP listener on addr, skipping the test when the address
// family is unavailable (e.g. no IPv6 loopback in a container).
func listen(t *testing.T, addr string) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("cannot listen on %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln
}

// TestTCPRunnerConnects covers the success path for each address form an
// operator can configure — IPv4, IPv6, and a host name — and checks the
// connected address is recorded so the TUI can show which endpoint answered.
func TestTCPRunnerConnects(t *testing.T) {
	for _, tc := range []struct{ name, listenAddr string }{
		{"ipv4", "127.0.0.1:0"},
		{"ipv6", "[::1]:0"},
		{"hostname", "localhost:0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln := listen(t, tc.listenAddr)
			_, port, _ := net.SplitHostPort(ln.Addr().String())
			host, _, _ := net.SplitHostPort(tc.listenAddr)
			m := tcpMonitor(t, net.JoinHostPort(host, port), 2*time.Second)

			res, err := probe.NewTCPRunner().Run(context.Background(), m)
			if err != nil {
				t.Fatalf("Run: unexpected runner error %v", err)
			}
			if !res.Success || res.Error != "" {
				t.Fatalf("Success = %v, Error = %q; want a successful connect", res.Success, res.Error)
			}
			if got := tcpDetails(t, res).RemoteAddr; got != ln.Addr().String() {
				t.Errorf("remote_addr = %q, want the listener address %q", got, ln.Addr().String())
			}
			if res.Duration <= 0 || res.FinishedAt.Before(res.StartedAt) {
				t.Errorf("timing not recorded: duration %v, started %v, finished %v", res.Duration, res.StartedAt, res.FinishedAt)
			}
		})
	}
}

// TestTCPRunnerClosesConnection verifies the probe hangs up right after the
// connect: a monitor that leaked sockets would exhaust the target's (or our
// own) descriptors over days of checks.
func TestTCPRunnerClosesConnection(t *testing.T) {
	ln := listen(t, "127.0.0.1:0")
	accepted := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			accepted <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err = conn.Read(make([]byte, 1))
		accepted <- err
	}()

	res, err := probe.NewTCPRunner().Run(context.Background(), tcpMonitor(t, ln.Addr().String(), 2*time.Second))
	if err != nil || !res.Success {
		t.Fatalf("Run = %+v, %v; want success", res, err)
	}
	if err := <-accepted; !errors.Is(err, io.EOF) {
		t.Errorf("server read = %v, want io.EOF (probe closed the connection without sending data)", err)
	}
}

// TestTCPRunnerFailures covers the per-check failures: each must be a failed
// Result (driving the state machine), never a runner error, with a cause the
// operator can act on and no remote address.
func TestTCPRunnerFailures(t *testing.T) {
	// A port that was just released refuses connections.
	ln := listen(t, "127.0.0.1:0")
	closedAddr := ln.Addr().String()
	_ = ln.Close()

	open := listen(t, "127.0.0.1:0")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name    string
		ctx     context.Context
		addr    string
		timeout time.Duration
		want    string
	}{
		{"refused", context.Background(), closedAddr, 2 * time.Second, "connect: connection refused"},
		// A deadline that has already passed must fail even against a live
		// listener: the monitor timeout bounds the connect.
		{"timeout", context.Background(), open.Addr().String(), time.Nanosecond, "connect: timed out"},
		{"canceled", canceled, open.Addr().String(), 2 * time.Second, "connect: canceled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := probe.NewTCPRunner().Run(tc.ctx, tcpMonitor(t, tc.addr, tc.timeout))
			if err != nil {
				t.Fatalf("Run: runner error %v, want a failed Result", err)
			}
			if res.Success {
				t.Fatal("Success = true, want false")
			}
			if res.Error != tc.want {
				t.Errorf("Error = %q, want %q", res.Error, tc.want)
			}
			if got := tcpDetails(t, res).RemoteAddr; got != "" {
				t.Errorf("remote_addr = %q, want empty when no connection was made", got)
			}
		})
	}
}

// TestTCPRunnerRejectsInvalidConfig checks that configuration which escaped
// validation is a runner-level error (SPEC §15.4), not a "down" check.
func TestTCPRunnerRejectsInvalidConfig(t *testing.T) {
	for _, cfg := range []string{
		`{"host":"example.com","port":0}`,
		`{"host":"","port":22}`,
		`{"host":"example.com:22","port":22}`,
		`not json`,
	} {
		m := monitor.Monitor{Type: monitor.MonitorTypeTCP, Timeout: time.Second, Config: json.RawMessage(cfg)}
		if _, err := probe.NewTCPRunner().Run(context.Background(), m); err == nil || !strings.Contains(err.Error(), "tcp monitor config") {
			t.Errorf("config %s: err = %v, want a tcp monitor config error", cfg, err)
		}
	}
}

// TestNewDispatcherRoutesEachType pins the runner registry (SPEC §15.2): TCP
// and DNS monitors reach their own runners, and a type without a runner
// (ICMP ping, not implemented yet) fails loudly instead of being probed by
// some other runner.
func TestNewDispatcherRoutesEachType(t *testing.T) {
	d := probe.NewDispatcher()
	ln := listen(t, "127.0.0.1:0")

	cr, err := d.Dispatch(context.Background(), tcpMonitor(t, ln.Addr().String(), 2*time.Second))
	if err != nil || !cr.Success {
		t.Errorf("tcp dispatch = %+v, %v; want a successful TCP check", cr, err)
	}
	if !strings.Contains(string(cr.Details), `"remote_addr"`) {
		t.Errorf("tcp dispatch details = %s, want TCP details", cr.Details)
	}

	// A config the DNS validator rejects proves the monitor reached the DNS
	// runner without needing a DNS server.
	dnsMon := monitor.Monitor{Type: monitor.MonitorTypeDNS, Timeout: time.Second, Config: json.RawMessage(`{"name":"example.com","record_type":"ANY"}`)}
	if _, err := d.Dispatch(context.Background(), dnsMon); err == nil || !strings.Contains(err.Error(), "dns monitor config") {
		t.Errorf("dns dispatch err = %v, want the DNS runner's config error", err)
	}

	pingMon := monitor.Monitor{Type: monitor.MonitorTypePing, Timeout: time.Second, Config: json.RawMessage(`{"host":"127.0.0.1"}`)}
	if _, err := d.Dispatch(context.Background(), pingMon); err == nil || !strings.Contains(err.Error(), "no runner registered") {
		t.Errorf("ping dispatch err = %v, want no runner registered", err)
	}
}

// TestValidationAcceptsOnlyRunnableTypes keeps monitor validation and the
// runner registry in step: a monitor type is creatable exactly when
// NewDispatcher has a runner for it. Otherwise the API would accept monitors
// that fail every check (ping, until its runner exists) or refuse ones that
// could run.
func TestValidationAcceptsOnlyRunnableTypes(t *testing.T) {
	d := probe.NewDispatcher()
	for _, typ := range []monitor.MonitorType{
		monitor.MonitorTypeHTTP, monitor.MonitorTypeTCP, monitor.MonitorTypePing, monitor.MonitorTypeDNS,
	} {
		m := monitor.Monitor{Name: "m", Type: typ, Interval: time.Minute, Timeout: time.Second, Config: json.RawMessage(`{}`)}

		var fe *monitor.FieldError
		refused := errors.As(monitor.ValidateMonitor(&m), &fe) && fe.Field == "type"

		// The empty config makes a registered runner fail on its config,
		// never with "no runner registered".
		_, err := d.Dispatch(context.Background(), m)
		unrunnable := err != nil && strings.Contains(err.Error(), "no runner registered")

		if refused != unrunnable {
			t.Errorf("type %s: validation refuses = %v, but dispatcher has no runner = %v", typ, refused, unrunnable)
		}
	}
}
