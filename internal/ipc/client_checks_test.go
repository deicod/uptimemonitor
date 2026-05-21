package ipc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// startChecksTestServer wires a real IPC server with both the run and recent-
// checks endpoints so client tests exercise the full HTTP round-trip.
func startChecksTestServer(t *testing.T, svc MonitorService, checker ManualChecker, reader CheckResultReader) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "test.sock")
	startTestServer(t, sock, NewRouter(fakeStatusProvider{}, svc, nil, nil,
		WithManualChecker(checker), WithCheckResults(reader)))
	return NewClient(sock)
}

// TestClientRunMonitor pins the contract that a successful manual-trigger
// surfaces as a typed response, and that the monitor id reaches the scheduler.
func TestClientRunMonitor(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	checker := &fakeChecker{result: true}
	client := startChecksTestServer(t, svc, checker, &fakeCheckReader{})

	resp, err := client.RunMonitor(context.Background(), "01HX")
	if err != nil {
		t.Fatalf("RunMonitor: %v", err)
	}
	if resp.Status != "queued" {
		t.Errorf("Status = %q, want queued", resp.Status)
	}
	if len(checker.triggered) != 1 || checker.triggered[0] != "01HX" {
		t.Errorf("triggered = %v, want [01HX]", checker.triggered)
	}
}

// TestClientRunMonitorNotFound asserts the server's 404 response is decoded as
// a typed APIError so callers can branch on Code rather than parsing text.
func TestClientRunMonitorNotFound(t *testing.T) {
	svc := &fakeMonitorService{getErr: sqlite.ErrNotFound}
	client := startChecksTestServer(t, svc, &fakeChecker{result: true}, &fakeCheckReader{})

	_, err := client.RunMonitor(context.Background(), "01HXMISSING")
	if err == nil {
		t.Fatal("RunMonitor should fail")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if apiErr.Code != ErrNotFound {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrNotFound)
	}
}

// TestClientRecentChecks covers the recent-checks happy path: the response
// list is decoded and the limit query parameter is propagated.
func TestClientRecentChecks(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	reader := &fakeCheckReader{listResult: []*monitor.CheckResult{sampleCheckResult()}}
	client := startChecksTestServer(t, svc, &fakeChecker{result: true}, reader)

	got, err := client.RecentChecks(context.Background(), "01HX", 5)
	if err != nil {
		t.Fatalf("RecentChecks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("checks len = %d, want 1", len(got))
	}
	if got[0].ID != "01HXCHECK" || got[0].State != string(monitor.StateUp) {
		t.Errorf("check = %+v, want fixture", got[0])
	}
	if reader.listLimit != 5 {
		t.Errorf("listLimit = %d, want 5", reader.listLimit)
	}
}

// TestClientRecentChecksDefaultLimit verifies a zero limit omits the query
// parameter so the server's default applies.
func TestClientRecentChecksDefaultLimit(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	reader := &fakeCheckReader{}
	client := startChecksTestServer(t, svc, &fakeChecker{result: true}, reader)

	if _, err := client.RecentChecks(context.Background(), "01HX", 0); err != nil {
		t.Fatalf("RecentChecks: %v", err)
	}
	if reader.listLimit != defaultListLimit {
		t.Errorf("listLimit = %d, want default %d", reader.listLimit, defaultListLimit)
	}
}
