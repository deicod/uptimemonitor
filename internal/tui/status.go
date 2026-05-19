package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// statusLoadedMsg delivers the service status fetched by the status screen.
type statusLoadedMsg struct{ status ipc.StatusResponse }

// statusRefreshKey re-fetches the service status without leaving the screen.
var statusRefreshKey = key.NewBinding(
	key.WithKeys("r"),
	key.WithHelp("r", "refresh"),
)

// statusScreen shows the full service status from /v1/status (SPEC §10.5) so
// the operator can inspect version, storage health, the scheduler, and monitor
// counts on a dedicated screen.
type statusScreen struct {
	client Client
	status *ipc.StatusResponse
}

// newStatusScreen builds the status screen bound to client.
func newStatusScreen(client Client) *statusScreen {
	return &statusScreen{client: client}
}

// Init fetches the service status (SPEC §19.3).
func (s *statusScreen) Init() tea.Cmd {
	return fetchServiceStatusCmd(s.client)
}

// Update stores the fetched status and re-fetches it on the refresh key.
func (s *statusScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case statusLoadedMsg:
		status := msg.status
		s.status = &status
	case tea.KeyPressMsg:
		if key.Matches(msg, statusRefreshKey) {
			return s, fetchServiceStatusCmd(s.client)
		}
	}
	return s, nil
}

// View renders every SPEC §10.5 status field.
func (s *statusScreen) View() string {
	var b strings.Builder
	b.WriteString("service status\n\n")
	if s.status == nil {
		b.WriteString("connecting to service…")
		return b.String()
	}
	st := s.status
	fmt.Fprintf(&b, "version:    %s\n", st.Version)
	fmt.Fprintf(&b, "state:      %s\n", st.State)
	fmt.Fprintf(&b, "started:    %s\n", formatStarted(st.StartedAt))
	fmt.Fprintf(&b, "uptime:     %s\n", formatUptime(st.StartedAt))
	fmt.Fprintf(&b, "sqlite:     %s\n", healthLabel(st.SQLite.OK))
	fmt.Fprintf(&b, "tsdb:       %s\n", healthLabel(st.TSDB.OK))
	fmt.Fprintf(&b, "scheduler:  running=%t workers=%d\n", st.Scheduler.Running, st.Scheduler.Workers)
	fmt.Fprintf(&b, "monitors:   total=%d active=%d\n", st.Monitors.Total, st.Monitors.Active)
	b.WriteString("\nr refresh")
	return b.String()
}

// Title is the screen name shown in the status bar.
func (s *statusScreen) Title() string { return "Status" }

// formatStarted renders the service start time, or a dash when unset.
func formatStarted(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format(time.RFC3339)
}

// formatUptime renders the elapsed time since the service started, or a dash
// when the start time is unset.
func formatUptime(started time.Time) string {
	if started.IsZero() {
		return "—"
	}
	return time.Since(started).Round(time.Second).String()
}

// fetchServiceStatusCmd fetches the service status over IPC (SPEC §19.3).
func fetchServiceStatusCmd(c Client) tea.Cmd {
	return ipcCmd(c.Status, func(st ipc.StatusResponse) tea.Msg {
		return statusLoadedMsg{status: st}
	})
}
