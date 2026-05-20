package probe_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/probe"
)

// httpMonitor builds a Monitor whose Config is the JSON form of cfg. Using a
// helper keeps each test focused on the behaviour it is exercising rather than
// re-encoding the SPEC §11.2 config shape inline.
func httpMonitor(t *testing.T, url string, min, max int, timeout time.Duration) monitor.Monitor {
	t.Helper()
	cfg, err := json.Marshal(monitor.HTTPMonitorConfig{
		URL:               url,
		Method:            "GET",
		ExpectedStatusMin: min,
		ExpectedStatusMax: max,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return monitor.Monitor{
		ID:       "test",
		Name:     "test",
		Type:     monitor.MonitorTypeHTTP,
		Enabled:  true,
		Interval: time.Second,
		Timeout:  timeout,
		Config:   cfg,
	}
}

// TestHTTPRunnerType pins the runner to MonitorTypeHTTP so the dispatcher in
// M7.3 can rely on the type advertised at registration matching the type
// handled at Run.
func TestHTTPRunnerType(t *testing.T) {
	r := probe.NewHTTPRunner()
	if got := r.Type(); got != monitor.MonitorTypeHTTP {
		t.Errorf("Type() = %q, want %q", got, monitor.MonitorTypeHTTP)
	}
}

// TestHTTPRunnerInRangeStatusIsSuccess covers SPEC §15.3: a response within
// the configured expected range is a successful check, with the status code
// captured and no error string.
func TestHTTPRunnerInRangeStatusIsSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := httpMonitor(t, srv.URL, 200, 299, 2*time.Second)
	res, err := probe.NewHTTPRunner().Run(context.Background(), m)
	if err != nil {
		t.Fatalf("Run: unexpected error %v", err)
	}
	if !res.Success {
		t.Errorf("Success = false, want true (status 200 in 200-299)")
	}
	if res.HTTPStatusCode == nil || *res.HTTPStatusCode != 200 {
		t.Errorf("HTTPStatusCode = %v, want 200", res.HTTPStatusCode)
	}
	if res.Error != "" {
		t.Errorf("Error = %q, want empty on success", res.Error)
	}
	if res.Duration <= 0 {
		t.Errorf("Duration = %v, want positive", res.Duration)
	}
	if !res.FinishedAt.After(res.StartedAt) && !res.FinishedAt.Equal(res.StartedAt) {
		t.Errorf("FinishedAt %v not after StartedAt %v", res.FinishedAt, res.StartedAt)
	}
}

// TestHTTPRunnerOutOfRangeStatusIsFailure covers SPEC §15.3: a response whose
// status is outside the expected range is a failed check that still records
// the status code (so the UI/storage can show "got 500, expected 200-299"),
// and the Error string is populated to describe the mismatch.
func TestHTTPRunnerOutOfRangeStatusIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := httpMonitor(t, srv.URL, 200, 299, 2*time.Second)
	res, err := probe.NewHTTPRunner().Run(context.Background(), m)
	if err != nil {
		t.Fatalf("Run: unexpected error %v", err)
	}
	if res.Success {
		t.Errorf("Success = true, want false (status 500 outside 200-299)")
	}
	if res.HTTPStatusCode == nil || *res.HTTPStatusCode != 500 {
		t.Errorf("HTTPStatusCode = %v, want 500", res.HTTPStatusCode)
	}
	if res.Error == "" {
		t.Error("Error empty, want a description of the out-of-range status")
	}
}

// TestHTTPRunnerTimeoutIsFailure covers SPEC §15.2 (per-monitor timeout) and
// §15.4 (sanitised transport errors). A request that exceeds the monitor's
// timeout is a failed check with no recorded status, and the error string must
// not leak the target URL.
func TestHTTPRunnerTimeoutIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(500 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	m := httpMonitor(t, srv.URL, 200, 299, 20*time.Millisecond)
	res, err := probe.NewHTTPRunner().Run(context.Background(), m)
	if err != nil {
		t.Fatalf("Run: unexpected error %v", err)
	}
	if res.Success {
		t.Errorf("Success = true, want false on timeout")
	}
	if res.HTTPStatusCode != nil {
		t.Errorf("HTTPStatusCode = %v, want nil (no response received)", res.HTTPStatusCode)
	}
	if res.Error == "" {
		t.Error("Error empty, want a sanitised timeout description")
	}
	if strings.Contains(res.Error, srv.URL) {
		t.Errorf("Error %q contains target URL %q (SPEC §15.4: sanitise)", res.Error, srv.URL)
	}
}

// TestHTTPRunnerBadHostIsSanitisedFailure covers SPEC §15.4: a DNS / dial
// failure is reported as a failed check with a sanitised error string that
// does not echo the configured host. The .invalid TLD is reserved by RFC 2606
// and is guaranteed to fail DNS resolution.
func TestHTTPRunnerBadHostIsSanitisedFailure(t *testing.T) {
	const badHost = "uptimemonitor-does-not-exist.invalid"
	m := httpMonitor(t, "http://"+badHost, 200, 299, time.Second)
	res, err := probe.NewHTTPRunner().Run(context.Background(), m)
	if err != nil {
		t.Fatalf("Run: unexpected error %v", err)
	}
	if res.Success {
		t.Errorf("Success = true, want false on DNS failure")
	}
	if res.HTTPStatusCode != nil {
		t.Errorf("HTTPStatusCode = %v, want nil", res.HTTPStatusCode)
	}
	if res.Error == "" {
		t.Error("Error empty, want a sanitised description of the failure")
	}
	if strings.Contains(res.Error, badHost) {
		t.Errorf("Error %q leaks host %q (SPEC §15.4: sanitise)", res.Error, badHost)
	}
}

// TestHTTPRunnerMalformedConfigReturnsError covers the Runner interface
// contract (SPEC §15.1, runner.go): malformed config is reported through the
// error return, not as a Result, so the dispatcher can distinguish a probe
// observation from a programmer/setup error.
func TestHTTPRunnerMalformedConfigReturnsError(t *testing.T) {
	m := monitor.Monitor{
		ID:       "test",
		Name:     "test",
		Type:     monitor.MonitorTypeHTTP,
		Interval: time.Second,
		Timeout:  time.Second,
		Config:   json.RawMessage(`{not json`),
	}
	if _, err := probe.NewHTTPRunner().Run(context.Background(), m); err == nil {
		t.Error("Run with malformed config returned nil error, want error")
	}
}
