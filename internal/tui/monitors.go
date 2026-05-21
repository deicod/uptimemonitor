package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// monitorsLoadedMsg delivers the monitor list fetched by the monitor list
// screen.
type monitorsLoadedMsg struct{ monitors []ipc.MonitorResponse }

// monitorStateLoadedMsg delivers the live state for a single monitor row,
// derived from that monitor's most recent check (PLAN M7.8).
type monitorStateLoadedMsg struct {
	monitorID string
	state     string
}

// openMonitorDetailMsg requests navigation to the detail screen for a monitor.
// The monitor list screen emits it on the select key; the detail screen
// (PLAN M6.4) handles it.
type openMonitorDetailMsg struct{ monitorID string }

// openMonitorFormMsg requests navigation to the create/edit form. An empty
// monitorID means "create a new monitor". The form screen (PLAN M6.5) handles
// it.
type openMonitorFormMsg struct{ monitorID string }

// Monitor list key bindings, in addition to the global keymap.
var (
	monitorRefreshKey = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))
	monitorUpKey      = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up"))
	monitorDownKey    = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down"))
	monitorOpenKey    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail"))
	monitorNewKey     = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new"))
	monitorEditKey    = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit"))
	monitorDeleteKey  = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
)

// monitorRowStyle highlights the row under the cursor.
var monitorRowStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("231")).
	Background(lipgloss.Color("238"))

// monitorListScreen lists the configured monitors from /v1/monitors and lets
// the operator select one and navigate to its detail screen or to the form.
// Live state and the manual-check key are wired in PLAN M7.8.
type monitorListScreen struct {
	client   Client
	monitors []ipc.MonitorResponse
	states   map[string]string
	cursor   int
	loaded   bool
}

// newMonitorListScreen builds the monitor list screen bound to client.
func newMonitorListScreen(client Client) *monitorListScreen {
	return &monitorListScreen{client: client, states: map[string]string{}}
}

// Init fetches the monitor list (SPEC §19.3).
func (s *monitorListScreen) Init() tea.Cmd { return fetchMonitorsCmd(s.client) }

// Update caches the fetched list and handles selection and navigation keys.
func (s *monitorListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case monitorsLoadedMsg:
		s.monitors = msg.monitors
		s.loaded = true
		s.clampCursor()
		return s, s.fetchStatesCmd()
	case monitorStateLoadedMsg:
		s.states[msg.monitorID] = msg.state
	case monitorsChangedMsg:
		// A create or edit committed: re-fetch so the row reflects the change
		// without the operator pressing refresh.
		return s, fetchMonitorsCmd(s.client)
	case openMonitorDetailMsg:
		return s, PushScreen(newMonitorDetailScreen(s.client, msg.monitorID))
	case openMonitorFormMsg:
		return s, PushScreen(newMonitorFormScreen(s.client, msg.monitorID))
	case tea.KeyPressMsg:
		return s, s.handleKey(msg)
	}
	return s, nil
}

// fetchStatesCmd issues one RecentChecks(limit=1) per monitor in the list, so
// the STATE column reflects each monitor's most recent observation. Returns
// nil when the list is empty so no spurious batch is dispatched.
func (s *monitorListScreen) fetchStatesCmd() tea.Cmd {
	if len(s.monitors) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(s.monitors))
	for _, m := range s.monitors {
		cmds = append(cmds, fetchMonitorStateCmd(s.client, m.ID))
	}
	return tea.Batch(cmds...)
}

// fetchMonitorStateCmd fetches the latest check for monitorID and emits a
// monitorStateLoadedMsg with its state, or "unknown" when no check exists yet.
func fetchMonitorStateCmd(c Client, monitorID string) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) ([]ipc.CheckResultResponse, error) {
			return c.RecentChecks(ctx, monitorID, 1)
		},
		func(checks []ipc.CheckResultResponse) tea.Msg {
			state := "unknown"
			if len(checks) > 0 {
				state = checks[0].State
			}
			return monitorStateLoadedMsg{monitorID: monitorID, state: state}
		},
	)
}

// handleKey applies a key press to the screen and returns any resulting
// command (a re-fetch or a navigation message).
func (s *monitorListScreen) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, monitorRefreshKey):
		return fetchMonitorsCmd(s.client)
	case key.Matches(msg, monitorUpKey):
		if s.cursor > 0 {
			s.cursor--
		}
	case key.Matches(msg, monitorDownKey):
		if s.cursor < len(s.monitors)-1 {
			s.cursor++
		}
	case key.Matches(msg, monitorOpenKey):
		if m, ok := s.selected(); ok {
			id := m.ID
			return func() tea.Msg { return openMonitorDetailMsg{monitorID: id} }
		}
	case key.Matches(msg, monitorNewKey):
		return func() tea.Msg { return openMonitorFormMsg{} }
	case key.Matches(msg, monitorEditKey):
		if m, ok := s.selected(); ok {
			id := m.ID
			return func() tea.Msg { return openMonitorFormMsg{monitorID: id} }
		}
	case key.Matches(msg, monitorDeleteKey):
		if m, ok := s.selected(); ok {
			return s.openDeleteConfirm(m)
		}
	case key.Matches(msg, monitorManualCheckKey):
		if m, ok := s.selected(); ok {
			return s.runManualCheckCmd(m.ID)
		}
	}
	return nil
}

// runManualCheckCmd triggers a manual check for monitorID and then refreshes
// the list's state map so the row reflects the new observation without the
// operator pressing refresh (PLAN M7.8).
func (s *monitorListScreen) runManualCheckCmd(monitorID string) tea.Cmd {
	client := s.client
	return tea.Sequence(
		ipcCmd(
			func(ctx context.Context) (ipc.RunMonitorResponse, error) {
				return client.RunMonitor(ctx, monitorID)
			},
			func(ipc.RunMonitorResponse) tea.Msg { return monitorManualCheckQueuedMsg{} },
		),
		fetchMonitorStateCmd(client, monitorID),
	)
}

// openDeleteConfirm pushes a confirmation dialog naming the monitor that is
// about to be deleted (SPEC §19.4); confirming runs the delete over IPC.
func (s *monitorListScreen) openDeleteConfirm(m ipc.MonitorResponse) tea.Cmd {
	prompt := fmt.Sprintf("Delete monitor %q? This cannot be undone.", m.Name)
	return PushScreen(newConfirmScreen("Delete monitor", prompt, deleteMonitorCmd(s.client, m.ID)))
}

// selected returns the monitor under the cursor; ok is false when the list is
// empty.
func (s *monitorListScreen) selected() (ipc.MonitorResponse, bool) {
	if s.cursor < 0 || s.cursor >= len(s.monitors) {
		return ipc.MonitorResponse{}, false
	}
	return s.monitors[s.cursor], true
}

// clampCursor keeps the cursor within the bounds of the current list, so a
// shrinking refresh never leaves it pointing past the end.
func (s *monitorListScreen) clampCursor() {
	if s.cursor >= len(s.monitors) {
		s.cursor = len(s.monitors) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// View renders the monitor list with the column header and a cursor.
func (s *monitorListScreen) View() string {
	var b strings.Builder
	b.WriteString("monitors\n\n")
	if !s.loaded {
		b.WriteString("loading monitors…")
		return b.String()
	}
	if len(s.monitors) == 0 {
		b.WriteString("no monitors — press n to create one")
		return b.String()
	}
	fmt.Fprintf(&b, "  %-28s %-6s %-10s %-8s %-8s %s\n",
		"NAME", "TYPE", "INTERVAL", "STATE", "ENABLED", "NOTIFY")
	for i, m := range s.monitors {
		row := fmt.Sprintf("%-28s %-6s %-10s %-8s %-8s %s",
			truncate(m.Name, 28), m.Type,
			time.Duration(m.Interval).String(),
			s.stateFor(m), yesNo(m.Enabled), yesNo(m.NotificationsEnabled))
		cursor := "  "
		if i == s.cursor {
			cursor = "› "
			row = monitorRowStyle.Render(row)
		}
		b.WriteString(cursor)
		b.WriteString(row)
		b.WriteString("\n")
	}
	b.WriteString("\n↑/↓ move • enter detail • n new • e edit • c check now • d delete • r refresh")
	return b.String()
}

// stateFor returns the live state to render for monitor m. The state map is
// populated asynchronously after the list load (PLAN M7.8); until it arrives,
// the row falls back to paused for disabled monitors and a dash placeholder
// otherwise, so the column never appears blank.
func (s *monitorListScreen) stateFor(m ipc.MonitorResponse) string {
	if st, ok := s.states[m.ID]; ok {
		return st
	}
	if !m.Enabled {
		return "paused"
	}
	return "—"
}

// Title is the screen name shown in the status bar.
func (s *monitorListScreen) Title() string { return "Monitors" }

// truncate shortens s to at most n runes, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// yesNo renders a bool as a compact word for the list columns.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// deleteMonitorCmd deletes a monitor over IPC. On success it emits
// monitorsChangedMsg so the list re-fetches after the confirm screen pops; on
// failure it emits an errMsg for the root error dialog (SPEC §19.3–19.4).
func deleteMonitorCmd(c Client, id string) tea.Cmd {
	return func() tea.Msg {
		if err := c.DeleteMonitor(context.Background(), id); err != nil {
			return errMsg{err: err}
		}
		return monitorsChangedMsg{}
	}
}

// fetchMonitorsCmd fetches the monitor list over IPC (SPEC §19.3).
func fetchMonitorsCmd(c Client) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) ([]ipc.MonitorResponse, error) {
			return c.ListMonitors(ctx, ipc.MonitorListFilter{})
		},
		func(ms []ipc.MonitorResponse) tea.Msg { return monitorsLoadedMsg{monitors: ms} },
	)
}
