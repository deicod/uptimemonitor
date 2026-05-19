package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// stubScreen is a minimal Screen used to exercise the root model's router
// without depending on the real (later-milestone) screens.
type stubScreen struct {
	title string
	// onKey, when set, is returned as the command for any key press, letting
	// a test drive a screen transition through a key.
	onKey tea.Cmd
}

func (s *stubScreen) Init() tea.Cmd { return nil }

func (s *stubScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok {
		return s, s.onKey
	}
	return s, nil
}

func (s *stubScreen) View() string  { return "stub:" + s.title }
func (s *stubScreen) Title() string { return s.title }

// runeKey builds a printable key press for the given rune.
func runeKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// TestUpdatePushPopScreen verifies the screen router: pushing makes the new
// screen active and popping returns to the previous one. Navigation correctness
// is what lets the TUI move between the SPEC §19.1 screens.
func TestUpdatePushPopScreen(t *testing.T) {
	m := newModel(stubClient{})
	other := &stubScreen{title: "Other"}

	pushed, _ := m.Update(pushScreenMsg{screen: other})
	got := pushed.(Model)
	if got.top() != Screen(other) {
		t.Fatalf("push did not make the new screen active, top is %q", got.top().Title())
	}

	popped, _ := got.Update(popScreenMsg{})
	if popped.(Model).top().Title() != "Home" {
		t.Fatalf("pop did not return to the home screen, top is %q", popped.(Model).top().Title())
	}
}

// TestUpdatePopAtRootIsNoop verifies popping the last screen leaves the home
// screen in place rather than emptying the stack and crashing the renderer.
func TestUpdatePopAtRootIsNoop(t *testing.T) {
	m := newModel(stubClient{})
	popped, _ := m.Update(popScreenMsg{})
	if got := popped.(Model); len(got.screens) != 1 {
		t.Fatalf("pop at root changed the stack depth to %d, want 1", len(got.screens))
	}
}

// TestUpdateTransitionsBetweenScreensOnKey verifies the full key-driven
// transition path: a key press reaches the active screen, whose command yields
// a navigation message that the root model applies.
func TestUpdateTransitionsBetweenScreensOnKey(t *testing.T) {
	other := &stubScreen{title: "Other"}
	home := &stubScreen{title: "Home", onKey: PushScreen(other)}
	m := newModel(stubClient{})
	m.screens = []Screen{home}

	model, cmd := m.Update(runeKey('x'))
	if cmd == nil {
		t.Fatal("key press produced no command from the active screen")
	}
	model, _ = model.(Model).Update(cmd())
	if got := model.(Model); got.top() != Screen(other) {
		t.Fatalf("key did not transition to the pushed screen, top is %q", got.top().Title())
	}
}

// TestUpdateQuitKey verifies the global quit binding produces tea.Quit.
func TestUpdateQuitKey(t *testing.T) {
	m := newModel(stubClient{})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+c did not produce tea.Quit, got %T", cmd())
	}
}

// TestUpdateErrMsgRoutesToDialog verifies an errMsg is routed to the error
// dialog (SPEC §19.4) so IPC failures are surfaced, not lost.
func TestUpdateErrMsgRoutesToDialog(t *testing.T) {
	m := newModel(stubClient{})
	model, _ := m.Update(errMsg{err: errors.New("boom")})

	got := model.(Model)
	if got.err == nil {
		t.Fatal("errMsg did not open the error dialog")
	}
	if !strings.Contains(got.View().Content, "boom") {
		t.Errorf("error dialog does not render the message:\n%s", got.View().Content)
	}
}

// TestUpdateErrorDialogDismissedByKey verifies any key dismisses an open error
// dialog instead of being delivered to the underlying screen (SPEC §19.4).
func TestUpdateErrorDialogDismissedByKey(t *testing.T) {
	m := newModel(stubClient{})
	m.err = errors.New("boom")

	model, _ := m.Update(runeKey('x'))
	if model.(Model).err != nil {
		t.Error("key press did not dismiss the error dialog")
	}
}
