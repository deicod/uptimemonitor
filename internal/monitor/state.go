package monitor

// StateInput is the trigger of a possible state transition: either the
// outcome of a check (success/failure) or a user-driven enable/disable
// action.
type StateInput int

// StateInput values.
const (
	// InputSuccess is a check that met its success criteria.
	InputSuccess StateInput = iota
	// InputFailure is a check that did not meet its success criteria.
	InputFailure
	// InputDisable is a user disabling the monitor.
	InputDisable
	// InputEnable is a user enabling a previously paused monitor.
	InputEnable
)

// StateTransition is the result of applying a StateInput to a current
// MonitorState. It names the next state and the side effects the caller must
// perform (SPEC §17.3): writing a monitor_state_changed event, opening or
// resolving an incident, and queueing down/recovery notifications.
//
// The state machine is pure: it neither writes events nor sends
// notifications, so the caller (M7.6 check pipeline) owns persistence and
// can run the transition deterministically in tests.
type StateTransition struct {
	From            MonitorState
	To              MonitorState
	EmitStateChange bool
	OpenIncident    bool
	ResolveIncident bool
	NotifyDown      bool
	NotifyRecovery  bool
}

// Changed reports whether the transition actually moves the monitor to a new
// state.
func (t StateTransition) Changed() bool { return t.From != t.To }

// NextState returns the transition for (current, input) per SPEC §17.2-17.3.
// It is pure; the caller applies the side effects described by the returned
// StateTransition.
func NextState(current MonitorState, input StateInput) StateTransition {
	t := StateTransition{From: current, To: current}
	switch input {
	case InputDisable:
		if current != StatePaused {
			t.To = StatePaused
			t.EmitStateChange = true
		}
	case InputEnable:
		if current == StatePaused {
			t.To = StateUnknown
			t.EmitStateChange = true
		}
	case InputSuccess:
		switch current {
		case StateUnknown:
			t.To = StateUp
			t.EmitStateChange = true
		case StateDown:
			t.To = StateUp
			t.EmitStateChange = true
			t.ResolveIncident = true
			t.NotifyRecovery = true
		}
	case InputFailure:
		switch current {
		case StateUnknown, StateUp:
			t.To = StateDown
			t.EmitStateChange = true
			t.OpenIncident = true
			t.NotifyDown = true
		}
	}
	return t
}
