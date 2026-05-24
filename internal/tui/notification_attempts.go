package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// notificationAttemptsLoadedMsg delivers the attempt list fetched by the screen.
type notificationAttemptsLoadedMsg struct {
	attempts []ipc.NotificationAttemptResponse
}

// notificationAttemptsScreen is a read-only list of recent delivery attempts
// across all targets (SPEC §12.4, §10.5). It joins target names from the target
// list so a ULID target_id is not the only identifier shown.
type notificationAttemptsScreen struct {
	client       Client
	attempts     []ipc.NotificationAttemptResponse
	targetNames  map[string]string
	loaded       bool
	targetsKnown bool
}

// newNotificationAttemptsScreen builds the attempts list bound to client.
func newNotificationAttemptsScreen(client Client) *notificationAttemptsScreen {
	return &notificationAttemptsScreen{client: client, targetNames: map[string]string{}}
}

// Init fetches the attempts and the targets (for name lookup) (SPEC §19.3).
func (s *notificationAttemptsScreen) Init() tea.Cmd {
	return tea.Batch(fetchNotificationAttemptsCmd(s.client), fetchNotificationTargetsCmd(s.client))
}

// Update caches the fetched data and handles the refresh key.
func (s *notificationAttemptsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case notificationAttemptsLoadedMsg:
		s.attempts = msg.attempts
		s.loaded = true
	case notificationTargetsLoadedMsg:
		for _, t := range msg.targets {
			s.targetNames[t.ID] = t.Name
		}
		s.targetsKnown = true
	case tea.KeyPressMsg:
		if key.Matches(msg, ntRefreshKey) {
			return s, s.Init()
		}
	}
	return s, nil
}

// View renders the attempt table.
func (s *notificationAttemptsScreen) View() string {
	var b strings.Builder
	b.WriteString("notification attempts\n\n")
	if !s.loaded {
		b.WriteString("loading attempts…")
		return b.String()
	}
	if len(s.attempts) == 0 {
		b.WriteString("no delivery attempts yet")
		return b.String()
	}
	fmt.Fprintf(&b, "  %-16s %-20s %-18s %-8s %-3s %s\n",
		"TIME", "TARGET", "EVENT", "STATUS", "#", "ERROR")
	for _, a := range s.attempts {
		fmt.Fprintf(&b, "  %-16s %-20s %-18s %-8s %-3d %s\n",
			a.CreatedAt.Local().Format("2006-01-02 15:04"),
			truncate(s.targetName(a.TargetID), 20),
			truncate(a.EventType, 18),
			a.Status, a.AttemptNumber, truncate(a.Error, 40))
	}
	b.WriteString("\nr refresh • esc back")
	return b.String()
}

// targetName resolves a target_id to its name, falling back to a short id when
// the target list has not loaded or the target was deleted.
func (s *notificationAttemptsScreen) targetName(id string) string {
	if name, ok := s.targetNames[id]; ok {
		return name
	}
	return truncate(id, 12)
}

// Title is the screen name shown in the status bar.
func (s *notificationAttemptsScreen) Title() string { return "Attempts" }

// fetchNotificationAttemptsCmd fetches recent attempts over IPC (SPEC §19.3). A
// zero limit lets the service apply its default page size.
func fetchNotificationAttemptsCmd(c Client) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) ([]ipc.NotificationAttemptResponse, error) {
			return c.ListNotificationAttempts(ctx, 0)
		},
		func(as []ipc.NotificationAttemptResponse) tea.Msg {
			return notificationAttemptsLoadedMsg{attempts: as}
		},
	)
}
