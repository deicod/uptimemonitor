package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// Init starts the initial screen (SPEC §19.1).
func (m Model) Init() tea.Cmd {
	return m.top().Init()
}

// Update implements the Bubble Tea update loop. It handles the global
// concerns — window size, the error dialog, global keys, and screen
// navigation — and forwards everything else to the screen on top of the stack.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.forward(msg)

	case errMsg:
		// Route the failure to the error dialog (SPEC §19.4).
		m.err = msg.err
		return m, nil

	case tea.KeyPressMsg:
		// While the error dialog is open, any key dismisses it.
		if m.err != nil {
			m.err = nil
			return m, nil
		}
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Back):
			return m.pop()
		}
		return m.forward(msg)

	case pushScreenMsg:
		return m.push(msg.screen)

	case popScreenMsg:
		return m.pop()

	default:
		return m.forward(msg)
	}
}

// push adds screen to the stack and initialises it.
func (m Model) push(s Screen) (tea.Model, tea.Cmd) {
	m.screens = append(m.screens, s)
	return m, s.Init()
}

// pop returns to the previous screen; at the root screen it does nothing.
func (m Model) pop() (tea.Model, tea.Cmd) {
	if len(m.screens) <= 1 {
		return m, nil
	}
	m.screens = m.screens[:len(m.screens)-1]
	return m, nil
}

// forward routes msg to the screen on top of the stack.
func (m Model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	top, cmd := m.top().Update(msg)
	m.screens[len(m.screens)-1] = top
	return m, cmd
}
