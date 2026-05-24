package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
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

// ---------- notification target CRUD / test / attempts (M9.10) ----------

// fakeTargetStore is an in-memory NotificationTargetStore for handler tests. It
// mirrors the parts of the real repo the IPC layer relies on: Get/List redact
// the "secret" config key, GetWithSecrets returns it intact, and a missing id
// yields sqlite.ErrNotFound. That lets these tests prove the "secrets are never
// returned" rule (SPEC §18.9) without a real database. It deliberately does
// *not* reproduce the repo's blank-secret merge on Update — that behaviour is
// exercised end-to-end against the real repo in the client round-trip test.
type fakeTargetStore struct {
	mu   sync.Mutex
	byID map[string]*notify.Target
}

func newFakeTargetStore() *fakeTargetStore {
	return &fakeTargetStore{byID: map[string]*notify.Target{}}
}

func (s *fakeTargetStore) put(t *notify.Target) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[t.ID] = cloneTarget(t)
}

func (s *fakeTargetStore) List(context.Context) ([]*notify.Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*notify.Target, 0, len(ids))
	for _, id := range ids {
		out = append(out, redactTarget(s.byID[id]))
	}
	return out, nil
}

func (s *fakeTargetStore) Get(_ context.Context, id string) (*notify.Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.byID[id]
	if !ok {
		return nil, sqlite.ErrNotFound
	}
	return redactTarget(t), nil
}

func (s *fakeTargetStore) GetWithSecrets(_ context.Context, id string) (*notify.Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.byID[id]
	if !ok {
		return nil, sqlite.ErrNotFound
	}
	return cloneTarget(t), nil
}

func (s *fakeTargetStore) Insert(_ context.Context, t *notify.Target) error {
	s.put(t)
	return nil
}

func (s *fakeTargetStore) Update(_ context.Context, t *notify.Target) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[t.ID]; !ok {
		return sqlite.ErrNotFound
	}
	s.byID[t.ID] = cloneTarget(t)
	return nil
}

func (s *fakeTargetStore) SoftDelete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[id]; !ok {
		return sqlite.ErrNotFound
	}
	delete(s.byID, id)
	return nil
}

func cloneTarget(t *notify.Target) *notify.Target {
	cp := *t
	if t.Config != nil {
		cp.Config = append(json.RawMessage(nil), t.Config...)
	}
	return &cp
}

// redactTarget blanks the "secret" config key, standing in for the real repo's
// SecretFieldsFunc-driven redaction (SPEC §18.9).
func redactTarget(t *notify.Target) *notify.Target {
	cp := cloneTarget(t)
	if len(cp.Config) == 0 {
		return cp
	}
	m := map[string]any{}
	if err := json.Unmarshal(cp.Config, &m); err != nil {
		return cp
	}
	if _, ok := m["secret"]; ok {
		m["secret"] = ""
		if b, err := json.Marshal(m); err == nil {
			cp.Config = b
		}
	}
	return cp
}

// fakeAttemptReader records the limit it was asked for and returns a fixed set.
type fakeAttemptReader struct {
	attempts []*notify.Attempt
	err      error
	gotLimit int
}

func (f *fakeAttemptReader) ListRecent(_ context.Context, limit int) ([]*notify.Attempt, error) {
	f.gotLimit = limit
	return f.attempts, f.err
}

// fakeTester records the call to Test and returns a configurable error, so the
// handler test can verify wiring (real-pipeline delivery is covered by the
// client round-trip test).
type fakeTester struct {
	called    bool
	gotTarget *notify.Target
	gotMsg    notify.Message
	err       error
}

func (f *fakeTester) Test(_ context.Context, target *notify.Target, msg notify.Message) error {
	f.called = true
	f.gotTarget = target
	f.gotMsg = msg
	return f.err
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) *APIError {
	t.Helper()
	apiErr, err := DecodeError(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode error envelope: %v (body=%q)", err, rec.Body)
	}
	return apiErr
}

// TestCreateTargetHandler_RedactsSecretInResponse pins the headline M9.10 rule:
// a created target's response carries the stored, redacted config, never the
// secret the caller just submitted. The handler proves it by re-reading through
// Get rather than echoing the request body.
func TestCreateTargetHandler_RedactsSecretInResponse(t *testing.T) {
	store := newFakeTargetStore()
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationTargets(store))

	body := `{"name":"Ops","kind":"gotify","enabled":true,` +
		`"config":{"server_url":"https://g","secret":"tok"}}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/notifications/targets", strings.NewReader(body)))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusCreated, rec.Body)
	}
	var resp NotificationTargetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == "" {
		t.Error("created target has no server-assigned ID")
	}
	if resp.Name != "Ops" || resp.Kind != "gotify" || !resp.Enabled {
		t.Errorf("identity mismatch: %+v", resp)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(resp.Config, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg["secret"] != "" {
		t.Errorf("response leaked secret: %v", cfg["secret"])
	}
	if cfg["server_url"] != "https://g" {
		t.Errorf("public field lost: %v", cfg["server_url"])
	}
	// The store kept the real secret so the delivery pipeline can authenticate.
	stored, err := store.GetWithSecrets(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("GetWithSecrets: %v", err)
	}
	raw := map[string]any{}
	if err := json.Unmarshal(stored.Config, &raw); err != nil {
		t.Fatalf("decode stored: %v", err)
	}
	if raw["secret"] != "tok" {
		t.Errorf("stored secret = %v, want preserved \"tok\"", raw["secret"])
	}
}

// TestCreateTargetHandler_Validation covers the required-field checks the
// handler performs before persistence.
func TestCreateTargetHandler_Validation(t *testing.T) {
	store := newFakeTargetStore()
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationTargets(store))

	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"blank name", `{"name":"  ","kind":"gotify"}`, "name"},
		{"missing kind", `{"name":"Ops","kind":""}`, "kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/notifications/targets", strings.NewReader(tc.body)))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusUnprocessableEntity, rec.Body)
			}
			apiErr := decodeEnvelope(t, rec)
			if apiErr.Code != ErrValidation || apiErr.Field != tc.field {
				t.Errorf("error = %+v, want validation_error field=%s", apiErr, tc.field)
			}
		})
	}
}

// TestListTargetsHandler_RedactsSecrets verifies the list endpoint returns
// targets in creation order with secrets blanked.
func TestListTargetsHandler_RedactsSecrets(t *testing.T) {
	store := newFakeTargetStore()
	store.put(&notify.Target{ID: "01HA", Name: "A", Kind: "gotify", Enabled: true,
		Config: json.RawMessage(`{"secret":"a"}`)})
	store.put(&notify.Target{ID: "01HB", Name: "B", Kind: "slack", Enabled: false,
		Config: json.RawMessage(`{"webhook_url":"https://h"}`)})
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationTargets(store))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/notifications/targets", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body)
	}
	var resp NotificationTargetListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Targets) != 2 || resp.Targets[0].ID != "01HA" || resp.Targets[1].ID != "01HB" {
		t.Fatalf("targets = %+v, want [01HA,01HB]", resp.Targets)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(resp.Targets[0].Config, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg["secret"] != "" {
		t.Errorf("list leaked secret: %v", cfg["secret"])
	}
}

// TestGetTargetHandler_NotFound maps a missing target to not_found.
func TestGetTargetHandler_NotFound(t *testing.T) {
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationTargets(newFakeTargetStore()))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/notifications/targets/missing", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%q)", rec.Code, rec.Body)
	}
	if apiErr := decodeEnvelope(t, rec); apiErr.Code != ErrNotFound {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrNotFound)
	}
}

// TestUpdateTargetHandler_Partial verifies a name-only PATCH applies the name,
// leaves the kind untouched (kind is immutable), and returns the redacted view.
func TestUpdateTargetHandler_Partial(t *testing.T) {
	store := newFakeTargetStore()
	store.put(&notify.Target{ID: "01HT", Name: "Ops", Kind: "gotify", Enabled: true,
		Config: json.RawMessage(`{"server_url":"https://g","secret":"tok"}`)})
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationTargets(store))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/notifications/targets/01HT",
		strings.NewReader(`{"name":"Renamed"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body)
	}
	var resp NotificationTargetResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "Renamed" {
		t.Errorf("Name = %q, want Renamed", resp.Name)
	}
	if resp.Kind != "gotify" {
		t.Errorf("Kind = %q, want gotify (kind is immutable)", resp.Kind)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(resp.Config, &cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg["secret"] != "" {
		t.Errorf("update response leaked secret: %v", cfg["secret"])
	}
}

// TestDeleteTargetHandler covers a successful soft-delete (204, then 404 on a
// subsequent read) and deleting a missing target (404).
func TestDeleteTargetHandler(t *testing.T) {
	store := newFakeTargetStore()
	store.put(&notify.Target{ID: "01HT", Name: "Ops", Kind: "gotify"})
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationTargets(store))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/notifications/targets/01HT", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204 (body=%q)", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/notifications/targets/01HT", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/notifications/targets/01HT", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", rec.Code)
	}
}

// TestTestTargetHandler_Success verifies the test endpoint loads the target
// with its real secret, builds a manual_test message carrying the target name,
// and reports sent=true.
func TestTestTargetHandler_Success(t *testing.T) {
	store := newFakeTargetStore()
	store.put(&notify.Target{ID: "01HT", Name: "Ops", Kind: "fake", Enabled: true,
		Config: json.RawMessage(`{"secret":"tok"}`)})
	tester := &fakeTester{}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil,
		WithNotificationTargets(store), WithNotificationTester(tester))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/notifications/targets/01HT/test", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body)
	}
	var resp TestNotificationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Sent {
		t.Error("Sent = false, want true")
	}
	if !tester.called {
		t.Fatal("tester.Test was not called")
	}
	// Delivery must use the real credentials, not the redacted view.
	raw := map[string]any{}
	if err := json.Unmarshal(tester.gotTarget.Config, &raw); err != nil {
		t.Fatalf("decode tester config: %v", err)
	}
	if raw["secret"] != "tok" {
		t.Errorf("tester saw secret = %v, want unredacted \"tok\"", raw["secret"])
	}
	if tester.gotMsg.EventType != notify.EventManualTest {
		t.Errorf("event type = %q, want %q", tester.gotMsg.EventType, notify.EventManualTest)
	}
	if tester.gotMsg.MonitorName != "Ops" {
		t.Errorf("message name = %q, want target name \"Ops\"", tester.gotMsg.MonitorName)
	}
}

// TestTestTargetHandler_ProviderError maps a delivery failure to provider_error
// while still returning the sanitised reason.
func TestTestTargetHandler_ProviderError(t *testing.T) {
	store := newFakeTargetStore()
	store.put(&notify.Target{ID: "01HT", Name: "Ops", Kind: "fake", Enabled: true})
	tester := &fakeTester{err: errors.New("connection refused")}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil,
		WithNotificationTargets(store), WithNotificationTester(tester))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/notifications/targets/01HT/test", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d (body=%q)", rec.Code, http.StatusBadGateway, rec.Body)
	}
	apiErr := decodeEnvelope(t, rec)
	if apiErr.Code != ErrProvider {
		t.Errorf("code = %q, want %q", apiErr.Code, ErrProvider)
	}
	if !strings.Contains(apiErr.Message, "connection refused") {
		t.Errorf("message = %q, want it to mention the provider error", apiErr.Message)
	}
}

// TestTestTargetHandler_NotFound returns 404 for an unknown target and never
// calls the tester.
func TestTestTargetHandler_NotFound(t *testing.T) {
	tester := &fakeTester{}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil,
		WithNotificationTargets(newFakeTargetStore()), WithNotificationTester(tester))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/notifications/targets/missing/test", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%q)", rec.Code, rec.Body)
	}
	if tester.called {
		t.Error("tester.Test was called for a missing target")
	}
}

// TestListAttemptsHandler verifies the global attempts endpoint encodes the
// full attempt shape and applies the default limit when none is given.
func TestListAttemptsHandler(t *testing.T) {
	sent := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	mid := "01HXMON"
	reader := &fakeAttemptReader{attempts: []*notify.Attempt{
		{ID: "01HA2", TargetID: "01HT", EventType: notify.EventManualTest,
			Status: notify.AttemptStatusSuccess, AttemptNumber: 1, CreatedAt: sent, SentAt: &sent},
		{ID: "01HA1", TargetID: "01HT", MonitorID: &mid, EventType: notify.EventMonitorDown,
			Status: notify.AttemptStatusFailure, AttemptNumber: 2, Error: "boom", CreatedAt: sent},
	}}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationAttempts(reader))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/notifications/attempts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%q)", rec.Code, rec.Body)
	}
	var resp NotificationAttemptListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Attempts) != 2 {
		t.Fatalf("attempts len = %d, want 2", len(resp.Attempts))
	}
	if reader.gotLimit != defaultListLimit {
		t.Errorf("default limit = %d, want %d", reader.gotLimit, defaultListLimit)
	}
	a0 := resp.Attempts[0]
	if a0.ID != "01HA2" || a0.Status != notify.AttemptStatusSuccess || a0.SentAt == nil {
		t.Errorf("attempt[0] = %+v", a0)
	}
	a1 := resp.Attempts[1]
	if a1.Error != "boom" || a1.MonitorID == nil || *a1.MonitorID != mid {
		t.Errorf("attempt[1] = %+v", a1)
	}
}

// TestListAttemptsHandler_LimitParam passes a valid limit through and rejects a
// malformed one with bad_request.
func TestListAttemptsHandler_LimitParam(t *testing.T) {
	reader := &fakeAttemptReader{}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationAttempts(reader))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/notifications/attempts?limit=5", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if reader.gotLimit != 5 {
		t.Errorf("limit = %d, want 5", reader.gotLimit)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/notifications/attempts?limit=nope", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed limit status = %d, want 400", rec.Code)
	}
}

// fakeSettingStore is an in-memory NotificationSettingStore for handler tests.
type fakeSettingStore struct {
	enabled bool
	gotSet  *bool
}

func (f *fakeSettingStore) NotificationsEnabled(context.Context) bool { return f.enabled }
func (f *fakeSettingStore) SetNotificationsEnabled(_ context.Context, enabled bool) error {
	f.enabled = enabled
	f.gotSet = &enabled
	return nil
}

// TestNotificationSettingsHandlers covers the global toggle: GET reports the
// effective value and PUT flips it, returning the new state.
func TestNotificationSettingsHandlers(t *testing.T) {
	store := &fakeSettingStore{enabled: true}
	mux := NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationSettings(store))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/notifications/settings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200 (body=%q)", rec.Code, rec.Body)
	}
	var got NotificationSettingsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if !got.Enabled {
		t.Error("GET enabled = false, want true")
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/notifications/settings", strings.NewReader(`{"enabled":false}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200 (body=%q)", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode PUT: %v", err)
	}
	if got.Enabled {
		t.Error("PUT response enabled = true, want false")
	}
	if store.gotSet == nil || *store.gotSet != false {
		t.Errorf("store SetNotificationsEnabled = %v, want false", store.gotSet)
	}
}
