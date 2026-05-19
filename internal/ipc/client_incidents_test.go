package ipc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// startIncidentTestServer starts a real IPC server backed by the given fake
// readers, so client tests exercise the full HTTP round-trip over the Unix
// socket against the actual route table.
func startIncidentTestServer(t *testing.T, incidents IncidentReader, events EventReader) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "test.sock")
	startTestServer(t, sock, NewRouter(fakeStatusProvider{}, nil, incidents, events))
	return NewClient(sock)
}

// ---------- ListIncidents ----------

func TestClientListIncidents(t *testing.T) {
	incidents := &fakeIncidentReader{listAllResult: []*monitor.Incident{sampleIncident()}}
	client := startIncidentTestServer(t, incidents, nil)

	got, err := client.ListIncidents(context.Background())
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("incidents len = %d, want 1", len(got))
	}
	if got[0].ID != "01HXINCIDENT" || got[0].Reason != "connection refused" {
		t.Errorf("incident = %+v, want the fixture", got[0])
	}
	if got[0].ResolvedAt == nil || got[0].EndEventID == nil {
		t.Errorf("resolution fields lost over the wire: %+v", got[0])
	}
}

func TestClientListMonitorIncidents(t *testing.T) {
	incidents := &fakeIncidentReader{listResult: []*monitor.Incident{sampleIncident()}}
	client := startIncidentTestServer(t, incidents, nil)

	got, err := client.ListMonitorIncidents(context.Background(), "01HXMONITOR")
	if err != nil {
		t.Fatalf("ListMonitorIncidents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("incidents len = %d, want 1", len(got))
	}
	// The monitor id must survive the round-trip and scope the query.
	if incidents.listMonitorID != "01HXMONITOR" {
		t.Errorf("monitor id = %q, want 01HXMONITOR", incidents.listMonitorID)
	}
}

// ---------- ListEvents ----------

func TestClientListEvents(t *testing.T) {
	events := &fakeEventReader{listResult: []*monitor.Event{sampleEvent()}}
	client := startIncidentTestServer(t, nil, events)

	got, err := client.ListEvents(context.Background())
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("events len = %d, want 1", len(got))
	}
	if got[0].ID != "01HXEVENT" || string(got[0].Data) != `{"to":"down"}` {
		t.Errorf("event = %+v, want the fixture", got[0])
	}
}

func TestClientListMonitorEvents(t *testing.T) {
	events := &fakeEventReader{byMonitor: []*monitor.Event{sampleEvent()}}
	client := startIncidentTestServer(t, nil, events)

	got, err := client.ListMonitorEvents(context.Background(), "01HXMONITOR")
	if err != nil {
		t.Fatalf("ListMonitorEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("events len = %d, want 1", len(got))
	}
	if events.byMonitorID != "01HXMONITOR" {
		t.Errorf("monitor id = %q, want 01HXMONITOR", events.byMonitorID)
	}
}

// ---------- error envelope ----------

func TestClientListIncidentsError(t *testing.T) {
	incidents := &fakeIncidentReader{err: errors.New("disk gone")}
	client := startIncidentTestServer(t, incidents, nil)

	_, err := client.ListIncidents(context.Background())
	if err == nil {
		t.Fatal("ListIncidents returned nil error, want *APIError")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *APIError", err, err)
	}
	if apiErr.Code != ErrInternal {
		t.Errorf("Code = %q, want %q", apiErr.Code, ErrInternal)
	}
}
