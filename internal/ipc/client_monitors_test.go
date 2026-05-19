package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// startMonitorTestServer starts a real IPC server backed by the given fake
// monitor service, so client tests exercise the full HTTP round-trip over the
// Unix socket against the actual route table.
func startMonitorTestServer(t *testing.T, fake MonitorService) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "test.sock")
	// startTestServer registers its own cleanup that stops the server.
	startTestServer(t, sock, NewRouter(fakeStatusProvider{}, fake, nil, nil))
	return NewClient(sock)
}

// ---------- ListMonitors ----------

func TestClientListMonitors(t *testing.T) {
	fake := &fakeMonitorService{listResult: []*monitor.Monitor{sampleMonitor()}}
	client := startMonitorTestServer(t, fake)

	enabled := true
	got, err := client.ListMonitors(context.Background(), MonitorListFilter{
		State:   string(monitor.StateUp),
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("monitors len = %d, want 1", len(got))
	}
	if got[0].ID != "01HX" || got[0].Name != "Old" {
		t.Errorf("monitor = %+v, want id=01HX name=Old", got[0])
	}

	// The filter must survive the round-trip and reach the service typed.
	if fake.listFilter.State == nil || *fake.listFilter.State != monitor.StateUp {
		t.Errorf("filter State = %v, want up", fake.listFilter.State)
	}
	if fake.listFilter.Enabled == nil || !*fake.listFilter.Enabled {
		t.Errorf("filter Enabled = %v, want true", fake.listFilter.Enabled)
	}
}

func TestClientListMonitorsNoFilter(t *testing.T) {
	fake := &fakeMonitorService{listResult: []*monitor.Monitor{sampleMonitor()}}
	client := startMonitorTestServer(t, fake)

	if _, err := client.ListMonitors(context.Background(), MonitorListFilter{}); err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	// An empty filter must apply no constraints at the service.
	if fake.listFilter.State != nil || fake.listFilter.Enabled != nil {
		t.Errorf("filter = %+v, want empty", fake.listFilter)
	}
}

// ---------- CreateMonitor ----------

func TestClientCreateMonitor(t *testing.T) {
	fake := &fakeMonitorService{}
	client := startMonitorTestServer(t, fake)

	req := CreateMonitorRequest{
		Name:                 "Example",
		Type:                 string(monitor.MonitorTypeHTTP),
		Enabled:              true,
		Interval:             Duration(60 * time.Second),
		Timeout:              Duration(10 * time.Second),
		Config:               json.RawMessage(`{"url":"https://example.com"}`),
		NotificationsEnabled: true,
	}
	got, err := client.CreateMonitor(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if got.ID == "" {
		t.Error("response has no ID")
	}
	if got.Name != "Example" {
		t.Errorf("Name = %q, want Example", got.Name)
	}

	// The request body must reach the service fully decoded.
	if fake.createdMonitor.Type != monitor.MonitorTypeHTTP {
		t.Errorf("service Type = %q, want http", fake.createdMonitor.Type)
	}
	if fake.createdMonitor.Interval != 60*time.Second {
		t.Errorf("service Interval = %v, want 60s", fake.createdMonitor.Interval)
	}
}

func TestClientCreateMonitorValidationError(t *testing.T) {
	fake := &fakeMonitorService{
		createErr: &monitor.FieldError{Field: "url", Message: "must not be empty"},
	}
	client := startMonitorTestServer(t, fake)

	_, err := client.CreateMonitor(context.Background(), CreateMonitorRequest{Name: "x"})
	if err == nil {
		t.Fatal("CreateMonitor returned nil error, want *APIError")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *APIError", err, err)
	}
	if apiErr.Code != ErrValidation {
		t.Errorf("Code = %q, want %q", apiErr.Code, ErrValidation)
	}
	if apiErr.Field != "url" {
		t.Errorf("Field = %q, want url", apiErr.Field)
	}
}

// ---------- GetMonitor ----------

func TestClientGetMonitor(t *testing.T) {
	fake := &fakeMonitorService{getResult: sampleMonitor()}
	client := startMonitorTestServer(t, fake)

	got, err := client.GetMonitor(context.Background(), "01HX")
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.ID != "01HX" || got.Name != "Old" {
		t.Errorf("monitor = %+v, want id=01HX name=Old", got)
	}
	if time.Duration(got.Interval) != 60*time.Second {
		t.Errorf("Interval = %v, want 60s", time.Duration(got.Interval))
	}
}

func TestClientGetMonitorNotFound(t *testing.T) {
	fake := &fakeMonitorService{getErr: sqlite.ErrNotFound}
	client := startMonitorTestServer(t, fake)

	_, err := client.GetMonitor(context.Background(), "01HXMISSING")
	if err == nil {
		t.Fatal("GetMonitor returned nil error, want *APIError")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *APIError", err, err)
	}
	if apiErr.Code != ErrNotFound {
		t.Errorf("Code = %q, want %q", apiErr.Code, ErrNotFound)
	}
}

// ---------- UpdateMonitor ----------

func TestClientUpdateMonitor(t *testing.T) {
	fake := &fakeMonitorService{getResult: sampleMonitor()}
	client := startMonitorTestServer(t, fake)

	name := "New Name"
	got, err := client.UpdateMonitor(context.Background(), "01HX", UpdateMonitorRequest{Name: &name})
	if err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}
	if got.Name != "New Name" {
		t.Errorf("Name = %q, want New Name", got.Name)
	}

	// A partial update changes only the supplied field; the rest is preserved.
	upd := fake.updatedMonitor
	if upd == nil {
		t.Fatal("service Update was not called")
	}
	if upd.Name != "New Name" {
		t.Errorf("service Name = %q, want New Name", upd.Name)
	}
	if upd.Interval != 60*time.Second {
		t.Errorf("service Interval = %v, want 60s (preserved)", upd.Interval)
	}
}

func TestClientUpdateMonitorNotFound(t *testing.T) {
	fake := &fakeMonitorService{getErr: sqlite.ErrNotFound}
	client := startMonitorTestServer(t, fake)

	name := "New"
	_, err := client.UpdateMonitor(context.Background(), "01HX", UpdateMonitorRequest{Name: &name})
	if err == nil {
		t.Fatal("UpdateMonitor returned nil error, want *APIError")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *APIError", err, err)
	}
	if apiErr.Code != ErrNotFound {
		t.Errorf("Code = %q, want %q", apiErr.Code, ErrNotFound)
	}
}

// ---------- DeleteMonitor ----------

func TestClientDeleteMonitor(t *testing.T) {
	fake := &fakeMonitorService{}
	client := startMonitorTestServer(t, fake)

	if err := client.DeleteMonitor(context.Background(), "01HX"); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}
	if fake.deletedID != "01HX" {
		t.Errorf("deleted ID = %q, want 01HX", fake.deletedID)
	}
}

func TestClientDeleteMonitorNotFound(t *testing.T) {
	fake := &fakeMonitorService{deleteErr: sqlite.ErrNotFound}
	client := startMonitorTestServer(t, fake)

	err := client.DeleteMonitor(context.Background(), "01HX")
	if err == nil {
		t.Fatal("DeleteMonitor returned nil error, want *APIError")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *APIError", err, err)
	}
	if apiErr.Code != ErrNotFound {
		t.Errorf("Code = %q, want %q", apiErr.Code, ErrNotFound)
	}
}
