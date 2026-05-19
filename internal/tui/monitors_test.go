package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// sampleMonitors is a representative monitor list covering both enabled and
// disabled monitors so the screen's rendering and selection are exercised.
func sampleMonitors() []ipc.MonitorResponse {
	return []ipc.MonitorResponse{
		{ID: "01A", Name: "API", Type: "http", Enabled: true,
			Interval: ipc.Duration(60 * time.Second), NotificationsEnabled: true},
		{ID: "01B", Name: "Website", Type: "http", Enabled: false,
			Interval: ipc.Duration(30 * time.Second), NotificationsEnabled: false},
	}
}

// TestMonitorListLoadsMonitors verifies the screen fetches the monitor list on
// Init and caches it, so the operator sees the configured monitors instead of
// an empty panel.
func TestMonitorListLoadsMonitors(t *testing.T) {
	s := newMonitorListScreen(stubClient{monitors: sampleMonitors()})

	cmd := s.Init()
	if cmd == nil {
		t.Fatal("monitor list Init returned no command")
	}

	scr, _ := s.Update(cmd())
	ls, ok := scr.(*monitorListScreen)
	if !ok {
		t.Fatalf("monitor list Update returned %T, want *monitorListScreen", scr)
	}
	if len(ls.monitors) != 2 {
		t.Fatalf("monitor list did not cache the fetched monitors: %+v", ls.monitors)
	}
}

// TestMonitorListSelectionMoves verifies the cursor moves with the navigation
// keys and stays within bounds, since selection is what drives detail and edit
// navigation.
func TestMonitorListSelectionMoves(t *testing.T) {
	s := newMonitorListScreen(stubClient{})
	s.Update(monitorsLoadedMsg{monitors: sampleMonitors()})

	s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if s.cursor != 1 {
		t.Fatalf("down key: cursor = %d, want 1", s.cursor)
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if s.cursor != 1 {
		t.Fatalf("down key past the end: cursor = %d, want 1 (clamped)", s.cursor)
	}
	s.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	s.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if s.cursor != 0 {
		t.Fatalf("up key past the start: cursor = %d, want 0 (clamped)", s.cursor)
	}
}

// TestMonitorListEnterEmitsDetailNavigation verifies the select key emits a
// detail-navigation message carrying the selected monitor's ID, so M6.4 can
// open the right monitor.
func TestMonitorListEnterEmitsDetailNavigation(t *testing.T) {
	s := newMonitorListScreen(stubClient{})
	s.Update(monitorsLoadedMsg{monitors: sampleMonitors()})

	_, cmd := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter key produced no navigation command")
	}
	msg, ok := cmd().(openMonitorDetailMsg)
	if !ok {
		t.Fatalf("enter key emitted %T, want openMonitorDetailMsg", cmd())
	}
	if msg.monitorID != "01A" {
		t.Errorf("detail navigation carries monitor ID %q, want 01A", msg.monitorID)
	}
}

// TestMonitorListEnterWithNoMonitors verifies the select key is a no-op when
// the list is empty, so navigation never references a missing monitor.
func TestMonitorListEnterWithNoMonitors(t *testing.T) {
	s := newMonitorListScreen(stubClient{})
	s.Update(monitorsLoadedMsg{monitors: nil})

	if _, cmd := s.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd != nil {
		t.Fatalf("enter on an empty list emitted %T, want nil", cmd())
	}
}

// TestMonitorListNewKeyEmitsFormNavigation verifies the new-monitor key emits a
// form-navigation message with no monitor ID, signalling a create.
func TestMonitorListNewKeyEmitsFormNavigation(t *testing.T) {
	s := newMonitorListScreen(stubClient{})
	s.Update(monitorsLoadedMsg{monitors: sampleMonitors()})

	_, cmd := s.Update(runeKey('n'))
	if cmd == nil {
		t.Fatal("new key produced no navigation command")
	}
	msg, ok := cmd().(openMonitorFormMsg)
	if !ok {
		t.Fatalf("new key emitted %T, want openMonitorFormMsg", cmd())
	}
	if msg.monitorID != "" {
		t.Errorf("create navigation carries monitor ID %q, want empty", msg.monitorID)
	}
}

// TestMonitorListEditKeyEmitsFormNavigation verifies the edit key emits a
// form-navigation message carrying the selected monitor's ID.
func TestMonitorListEditKeyEmitsFormNavigation(t *testing.T) {
	s := newMonitorListScreen(stubClient{})
	s.Update(monitorsLoadedMsg{monitors: sampleMonitors()})
	s.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	_, cmd := s.Update(runeKey('e'))
	if cmd == nil {
		t.Fatal("edit key produced no navigation command")
	}
	msg, ok := cmd().(openMonitorFormMsg)
	if !ok {
		t.Fatalf("edit key emitted %T, want openMonitorFormMsg", cmd())
	}
	if msg.monitorID != "01B" {
		t.Errorf("edit navigation carries monitor ID %q, want 01B", msg.monitorID)
	}
}

// TestMonitorListRefreshKey verifies the refresh key re-fetches the list so the
// operator can update a stale view without leaving the screen.
func TestMonitorListRefreshKey(t *testing.T) {
	s := newMonitorListScreen(stubClient{monitors: sampleMonitors()})
	_, cmd := s.Update(runeKey('r'))
	if cmd == nil {
		t.Fatal("refresh key produced no command")
	}
	if _, ok := cmd().(monitorsLoadedMsg); !ok {
		t.Fatalf("refresh key did not re-fetch the list, got %T", cmd())
	}
}

// TestMonitorListViewRendersColumns verifies the View renders the column header
// and one row per monitor, so the operator can read each monitor's config.
func TestMonitorListViewRendersColumns(t *testing.T) {
	s := newMonitorListScreen(stubClient{})
	s.Update(monitorsLoadedMsg{monitors: sampleMonitors()})

	view := s.View()
	for _, want := range []string{"NAME", "TYPE", "INTERVAL", "API", "Website", "1m0s"} {
		if !strings.Contains(view, want) {
			t.Errorf("monitor list view missing %q:\n%s", want, view)
		}
	}
}

// TestMonitorListViewBeforeLoad verifies the screen shows a loading placeholder
// before the first response arrives instead of a blank panel.
func TestMonitorListViewBeforeLoad(t *testing.T) {
	s := newMonitorListScreen(stubClient{})
	if !strings.Contains(s.View(), "loading") {
		t.Errorf("monitor list view before load shows no placeholder:\n%s", s.View())
	}
}

// TestHomeNavigatesToMonitors verifies the home screen opens the monitor list
// on its navigation key, so the list screen is reachable by the operator.
func TestHomeNavigatesToMonitors(t *testing.T) {
	s := newHomeScreen(stubClient{})
	_, cmd := s.Update(runeKey('m'))
	if cmd == nil {
		t.Fatal("monitors navigation key produced no command")
	}
	if _, ok := cmd().(pushScreenMsg); !ok {
		t.Fatalf("home did not push a screen, got %T", cmd())
	}
}
