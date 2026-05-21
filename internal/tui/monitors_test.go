package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// deleteClient records the monitor ID a delete reached, and optionally
// returns an error from DeleteMonitor — letting tests verify both the success
// and failure paths of the confirm-then-delete flow.
type deleteClient struct {
	stubClient
	deletedID string
	deleteErr error
}

var _ Client = (*deleteClient)(nil)

func (c *deleteClient) DeleteMonitor(_ context.Context, id string) error {
	c.deletedID = id
	return c.deleteErr
}

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

// TestMonitorListDeleteKeyOpensConfirm verifies the delete key pushes a
// confirmation dialog naming the selected monitor, so a destructive delete
// always passes through confirmation (SPEC §19.4).
func TestMonitorListDeleteKeyOpensConfirm(t *testing.T) {
	dc := &deleteClient{stubClient: stubClient{monitors: sampleMonitors()}}
	s := newMonitorListScreen(dc)
	s.Update(monitorsLoadedMsg{monitors: sampleMonitors()})

	_, cmd := s.Update(runeKey('d'))
	if cmd == nil {
		t.Fatal("delete key produced no command")
	}
	push, ok := cmd().(pushScreenMsg)
	if !ok {
		t.Fatalf("delete key emitted %T, want pushScreenMsg", cmd())
	}
	cs, ok := push.screen.(*confirmScreen)
	if !ok {
		t.Fatalf("delete key pushed %T, want *confirmScreen", push.screen)
	}
	if !strings.Contains(cs.View(), "API") {
		t.Errorf("confirm prompt does not name the selected monitor:\n%s", cs.View())
	}
	if dc.deletedID != "" {
		t.Error("delete reached the service before confirmation")
	}
}

// TestMonitorListDeleteKeyEmptyListIsNoop verifies the delete key does nothing
// when no monitor is selected, so the dialog cannot open with no target.
func TestMonitorListDeleteKeyEmptyListIsNoop(t *testing.T) {
	s := newMonitorListScreen(&deleteClient{})
	s.Update(monitorsLoadedMsg{monitors: nil})

	if _, cmd := s.Update(runeKey('d')); cmd != nil {
		t.Fatalf("delete on an empty list emitted %T, want nil", cmd())
	}
}

// TestDeleteMonitorCmdSuccess verifies a confirmed delete actually calls the
// service and signals the list to re-fetch, so a successful delete removes the
// row without the operator pressing refresh.
func TestDeleteMonitorCmdSuccess(t *testing.T) {
	dc := &deleteClient{}
	cmd := deleteMonitorCmd(dc, "01A")
	msg := cmd()
	if _, ok := msg.(monitorsChangedMsg); !ok {
		t.Fatalf("delete success produced %T, want monitorsChangedMsg", msg)
	}
	if dc.deletedID != "01A" {
		t.Errorf("delete called with id %q, want 01A", dc.deletedID)
	}
}

// TestDeleteMonitorCmdFailure verifies an IPC failure on delete is surfaced
// through the error dialog (SPEC §19.4) rather than swallowed.
func TestDeleteMonitorCmdFailure(t *testing.T) {
	dc := &deleteClient{deleteErr: errors.New("boom")}
	cmd := deleteMonitorCmd(dc, "01A")
	em, ok := cmd().(errMsg)
	if !ok {
		t.Fatalf("delete failure produced %T, want errMsg", cmd())
	}
	if em.err == nil || !strings.Contains(em.err.Error(), "boom") {
		t.Errorf("errMsg does not carry the underlying error: %v", em.err)
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
	for _, want := range []string{"NAME", "TYPE", "INTERVAL", "STATE", "API", "Website", "1m0s"} {
		if !strings.Contains(view, want) {
			t.Errorf("monitor list view missing %q:\n%s", want, view)
		}
	}
}

// TestMonitorListShowsLiveState verifies each monitor row reflects the latest
// observed state once the per-monitor recent-checks fetches return, so the
// operator can see at a glance which monitors are up or down (PLAN M7.8).
func TestMonitorListShowsLiveState(t *testing.T) {
	s := newMonitorListScreen(stubClient{})
	s.Update(monitorsLoadedMsg{monitors: sampleMonitors()})

	s.Update(monitorStateLoadedMsg{monitorID: "01A", state: "up"})
	s.Update(monitorStateLoadedMsg{monitorID: "01B", state: "down"})

	view := s.View()
	apiLine, websiteLine := lineFor(view, "API"), lineFor(view, "Website")
	if !strings.Contains(apiLine, "up") {
		t.Errorf("API row does not show state up:\n%s", apiLine)
	}
	if !strings.Contains(websiteLine, "down") {
		t.Errorf("Website row does not show state down:\n%s", websiteLine)
	}
}

// lineFor finds the first line in s containing needle. Used to assert per-row
// content without depending on column widths or whitespace.
func lineFor(s, needle string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

// TestMonitorListFetchesStateOnLoad verifies that after the monitor list is
// loaded, the screen issues a per-monitor recent-checks lookup so the state
// column can be populated without operator action (PLAN M7.8).
func TestMonitorListFetchesStateOnLoad(t *testing.T) {
	rc := &runRecordingClient{stubClient: stubClient{
		checks: []ipc.CheckResultResponse{{State: "up"}},
	}}
	s := newMonitorListScreen(rc)

	_, cmd := s.Update(monitorsLoadedMsg{monitors: sampleMonitors()})
	if cmd == nil {
		t.Fatal("loading monitors did not schedule a state fetch")
	}
	// The screen emits a batch of per-monitor RecentChecks commands.
	for _, c := range collectCmds(cmd) {
		c()
	}
	if len(rc.checkIDs) != 2 {
		t.Errorf("expected one RecentChecks call per monitor, got %d (%v)", len(rc.checkIDs), rc.checkIDs)
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
