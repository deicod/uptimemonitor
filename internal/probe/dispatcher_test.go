package probe_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/probe"
)

// recordingRunner stands in for a real probe so the dispatcher test can
// observe which runner was picked without depending on network behaviour.
// It also lets the test assert that the monitor passed to Run was the one the
// dispatcher was asked to dispatch.
type recordingRunner struct {
	typ    monitor.MonitorType
	res    probe.Result
	err    error
	called bool
	gotID  string
}

func (r *recordingRunner) Type() monitor.MonitorType { return r.typ }

func (r *recordingRunner) Run(_ context.Context, m monitor.Monitor) (probe.Result, error) {
	r.called = true
	r.gotID = m.ID
	return r.res, r.err
}

// TestDispatcherRoutesByMonitorType is the core dispatcher contract: given a
// monitor, the dispatcher picks the runner whose Type() matches m.Type and
// passes the monitor through unchanged. Using a fake runner keeps the test
// focused on routing rather than HTTP semantics (covered in http_test.go).
func TestDispatcherRoutesByMonitorType(t *testing.T) {
	started := time.Now()
	finished := started.Add(15 * time.Millisecond)
	status := 200
	stub := &recordingRunner{
		typ: monitor.MonitorTypeHTTP,
		res: probe.Result{
			StartedAt:      started,
			FinishedAt:     finished,
			Duration:       finished.Sub(started),
			Success:        true,
			HTTPStatusCode: &status,
		},
	}

	d := probe.NewDispatcher()
	d.Register(stub) // replace the default HTTP runner with the stub.

	m := monitor.Monitor{ID: "mon-1", Type: monitor.MonitorTypeHTTP, Timeout: time.Second}
	cr, err := d.Dispatch(context.Background(), m)
	if err != nil {
		t.Fatalf("Dispatch: unexpected error %v", err)
	}
	if !stub.called {
		t.Fatal("registered runner was not invoked")
	}
	if stub.gotID != "mon-1" {
		t.Errorf("runner saw monitor ID %q, want %q", stub.gotID, "mon-1")
	}
	if cr.MonitorID != "mon-1" {
		t.Errorf("CheckResult.MonitorID = %q, want %q", cr.MonitorID, "mon-1")
	}
	if cr.ID == "" {
		t.Error("CheckResult.ID empty, want a generated ULID")
	}
	if !cr.Success {
		t.Error("CheckResult.Success = false, want true (carried from probe.Result)")
	}
	if cr.HTTPStatusCode == nil || *cr.HTTPStatusCode != 200 {
		t.Errorf("CheckResult.HTTPStatusCode = %v, want 200", cr.HTTPStatusCode)
	}
	if cr.Duration != finished.Sub(started) {
		t.Errorf("CheckResult.Duration = %v, want %v", cr.Duration, finished.Sub(started))
	}
}

// TestDispatcherUnknownTypeReturnsError covers the negative path: a monitor
// whose Type has no registered runner is a programmer/setup error, not an
// observation, so the dispatcher returns an error rather than fabricating a
// failed CheckResult that would look like a real probe failure.
func TestDispatcherUnknownTypeReturnsError(t *testing.T) {
	d := probe.NewDispatcher()
	m := monitor.Monitor{ID: "mon-x", Type: monitor.MonitorType("tcp"), Timeout: time.Second}
	if _, err := d.Dispatch(context.Background(), m); err == nil {
		t.Error("Dispatch with unknown type returned nil error, want error")
	}
}

// TestDispatcherPropagatesRunnerError mirrors the Runner contract: errors from
// the runner (malformed config etc.) flow through Dispatch unchanged so the
// pipeline can distinguish them from probe-level failures.
func TestDispatcherPropagatesRunnerError(t *testing.T) {
	wantErr := errStub("boom")
	stub := &recordingRunner{typ: monitor.MonitorTypeHTTP, err: wantErr}
	d := probe.NewDispatcher()
	d.Register(stub)

	m := monitor.Monitor{ID: "mon-2", Type: monitor.MonitorTypeHTTP, Timeout: time.Second}
	_, err := d.Dispatch(context.Background(), m)
	if err == nil {
		t.Fatal("Dispatch returned nil error, want runner error")
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }

// TestNewDispatcherIncludesHTTPRunner verifies the default registration: a
// freshly built dispatcher can handle HTTP monitors without manual Register
// calls, so the service can use it directly. The test uses a live httptest
// server to confirm an actual round-trip, not just registration presence.
func TestNewDispatcherIncludesHTTPRunner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg, err := json.Marshal(monitor.HTTPMonitorConfig{
		URL:               srv.URL,
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	m := monitor.Monitor{
		ID:       "mon-http",
		Type:     monitor.MonitorTypeHTTP,
		Timeout:  2 * time.Second,
		Interval: time.Second,
		Config:   cfg,
	}

	cr, err := probe.NewDispatcher().Dispatch(context.Background(), m)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !cr.Success {
		t.Errorf("CheckResult.Success = false, want true")
	}
	if cr.HTTPStatusCode == nil || *cr.HTTPStatusCode != 200 {
		t.Errorf("CheckResult.HTTPStatusCode = %v, want 200", cr.HTTPStatusCode)
	}
}
