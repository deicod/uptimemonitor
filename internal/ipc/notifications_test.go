package ipc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/uptimemonitor/internal/notify"
)

// fakeProvider is a minimal notify.Provider used by the IPC handler tests so
// they do not pull in the real provider implementations (M9.5–M9.8).
type fakeProvider struct {
	kind        string
	displayName string
	fields      []notify.Field
}

func (f *fakeProvider) Kind() string                                                { return f.kind }
func (f *fakeProvider) DisplayName() string                                         { return f.displayName }
func (f *fakeProvider) Fields() []notify.Field                                      { return f.fields }
func (f *fakeProvider) Validate(context.Context, json.RawMessage) error             { return nil }
func (f *fakeProvider) Send(context.Context, json.RawMessage, notify.Message) error { return nil }

// fakeProviderRegistry is the in-test stand-in for *notify.Registry so the
// handler tests can pin the wire shape without depending on Register
// behaviour (already covered in internal/notify/registry_test.go).
type fakeProviderRegistry struct {
	providers []notify.Provider
}

func (f *fakeProviderRegistry) List() []notify.Provider { return f.providers }

// TestListProvidersHandler_EmptyRegistry pins that an empty registry yields
// `{"providers":[]}` rather than `{"providers":null}`. TUI / client code
// loops over the array unconditionally; a null would crash it and the
// JSON-tags-only contract is what makes that safe.
func TestListProvidersHandler_EmptyRegistry(t *testing.T) {
	reg := &fakeProviderRegistry{}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationRegistry(reg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/notifications/providers", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusOK, rec.Body)
	}
	// We deliberately parse into a map first so we can distinguish
	// `"providers": []` from `"providers": null` — a typed struct with a
	// []NotificationProviderResponse field would accept both as len()==0.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v (body=%q)", err, rec.Body)
	}
	got, ok := raw["providers"]
	if !ok {
		t.Fatalf("response missing providers field: %q", rec.Body)
	}
	if string(got) != "[]" {
		t.Errorf("providers = %q, want \"[]\"", got)
	}
}

// TestListProvidersHandler_ReturnsFields covers the wire format with one
// provider that has the full SPEC §18.4 field metadata. The TUI uses this
// payload to render forms (M9.12) so every advertised field has to round
// trip: name, label, type, required, secret, default, description.
func TestListProvidersHandler_ReturnsFields(t *testing.T) {
	reg := &fakeProviderRegistry{providers: []notify.Provider{
		&fakeProvider{
			kind:        "webhook",
			displayName: "Webhook",
			fields: []notify.Field{
				{
					Name:     "url",
					Label:    "Webhook URL",
					Type:     notify.FieldTypeSecretString,
					Required: true,
					Secret:   true,
				},
				{
					Name:        "method",
					Label:       "HTTP Method",
					Type:        notify.FieldTypeString,
					Required:    true,
					Default:     "POST",
					Description: "HTTP method used to post the payload.",
				},
			},
		},
	}}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationRegistry(reg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/notifications/providers", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusOK, rec.Body)
	}
	var resp NotificationProvidersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body=%q)", err, rec.Body)
	}
	if len(resp.Providers) != 1 {
		t.Fatalf("providers len = %d, want 1", len(resp.Providers))
	}
	p := resp.Providers[0]
	if p.Kind != "webhook" || p.DisplayName != "Webhook" {
		t.Errorf("kind/display = %q/%q, want webhook/Webhook", p.Kind, p.DisplayName)
	}
	if len(p.Fields) != 2 {
		t.Fatalf("fields len = %d, want 2", len(p.Fields))
	}
	url := p.Fields[0]
	if url.Name != "url" || url.Type != notify.FieldTypeSecretString || !url.Required || !url.Secret {
		t.Errorf("url field = %+v", url)
	}
	method := p.Fields[1]
	if method.Default != "POST" || method.Description == "" {
		t.Errorf("method field lost default/description: %+v", method)
	}
}

// TestListProvidersHandler_OrderingFromRegistry pins that the handler
// preserves the order the registry returns. The registry sorts by kind
// (covered in registry_test) and the handler must not re-shuffle that.
func TestListProvidersHandler_OrderingFromRegistry(t *testing.T) {
	reg := &fakeProviderRegistry{providers: []notify.Provider{
		&fakeProvider{kind: "discord", displayName: "Discord"},
		&fakeProvider{kind: "email", displayName: "Email"},
		&fakeProvider{kind: "webhook", displayName: "Webhook"},
	}}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationRegistry(reg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/notifications/providers", nil)
	mux.ServeHTTP(rec, req)

	var resp NotificationProvidersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"discord", "email", "webhook"}
	if len(resp.Providers) != len(want) {
		t.Fatalf("providers len = %d, want %d", len(resp.Providers), len(want))
	}
	for i, kind := range want {
		if resp.Providers[i].Kind != kind {
			t.Errorf("[%d] kind = %q, want %q", i, resp.Providers[i].Kind, kind)
		}
	}
}

// TestListProvidersHandler_NotRegistered pins that the providers route is
// only mounted when WithNotificationRegistry is supplied — the same pattern
// other optional endpoint groups (history, checks) use to keep partial
// routers safe to construct. The server-level 404→envelope conversion is
// already covered by TestServerUnknownRoute, so here we only assert the
// status code: that the route is genuinely absent rather than silently
// registered against a nil registry.
func TestListProvidersHandler_NotRegistered(t *testing.T) {
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/notifications/providers", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
