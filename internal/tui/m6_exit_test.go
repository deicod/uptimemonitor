package tui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// m6Client is a tracking fake client for the M6 exit check. It records every
// mutating call (create, update, delete) so the test can assert the full TUI
// lifecycle reached the service layer.
type m6Client struct {
	stubClient
	mu      sync.Mutex
	created []ipc.CreateMonitorRequest
	updated []ipc.UpdateMonitorRequest
	deleted []string

	monitors []ipc.MonitorResponse

	incidents []ipc.IncidentResponse
	events    []ipc.EventResponse

	nextID int
}

var _ Client = (*m6Client)(nil)

func (c *m6Client) Status(context.Context) (ipc.StatusResponse, error) {
	return ipc.StatusResponse{Version: "test", State: "ready"}, nil
}

func (c *m6Client) ListMonitors(context.Context, ipc.MonitorListFilter) ([]ipc.MonitorResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.monitors, nil
}

func (c *m6Client) GetMonitor(_ context.Context, id string) (ipc.MonitorResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.monitors {
		if m.ID == id {
			return m, nil
		}
	}
	return ipc.MonitorResponse{}, nil
}

func (c *m6Client) CreateMonitor(_ context.Context, req ipc.CreateMonitorRequest) (ipc.MonitorResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	m := ipc.MonitorResponse{
		ID:                   fmt.Sprintf("m%d", c.nextID),
		Name:                 req.Name,
		Type:                 req.Type,
		Enabled:              req.Enabled,
		Interval:             req.Interval,
		Timeout:              req.Timeout,
		Config:               req.Config,
		NotificationsEnabled: req.NotificationsEnabled,
	}
	c.monitors = append(c.monitors, m)
	c.created = append(c.created, req)
	return m, nil
}

func (c *m6Client) UpdateMonitor(_ context.Context, id string, req ipc.UpdateMonitorRequest) (ipc.MonitorResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, m := range c.monitors {
		if m.ID == id {
			if req.Name != nil {
				m.Name = *req.Name
			}
			if req.Interval != nil {
				m.Interval = *req.Interval
			}
			if req.Timeout != nil {
				m.Timeout = *req.Timeout
			}
			if req.Config != nil {
				m.Config = req.Config
			}
			if req.NotificationsEnabled != nil {
				m.NotificationsEnabled = *req.NotificationsEnabled
			}
			c.monitors[i] = m
			c.updated = append(c.updated, req)
			return m, nil
		}
	}
	c.updated = append(c.updated, req)
	return ipc.MonitorResponse{}, nil
}

func (c *m6Client) DeleteMonitor(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, m := range c.monitors {
		if m.ID == id {
			c.monitors = append(c.monitors[:i], c.monitors[i+1:]...)
			break
		}
	}
	c.deleted = append(c.deleted, id)
	return nil
}

func (c *m6Client) ListMonitorIncidents(_ context.Context, _ string) ([]ipc.IncidentResponse, error) {
	return c.incidents, nil
}

func (c *m6Client) ListMonitorEvents(_ context.Context, _ string) ([]ipc.EventResponse, error) {
	return c.events, nil
}

// TestM6ExitCheck exercises the full TUI lifecycle (SPEC §28): a user can
// create, view, edit, and delete an HTTP monitor in the TUI, with destructive
// actions confirmed. It drives the root Model with a tracking fake client,
// verifying the screen stack, navigation, and IPC command wiring end to end
// without a running service.
func TestM6ExitCheck(t *testing.T) {
	fc := &m6Client{
		incidents: []ipc.IncidentResponse{
			{ID: "i1", MonitorID: "m1", StartedAt: time.Now().Add(-time.Hour), Reason: "probe failed"},
		},
		events: []ipc.EventResponse{
			{ID: "e1", Type: "monitor_created", CreatedAt: time.Now().Add(-2 * time.Hour)},
		},
	}

	m := newModel(fc)

	// --- Step 0: Home screen loads status ---
	m = drain(t, m, m.top().Init())
	assertNoError(t, m)
	if !strings.Contains(m.top().View(), "test") {
		t.Fatalf("home screen does not show the fetched version:\n%s", m.top().View())
	}

	// --- Step 1: Navigate to monitor list (press 'm') ---
	m = sendKey(t, m, runeKey('m'))
	assertScreen(t, m, "Monitors")
	// The list screen's Init fires a fetch. Drain it.
	m = drain(t, m, m.top().Init())
	if !strings.Contains(m.top().View(), "no monitors") {
		t.Fatalf("monitor list should show 'no monitors' before create:\n%s", m.top().View())
	}

	// --- Step 2: Open the create form (press 'n') ---
	m = sendKey(t, m, runeKey('n'))
	assertScreen(t, m, "New Monitor")

	// Drain the form Init (focus blink).
	fs := m.top().(*monitorFormScreen)
	m = drain(t, m, fs.Init())
	fs = m.top().(*monitorFormScreen)
	fs.setText("name", "My Website")
	fs.setText("url", "https://example.com")

	// --- Step 3: Submit the form ---
	_, cmd := fs.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("form submit produced no command")
	}
	msg := cmd()
	if _, ok := msg.(monitorSavedMsg); !ok {
		t.Fatalf("create command produced %T, want monitorSavedMsg", msg)
	}

	// Feed monitorSavedMsg through the root model. The form's Update produces
	// tea.Sequence(PopScreen, emitMonitorsChanged); drain handles both.
	m, cmd = stepUpdate(m, msg)
	m = drain(t, m, cmd)
	assertScreen(t, m, "Monitors")

	if len(fc.created) != 1 {
		t.Fatalf("expected 1 create call, got %d", len(fc.created))
	}
	if fc.created[0].Name != "My Website" {
		t.Errorf("created monitor name = %q, want My Website", fc.created[0].Name)
	}

	// --- Step 4: View the created monitor (detail) ---
	// Re-fetch the list with the created monitor.
	ls := m.top().(*monitorListScreen)
	m, _ = stepUpdate(m, monitorsLoadedMsg{monitors: fc.monitors})

	// Press enter → openMonitorDetailMsg → pushScreenMsg.
	m = sendKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	assertScreen(t, m, "Monitor")

	// Drain the detail Init (batched fetch of monitor + incidents + events).
	ds := m.top().(*monitorDetailScreen)
	m = execBatchToModel(t, m, ds.Init())
	ds = m.top().(*monitorDetailScreen)

	if ds.monitor == nil || ds.monitor.Name != "My Website" {
		t.Fatalf("detail screen did not load the monitor:\n%s", ds.View())
	}
	view := ds.View()
	for _, want := range []string{"My Website", "https://example.com"} {
		if !strings.Contains(view, want) {
			t.Errorf("detail view missing %q:\n%s", want, view)
		}
	}

	// --- Step 5: Go back, then edit the monitor ---
	m, _ = stepUpdate(m, popScreenMsg{})
	assertScreen(t, m, "Monitors")

	ls = m.top().(*monitorListScreen)
	m, _ = stepUpdate(m, monitorsLoadedMsg{monitors: fc.monitors})

	// Press 'e' to edit.
	m = sendKey(t, m, runeKey('e'))
	assertScreen(t, m, "Edit Monitor")

	// Drain the edit form Init (fetch + focus).
	fs = m.top().(*monitorFormScreen)
	m = drain(t, m, fs.Init())
	fs = m.top().(*monitorFormScreen)

	if !fs.loaded {
		t.Fatal("edit form did not load")
	}
	if fs.text("name") != "My Website" {
		t.Errorf("edit form name = %q, want My Website", fs.text("name"))
	}

	// Change the name and submit.
	fs.setText("name", "My Website 2")
	_, cmd = fs.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("edit submit produced no command")
	}
	msg = cmd()
	if _, ok := msg.(monitorSavedMsg); !ok {
		t.Fatalf("update command produced %T, want monitorSavedMsg", msg)
	}

	// Feed monitorSavedMsg through root model → pop + monitorsChanged.
	m, cmd = stepUpdate(m, msg)
	m = drain(t, m, cmd)
	assertScreen(t, m, "Monitors")

	if len(fc.updated) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(fc.updated))
	}
	if fc.updated[0].Name == nil || *fc.updated[0].Name != "My Website 2" {
		t.Errorf("updated name = %v, want My Website 2", fc.updated[0].Name)
	}

	// --- Step 6: Delete with confirmation ---
	ls = m.top().(*monitorListScreen)
	m, _ = stepUpdate(m, monitorsLoadedMsg{monitors: fc.monitors})

	// Press 'd' → confirm screen pushed.
	_, cmd = ls.Update(runeKey('d'))
	if cmd == nil {
		t.Fatal("delete key produced no command")
	}
	delPushMsg := cmd().(pushScreenMsg)
	m, _ = stepUpdate(m, delPushMsg)
	assertScreen(t, m, "Confirm")

	cs := m.top().(*confirmScreen)
	if !strings.Contains(cs.View(), "My Website") {
		t.Errorf("confirm dialog does not name the monitor:\n%s", cs.View())
	}

	// Confirm: 'y' key through root model → PopScreen + deleteMonitorCmd.
	m, cmd = stepUpdate(m, runeKey('y'))
	m = drain(t, m, cmd)
	assertScreen(t, m, "Monitors")

	if len(fc.deleted) != 1 || fc.deleted[0] != "m1" {
		t.Fatalf("expected delete of m1, got %v", fc.deleted)
	}

	// Load the now-empty list.
	ls = m.top().(*monitorListScreen)
	m, _ = stepUpdate(m, monitorsLoadedMsg{monitors: fc.monitors})

	view = ls.View()
	if !strings.Contains(view, "no monitors") {
		t.Errorf("after delete, list should be empty:\n%s", view)
	}

	// --- Final assertion: the full lifecycle ---
	if len(fc.created) != 1 {
		t.Errorf("lifecycle created %d monitors, want 1", len(fc.created))
	}
	if len(fc.updated) != 1 {
		t.Errorf("lifecycle updated %d monitors, want 1", len(fc.updated))
	}
	if len(fc.deleted) != 1 {
		t.Errorf("lifecycle deleted %d monitors, want 1", len(fc.deleted))
	}
}

// stepUpdate calls m.Update and casts back to Model.
func stepUpdate(m Model, msg tea.Msg) (Model, tea.Cmd) {
	model, cmd := m.Update(msg)
	return model.(Model), cmd
}

// sendKey sends a key press through the root model and drains all resulting
// navigation/IPC commands until no more remain. This simulates the Bubble Tea
// runtime processing a single key press.
func sendKey(t *testing.T, m Model, key tea.KeyPressMsg) Model {
	t.Helper()
	m, cmd := stepUpdate(m, key)
	return drain(t, m, cmd)
}

// drain repeatedly executes commands through the root model until no more
// commands are produced.
func drain(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for cmd != nil {
		msg := cmd()
		switch v := msg.(type) {
		case pushScreenMsg:
			var next tea.Cmd
			m, next = stepUpdate(m, v)
			m = drain(t, m, next)
			m = drain(t, m, m.top().Init())
		case popScreenMsg:
			var next tea.Cmd
			m, next = stepUpdate(m, v)
			m = drain(t, m, next)
		case tea.BatchMsg:
			for _, c := range v {
				m = drain(t, m, c)
			}
		default:
			// tea.Sequence produces an unexported sequenceMsg which is []tea.Cmd.
			// Try to detect it via a type assertion on the underlying slice.
			if cmds, ok := toCmdSlice(msg); ok {
				for _, c := range cmds {
					m = drain(t, m, c)
				}
			} else {
				var next tea.Cmd
				m, next = stepUpdate(m, v)
				m = drain(t, m, next)
			}
		}
		cmd = nil
	}
	return m
}

// toCmdSlice uses reflect to detect the unexported sequenceMsg type from
// tea.Sequence, which is []tea.Cmd under the hood.
func toCmdSlice(msg tea.Msg) ([]tea.Cmd, bool) {
	// The runtime type name for sequenceMsg includes the package path.
	// Use reflect to match the underlying slice-of-func type.
	import_reflect := reflect.TypeOf(msg)
	if import_reflect == nil {
		return nil, false
	}
	if import_reflect.Kind() != reflect.Slice {
		return nil, false
	}
	if import_reflect.Elem().Kind() != reflect.Func {
		return nil, false
	}
	val := reflect.ValueOf(msg)
	cmds := make([]tea.Cmd, val.Len())
	for i := 0; i < val.Len(); i++ {
		cmds[i] = val.Index(i).Interface().(tea.Cmd)
	}
	return cmds, true
}

// execBatchToModel runs a batched Init command against the model's top screen.
func execBatchToModel(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	for _, c := range collectCmds(cmd) {
		var next tea.Cmd
		top, next := m.top().Update(c())
		m.screens[len(m.screens)-1] = top
		for _, inner := range collectCmds(next) {
			top, _ = m.top().Update(inner())
			m.screens[len(m.screens)-1] = top
		}
	}
	return m
}

func assertScreen(t *testing.T, m Model, want string) {
	t.Helper()
	if got := m.top().Title(); got != want {
		t.Fatalf("active screen = %q, want %q", got, want)
	}
}

func assertNoError(t *testing.T, m Model) {
	t.Helper()
	if m.err != nil {
		t.Fatalf("unexpected error dialog: %v", m.err)
	}
}
