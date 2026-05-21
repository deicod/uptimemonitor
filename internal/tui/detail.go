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

// monitorChecksLoadedMsg delivers the monitor's recent check_results, ordered
// most-recent-first. The first entry is treated as the live state of the
// monitor on the detail screen (PLAN M7.8).
type monitorChecksLoadedMsg struct{ checks []ipc.CheckResultResponse }

// detailRecentLimit caps the incident, event, and check lists rendered on the
// detail screen so a long-running monitor does not flood the panel.
const detailRecentLimit = 10

// monitorManualCheckKey triggers an out-of-band check via POST
// /v1/monitors/{id}/run and refreshes the screen so its outcome appears
// without operator action (SPEC §10.5; PLAN M7.8).
var monitorManualCheckKey = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "check now"))

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
	checks    []ipc.CheckResultResponse
}

// newMonitorDetailScreen builds the detail screen for monitorID, bound to
// client.
func newMonitorDetailScreen(client Client, monitorID string) *monitorDetailScreen {
	return &monitorDetailScreen{client: client, monitorID: monitorID}
}

// Init fetches the monitor and its incidents and events concurrently
// (SPEC §19.3).
func (s *monitorDetailScreen) Init() tea.Cmd { return s.fetchAll() }

// fetchAll batches the four independent IPC reads the screen needs.
func (s *monitorDetailScreen) fetchAll() tea.Cmd {
	return tea.Batch(
		fetchMonitorCmd(s.client, s.monitorID),
		fetchMonitorIncidentsCmd(s.client, s.monitorID),
		fetchMonitorEventsCmd(s.client, s.monitorID),
		fetchMonitorChecksCmd(s.client, s.monitorID, detailRecentLimit),
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
	case monitorChecksLoadedMsg:
		s.checks = msg.checks
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, monitorRefreshKey):
			return s, s.fetchAll()
		case key.Matches(msg, monitorManualCheckKey):
			return s, s.runManualCheck()
		}
	}
	return s, nil
}

// runManualCheck triggers a manual check via IPC and, on success, refreshes
// the screen so the new check appears in the recent-checks list. Failures are
// routed to the error dialog by ipcCmd (SPEC §19.3).
func (s *monitorDetailScreen) runManualCheck() tea.Cmd {
	id := s.monitorID
	client := s.client
	return tea.Sequence(
		ipcCmd(
			func(ctx context.Context) (ipc.RunMonitorResponse, error) {
				return client.RunMonitor(ctx, id)
			},
			func(ipc.RunMonitorResponse) tea.Msg { return monitorManualCheckQueuedMsg{} },
		),
		s.fetchAll(),
	)
}

// monitorManualCheckQueuedMsg is the marker sent after a successful manual
// trigger. It carries no payload — the screen only needs to know the run was
// accepted so the subsequent refresh is meaningful.
type monitorManualCheckQueuedMsg struct{}

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

	fmt.Fprintf(&b, "\nstate:     %s\n", liveState(m, s.checks))
	b.WriteString("history:   (history shown from M8)\n")
	b.WriteString("notify:    (notification summary shown from M9)\n")

	b.WriteString("\nrecent checks\n")
	b.WriteString(renderChecks(s.checks))

	b.WriteString("\nincidents\n")
	b.WriteString(renderIncidents(s.incidents))

	b.WriteString("\nevents\n")
	b.WriteString(renderEvents(s.events))

	b.WriteString("\nr refresh • c check now • esc back")
	return b.String()
}

// liveState returns the live state to render for the monitor: the most recent
// check's state when one exists, otherwise paused/unknown depending on the
// monitor's enabled flag (SPEC §11.4 — a disabled monitor sits in paused).
func liveState(m *ipc.MonitorResponse, checks []ipc.CheckResultResponse) string {
	if len(checks) > 0 {
		return checks[0].State
	}
	if !m.Enabled {
		return "paused"
	}
	return "unknown"
}

// renderChecks formats up to detailRecentLimit checks, most recent first as
// returned by the service. Each row carries the start time, derived state, an
// http status code when present, the duration, and any sanitised error string.
func renderChecks(checks []ipc.CheckResultResponse) string {
	if len(checks) == 0 {
		return "  none\n"
	}
	var b strings.Builder
	for i, c := range checks {
		if i >= detailRecentLimit {
			break
		}
		status := "—"
		if c.HTTPStatusCode != nil {
			status = fmt.Sprintf("%d", *c.HTTPStatusCode)
		}
		extra := ""
		if c.Error != "" {
			extra = "  " + c.Error
		}
		fmt.Fprintf(&b, "  %s  %-4s  %-3s  %dms%s\n",
			c.StartedAt.Format(time.RFC3339), c.State, status, c.DurationMs, extra)
	}
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

// fetchMonitorChecksCmd fetches the most recent check_results for a monitor
// over IPC, capped at limit (SPEC §10.5).
func fetchMonitorChecksCmd(c Client, id string, limit int) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) ([]ipc.CheckResultResponse, error) {
			return c.RecentChecks(ctx, id, limit)
		},
		func(cs []ipc.CheckResultResponse) tea.Msg { return monitorChecksLoadedMsg{checks: cs} },
	)
}
