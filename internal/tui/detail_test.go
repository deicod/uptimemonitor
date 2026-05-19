package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// sampleDetailMonitor is a representative monitor for the detail screen tests.
func sampleDetailMonitor() ipc.MonitorResponse {
	return ipc.MonitorResponse{
		ID:                   "01A",
		Name:                 "API",
		Type:                 "http",
		Enabled:              true,
		Interval:             ipc.Duration(60 * time.Second),
		Timeout:              ipc.Duration(10 * time.Second),
		Config:               json.RawMessage(`{"url":"https://example.com"}`),
		NotificationsEnabled: true,
	}
}

// sampleDetailIncidents is a representative incident list for the detail tests.
func sampleDetailIncidents() []ipc.IncidentResponse {
	return []ipc.IncidentResponse{
		{ID: "i1", MonitorID: "01A", StartedAt: time.Now().Add(-time.Hour), Reason: "probe failed"},
	}
}

// sampleDetailEvents is a representative event list for the detail tests.
func sampleDetailEvents() []ipc.EventResponse {
	return []ipc.EventResponse{
		{ID: "e1", Type: "monitor_created", CreatedAt: time.Now().Add(-2 * time.Hour)},
	}
}

// detailStub is a fake client carrying the detail screen's three responses.
func detailStub() stubClient {
	return stubClient{
		monitor:   sampleDetailMonitor(),
		incidents: sampleDetailIncidents(),
		events:    sampleDetailEvents(),
	}
}

// applyBatch runs every command produced by a Bubble Tea batch command and
// feeds each resulting message into the screen, returning the final screen.
// The detail screen's Init batches three independent IPC fetches.
func applyBatch(t *testing.T, s Screen, cmd tea.Cmd) Screen {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got nil")
	}
	for _, c := range collectCmds(cmd) {
		var next tea.Cmd
		s, next = s.Update(c())
		for _, inner := range collectCmds(next) {
			s, _ = s.Update(inner())
		}
	}
	return s
}

// collectCmds flattens a command — which may be a tea.Batch — into the list of
// leaf commands it represents.
func collectCmds(cmd tea.Cmd) []tea.Cmd {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		return msg
	default:
		// A non-batch command: wrap it so its single message is replayed.
		return []tea.Cmd{func() tea.Msg { return msg }}
	}
}

// TestMonitorDetailLoadsData verifies the detail screen fetches the monitor,
// its incidents, and its events on Init and caches all three, so the operator
// sees the monitor's full picture instead of an empty panel.
func TestMonitorDetailLoadsData(t *testing.T) {
	s := newMonitorDetailScreen(detailStub(), "01A")

	scr := applyBatch(t, s, s.Init())
	ds, ok := scr.(*monitorDetailScreen)
	if !ok {
		t.Fatalf("detail Update returned %T, want *monitorDetailScreen", scr)
	}
	if ds.monitor == nil || ds.monitor.Name != "API" {
		t.Fatalf("detail screen did not cache the monitor: %+v", ds.monitor)
	}
	if len(ds.incidents) != 1 {
		t.Fatalf("detail screen did not cache incidents: %+v", ds.incidents)
	}
	if len(ds.events) != 1 {
		t.Fatalf("detail screen did not cache events: %+v", ds.events)
	}
}

// TestMonitorDetailViewRendersSections verifies the View renders the monitor
// config, the incident, and the event, so the screen communicates the
// monitor's state rather than a subset of it.
func TestMonitorDetailViewRendersSections(t *testing.T) {
	s := newMonitorDetailScreen(detailStub(), "01A")
	scr := applyBatch(t, s, s.Init())

	view := scr.View()
	for _, want := range []string{
		"API", "http", "1m0s", "10s",
		"https://example.com",
		"probe failed",
		"monitor_created",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q:\n%s", want, view)
		}
	}
}

// TestMonitorDetailViewBeforeLoad verifies the screen shows a loading
// placeholder before the monitor response arrives instead of a blank panel.
func TestMonitorDetailViewBeforeLoad(t *testing.T) {
	s := newMonitorDetailScreen(detailStub(), "01A")
	if !strings.Contains(s.View(), "loading") {
		t.Errorf("detail view before load shows no placeholder:\n%s", s.View())
	}
}

// TestMonitorDetailRefreshKey verifies the refresh key re-fetches the data so
// the operator can update a stale view without leaving the screen.
func TestMonitorDetailRefreshKey(t *testing.T) {
	s := newMonitorDetailScreen(detailStub(), "01A")
	_, cmd := s.Update(runeKey('r'))
	if cmd == nil {
		t.Fatal("refresh key produced no command")
	}
}

// TestMonitorListEnterOpensDetailScreen verifies the monitor list pushes the
// detail screen when it receives the detail-navigation message, so selecting a
// monitor actually opens its detail view.
func TestMonitorListEnterOpensDetailScreen(t *testing.T) {
	s := newMonitorListScreen(detailStub())
	s.Update(monitorsLoadedMsg{monitors: sampleMonitors()})

	_, cmd := s.Update(openMonitorDetailMsg{monitorID: "01A"})
	if cmd == nil {
		t.Fatal("detail navigation message produced no command")
	}
	push, ok := cmd().(pushScreenMsg)
	if !ok {
		t.Fatalf("monitor list emitted %T, want pushScreenMsg", cmd())
	}
	if _, ok := push.screen.(*monitorDetailScreen); !ok {
		t.Fatalf("monitor list pushed %T, want *monitorDetailScreen", push.screen)
	}
}
