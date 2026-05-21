package notify

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrUnknownEventType is returned by Render when msg.EventType is not one of
// the MVP events (SPEC §18.2). Callers should match it with errors.Is so the
// delivery pipeline can distinguish "unrenderable message" from a downstream
// I/O failure and record a permanent attempt failure rather than retrying.
var ErrUnknownEventType = errors.New("notify: unknown event type")

// Render fills in msg.Title and msg.Body for the MVP event types
// (monitor_down, monitor_recovered, manual_test) defined in SPEC §18.2. It
// returns a new Message value rather than mutating the input so the delivery
// pipeline can retain the unrendered message for attempt logging.
//
// The renderer references only the explicitly-public Message fields
// (EventType, MonitorID, MonitorName, State, Time, URL); provider config and
// Metadata are deliberately ignored so secrets cannot leak into rendered
// output (SPEC §18.9).
func Render(msg Message) (Message, error) {
	when := msg.Time.UTC().Format(time.RFC3339)

	switch msg.EventType {
	case EventMonitorDown:
		msg.Title = fmt.Sprintf("Monitor down: %s", msg.MonitorName)
		msg.Body = transitionBody(msg.MonitorName, msg.MonitorID, "went down", when, msg.URL)
	case EventMonitorRecovered:
		msg.Title = fmt.Sprintf("Monitor recovered: %s", msg.MonitorName)
		msg.Body = transitionBody(msg.MonitorName, msg.MonitorID, "recovered", when, msg.URL)
	case EventManualTest:
		msg.Title = fmt.Sprintf("Test notification: %s", msg.MonitorName)
		msg.Body = fmt.Sprintf(
			"This is a test notification from uptimemonitor for monitor %q (%s), sent at %s.",
			msg.MonitorName, msg.MonitorID, when,
		)
	default:
		return msg, fmt.Errorf("%w: %q", ErrUnknownEventType, msg.EventType)
	}
	return msg, nil
}

// transitionBody renders the body shared by monitor_down and monitor_recovered.
// The URL line is omitted entirely when url is empty so providers don't ship
// a dangling "URL: " trailer for monitors that never had one populated.
func transitionBody(name, id, verb, when, url string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Monitor %q (%s) %s at %s.", name, id, verb, when)
	if url != "" {
		fmt.Fprintf(&b, "\nURL: %s", url)
	}
	return b.String()
}
