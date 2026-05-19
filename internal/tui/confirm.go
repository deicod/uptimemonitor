package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Confirmation dialog key bindings, in addition to the global esc-back that
// already cancels any screen.
var (
	confirmYesKey = key.NewBinding(key.WithKeys("y", "enter"), key.WithHelp("y/enter", "confirm"))
	confirmNoKey  = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "cancel"))
)

// confirmBoxStyle frames the dialog so it stands out as an interrupt over the
// underlying screen.
var confirmBoxStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("231")).
	Background(lipgloss.Color("239")).
	Padding(1, 2).
	Margin(1, 0)

// confirmScreen is the reusable destructive-action confirmation dialog
// (SPEC §19.4). It carries a prompt that should name the affected object and a
// command to run on confirm; cancel simply pops the dialog and runs nothing.
type confirmScreen struct {
	title     string
	prompt    string
	onConfirm tea.Cmd
}

// newConfirmScreen builds a confirmation dialog. The caller is responsible for
// including the affected object name in prompt (SPEC §19.4) so the operator can
// see what they are about to act on.
func newConfirmScreen(title, prompt string, onConfirm tea.Cmd) *confirmScreen {
	return &confirmScreen{title: title, prompt: prompt, onConfirm: onConfirm}
}

// Init has no work to do; the dialog is fully populated at construction.
func (s *confirmScreen) Init() tea.Cmd { return nil }

// Update routes the confirm and cancel keys. Confirm pops the dialog and then
// runs the stored action, in that order, so the action's resulting message is
// delivered to the screen underneath rather than to the dialog itself.
func (s *confirmScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(k, confirmYesKey):
			return s, tea.Sequence(PopScreen, s.onConfirm)
		case key.Matches(k, confirmNoKey):
			return s, PopScreen
		}
	}
	return s, nil
}

// View renders the title, prompt, and key hints inside the dialog box.
func (s *confirmScreen) View() string {
	body := s.title + "\n\n" + s.prompt + "\n\ny confirm • n cancel"
	return confirmBoxStyle.Render(body)
}

// Title is the screen name shown in the status bar.
func (s *confirmScreen) Title() string { return "Confirm" }
