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
)

// fakeIncidentReader is an IncidentReader that records its inputs and returns
// pre-configured results, so handler tests exercise routing, query parsing,
// and response encoding without a real database.
type fakeIncidentReader struct {
	listAllResult []*monitor.Incident
	listResult    []*monitor.Incident
	err           error

	listAllLimit  int
	listMonitorID string
	listLimit     int
}

func (f *fakeIncidentReader) ListAll(_ context.Context, limit int) ([]*monitor.Incident, error) {
	f.listAllLimit = limit
	return f.listAllResult, f.err
}

func (f *fakeIncidentReader) List(_ context.Context, monitorID string, limit int) ([]*monitor.Incident, error) {
	f.listMonitorID = monitorID
	f.listLimit = limit
	return f.listResult, f.err
}

// fakeEventReader is an EventReader that records its inputs and returns
// pre-configured results.
type fakeEventReader struct {
	listResult []*monitor.Event
	byMonitor  []*monitor.Event
	err        error

	listLimit    int
	byMonitorID  string
	byMonitorLim int
}

func (f *fakeEventReader) List(_ context.Context, limit int) ([]*monitor.Event, error) {
	f.listLimit = limit
	return f.listResult, f.err
}

func (f *fakeEventReader) ListByMonitor(_ context.Context, monitorID string, limit int) ([]*monitor.Event, error) {
	f.byMonitorID = monitorID
	f.byMonitorLim = limit
	return f.byMonitor, f.err
}

// sampleIncident returns a resolved incident used as a fixture.
func sampleIncident() *monitor.Incident {
	resolved := time.Date(2026, 5, 18, 13, 0, 0, 0, time.UTC)
	end := "01HXENDEVENT"
	return &monitor.Incident{
		ID:           "01HXINCIDENT",
		MonitorID:    "01HXMONITOR",
		StartedAt:    time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
		ResolvedAt:   &resolved,
		StartEventID: "01HXSTARTEVENT",
		EndEventID:   &end,
		Reason:       "connection refused",
	}
}

// sampleEvent returns a monitor-scoped event used as a fixture.
func sampleEvent() *monitor.Event {
	mid := "01HXMONITOR"
	return &monitor.Event{
		ID:        "01HXEVENT",
		Type:      monitor.EventMonitorStateChanged,
		MonitorID: &mid,
		Data:      json.RawMessage(`{"to":"down"}`),
		CreatedAt: time.Date(2026, 5, 18, 12, 30, 0, 0, time.UTC),
	}
}

// ---------- GET /v1/incidents ----------

func TestListIncidentsHandler(t *testing.T) {
	incidents := &fakeIncidentReader{listAllResult: []*monitor.Incident{sampleIncident()}}
	mux := NewRouter(fakeStatusProvider{}, nil, incidents, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusOK, rec.Body)
	}
	var got IncidentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Incidents) != 1 {
		t.Fatalf("incidents len = %d, want 1", len(got.Incidents))
	}
	in := got.Incidents[0]
	// The full incident, including resolution fields, must survive encoding —
	// the TUI incident view renders all of them.
	if in.ID != "01HXINCIDENT" || in.MonitorID != "01HXMONITOR" {
		t.Errorf("incident = %+v, want id/monitor of the fixture", in)
	}
	if in.ResolvedAt == nil || in.EndEventID == nil {
		t.Errorf("resolution fields lost: %+v", in)
	}
	if in.Reason != "connection refused" {
		t.Errorf("Reason = %q, want connection refused", in.Reason)
	}
	// No ?limit= means the default cap reaches the repository.
	if incidents.listAllLimit != defaultListLimit {
		t.Errorf("limit = %d, want default %d", incidents.listAllLimit, defaultListLimit)
	}
}

func TestListIncidentsHandlerLimit(t *testing.T) {
	incidents := &fakeIncidentReader{}
	mux := NewRouter(fakeStatusProvider{}, nil, incidents, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents?limit=5", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	// An explicit limit must reach the repository so callers can bound results.
	if incidents.listAllLimit != 5 {
		t.Errorf("limit = %d, want 5", incidents.listAllLimit)
	}
}

func TestListIncidentsHandlerInvalidLimit(t *testing.T) {
	mux := NewRouter(fakeStatusProvider{}, nil, &fakeIncidentReader{}, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents?limit=-3", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	apiErr, err := DecodeError(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if apiErr.Code != ErrBadRequest || apiErr.Field != "limit" {
		t.Errorf("error = %+v, want bad_request on limit", apiErr)
	}
}

// ---------- GET /v1/monitors/{id}/incidents ----------

func TestListMonitorIncidentsHandler(t *testing.T) {
	incidents := &fakeIncidentReader{listResult: []*monitor.Incident{sampleIncident()}}
	mux := NewRouter(fakeStatusProvider{}, nil, incidents, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HXMONITOR/incidents", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusOK, rec.Body)
	}
	var got IncidentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Incidents) != 1 {
		t.Fatalf("incidents len = %d, want 1", len(got.Incidents))
	}
	// The path id must reach the repository as the scoping monitor id.
	if incidents.listMonitorID != "01HXMONITOR" {
		t.Errorf("monitor id = %q, want 01HXMONITOR", incidents.listMonitorID)
	}
}

// ---------- GET /v1/events ----------

func TestListEventsHandler(t *testing.T) {
	events := &fakeEventReader{listResult: []*monitor.Event{sampleEvent()}}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, events)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusOK, rec.Body)
	}
	var got EventListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Events) != 1 {
		t.Fatalf("events len = %d, want 1", len(got.Events))
	}
	e := got.Events[0]
	if e.ID != "01HXEVENT" || e.Type != monitor.EventMonitorStateChanged {
		t.Errorf("event = %+v, want id/type of the fixture", e)
	}
	// The JSON data payload must round-trip unchanged.
	if string(e.Data) != `{"to":"down"}` {
		t.Errorf("Data = %s, want the fixture payload", e.Data)
	}
	if events.listLimit != defaultListLimit {
		t.Errorf("limit = %d, want default %d", events.listLimit, defaultListLimit)
	}
}

// ---------- GET /v1/monitors/{id}/events ----------

func TestListMonitorEventsHandler(t *testing.T) {
	events := &fakeEventReader{byMonitor: []*monitor.Event{sampleEvent()}}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, events)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HXMONITOR/events?limit=7", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusOK, rec.Body)
	}
	var got EventListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Events) != 1 {
		t.Fatalf("events len = %d, want 1", len(got.Events))
	}
	if events.byMonitorID != "01HXMONITOR" || events.byMonitorLim != 7 {
		t.Errorf("scoping = (%q, %d), want (01HXMONITOR, 7)", events.byMonitorID, events.byMonitorLim)
	}
}

// ---------- repository error mapping ----------

func TestListIncidentsHandlerRepoError(t *testing.T) {
	incidents := &fakeIncidentReader{err: errors.New("disk gone")}
	mux := NewRouter(fakeStatusProvider{}, nil, incidents, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/incidents", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	apiErr, err := DecodeError(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if apiErr.Code != ErrInternal {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrInternal)
	}
}
