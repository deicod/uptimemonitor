package fake

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/notify"
)

func TestProviderImplementsInterface(t *testing.T) {
	var _ notify.Provider = New()
}

func TestKindAndDisplayName(t *testing.T) {
	p := New()
	if p.Kind() != "fake" {
		t.Errorf("Kind() = %q, want fake", p.Kind())
	}
	if p.DisplayName() == "" {
		t.Error("DisplayName() is empty")
	}
	if p.Fields() == nil {
		t.Error("Fields() = nil, want a non-nil empty slice (renders as [] on the wire)")
	}
}

func TestValidate(t *testing.T) {
	p := New()
	if err := p.Validate(context.Background(), nil); err != nil {
		t.Errorf("Validate(nil) = %v, want nil (fake has no required fields)", err)
	}
	if err := p.Validate(context.Background(), json.RawMessage(`{"a":1}`)); err != nil {
		t.Errorf("Validate(valid json) = %v, want nil", err)
	}
	if err := p.Validate(context.Background(), json.RawMessage(`{bad`)); err == nil {
		t.Error("Validate(malformed json) = nil, want error")
	}
}

// TestSendRecords is the reason the fake exists: downstream tests (delivery
// pipeline, IPC test endpoint, e2e smoke) assert on what was sent, so Send has
// to capture both the config and the message faithfully.
func TestSendRecords(t *testing.T) {
	p := New()
	cfg := json.RawMessage(`{"k":"v"}`)
	msg := notify.NewMonitorDownMessage("01HMON", "Example", time.Unix(0, 0).UTC())
	msg.Title = "t"
	msg.Body = "b"

	if err := p.Send(context.Background(), cfg, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sends := p.Sends()
	if len(sends) != 1 {
		t.Fatalf("Sends() len = %d, want 1", len(sends))
	}
	if sends[0].Message.MonitorID != "01HMON" || sends[0].Message.Title != "t" || sends[0].Message.Body != "b" {
		t.Errorf("recorded message = %+v", sends[0].Message)
	}
	if string(sends[0].Config) != string(cfg) {
		t.Errorf("recorded config = %q, want %q", sends[0].Config, cfg)
	}
}

// TestSendRecordsConfigSnapshot guards against aliasing: the pipeline reuses
// config buffers, so the fake must copy them or later mutations would corrupt
// already-recorded sends and make assertions lie.
func TestSendRecordsConfigSnapshot(t *testing.T) {
	p := New()
	cfg := json.RawMessage(`{"k":"v"}`)
	if err := p.Send(context.Background(), cfg, notify.Message{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	cfg[0] = 'X' // mutate the caller's slice after Send returned
	if string(p.Sends()[0].Config) == string(cfg) {
		t.Error("recorded config aliases the caller's slice; want a snapshot copy")
	}
}

// TestSendFuncControlsErrorButStillRecords lets retry tests (M9.9) force a
// failure while still observing the attempt — the send must be recorded even
// when the hook reports an error.
func TestSendFuncControlsErrorButStillRecords(t *testing.T) {
	p := New()
	sentinel := errors.New("boom")
	p.SetSendFunc(func(notify.Message) error { return sentinel })

	err := p.Send(context.Background(), nil, notify.Message{MonitorID: "x"})
	if !errors.Is(err, sentinel) {
		t.Errorf("Send err = %v, want sentinel", err)
	}
	if len(p.Sends()) != 1 {
		t.Errorf("send not recorded despite failure: got %d", len(p.Sends()))
	}
}

// TestSendConcurrent exercises the mutex under the race detector: the delivery
// pipeline calls Send from multiple worker goroutines, so an unsynchronised
// fake would be a flaky-test factory.
func TestSendConcurrent(t *testing.T) {
	p := New()
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_ = p.Send(context.Background(), nil, notify.Message{})
		})
	}
	wg.Wait()
	if len(p.Sends()) != 50 {
		t.Errorf("Sends() len = %d, want 50", len(p.Sends()))
	}
}
