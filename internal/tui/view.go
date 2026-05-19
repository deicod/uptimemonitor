package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var (
	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Background(lipgloss.Color("238")).
			Padding(0, 1)

	errorBoxStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("160")).
			Padding(1, 2).
			Margin(1, 0)
)

// View renders the active screen with the global status bar, or the error
// dialog when an error is pending (SPEC §19.1, §19.4). The TUI runs in the
// alternate screen buffer so it restores the operator's terminal on exit.
func (m Model) View() tea.View {
	content := m.top().View() + "\n" + m.statusBar()
	if m.err != nil {
		content = m.errorView()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// errorView renders the error dialog (SPEC §19.4).
func (m Model) errorView() string {
	box := errorBoxStyle.Render("Error\n\n" + m.err.Error() + "\n\npress any key to dismiss")
	return box + "\n" + m.statusBar()
}

// statusBar renders the breadcrumb of open screens plus the global key hints.
func (m Model) statusBar() string {
	titles := make([]string, len(m.screens))
	for i, s := range m.screens {
		titles[i] = s.Title()
	}
	return statusBarStyle.Render(strings.Join(titles, " › ") + "  —  esc back • ctrl+c quit")
}
