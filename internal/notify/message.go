package notify

import "time"

// MVP notification event types (SPEC §18.2). These string values appear in
// notification_attempts rows and in any payload a provider produces, so they
// are part of the persisted contract.
const (
	EventMonitorDown      = "monitor_down"
	EventMonitorRecovered = "monitor_recovered"
	EventManualTest       = "manual_test"
)

// Message is the per-notification payload handed to Provider.Send.
// The State field carries the monitor state that triggered the message
// (e.g. "down", "up"); it is left empty for non-transition events such as
// manual_test. Title and Body are filled in by the templating layer (M9.3).
//
// SPEC §18.2.
type Message struct {
	EventType   string
	MonitorID   string
	MonitorName string
	State       string
	Title       string
	Body        string
	URL         string
	Time        time.Time
	Metadata    map[string]string
}

// NewMessage is the single Message constructor. It guarantees Metadata is a
// non-nil map so callers can write into it without nil checks; Title and
// Body are intentionally left blank for the templating layer (M9.3) to fill.
func NewMessage(eventType, monitorID, monitorName, state string, when time.Time) Message {
	return Message{
		EventType:   eventType,
		MonitorID:   monitorID,
		MonitorName: monitorName,
		State:       state,
		Time:        when,
		Metadata:    map[string]string{},
	}
}

// NewMonitorDownMessage builds a monitor_down message; the State field is
// pinned to "down" so providers can rely on the pairing.
func NewMonitorDownMessage(monitorID, monitorName string, when time.Time) Message {
	return NewMessage(EventMonitorDown, monitorID, monitorName, "down", when)
}

// NewMonitorRecoveredMessage builds a monitor_recovered message with
// State="up".
func NewMonitorRecoveredMessage(monitorID, monitorName string, when time.Time) Message {
	return NewMessage(EventMonitorRecovered, monitorID, monitorName, "up", when)
}

// NewManualTestMessage builds a manual_test message. State is left empty:
// a manual test does not reflect the monitor's actual state and must not
// claim to (SPEC §18.2).
func NewManualTestMessage(monitorID, monitorName string, when time.Time) Message {
	return NewMessage(EventManualTest, monitorID, monitorName, "", when)
}
