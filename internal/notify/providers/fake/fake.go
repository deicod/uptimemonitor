// Package fake provides an in-memory notify.Provider for tests. It records
// every Send so assertions can inspect what the delivery pipeline produced,
// and an optional hook lets a test force Send to fail (to exercise retry in
// M9.9). It performs no I/O and is never registered in a production registry.
package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/deicod/uptimemonitor/internal/notify"
)

// Provider is a recording, no-I/O notification provider for tests.
type Provider struct {
	mu       sync.Mutex
	sends    []Sent
	sendFunc func(notify.Message) error
}

// Sent captures one Send call: the config it was given and the message it was
// asked to deliver.
type Sent struct {
	Config  json.RawMessage
	Message notify.Message
}

var _ notify.Provider = (*Provider)(nil)

// New returns a fake provider that records sends and reports success.
func New() *Provider { return &Provider{} }

// Kind implements notify.Provider.
func (p *Provider) Kind() string { return "fake" }

// DisplayName implements notify.Provider.
func (p *Provider) DisplayName() string { return "Fake (testing)" }

// Fields reports no configurable fields; the fake needs no config. A non-nil
// empty slice keeps it consistent with real providers on the wire.
func (p *Provider) Fields() []notify.Field { return []notify.Field{} }

// Validate accepts any config but rejects malformed JSON, so a caller passing
// a broken non-empty config gets a clear error instead of a silent accept.
func (p *Provider) Validate(_ context.Context, config json.RawMessage) error {
	if len(config) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(config, &v); err != nil {
		return fmt.Errorf("fake: invalid config json: %w", err)
	}
	return nil
}

// Send records the call and returns the result of the configured send hook
// (nil when none is set). The send is recorded before the hook runs so a
// forced failure remains observable.
func (p *Provider) Send(_ context.Context, config json.RawMessage, msg notify.Message) error {
	p.mu.Lock()
	p.sends = append(p.sends, Sent{Config: cloneRaw(config), Message: msg})
	fn := p.sendFunc
	p.mu.Unlock()
	if fn != nil {
		return fn(msg)
	}
	return nil
}

// SetSendFunc installs a hook that decides the error Send returns. Pass nil to
// restore the default (always succeed). Set it before concurrent use.
func (p *Provider) SetSendFunc(fn func(notify.Message) error) {
	p.mu.Lock()
	p.sendFunc = fn
	p.mu.Unlock()
}

// Sends returns a snapshot copy of the recorded sends.
func (p *Provider) Sends() []Sent {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Sent, len(p.sends))
	copy(out, p.sends)
	return out
}

// cloneRaw copies the config bytes so a caller reusing its buffer can't mutate
// an already-recorded send.
func cloneRaw(b json.RawMessage) json.RawMessage {
	if b == nil {
		return nil
	}
	out := make(json.RawMessage, len(b))
	copy(out, b)
	return out
}
