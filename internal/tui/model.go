// Package tui implements the uptimemonitor terminal UI. It is a pure IPC
// client (SPEC §19.2): every read and write goes through the service over the
// Unix socket, and no model holds persistent state of its own.
package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Screen is a single view in the TUI. Each screen owns its own view state and
// cached API responses (SPEC §19.2) and handles its own input; the root Model
// routes messages to the screen on top of the stack and renders it.
type Screen interface {
	// Init returns an optional command to run when the screen becomes active.
	Init() tea.Cmd
	// Update handles a message and returns the (possibly updated) screen.
	Update(tea.Msg) (Screen, tea.Cmd)
	// View renders the screen body, excluding the global status bar.
	View() string
	// Title is the short screen name shown in the status bar.
	Title() string
}

// pushScreenMsg asks the root model to push a new screen onto the stack.
type pushScreenMsg struct{ screen Screen }

// popScreenMsg asks the root model to pop the current screen.
type popScreenMsg struct{}

// PushScreen returns a command that navigates to s by pushing it onto the
// screen stack (SPEC §19.1).
func PushScreen(s Screen) tea.Cmd {
	return func() tea.Msg { return pushScreenMsg{screen: s} }
}

// PopScreen is a command that returns to the previous screen.
func PopScreen() tea.Msg { return popScreenMsg{} }

// errMsg carries an error to the root model, which displays it in the error
// dialog (SPEC §19.4). IPC commands return errMsg when a request fails.
type errMsg struct{ err error }

// Model is the root Bubble Tea model. It owns the screen stack, the global
// keymap, and the error dialog; it caches no domain state of its own
// (SPEC §19.2).
type Model struct {
	client  Client
	keys    KeyMap
	screens []Screen
	err     error
	width   int
	height  int
}

// newModel builds the root model with the home screen as the initial screen.
func newModel(client Client) Model {
	return Model{
		client:  client,
		keys:    DefaultKeyMap(),
		screens: []Screen{newHomeScreen(client)},
	}
}

// top returns the screen currently on top of the stack. The stack always holds
// at least the home screen, so this never indexes out of range.
func (m Model) top() Screen { return m.screens[len(m.screens)-1] }
