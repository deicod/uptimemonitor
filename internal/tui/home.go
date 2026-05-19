package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// homeStatusKey opens the dedicated service status screen.
var homeStatusKey = key.NewBinding(
	key.WithKeys("s"),
	key.WithHelp("s", "status"),
)

// homeStatusLoadedMsg delivers the service status fetched by the home screen.
type homeStatusLoadedMsg struct{ status ipc.StatusResponse }

// homeScreen is the initial screen. It fetches and displays the service status
// so the operator can confirm the TUI is connected. Later milestones add the
// monitor and notification screens reachable from here.
type homeScreen struct {
	client Client
	status *ipc.StatusResponse
}

// newHomeScreen builds the home screen bound to client.
func newHomeScreen(client Client) *homeScreen {
	return &homeScreen{client: client}
}

// Init fetches the service status (SPEC §19.3).
func (s *homeScreen) Init() tea.Cmd {
	return fetchStatusCmd(s.client)
}

// Update stores the fetched status and opens the status screen on its key.
func (s *homeScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case homeStatusLoadedMsg:
		status := msg.status
		s.status = &status
	case tea.KeyPressMsg:
		if key.Matches(msg, homeStatusKey) {
			return s, PushScreen(newStatusScreen(s.client))
		}
	}
	return s, nil
}

// View renders the connection state and service status.
func (s *homeScreen) View() string {
	var b strings.Builder
	b.WriteString("uptimemonitor\n\n")
	if s.status == nil {
		b.WriteString("connecting to service…")
		return b.String()
	}
	st := s.status
	fmt.Fprintf(&b, "version:   %s\n", st.Version)
	fmt.Fprintf(&b, "state:     %s\n", st.State)
	fmt.Fprintf(&b, "sqlite:    %s\n", healthLabel(st.SQLite.OK))
	fmt.Fprintf(&b, "tsdb:      %s\n", healthLabel(st.TSDB.OK))
	fmt.Fprintf(&b, "scheduler: running=%t workers=%d\n", st.Scheduler.Running, st.Scheduler.Workers)
	fmt.Fprintf(&b, "monitors:  total=%d active=%d", st.Monitors.Total, st.Monitors.Active)
	return b.String()
}

// Title is the screen name shown in the status bar.
func (s *homeScreen) Title() string { return "Home" }

// healthLabel renders a storage health flag as a word.
func healthLabel(ok bool) string {
	if ok {
		return "ok"
	}
	return "unhealthy"
}

// fetchStatusCmd fetches the service status over IPC (SPEC §19.3).
func fetchStatusCmd(c Client) tea.Cmd {
	return ipcCmd(c.Status, func(st ipc.StatusResponse) tea.Msg {
		return homeStatusLoadedMsg{status: st}
	})
}
