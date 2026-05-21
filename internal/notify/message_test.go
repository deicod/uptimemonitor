package notify

import (
	"testing"
	"time"
)

// Event type literals appear in the message payload sent to providers
// (SPEC §18.2) and in attempt records, so they must match the SPEC values.
func TestEventTypeValues(t *testing.T) {
	cases := map[string]string{
		EventMonitorDown:      "monitor_down",
		EventMonitorRecovered: "monitor_recovered",
		EventManualTest:       "manual_test",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("event type %q, want %q", got, want)
		}
	}
}

// NewMessage is the single construction point used by the delivery pipeline
// and helpers below. The test pins the field mapping and guarantees a
// non-nil Metadata map so callers can write into it without nil checks.
func TestNewMessage(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	msg := NewMessage(EventMonitorDown, "mon-1", "API", "down", now)

	if msg.EventType != EventMonitorDown {
		t.Errorf("EventType = %q, want %q", msg.EventType, EventMonitorDown)
	}
	if msg.MonitorID != "mon-1" {
		t.Errorf("MonitorID = %q, want %q", msg.MonitorID, "mon-1")
	}
	if msg.MonitorName != "API" {
		t.Errorf("MonitorName = %q, want %q", msg.MonitorName, "API")
	}
	if msg.State != "down" {
		t.Errorf("State = %q, want %q", msg.State, "down")
	}
	if !msg.Time.Equal(now) {
		t.Errorf("Time = %v, want %v", msg.Time, now)
	}
	if msg.Metadata == nil {
		t.Fatal("Metadata is nil; helpers should leave it writable")
	}
	msg.Metadata["k"] = "v"
	if msg.Metadata["k"] != "v" {
		t.Errorf("Metadata not writable")
	}
}

// The event-specific helpers wrap NewMessage so callers in the delivery
// pipeline and the manual-test endpoint can build messages without
// repeating the event-type / state pairing.
func TestEventHelpers(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

	down := NewMonitorDownMessage("mon-1", "API", now)
	if down.EventType != EventMonitorDown || down.State != "down" {
		t.Errorf("NewMonitorDownMessage = %+v", down)
	}

	rec := NewMonitorRecoveredMessage("mon-1", "API", now)
	if rec.EventType != EventMonitorRecovered || rec.State != "up" {
		t.Errorf("NewMonitorRecoveredMessage = %+v", rec)
	}

	// Manual test is not tied to a state transition (SPEC §18.2); it must
	// not lie about the monitor's current state.
	test := NewManualTestMessage("mon-1", "API", now)
	if test.EventType != EventManualTest || test.State != "" {
		t.Errorf("NewManualTestMessage = %+v", test)
	}
}
