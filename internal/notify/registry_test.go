package notify

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// fakeProvider is a minimal Provider used by registry tests. It captures the
// fields the registry surface cares about (Kind, DisplayName, Fields) and
// stubs out Validate/Send because the registry never invokes them itself —
// those run in the delivery pipeline (M9.9) and are out of scope here.
type fakeProvider struct {
	kind        string
	displayName string
	fields      []Field
}

func (f *fakeProvider) Kind() string                                         { return f.kind }
func (f *fakeProvider) DisplayName() string                                  { return f.displayName }
func (f *fakeProvider) Fields() []Field                                      { return f.fields }
func (f *fakeProvider) Validate(context.Context, json.RawMessage) error      { return nil }
func (f *fakeProvider) Send(context.Context, json.RawMessage, Message) error { return nil }

// TestRegistryRegisterLookup pins the basic contract: a provider registered
// under its Kind is retrievable by that same Kind. The delivery pipeline
// (M9.9) and the IPC providers endpoint (this task) both rely on Lookup to
// resolve persisted target rows back to the provider that handles them, so
// breaking this round-trip silently corrupts every notification.
func TestRegistryRegisterLookup(t *testing.T) {
	reg := NewRegistry()
	p := &fakeProvider{kind: "webhook", displayName: "Webhook"}

	if err := reg.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, err := reg.Lookup("webhook")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got != p {
		t.Errorf("Lookup returned a different provider instance than was registered")
	}
}

// TestRegistryUnknownKind covers the missing-provider path. Lookup must return
// ErrUnknownKind (matchable with errors.Is) so callers can distinguish a
// configuration error ("no provider for kind X") from any other failure —
// e.g. the IPC layer maps it to a not_found / validation_error, while the
// delivery pipeline turns it into a permanent attempt failure.
func TestRegistryUnknownKind(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Lookup("does-not-exist")
	if err == nil {
		t.Fatal("Lookup of unknown kind returned nil error")
	}
	if !errors.Is(err, ErrUnknownKind) {
		t.Errorf("error = %v, want errors.Is(...,ErrUnknownKind)", err)
	}
}

// TestRegistryDuplicateKind ensures the registry rejects collisions. Two
// providers under the same kind would make Lookup non-deterministic (last
// write wins in a map) and silently mis-route notifications, so we surface
// the conflict at registration time.
func TestRegistryDuplicateKind(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeProvider{kind: "webhook"}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	err := reg.Register(&fakeProvider{kind: "webhook", displayName: "different"})
	if err == nil {
		t.Fatal("duplicate Register returned nil error")
	}
}

// TestRegistryEmptyKind rejects providers that would register under the
// zero-value key. An empty kind is never valid in SPEC §18.5 and would mask
// a misconfigured provider behind a Lookup("") call.
func TestRegistryEmptyKind(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeProvider{kind: ""}); err == nil {
		t.Fatal("Register with empty kind returned nil error")
	}
}

// TestRegistryListEmpty pins that List on a fresh registry returns an empty
// (non-nil) slice. The IPC handler marshals the result directly into the
// providers endpoint's `"providers":[]` field; a nil slice would render as
// `null` and break clients that iterate the array unconditionally.
func TestRegistryListEmpty(t *testing.T) {
	reg := NewRegistry()
	got := reg.List()
	if got == nil {
		t.Fatal("List() on empty registry returned nil, want empty slice")
	}
	if len(got) != 0 {
		t.Errorf("List() len = %d, want 0", len(got))
	}
}

// TestRegistrySecretFields verifies SecretFields reports exactly the field
// names a provider marks secret, derived from its Fields() metadata. The
// notification target repository (M9.4) consumes this as its SecretFieldsFunc
// to redact secrets on read and preserve them on a blank update (SPEC §18.9);
// deriving the list from the provider keeps it from drifting out of sync with
// the provider's own declared fields.
func TestRegistrySecretFields(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeProvider{
		kind: "demo",
		fields: []Field{
			{Name: "url", Type: FieldTypeString},
			{Name: "token", Type: FieldTypeSecretString, Secret: true},
			{Name: "password", Type: FieldTypeSecretString, Secret: true},
		},
	}); err != nil {
		t.Fatalf("Register demo: %v", err)
	}
	if err := reg.Register(&fakeProvider{kind: "plain", fields: []Field{{Name: "url"}}}); err != nil {
		t.Fatalf("Register plain: %v", err)
	}

	got := reg.SecretFields("demo")
	if len(got) != 2 || got[0] != "token" || got[1] != "password" {
		t.Errorf("SecretFields(demo) = %v, want [token password]", got)
	}
	if got := reg.SecretFields("plain"); len(got) != 0 {
		t.Errorf("SecretFields(plain) = %v, want empty", got)
	}
	if got := reg.SecretFields("unknown"); got != nil {
		t.Errorf("SecretFields(unknown) = %v, want nil", got)
	}
}

// TestRegistryListOrdered pins that List returns providers in deterministic
// (kind-sorted) order. The wire response uses this order, and an unsorted
// map iteration would make the providers endpoint flaky for any TUI or
// integration test that compares output across runs.
func TestRegistryListOrdered(t *testing.T) {
	reg := NewRegistry()
	// Register in a deliberately non-alphabetical order so a map-iteration
	// implementation would have a non-trivial chance of returning sorted.
	for _, kind := range []string{"webhook", "discord", "email", "telegram"} {
		if err := reg.Register(&fakeProvider{kind: kind}); err != nil {
			t.Fatalf("Register(%q): %v", kind, err)
		}
	}
	got := reg.List()
	if len(got) != 4 {
		t.Fatalf("List() len = %d, want 4", len(got))
	}
	want := []string{"discord", "email", "telegram", "webhook"}
	for i, p := range got {
		if p.Kind() != want[i] {
			t.Errorf("List()[%d].Kind() = %q, want %q", i, p.Kind(), want[i])
		}
	}
}
