package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/deicod/uptimemonitor/internal/ipc"
)

// notificationTargetsLoadedMsg delivers the target list fetched by the screen.
type notificationTargetsLoadedMsg struct {
	targets []ipc.NotificationTargetResponse
}

// notificationGlobalLoadedMsg delivers the global notifications toggle state.
type notificationGlobalLoadedMsg struct{ enabled bool }

// notificationTargetsChangedMsg asks the target list to re-fetch after a
// create, edit, or delete committed elsewhere.
type notificationTargetsChangedMsg struct{}

// notificationTestSentMsg reports that a test notification was queued, so the
// list can show transient feedback rather than opening a dialog.
type notificationTestSentMsg struct{ name string }

// openNotificationFormMsg requests the create/edit form. An empty targetID
// means "create a new target".
type openNotificationFormMsg struct{ targetID string }

// openNotificationAttemptsMsg requests the delivery-attempts list.
type openNotificationAttemptsMsg struct{}

// Notification target list key bindings, in addition to the global keymap.
var (
	ntRefreshKey  = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))
	ntUpKey       = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up"))
	ntDownKey     = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down"))
	ntNewKey      = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new"))
	ntEditKey     = key.NewBinding(key.WithKeys("enter", "e"), key.WithHelp("enter", "edit"))
	ntDeleteKey   = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
	ntTestKey     = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "test"))
	ntAttemptsKey = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "attempts"))
	ntToggleKey   = key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "toggle global"))
)

// notificationRowStyle highlights the row under the cursor.
var notificationRowStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("231")).
	Background(lipgloss.Color("238"))

// notificationTargetListScreen lists notification targets from
// /v1/notifications/targets and lets the operator create, edit, delete, and
// test them, flip the global notifications toggle, and open the attempts list
// (SPEC §12.4, §18.9). Secrets never appear here — the service redacts them.
type notificationTargetListScreen struct {
	client        Client
	targets       []ipc.NotificationTargetResponse
	cursor        int
	loaded        bool
	globalEnabled bool
	globalLoaded  bool
	notice        string
}

// newNotificationTargetListScreen builds the target list bound to client.
func newNotificationTargetListScreen(client Client) *notificationTargetListScreen {
	return &notificationTargetListScreen{client: client}
}

// Init fetches the target list and the global toggle (SPEC §19.3).
func (s *notificationTargetListScreen) Init() tea.Cmd {
	return tea.Batch(fetchNotificationTargetsCmd(s.client), fetchNotificationGlobalCmd(s.client))
}

// Update caches fetched data and handles selection, navigation, and actions.
func (s *notificationTargetListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case notificationTargetsLoadedMsg:
		s.targets = msg.targets
		s.loaded = true
		s.clampCursor()
	case notificationGlobalLoadedMsg:
		s.globalEnabled = msg.enabled
		s.globalLoaded = true
	case notificationTargetsChangedMsg:
		return s, fetchNotificationTargetsCmd(s.client)
	case notificationTestSentMsg:
		s.notice = fmt.Sprintf("test notification queued for %q", msg.name)
	case openNotificationFormMsg:
		return s, PushScreen(newNotificationFormScreen(s.client, msg.targetID))
	case openNotificationAttemptsMsg:
		return s, PushScreen(newNotificationAttemptsScreen(s.client))
	case tea.KeyPressMsg:
		return s, s.handleKey(msg)
	}
	return s, nil
}

// handleKey applies a key press and returns any resulting command.
func (s *notificationTargetListScreen) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, ntRefreshKey):
		return s.Init()
	case key.Matches(msg, ntUpKey):
		if s.cursor > 0 {
			s.cursor--
		}
	case key.Matches(msg, ntDownKey):
		if s.cursor < len(s.targets)-1 {
			s.cursor++
		}
	case key.Matches(msg, ntNewKey):
		return func() tea.Msg { return openNotificationFormMsg{} }
	case key.Matches(msg, ntEditKey):
		if t, ok := s.selected(); ok {
			id := t.ID
			return func() tea.Msg { return openNotificationFormMsg{targetID: id} }
		}
	case key.Matches(msg, ntDeleteKey):
		if t, ok := s.selected(); ok {
			return s.openDeleteConfirm(t)
		}
	case key.Matches(msg, ntTestKey):
		if t, ok := s.selected(); ok {
			s.notice = ""
			return testNotificationTargetCmd(s.client, t.ID, t.Name)
		}
	case key.Matches(msg, ntAttemptsKey):
		return func() tea.Msg { return openNotificationAttemptsMsg{} }
	case key.Matches(msg, ntToggleKey):
		return toggleGlobalNotificationsCmd(s.client, !s.globalEnabled)
	}
	return nil
}

// openDeleteConfirm pushes a confirmation dialog naming the target before it is
// deleted (SPEC §19.4); confirming runs the delete over IPC.
func (s *notificationTargetListScreen) openDeleteConfirm(t ipc.NotificationTargetResponse) tea.Cmd {
	prompt := fmt.Sprintf("Delete notification target %q? This cannot be undone.", t.Name)
	return PushScreen(newConfirmScreen("Delete target", prompt, deleteNotificationTargetCmd(s.client, t.ID)))
}

// selected returns the target under the cursor; ok is false when empty.
func (s *notificationTargetListScreen) selected() (ipc.NotificationTargetResponse, bool) {
	if s.cursor < 0 || s.cursor >= len(s.targets) {
		return ipc.NotificationTargetResponse{}, false
	}
	return s.targets[s.cursor], true
}

// clampCursor keeps the cursor within the list bounds after a refresh.
func (s *notificationTargetListScreen) clampCursor() {
	if s.cursor >= len(s.targets) {
		s.cursor = len(s.targets) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// View renders the global toggle banner and the target table.
func (s *notificationTargetListScreen) View() string {
	var b strings.Builder
	b.WriteString("notification targets\n\n")
	b.WriteString("global notifications: " + s.globalLabel() + "\n\n")
	if !s.loaded {
		b.WriteString("loading targets…")
		return b.String()
	}
	if len(s.targets) == 0 {
		b.WriteString("no targets — press n to create one\n")
	} else {
		fmt.Fprintf(&b, "  %-28s %-10s %s\n", "NAME", "KIND", "ENABLED")
		for i, t := range s.targets {
			row := fmt.Sprintf("%-28s %-10s %s", truncate(t.Name, 28), t.Kind, yesNo(t.Enabled))
			cursor := "  "
			if i == s.cursor {
				cursor = "› "
				row = notificationRowStyle.Render(row)
			}
			b.WriteString(cursor + row + "\n")
		}
	}
	if s.notice != "" {
		b.WriteString("\n" + s.notice + "\n")
	}
	b.WriteString("\n↑/↓ move • enter edit • n new • t test • d delete • g toggle global • a attempts • r refresh")
	return b.String()
}

// globalLabel renders the global toggle state for the banner.
func (s *notificationTargetListScreen) globalLabel() string {
	if !s.globalLoaded {
		return "…"
	}
	if s.globalEnabled {
		return "on"
	}
	return "off"
}

// Title is the screen name shown in the status bar.
func (s *notificationTargetListScreen) Title() string { return "Notifications" }

// fetchNotificationTargetsCmd fetches the target list over IPC (SPEC §19.3).
func fetchNotificationTargetsCmd(c Client) tea.Cmd {
	return ipcCmd(c.ListNotificationTargets,
		func(ts []ipc.NotificationTargetResponse) tea.Msg {
			return notificationTargetsLoadedMsg{targets: ts}
		})
}

// fetchNotificationGlobalCmd fetches the global toggle over IPC.
func fetchNotificationGlobalCmd(c Client) tea.Cmd {
	return ipcCmd(c.GetNotificationsEnabled,
		func(enabled bool) tea.Msg { return notificationGlobalLoadedMsg{enabled: enabled} })
}

// toggleGlobalNotificationsCmd flips the global toggle over IPC and reports the
// resulting state.
func toggleGlobalNotificationsCmd(c Client, enabled bool) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) (bool, error) { return c.SetNotificationsEnabled(ctx, enabled) },
		func(got bool) tea.Msg { return notificationGlobalLoadedMsg{enabled: got} },
	)
}

// testNotificationTargetCmd sends a test notification over IPC. Success yields a
// transient notice; a provider failure becomes an errMsg for the error dialog
// (SPEC §19.3–19.4).
func testNotificationTargetCmd(c Client, id, name string) tea.Cmd {
	return ipcCmd(
		func(ctx context.Context) (ipc.TestNotificationResponse, error) {
			return c.TestNotificationTarget(ctx, id)
		},
		func(ipc.TestNotificationResponse) tea.Msg { return notificationTestSentMsg{name: name} },
	)
}

// deleteNotificationTargetCmd deletes a target over IPC. On success it emits
// notificationTargetsChangedMsg so the list re-fetches after the confirm screen
// pops; on failure it emits errMsg for the error dialog (SPEC §19.3–19.4).
func deleteNotificationTargetCmd(c Client, id string) tea.Cmd {
	return func() tea.Msg {
		if err := c.DeleteNotificationTarget(context.Background(), id); err != nil {
			return errMsg{err: err}
		}
		return notificationTargetsChangedMsg{}
	}
}
