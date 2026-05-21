package notify

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fixedTemplateTime is a deterministic timestamp used across template tests
// so the rendered body text is stable. Anything time-dependent in a Render
// call must format relative to this value — never time.Now() — or the
// assertions become flaky.
var fixedTemplateTime = time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

// TestRenderMonitorDown pins the wire output for the monitor_down event.
// Providers that surface only a notification title (mobile push, ntfy/Gotify
// subject) need the title alone to convey "this monitor is down", so the
// title leads with that signal; the body carries the machine-readable detail
// (monitor id, event time, monitored URL) that an on-call engineer needs to
// triage. Pinning the exact strings makes any unintentional format change
// loud — intentional changes update this test deliberately.
func TestRenderMonitorDown(t *testing.T) {
	msg := NewMonitorDownMessage("mon-1", "API", fixedTemplateTime)
	msg.URL = "https://api.example.com"

	got, err := Render(msg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantTitle := `Monitor down: API`
	wantBody := "Monitor \"API\" (mon-1) went down at 2026-05-21T12:00:00Z.\nURL: https://api.example.com"
	if got.Title != wantTitle {
		t.Errorf("Title = %q, want %q", got.Title, wantTitle)
	}
	if got.Body != wantBody {
		t.Errorf("Body = %q, want %q", got.Body, wantBody)
	}
}

// TestRenderMonitorDownNoURL covers the variant where the monitor has no URL
// associated with the message (the field defaults to "" when the pipeline
// hasn't populated it). The body must omit the URL line entirely rather than
// emit a dangling "URL: " — recipients would treat that as a bug, and some
// providers reject empty trailing fields.
func TestRenderMonitorDownNoURL(t *testing.T) {
	msg := NewMonitorDownMessage("mon-1", "API", fixedTemplateTime)

	got, err := Render(msg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantBody := `Monitor "API" (mon-1) went down at 2026-05-21T12:00:00Z.`
	if got.Body != wantBody {
		t.Errorf("Body = %q, want %q", got.Body, wantBody)
	}
	if strings.Contains(got.Body, "URL:") {
		t.Errorf("Body contains a URL: prefix when no URL was set: %q", got.Body)
	}
}

// TestRenderMonitorRecovered pins the recovery output. The title parallels
// the down title (same lead, opposite state) so a user skimming a notification
// timeline can pair them by eye; the body keeps the same shape for the same
// reason — only the verb changes.
func TestRenderMonitorRecovered(t *testing.T) {
	msg := NewMonitorRecoveredMessage("mon-1", "API", fixedTemplateTime)
	msg.URL = "https://api.example.com"

	got, err := Render(msg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantTitle := `Monitor recovered: API`
	wantBody := "Monitor \"API\" (mon-1) recovered at 2026-05-21T12:00:00Z.\nURL: https://api.example.com"
	if got.Title != wantTitle {
		t.Errorf("Title = %q, want %q", got.Title, wantTitle)
	}
	if got.Body != wantBody {
		t.Errorf("Body = %q, want %q", got.Body, wantBody)
	}
}

// TestRenderManualTest pins the manual-test output. The body must make clear
// that this is a test — not an actual incident — because the test endpoint
// (M9.10) reuses the same delivery pipeline that handles real alerts, and a
// recipient who mistakes a test for an outage will burn an on-call response.
func TestRenderManualTest(t *testing.T) {
	msg := NewManualTestMessage("mon-1", "API", fixedTemplateTime)

	got, err := Render(msg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	wantTitle := `Test notification: API`
	wantBody := `This is a test notification from uptimemonitor for monitor "API" (mon-1), sent at 2026-05-21T12:00:00Z.`
	if got.Title != wantTitle {
		t.Errorf("Title = %q, want %q", got.Title, wantTitle)
	}
	if got.Body != wantBody {
		t.Errorf("Body = %q, want %q", got.Body, wantBody)
	}
}

// TestRenderUnknownEventType ensures Render fails loudly on an unrecognised
// event type rather than silently emitting an empty title/body. The delivery
// pipeline (M9.9) needs this signal to record an attempt failure instead of
// dispatching a blank notification, which most providers would either reject
// or — worse — send as a confusing empty message.
func TestRenderUnknownEventType(t *testing.T) {
	msg := NewMessage("not_a_real_event", "mon-1", "API", "down", fixedTemplateTime)

	_, err := Render(msg)
	if err == nil {
		t.Fatal("Render of unknown event type returned nil error")
	}
	if !errors.Is(err, ErrUnknownEventType) {
		t.Errorf("error = %v, want errors.Is(...,ErrUnknownEventType)", err)
	}
}

// TestRenderNoMetadataLeak guards SPEC §18.9's "no secrets in output" rule.
// Metadata is a free-form map that the delivery pipeline may populate with
// debugging context — including, by accident, secret values. The renderer
// must reference only the explicitly-public Message fields (EventType,
// MonitorID, MonitorName, State, Time, URL), so no Metadata key or value
// should ever appear in Title or Body, even when a caller stuffs a
// secret-shaped key in there.
func TestRenderNoMetadataLeak(t *testing.T) {
	msg := NewMonitorDownMessage("mon-1", "API", fixedTemplateTime)
	msg.URL = "https://api.example.com"
	msg.Metadata["webhook_token"] = "super-secret-token-value"
	msg.Metadata["password"] = "hunter2"

	got, err := Render(msg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, secret := range []string{"super-secret-token-value", "hunter2", "webhook_token", "password"} {
		if strings.Contains(got.Title, secret) {
			t.Errorf("Title leaked metadata key/value %q: %q", secret, got.Title)
		}
		if strings.Contains(got.Body, secret) {
			t.Errorf("Body leaked metadata key/value %q: %q", secret, got.Body)
		}
	}
}

// TestRenderDoesNotMutateInput documents that Render returns a new Message
// value rather than mutating the caller's struct. The delivery pipeline keeps
// a pre-render copy of the message for attempt logging (SPEC §12.3
// notification_attempts), and an in-place mutation here would silently break
// that audit trail.
func TestRenderDoesNotMutateInput(t *testing.T) {
	msg := NewMonitorDownMessage("mon-1", "API", fixedTemplateTime)

	_, err := Render(msg)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if msg.Title != "" {
		t.Errorf("input msg.Title was mutated to %q", msg.Title)
	}
	if msg.Body != "" {
		t.Errorf("input msg.Body was mutated to %q", msg.Body)
	}
}
