package monitor

import "testing"

// TestNextState walks every (state, input) pair from SPEC §17.2-17.3. The
// table doubles as the canonical record of which transitions emit events,
// open/resolve incidents, and queue notifications — the M7.6 pipeline keys
// off these flags, so a regression here would silently break notifications
// or incident bookkeeping.
func TestNextState(t *testing.T) {
	cases := []struct {
		name  string
		from  MonitorState
		input StateInput
		want  StateTransition
	}{
		// From unknown — first classification creates an event but only the
		// failure path opens an incident and queues a down notification
		// (SPEC §17.3 "unknown -> up" / "unknown -> down").
		{
			"unknown+success->up",
			StateUnknown, InputSuccess,
			StateTransition{From: StateUnknown, To: StateUp, EmitStateChange: true},
		},
		{
			"unknown+failure->down opens incident and notifies",
			StateUnknown, InputFailure,
			StateTransition{From: StateUnknown, To: StateDown, EmitStateChange: true, OpenIncident: true, NotifyDown: true},
		},
		{
			"unknown+disable->paused",
			StateUnknown, InputDisable,
			StateTransition{From: StateUnknown, To: StatePaused, EmitStateChange: true},
		},
		{
			"unknown+enable is a no-op",
			StateUnknown, InputEnable,
			StateTransition{From: StateUnknown, To: StateUnknown},
		},

		// From up — repeated successes are silent; the first failure is the
		// incident open and the sole down notification (SPEC §18.8 spam rule).
		{
			"up+success stays up silently",
			StateUp, InputSuccess,
			StateTransition{From: StateUp, To: StateUp},
		},
		{
			"up+failure->down opens incident and notifies",
			StateUp, InputFailure,
			StateTransition{From: StateUp, To: StateDown, EmitStateChange: true, OpenIncident: true, NotifyDown: true},
		},
		{
			"up+disable->paused",
			StateUp, InputDisable,
			StateTransition{From: StateUp, To: StatePaused, EmitStateChange: true},
		},
		{
			"up+enable is a no-op",
			StateUp, InputEnable,
			StateTransition{From: StateUp, To: StateUp},
		},

		// From down — repeated failures are silent (spam rule); the first
		// success resolves the incident and queues the lone recovery
		// notification.
		{
			"down+failure stays down silently",
			StateDown, InputFailure,
			StateTransition{From: StateDown, To: StateDown},
		},
		{
			"down+success->up resolves incident and notifies recovery",
			StateDown, InputSuccess,
			StateTransition{From: StateDown, To: StateUp, EmitStateChange: true, ResolveIncident: true, NotifyRecovery: true},
		},
		{
			"down+disable->paused does not auto-resolve incident",
			StateDown, InputDisable,
			StateTransition{From: StateDown, To: StatePaused, EmitStateChange: true},
		},
		{
			"down+enable is a no-op",
			StateDown, InputEnable,
			StateTransition{From: StateDown, To: StateDown},
		},

		// From paused — only re-enable transitions out (to unknown). Manual
		// checks may still run while paused (SPEC §16.4) but must not change
		// state or queue notifications.
		{
			"paused+enable->unknown",
			StatePaused, InputEnable,
			StateTransition{From: StatePaused, To: StateUnknown, EmitStateChange: true},
		},
		{
			"paused+disable is a no-op",
			StatePaused, InputDisable,
			StateTransition{From: StatePaused, To: StatePaused},
		},
		{
			"paused+success does not unpause",
			StatePaused, InputSuccess,
			StateTransition{From: StatePaused, To: StatePaused},
		},
		{
			"paused+failure does not unpause",
			StatePaused, InputFailure,
			StateTransition{From: StatePaused, To: StatePaused},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NextState(tc.from, tc.input)
			if got != tc.want {
				t.Fatalf("NextState(%q, %v) = %+v, want %+v", tc.from, tc.input, got, tc.want)
			}
		})
	}
}

func TestStateTransitionChanged(t *testing.T) {
	if (StateTransition{From: StateUp, To: StateUp}).Changed() {
		t.Fatal("From == To should report Changed()=false")
	}
	if !(StateTransition{From: StateUp, To: StateDown}).Changed() {
		t.Fatal("From != To should report Changed()=true")
	}
}
