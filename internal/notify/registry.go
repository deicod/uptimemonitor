package notify

import (
	"errors"
	"fmt"
	"sort"
)

// ErrUnknownKind is returned by Registry.Lookup when no provider has been
// registered for the requested kind. Callers should match this with
// errors.Is so the error chain stays intact through wrapping.
var ErrUnknownKind = errors.New("notify: unknown provider kind")

// Registry is the lookup table from a provider Kind() string to the
// concrete Provider implementation (SPEC §18.3). It is built once at
// service startup — providers register themselves via Register — and is
// read-only thereafter, so it does not carry a mutex.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register adds p to the registry. It returns an error if p.Kind() is empty
// or if a provider has already been registered under the same kind: silently
// overwriting would make Lookup non-deterministic and mis-route notifications.
func (r *Registry) Register(p Provider) error {
	kind := p.Kind()
	if kind == "" {
		return errors.New("notify: provider has empty Kind()")
	}
	if _, exists := r.providers[kind]; exists {
		return fmt.Errorf("notify: provider %q is already registered", kind)
	}
	r.providers[kind] = p
	return nil
}

// Lookup returns the provider registered under kind. If no such provider
// exists it returns ErrUnknownKind wrapped with the requested kind so the
// caller can surface it without losing context.
func (r *Registry) Lookup(kind string) (Provider, error) {
	p, ok := r.providers[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}
	return p, nil
}

// SecretFields returns the names of the fields kind marks as secret, derived
// from the provider's Fields() metadata. An unknown kind yields nil. The
// notification target repository (M9.4) takes this as its SecretFieldsFunc, so
// secret redaction (SPEC §18.9) always tracks the provider's declared fields
// rather than a hand-maintained duplicate list.
func (r *Registry) SecretFields(kind string) []string {
	p, ok := r.providers[kind]
	if !ok {
		return nil
	}
	var out []string
	for _, f := range p.Fields() {
		if f.Secret {
			out = append(out, f.Name)
		}
	}
	return out
}

// List returns the registered providers sorted by Kind. The IPC providers
// endpoint and the TUI provider-picker depend on a deterministic order;
// returning an empty (non-nil) slice keeps JSON encoding stable too.
func (r *Registry) List() []Provider {
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind() < out[j].Kind() })
	return out
}
