package tui

import "charm.land/bubbles/v2/key"

// KeyMap holds the global key bindings shared across every screen. Screens may
// define additional, screen-specific bindings of their own.
type KeyMap struct {
	// Quit exits the TUI.
	Quit key.Binding
	// Back returns to the previous screen.
	Back key.Binding
}

// DefaultKeyMap returns the standard global key bindings. Quit is bound to
// ctrl+c only (not a bare letter) so it never collides with text entry on the
// form screens added in later milestones.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
	}
}
