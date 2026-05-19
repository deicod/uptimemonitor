package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// monitorDetailLoadedMsg delivers the monitor fetched by the detail screen.
type monitorDetailLoadedMsg struct{ monitor ipc.MonitorResponse }

// monitorIncidentsLoadedMsg delivers the monitor's incidents.
type monitorIncidentsLoadedMsg struct{ incidents []ipc.IncidentResponse }

// monitorEventsLoadedMsg delivers the monitor's events.
type monitorEventsLoadedMsg struct{ events []ipc.EventResponse }

// detailRecentLimit caps the incident and event lists rendered on the detail
// screen so a long-running monitor does not flood the panel.
const detailRecentLimit = 10

// monitorDetailScreen shows a single monitor's configuration, current state,
// recent checks, incidents, and events (SPEC §12.2). It loads the monitor and
// its incident/event history over IPC. History (M8) and the notification
// summary (M9) are placeholders until those milestones land; current state and
// recent checks become live in M7.8.
type monitorDetailScreen struct {
	client    Client
	monitorID string
	monitor   *ipc.MonitorResponse
	incidents []ipc.IncidentResponse
	events    []ipc.EventResponse
}

// newMonitorDetailScreen builds the detail screen for monitorID, bound to
// client.
func newMonitorDetailScreen(client Client, monitorID string) *monitorDetailScreen {
	return &monitorDetailScreen{client: client, monitorID: monitorID}
}

// Init fetches the monitor and its incidents and events concurrently
// (SPEC §19.3).
func (s *monitorDetailScreen) Init() tea.Cmd { return s.fetchAll() }

// fetchAll batches the three independent IPC reads the screen needs.
func (s *monitorDetailScreen) fetchAll() tea.Cmd {
	return tea.Batch(
		fetchMonitorCmd(s.client, s.monitorID),
		fetchMonitorIncidentsCmd(s.client, s.monitorID),
		fetchMonitorEventsCmd(s.client, s.monitorID),
	)
}

// Update caches the fetched data and re-fetches it on the refresh key.
func (s *monitorDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case monitorDetailLoadedMsg:
		m := msg.monitor
		s.monitor = &m
	case monitorIncidentsLoadedMsg:
		s.incidents = msg.incidents
	case monitorEventsLoadedMsg:
		s.events = msg.events
	case tea.KeyPressMsg:
		if key.Matches(msg, monitorRefreshKey) {
			return s, s.fetchAll()
		}
	}
	return s, nil
}

// View renders the monitor's configuration, state, incidents, and events.
func (s *monitorDetailScreen) View() string {
	var b strings.Builder
	b.WriteString("monitor detail\n\n")
	if s.monitor == nil {
		b.WriteString("loading monitor…")
		return b.String()
	}
	m := s.monitor

	fmt.Fprintf(&b, "name:      %s\n", m.Name)
	fmt.Fprintf(&b, "id:        %s\n", m.ID)
	fmt.Fprintf(&b, "type:      %s\n", m.Type)
	fmt.Fprintf(&b, "enabled:   %s\n", yesNo(m.Enabled))
	fmt.Fprintf(&b, "interval:  %s\n", time.Duration(m.Interval))
	fmt.Fprintf(&b, "timeout:   %s\n", time.Duration(m.Timeout))
	fmt.Fprintf(&b, "notify:    %s\n", yesNo(m.NotificationsEnabled))
	fmt.Fprintf(&b, "config:    %s\n", configText(m.Config))

	b.WriteString("\nstate:     (live state shown from M7.8)\n")
	b.WriteString("checks:    (recent checks shown from M7.8)\n")
	b.WriteString("history:   (history shown from M8)\n")
	b.WriteString("notify:    (notification summary shown from M9)\n")

	b.WriteString("\nincidents\n")
	b.WriteString(renderIncidents(s.incidents))

	b.WriteString("\nevents\n")
	b.WriteString(renderEvents(s.events))

	b.WriteString("\nr refresh • esc back")
	return b.String()
}

// Title is the screen name shown in the status bar.
func (s *monitorDetailScreen) Title() string { return "Monitor" }

// configText renders the monitor's raw JSON config, or a dash when empty.
func configText(cfg []byte) string {
	if len(cfg) == 0 || string(cfg) == "null" {
		return "—"
	}
	return string(cfg)
}

// renderIncidents formats up to detailRecentLimit incidents, most recent first
// as returned by the service.
func renderIncidents(incidents []ipc.IncidentResponse) string {
	if len(incidents) == 0 {
		return "  none\n"
	}
	var b strings.Builder
	for i, in := range incidents {
		if i >= detailRecentLimit {
			break
		}
		fmt.Fprintf(&b, "  %s  %s  %s\n",
			in.StartedAt.Format(time.RFC3339), incidentState(in), in.Reason)
	}
	return b.String()
}

// incidentState reports whether an incident is still open or has resolved.
func incidentState(in ipc.IncidentResponse) string {
	if in.ResolvedAt == nil {
		return "open"
	}
	return "resolved"
}

// renderEvents formats up to detailRecentLimit events, most recent first as
// returned by the service.
func renderEvents(events []ipc.EventResponse) string {
	if len(events) == 0 {
		return "  none\n"
	}
	var b strings.Builder
	for i, e := range events {
		if i >= detailRecentLimit {
			break
		}
		fmt.Fprintf(&b, "  %s  %s\n", e.CreatedAt.Format(time.RFC3339), e.Type)
	}
	return b.String()
}

// fetchMonitorCmd fetches a single monitor over IPC (SPEC §19.3).
func fetchMonitorCmd(c Client, id string) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) (ipc.MonitorResponse, error) {
			return c.GetMonitor(ctx, id)
		},
		func(m ipc.MonitorResponse) tea.Msg { return monitorDetailLoadedMsg{monitor: m} },
	)
}

// fetchMonitorIncidentsCmd fetches a monitor's incidents over IPC.
func fetchMonitorIncidentsCmd(c Client, id string) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) ([]ipc.IncidentResponse, error) {
			return c.ListMonitorIncidents(ctx, id)
		},
		func(in []ipc.IncidentResponse) tea.Msg { return monitorIncidentsLoadedMsg{incidents: in} },
	)
}

// fetchMonitorEventsCmd fetches a monitor's events over IPC.
func fetchMonitorEventsCmd(c Client, id string) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) ([]ipc.EventResponse, error) {
			return c.ListMonitorEvents(ctx, id)
		},
		func(ev []ipc.EventResponse) tea.Msg { return monitorEventsLoadedMsg{events: ev} },
	)
}
