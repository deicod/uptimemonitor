package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// fakeChecker is a ManualChecker that records its calls and returns a
// pre-configured result, so handler tests do not need a real scheduler.
type fakeChecker struct {
	triggered []string
	result    bool
}

func (f *fakeChecker) ManualTrigger(id string) bool {
	f.triggered = append(f.triggered, id)
	return f.result
}

// fakeCheckReader is a CheckResultReader that records its inputs and returns
// pre-configured results.
type fakeCheckReader struct {
	listResult []*monitor.CheckResult
	err        error

	listMonitorID string
	listLimit     int
}

func (f *fakeCheckReader) ListRecent(_ context.Context, monitorID string, limit int) ([]*monitor.CheckResult, error) {
	f.listMonitorID = monitorID
	f.listLimit = limit
	return f.listResult, f.err
}

// ---------- POST /v1/monitors/{id}/run ----------

// TestRunMonitorHandler verifies the happy path: the monitor exists, the
// scheduler accepts the trigger, and the response advertises the queued status.
// Returning 202 (not 200) signals async execution per SPEC §10.5.
func TestRunMonitorHandler(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	checker := &fakeChecker{result: true}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithManualChecker(checker))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/monitors/01HX/run", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusAccepted, rec.Body)
	}
	var got RunMonitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "queued" {
		t.Errorf("status = %q, want queued", got.Status)
	}
	if len(checker.triggered) != 1 || checker.triggered[0] != "01HX" {
		t.Errorf("triggered = %v, want [01HX]", checker.triggered)
	}
}

// TestRunMonitorHandlerNotFound asserts the existence check runs before the
// scheduler is touched: a missing monitor must yield 404 even when the
// scheduler would have accepted the trigger.
func TestRunMonitorHandlerNotFound(t *testing.T) {
	svc := &fakeMonitorService{getErr: sqlite.ErrNotFound}
	checker := &fakeChecker{result: true}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithManualChecker(checker))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/monitors/01HXMISSING/run", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if len(checker.triggered) != 0 {
		t.Errorf("scheduler was called for a missing monitor: %v", checker.triggered)
	}
}

// TestRunMonitorHandlerConflict covers the case where the scheduler refuses
// because a check is already in flight: the user must see a distinct error
// rather than a silent success, so they know their click did not start
// another check.
func TestRunMonitorHandlerConflict(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	checker := &fakeChecker{result: false}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithManualChecker(checker))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/monitors/01HX/run", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	apiErr, err := DecodeError(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if apiErr.Code != ErrConflict {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrConflict)
	}
}

// TestRunMonitorHandlerDisabledMonitor covers SPEC §16.4: a manual check for a
// disabled monitor must still reach the scheduler. The "does not unpause"
// guarantee is enforced by the state machine and verified by the integration
// test below — here we just pin the handler's contract.
func TestRunMonitorHandlerDisabledMonitor(t *testing.T) {
	disabled := sampleMonitor()
	disabled.Enabled = false
	svc := &fakeMonitorService{getResult: disabled}
	checker := &fakeChecker{result: true}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithManualChecker(checker))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/monitors/01HX/run", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}
	if len(checker.triggered) != 1 {
		t.Fatalf("scheduler not called for disabled monitor: %v", checker.triggered)
	}
}

// ---------- GET /v1/monitors/{id}/checks ----------

// TestListMonitorChecksHandler covers the happy path: an existing monitor with
// recent checks returns them in the response, the monitor id reaches the
// repository, and the default limit is applied when none is supplied.
func TestListMonitorChecksHandler(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	reader := &fakeCheckReader{listResult: []*monitor.CheckResult{sampleCheckResult()}}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithCheckResults(reader))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HX/checks", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusOK, rec.Body)
	}
	var got CheckResultListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Checks) != 1 {
		t.Fatalf("checks len = %d, want 1", len(got.Checks))
	}
	c := got.Checks[0]
	if c.ID != "01HXCHECK" || c.MonitorID != "01HX" {
		t.Errorf("check = %+v, want fixture ids", c)
	}
	if c.DurationMs != 42 {
		t.Errorf("DurationMs = %d, want 42", c.DurationMs)
	}
	if c.State != string(monitor.StateUp) {
		t.Errorf("State = %q, want up", c.State)
	}
	if reader.listMonitorID != "01HX" {
		t.Errorf("monitor id = %q, want 01HX", reader.listMonitorID)
	}
	if reader.listLimit != defaultListLimit {
		t.Errorf("limit = %d, want default %d", reader.listLimit, defaultListLimit)
	}
}

func TestListMonitorChecksHandlerLimit(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	reader := &fakeCheckReader{}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithCheckResults(reader))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HX/checks?limit=3", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if reader.listLimit != 3 {
		t.Errorf("limit = %d, want 3", reader.listLimit)
	}
}

func TestListMonitorChecksHandlerInvalidLimit(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithCheckResults(&fakeCheckReader{}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HX/checks?limit=0", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestListMonitorChecksHandlerNotFound(t *testing.T) {
	svc := &fakeMonitorService{getErr: sqlite.ErrNotFound}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithCheckResults(&fakeCheckReader{}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HXMISSING/checks", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestListMonitorChecksHandlerRepoError(t *testing.T) {
	svc := &fakeMonitorService{getResult: sampleMonitor()}
	reader := &fakeCheckReader{err: errors.New("disk gone")}
	mux := NewRouter(fakeStatusProvider{}, svc, nil, nil, WithCheckResults(reader))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HX/checks", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func sampleCheckResult() *monitor.CheckResult {
	code := 200
	return &monitor.CheckResult{
		ID:             "01HXCHECK",
		MonitorID:      "01HX",
		StartedAt:      time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		FinishedAt:     time.Date(2026, 5, 21, 12, 0, 0, 42_000_000, time.UTC),
		Duration:       42 * time.Millisecond,
		Success:        true,
		State:          monitor.StateUp,
		HTTPStatusCode: &code,
	}
}
