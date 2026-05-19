package ipc_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/ipc"
	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// TestMonitorCRUDOverIPC exercises the full monitor lifecycle over a real Unix
// socket: create → list → get → update → delete, then verifies incident and
// event reads. This is the M5 exit check.
func TestMonitorCRUDOverIPC(t *testing.T) {
	// Set up a temp SQLite database with migrations.
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Build the real monitor service and repositories.
	monitorSvc := monitor.NewService(
		sqlite.NewMonitorRepo(store),
		sqlite.NewMonitorStateRepo(store),
		sqlite.NewEventRepo(store),
	)
	incidentRepo := sqlite.NewIncidentRepo(store)
	eventRepo := sqlite.NewEventRepo(store)

	// Wire the IPC server with all M5 routes.
	sock := filepath.Join(t.TempDir(), "test.sock")
	mux := ipc.NewRouter(nil, monitorSvc, incidentRepo, eventRepo)
	srv := ipc.NewServer(sock, mux)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	waitForServer(t, sock)

	client := ipc.NewClient(sock)

	cfg, _ := json.Marshal(monitor.HTTPMonitorConfig{
		URL:               "https://example.com",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	})

	// ---- CREATE ----
	created, err := client.CreateMonitor(ctx, ipc.CreateMonitorRequest{
		Name:     "Example",
		Type:     "http",
		Enabled:  true,
		Interval: ipc.Duration(60 * time.Second),
		Timeout:  ipc.Duration(10 * time.Second),
		Config:   cfg,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created monitor has no ID")
	}
	if created.Name != "Example" {
		t.Errorf("Name = %q, want %q", created.Name, "Example")
	}
	if created.Type != "http" {
		t.Errorf("Type = %q, want %q", created.Type, "http")
	}
	if !created.Enabled {
		t.Error("Enabled = false, want true")
	}

	// ---- LIST ----
	monitors, err := client.ListMonitors(ctx, ipc.MonitorListFilter{})
	if err != nil {
		t.Fatalf("ListMonitors: %v", err)
	}
	if len(monitors) != 1 {
		t.Fatalf("len(monitors) = %d, want 1", len(monitors))
	}
	if monitors[0].ID != created.ID {
		t.Errorf("listed ID = %q, want %q", monitors[0].ID, created.ID)
	}

	// ---- GET ----
	got, err := client.GetMonitor(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if got.Name != "Example" {
		t.Errorf("got.Name = %q, want %q", got.Name, "Example")
	}

	// ---- UPDATE ----
	newName := "Renamed"
	updated, err := client.UpdateMonitor(ctx, created.ID, ipc.UpdateMonitorRequest{
		Name: &newName,
	})
	if err != nil {
		t.Fatalf("UpdateMonitor: %v", err)
	}
	if updated.Name != "Renamed" {
		t.Errorf("updated.Name = %q, want %q", updated.Name, "Renamed")
	}

	// Verify the update stuck via a fresh GET.
	got2, err := client.GetMonitor(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMonitor after update: %v", err)
	}
	if got2.Name != "Renamed" {
		t.Errorf("got2.Name = %q, want %q", got2.Name, "Renamed")
	}

	// ---- INCIDENTS (empty initially) ----
	incidents, err := client.ListMonitorIncidents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListMonitorIncidents: %v", err)
	}
	if len(incidents) != 0 {
		t.Errorf("incidents = %d, want 0", len(incidents))
	}

	allIncidents, err := client.ListIncidents(ctx)
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(allIncidents) != 0 {
		t.Errorf("all incidents = %d, want 0", len(allIncidents))
	}

	// ---- EVENTS (should have monitor_created + monitor_updated) ----
	events, err := client.ListMonitorEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListMonitorEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("monitor events = %d, want 2", len(events))
	}
	if events[0].Type != monitor.EventMonitorUpdated {
		t.Errorf("events[0].Type = %q, want %q", events[0].Type, monitor.EventMonitorUpdated)
	}
	if events[1].Type != monitor.EventMonitorCreated {
		t.Errorf("events[1].Type = %q, want %q", events[1].Type, monitor.EventMonitorCreated)
	}

	allEvents, err := client.ListEvents(ctx)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(allEvents) != 2 {
		t.Errorf("all events = %d, want 2", len(allEvents))
	}

	// ---- DELETE ----
	if err := client.DeleteMonitor(ctx, created.ID); err != nil {
		t.Fatalf("DeleteMonitor: %v", err)
	}

	// Verify the monitor is gone from the list.
	monitors, err = client.ListMonitors(ctx, ipc.MonitorListFilter{})
	if err != nil {
		t.Fatalf("ListMonitors after delete: %v", err)
	}
	if len(monitors) != 0 {
		t.Errorf("monitors after delete = %d, want 0", len(monitors))
	}

	// GET on a deleted monitor should return not_found.
	_, err = client.GetMonitor(ctx, created.ID)
	if err == nil {
		t.Fatal("GetMonitor after delete should fail")
	}
	var apiErr *ipc.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error type = %T, want *ipc.APIError", err)
	}
	if apiErr.Code != ipc.ErrNotFound {
		t.Errorf("error code = %q, want %q", apiErr.Code, ipc.ErrNotFound)
	}

	// Verify a monitor_deleted event was recorded.
	eventsAfterDelete, err := client.ListMonitorEvents(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListMonitorEvents after delete: %v", err)
	}
	if len(eventsAfterDelete) != 3 {
		t.Fatalf("events after delete = %d, want 3", len(eventsAfterDelete))
	}
	if eventsAfterDelete[0].Type != monitor.EventMonitorDeleted {
		t.Errorf("events[0].Type = %q, want %q", eventsAfterDelete[0].Type, monitor.EventMonitorDeleted)
	}

	// Clean shutdown.
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("server Start returned error: %v", err)
	}
}

// waitForServer polls until a Unix connection to the socket succeeds.
func waitForServer(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", path)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s not reachable within timeout", path)
}
