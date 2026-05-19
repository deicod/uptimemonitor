package ipc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// fakeMonitorService is a MonitorService that records its inputs and returns
// pre-configured results, so handler tests exercise request decoding, response
// encoding, and error mapping without a real service or database.
type fakeMonitorService struct {
	getResult  *monitor.Monitor
	getErr     error
	createErr  error
	updateErr  error
	listResult []*monitor.Monitor
	listErr    error
	deleteErr  error

	createdMonitor *monitor.Monitor
	updatedMonitor *monitor.Monitor
	deletedID      string
	listFilter     monitor.MonitorFilter
}

func (f *fakeMonitorService) List(_ context.Context, ff monitor.MonitorFilter) ([]*monitor.Monitor, error) {
	f.listFilter = ff
	return f.listResult, f.listErr
}

func (f *fakeMonitorService) Create(_ context.Context, m *monitor.Monitor) (*monitor.Monitor, error) {
	f.createdMonitor = m
	if f.createErr != nil {
		return nil, f.createErr
	}
	// Echo the input back with an ID, mirroring the real service.
	out := *m
	out.ID = "01HXMONITORTESTID00000000"
	return &out, nil
}

func (f *fakeMonitorService) Get(_ context.Context, _ string) (*monitor.Monitor, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.getResult, nil
}

func (f *fakeMonitorService) Update(_ context.Context, m *monitor.Monitor) (*monitor.Monitor, error) {
	f.updatedMonitor = m
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	return m, nil
}

func (f *fakeMonitorService) Delete(_ context.Context, id string) error {
	f.deletedID = id
	return f.deleteErr
}

// ---------- Duration JSON encoding ----------

// TestDurationJSON pins the wire format: durations cross the IPC boundary as
// Go duration strings (SPEC §10.5), not integer nanoseconds.
func TestDurationJSON(t *testing.T) {
	data, err := json.Marshal(Duration(90 * time.Second))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `"1m30s"` {
		t.Errorf("marshal = %s, want %q", data, `"1m30s"`)
	}

	var d Duration
	if err := json.Unmarshal([]byte(`"45s"`), &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if time.Duration(d) != 45*time.Second {
		t.Errorf("unmarshal = %v, want 45s", time.Duration(d))
	}
}

// ---------- Create monitor: happy path ----------

func TestCreateMonitorHandler(t *testing.T) {
	fake := &fakeMonitorService{}
	mux := NewRouter(fakeStatusProvider{}, fake)

	body := `{"name":"Example","type":"http","enabled":true,"interval":"60s",` +
		`"timeout":"10s","config":{"url":"https://example.com","method":"GET",` +
		`"expected_status_min":200,"expected_status_max":299},"notifications_enabled":true}`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/monitors", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusCreated, rec.Body)
	}

	var got MonitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" {
		t.Error("response has no ID")
	}
	if got.Name != "Example" {
		t.Errorf("Name = %q, want Example", got.Name)
	}
	if time.Duration(got.Interval) != 60*time.Second {
		t.Errorf("Interval = %v, want 60s", time.Duration(got.Interval))
	}

	// The handler must hand the service a fully decoded domain monitor.
	if fake.createdMonitor.Type != monitor.MonitorTypeHTTP {
		t.Errorf("service Type = %q, want http", fake.createdMonitor.Type)
	}
	if fake.createdMonitor.Timeout != 10*time.Second {
		t.Errorf("service Timeout = %v, want 10s", fake.createdMonitor.Timeout)
	}
	if !fake.createdMonitor.NotificationsEnabled {
		t.Error("service NotificationsEnabled = false, want true")
	}
}

// ---------- Create monitor: validation_error shape ----------

func TestCreateMonitorValidationError(t *testing.T) {
	fake := &fakeMonitorService{
		createErr: &monitor.FieldError{Field: "url", Message: "must not be empty"},
	}
	mux := NewRouter(fakeStatusProvider{}, fake)

	body := `{"name":"Example","type":"http","interval":"60s","timeout":"10s","config":{}}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/monitors", strings.NewReader(body))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	apiErr, err := DecodeError(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if apiErr.Code != ErrValidation {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrValidation)
	}
	if apiErr.Field != "url" {
		t.Errorf("field = %q, want url", apiErr.Field)
	}
}

// ---------- Create monitor: malformed JSON ----------

func TestCreateMonitorBadJSON(t *testing.T) {
	mux := NewRouter(fakeStatusProvider{}, &fakeMonitorService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/monitors", strings.NewReader("{not json"))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	apiErr, err := DecodeError(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if apiErr.Code != ErrBadRequest {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrBadRequest)
	}
}

// ---------- Get monitor: not_found ----------

func TestGetMonitorNotFound(t *testing.T) {
	fake := &fakeMonitorService{getErr: sqlite.ErrNotFound}
	mux := NewRouter(fakeStatusProvider{}, fake)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HXMISSING", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	apiErr, err := DecodeError(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if apiErr.Code != ErrNotFound {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrNotFound)
	}
}

func TestGetMonitorHandler(t *testing.T) {
	fake := &fakeMonitorService{getResult: sampleMonitor()}
	mux := NewRouter(fakeStatusProvider{}, fake)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors/01HX", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got MonitorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "01HX" || got.Name != "Old" {
		t.Errorf("got = %+v, want id=01HX name=Old", got)
	}
}

// ---------- Update monitor: partial update preserves untouched fields ----------

func TestUpdateMonitorPartial(t *testing.T) {
	fake := &fakeMonitorService{getResult: sampleMonitor()}
	mux := NewRouter(fakeStatusProvider{}, fake)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/monitors/01HX",
		strings.NewReader(`{"name":"New Name"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusOK, rec.Body)
	}

	// A PATCH with only "name" must change name and leave every other field at
	// its stored value — that is the whole point of a partial update.
	upd := fake.updatedMonitor
	if upd == nil {
		t.Fatal("service Update was not called")
	}
	if upd.Name != "New Name" {
		t.Errorf("Name = %q, want New Name", upd.Name)
	}
	if upd.Interval != 60*time.Second {
		t.Errorf("Interval = %v, want 60s (should be preserved)", upd.Interval)
	}
	if upd.Timeout != 10*time.Second {
		t.Errorf("Timeout = %v, want 10s (should be preserved)", upd.Timeout)
	}
	if string(upd.Config) != `{"url":"https://example.com"}` {
		t.Errorf("Config = %s, want preserved", upd.Config)
	}
	if !upd.NotificationsEnabled {
		t.Error("NotificationsEnabled = false, want preserved true")
	}
}

func TestUpdateMonitorNotFound(t *testing.T) {
	fake := &fakeMonitorService{getErr: sqlite.ErrNotFound}
	mux := NewRouter(fakeStatusProvider{}, fake)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/v1/monitors/01HX",
		strings.NewReader(`{"name":"New"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ---------- Delete monitor ----------

func TestDeleteMonitorHandler(t *testing.T) {
	fake := &fakeMonitorService{}
	mux := NewRouter(fakeStatusProvider{}, fake)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1/monitors/01HX", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if fake.deletedID != "01HX" {
		t.Errorf("deleted ID = %q, want 01HX", fake.deletedID)
	}
}

func TestDeleteMonitorNotFound(t *testing.T) {
	fake := &fakeMonitorService{deleteErr: sqlite.ErrNotFound}
	mux := NewRouter(fakeStatusProvider{}, fake)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1/monitors/01HX", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// ---------- List monitors ----------

func TestListMonitorsHandler(t *testing.T) {
	fake := &fakeMonitorService{listResult: []*monitor.Monitor{sampleMonitor()}}
	mux := NewRouter(fakeStatusProvider{}, fake)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors?state=up&enabled=true", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var got MonitorListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Monitors) != 1 {
		t.Fatalf("monitors len = %d, want 1", len(got.Monitors))
	}

	// Query parameters must reach the service as a typed filter.
	if fake.listFilter.State == nil || *fake.listFilter.State != monitor.StateUp {
		t.Errorf("filter State = %v, want up", fake.listFilter.State)
	}
	if fake.listFilter.Enabled == nil || !*fake.listFilter.Enabled {
		t.Errorf("filter Enabled = %v, want true", fake.listFilter.Enabled)
	}
}

func TestListMonitorsInvalidState(t *testing.T) {
	mux := NewRouter(fakeStatusProvider{}, &fakeMonitorService{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/monitors?state=sideways", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// sampleMonitor returns a stored monitor used as the "existing" record in
// get and update tests.
func sampleMonitor() *monitor.Monitor {
	return &monitor.Monitor{
		ID:                   "01HX",
		Name:                 "Old",
		Type:                 monitor.MonitorTypeHTTP,
		Enabled:              true,
		Interval:             60 * time.Second,
		Timeout:              10 * time.Second,
		Config:               json.RawMessage(`{"url":"https://example.com"}`),
		NotificationsEnabled: true,
		CreatedAt:            time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
		UpdatedAt:            time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC),
	}
}
