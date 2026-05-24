package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/deicod/uptimemonitor/internal/notify"
	fakeprov "github.com/deicod/uptimemonitor/internal/notify/providers/fake"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// TestClientNotificationProviders exercises the full HTTP round-trip over
// the Unix socket so the client decodes the same wire shape the server
// produces. The handler tests in notifications_test.go already pin the
// envelope; here we verify the typed client returns the fields verbatim,
// which is what the TUI provider form (M9.12) consumes.
func TestClientNotificationProviders(t *testing.T) {
	reg := &fakeProviderRegistry{providers: []notify.Provider{
		&fakeProvider{
			kind:        "webhook",
			displayName: "Webhook",
			fields: []notify.Field{
				{Name: "url", Type: notify.FieldTypeSecretString, Required: true, Secret: true},
				{Name: "method", Type: notify.FieldTypeString, Required: true, Default: "POST"},
			},
		},
		&fakeProvider{kind: "email", displayName: "Email"},
	}}
	sock := filepath.Join(t.TempDir(), "test.sock")
	startTestServer(t, sock, NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationRegistry(reg)))
	client := NewClient(sock)

	got, err := client.NotificationProviders(context.Background())
	if err != nil {
		t.Fatalf("NotificationProviders: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("providers len = %d, want 2", len(got))
	}
	// The slice mirrors the registry's order so a TUI list won't shuffle.
	if got[0].Kind != "webhook" || got[1].Kind != "email" {
		t.Errorf("kinds = [%q,%q], want [webhook,email]", got[0].Kind, got[1].Kind)
	}
	if len(got[0].Fields) != 2 {
		t.Fatalf("webhook fields len = %d, want 2", len(got[0].Fields))
	}
	if !got[0].Fields[0].Secret || got[0].Fields[0].Type != notify.FieldTypeSecretString {
		t.Errorf("first field lost secret/type metadata: %+v", got[0].Fields[0])
	}
	if got[0].Fields[1].Default != "POST" {
		t.Errorf("default lost over the wire: %+v", got[0].Fields[1])
	}
}

// openTestStore opens a migrated temp SQLite store for the round-trip tests.
func openTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

// testSecretFields marks gotify's token secret so the round-trip can prove
// redaction and blank-secret preservation against the real repository.
func testSecretFields(kind string) []string {
	if kind == "gotify" {
		return []string{"token"}
	}
	return nil
}

func assertTokenRedacted(t *testing.T, where string, config json.RawMessage) {
	t.Helper()
	cfg := map[string]any{}
	if err := json.Unmarshal(config, &cfg); err != nil {
		t.Fatalf("%s: decode config: %v", where, err)
	}
	if v, ok := cfg["token"]; !ok || v != "" {
		t.Errorf("%s: token = %v, want redacted to \"\"", where, v)
	}
}

// TestClientNotificationTargetsRoundTrip exercises the full target lifecycle
// over a real Unix socket against the real SQLite repository: create → list →
// get → update → delete. It is the M9.10 secret contract proof — the token a
// caller submits is never returned, yet survives a blank-secret update in
// storage (SPEC §18.9).
func TestClientNotificationTargetsRoundTrip(t *testing.T) {
	store := openTestStore(t)
	repo := sqlite.NewNotificationTargetRepo(store, testSecretFields)

	sock := filepath.Join(t.TempDir(), "test.sock")
	startTestServer(t, sock, NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationTargets(repo)))
	client := NewClient(sock)
	ctx := context.Background()

	cfg, _ := json.Marshal(map[string]any{"server_url": "https://g", "token": "realtok"})
	created, err := client.CreateNotificationTarget(ctx, CreateNotificationTargetRequest{
		Name: "Ops", Kind: "gotify", Enabled: true, Config: cfg,
	})
	if err != nil {
		t.Fatalf("CreateNotificationTarget: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created target has no ID")
	}
	assertTokenRedacted(t, "create", created.Config)

	list, err := client.ListNotificationTargets(ctx)
	if err != nil {
		t.Fatalf("ListNotificationTargets: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
	assertTokenRedacted(t, "list", list[0].Config)

	got, err := client.GetNotificationTarget(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetNotificationTarget: %v", err)
	}
	assertTokenRedacted(t, "get", got.Config)

	// Update a public field while leaving the secret blank — the TUI "didn't
	// touch the token" flow. The stored secret must survive.
	upCfg, _ := json.Marshal(map[string]any{"server_url": "https://g2", "token": ""})
	name := "Renamed"
	updated, err := client.UpdateNotificationTarget(ctx, created.ID, UpdateNotificationTargetRequest{
		Name: &name, Config: upCfg,
	})
	if err != nil {
		t.Fatalf("UpdateNotificationTarget: %v", err)
	}
	if updated.Name != "Renamed" || updated.Kind != "gotify" {
		t.Errorf("updated identity = %q/%q, want Renamed/gotify", updated.Name, updated.Kind)
	}
	assertTokenRedacted(t, "update", updated.Config)

	raw, err := repo.GetWithSecrets(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetWithSecrets: %v", err)
	}
	rawCfg := map[string]any{}
	if err := json.Unmarshal(raw.Config, &rawCfg); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if rawCfg["token"] != "realtok" {
		t.Errorf("stored token = %v, want preserved \"realtok\"", rawCfg["token"])
	}
	if rawCfg["server_url"] != "https://g2" {
		t.Errorf("public field not updated: %v", rawCfg["server_url"])
	}

	if err := client.DeleteNotificationTarget(ctx, created.ID); err != nil {
		t.Fatalf("DeleteNotificationTarget: %v", err)
	}
	_, err = client.GetNotificationTarget(ctx, created.ID)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != ErrNotFound {
		t.Fatalf("get after delete err = %v, want not_found APIError", err)
	}
}

// TestClientTestNotificationTarget drives the test endpoint end-to-end through
// the real delivery pipeline with the fake provider, then reads the recorded
// attempt back through the global attempts endpoint — the M9.10 requirement
// that "the test endpoint invokes delivery with the fake provider."
func TestClientTestNotificationTarget(t *testing.T) {
	store := openTestStore(t)
	repo := sqlite.NewNotificationTargetRepo(store, testSecretFields)
	attempts := sqlite.NewNotificationAttemptRepo(store)

	reg := notify.NewRegistry()
	fp := fakeprov.New()
	if err := reg.Register(fp); err != nil {
		t.Fatalf("Register: %v", err)
	}
	pipe := notify.NewPipeline(reg, repo, attempts, notify.RetryConfig{MaxAttempts: 1}, nil)

	sock := filepath.Join(t.TempDir(), "test.sock")
	startTestServer(t, sock, NewRouter(fakeStatusProvider{}, nil, nil, nil,
		WithNotificationTargets(repo), WithNotificationTester(pipe), WithNotificationAttempts(attempts)))
	client := NewClient(sock)
	ctx := context.Background()

	created, err := client.CreateNotificationTarget(ctx, CreateNotificationTargetRequest{
		Name: "Ops", Kind: "fake", Enabled: true, Config: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("CreateNotificationTarget: %v", err)
	}

	res, err := client.TestNotificationTarget(ctx, created.ID)
	if err != nil {
		t.Fatalf("TestNotificationTarget: %v", err)
	}
	if !res.Sent {
		t.Error("Sent = false, want true")
	}

	sends := fp.Sends()
	if len(sends) != 1 {
		t.Fatalf("fake provider sends = %d, want 1", len(sends))
	}
	if sends[0].Message.EventType != notify.EventManualTest {
		t.Errorf("send event type = %q, want %q", sends[0].Message.EventType, notify.EventManualTest)
	}

	got, err := client.ListNotificationAttempts(ctx, 0)
	if err != nil {
		t.Fatalf("ListNotificationAttempts: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("attempts = %d, want 1", len(got))
	}
	if got[0].TargetID != created.ID {
		t.Errorf("attempt target = %q, want %q", got[0].TargetID, created.ID)
	}
	if got[0].Status != notify.AttemptStatusSuccess || got[0].EventType != notify.EventManualTest {
		t.Errorf("attempt = %+v, want success manual_test", got[0])
	}
}

// TestClientNotificationSettings round-trips the global toggle over the socket.
func TestClientNotificationSettings(t *testing.T) {
	store := &fakeSettingStore{enabled: true}
	sock := filepath.Join(t.TempDir(), "test.sock")
	startTestServer(t, sock, NewRouter(fakeStatusProvider{}, nil, nil, nil, WithNotificationSettings(store)))
	client := NewClient(sock)
	ctx := context.Background()

	enabled, err := client.GetNotificationsEnabled(ctx)
	if err != nil {
		t.Fatalf("GetNotificationsEnabled: %v", err)
	}
	if !enabled {
		t.Error("initial enabled = false, want true")
	}

	got, err := client.SetNotificationsEnabled(ctx, false)
	if err != nil {
		t.Fatalf("SetNotificationsEnabled: %v", err)
	}
	if got {
		t.Error("after set false, enabled = true, want false")
	}
	if again, _ := client.GetNotificationsEnabled(ctx); again {
		t.Error("toggle did not persist in the store")
	}
}
