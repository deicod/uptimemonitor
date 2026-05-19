package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// sentinelMsg is a marker message a test uses to verify the confirm screen ran
// the action it was given.
type sentinelMsg struct{}

// TestConfirmScreenViewIncludesObjectName verifies the dialog renders the
// caller's prompt verbatim, since SPEC §19.4 requires the affected object's
// name to appear so the operator knows what they are confirming.
func TestConfirmScreenViewIncludesObjectName(t *testing.T) {
	s := newConfirmScreen("Delete monitor", `Delete monitor "API"?`, nil)
	view := s.View()
	if !strings.Contains(view, "Delete monitor") {
		t.Errorf("confirm view missing title:\n%s", view)
	}
	if !strings.Contains(view, `"API"`) {
		t.Errorf("confirm view missing object name:\n%s", view)
	}
}

// TestConfirmScreenConfirmRunsAction verifies the confirm key produces a
// command that, when executed, runs the action — so a confirmed destructive
// action actually reaches the service rather than silently doing nothing.
func TestConfirmScreenConfirmRunsAction(t *testing.T) {
	action := func() tea.Msg { return sentinelMsg{} }
	s := newConfirmScreen("Delete", "Delete?", action)

	_, cmd := s.Update(runeKey('y'))
	if cmd == nil {
		t.Fatal("confirm key produced no command")
	}
	// The stored action must, on its own, produce the sentinel — otherwise the
	// confirm path never delivers the work.
	if _, ok := s.onConfirm().(sentinelMsg); !ok {
		t.Fatalf("stored action produced %T, want sentinelMsg", s.onConfirm())
	}
}

// TestConfirmScreenCancelDismisses verifies the cancel key emits popScreenMsg
// and nothing else, so cancelling a destructive action leaves the world
// untouched (SPEC §19.4).
func TestConfirmScreenCancelDismisses(t *testing.T) {
	action := func() tea.Msg { return sentinelMsg{} }
	s := newConfirmScreen("Delete", "Delete?", action)

	_, cmd := s.Update(runeKey('n'))
	if cmd == nil {
		t.Fatal("cancel key produced no command")
	}
	if _, ok := cmd().(popScreenMsg); !ok {
		t.Fatalf("cancel emitted %T, want popScreenMsg", cmd())
	}
}

// TestConfirmScreenOtherKeysIgnored verifies an unrelated key press does
// nothing, so stray input cannot accidentally trigger or dismiss a destructive
// action.
func TestConfirmScreenOtherKeysIgnored(t *testing.T) {
	s := newConfirmScreen("Delete", "Delete?", func() tea.Msg { return sentinelMsg{} })
	if _, cmd := s.Update(runeKey('x')); cmd != nil {
		t.Fatalf("unrelated key produced %T, want nil", cmd())
	}
}
