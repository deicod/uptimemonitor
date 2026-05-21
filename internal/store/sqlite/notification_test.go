package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/notify"
)

// secretFieldsFor returns secret field names for the test fixtures' kinds —
// the production wiring will route through notify.Registry, but tests stub a
// minimal table so they do not depend on M9.5+ provider implementations.
func secretFieldsFor(kind string) []string {
	switch kind {
	case "webhook":
		return nil
	case "email":
		return []string{"password"}
	case "gotify":
		return []string{"token"}
	}
	return nil
}

// sampleTarget builds a valid in-memory notification target for repo tests.
// Kind defaults to "gotify" because Gotify has both a public field
// (server_url) and a secret field (token), exercising the redaction logic.
func sampleTarget(t *testing.T) *notify.Target {
	t.Helper()

	cfg, err := json.Marshal(map[string]any{
		"server_url": "https://gotify.example.com",
		"token":      "supersecret",
		"priority":   5,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	return &notify.Target{
		ID:        monitor.NewID(),
		Name:      "Ops Gotify",
		Kind:      "gotify",
		Enabled:   true,
		Config:    cfg,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// TestNotificationTargetRepoRoundTrip verifies a target inserted into the
// repository round-trips through Get with all fields intact (after secret
// redaction). The delivery pipeline depends on the raw config surviving via
// GetWithSecrets; the redaction protects the IPC layer from leaking secrets.
func TestNotificationTargetRepoRoundTrip(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	repo := NewNotificationTargetRepo(store, secretFieldsFor)

	want := sampleTarget(t)
	if err := repo.Insert(ctx, want); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Default Get redacts secret fields — the API surface for IPC must
	// never leak them (SPEC §18.9).
	got, err := repo.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != want.ID || got.Name != want.Name || got.Kind != want.Kind || got.Enabled != want.Enabled {
		t.Errorf("identity mismatch: got %+v want %+v", got, want)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("timestamp mismatch: got %+v want %+v", got, want)
	}
	if got.DeletedAt != nil {
		t.Errorf("DeletedAt = %v, want nil", got.DeletedAt)
	}

	gotCfg := map[string]any{}
	if err := json.Unmarshal(got.Config, &gotCfg); err != nil {
		t.Fatalf("unmarshal redacted config: %v", err)
	}
	if v, ok := gotCfg["token"].(string); !ok || v != "" {
		t.Errorf("redacted token = %v, want empty string", gotCfg["token"])
	}
	if gotCfg["server_url"] != "https://gotify.example.com" {
		t.Errorf("public server_url field altered: %v", gotCfg["server_url"])
	}

	// GetWithSecrets returns the raw stored config — the delivery pipeline
	// must see the real secret to actually authenticate.
	raw, err := repo.GetWithSecrets(ctx, want.ID)
	if err != nil {
		t.Fatalf("GetWithSecrets: %v", err)
	}
	rawCfg := map[string]any{}
	if err := json.Unmarshal(raw.Config, &rawCfg); err != nil {
		t.Fatalf("unmarshal raw config: %v", err)
	}
	if rawCfg["token"] != "supersecret" {
		t.Errorf("raw token = %v, want \"supersecret\"", rawCfg["token"])
	}
}

// TestNotificationTargetRepoGetNotFound verifies a missing id is reported as
// ErrNotFound so IPC handlers can map it to a 404.
func TestNotificationTargetRepoGetNotFound(t *testing.T) {
	store := openMigrated(t)
	repo := NewNotificationTargetRepo(store, secretFieldsFor)

	if _, err := repo.Get(context.Background(), monitor.NewID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
	if _, err := repo.GetWithSecrets(context.Background(), monitor.NewID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetWithSecrets(missing) = %v, want ErrNotFound", err)
	}
}

// TestNotificationTargetRepoList verifies List returns all non-deleted
// targets, ordered by id (creation order), with secrets redacted. The TUI
// target list calls through to this; an unredacted leak would put secrets
// on a screen that may be observed by lower-privilege operators.
func TestNotificationTargetRepoList(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	repo := NewNotificationTargetRepo(store, secretFieldsFor)

	a := sampleTarget(t)
	b := sampleTarget(t)
	for _, tg := range []*notify.Target{a, b} {
		if err := repo.Insert(ctx, tg); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List returned %d, want 2", len(all))
	}
	// Order by id ascending — ULIDs sort lexically by time, so the first
	// insertion comes first.
	if all[0].ID != a.ID || all[1].ID != b.ID {
		t.Errorf("List order = %s,%s; want %s,%s", all[0].ID, all[1].ID, a.ID, b.ID)
	}
	for _, tg := range all {
		cfg := map[string]any{}
		if err := json.Unmarshal(tg.Config, &cfg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if v, ok := cfg["token"].(string); !ok || v != "" {
			t.Errorf("list target %s leaked secret: token=%v", tg.ID, cfg["token"])
		}
	}
}

// TestNotificationTargetRepoUpdatePreservesBlankSecret verifies that, when an
// update leaves a secret field blank, the stored secret is preserved rather
// than overwritten with empty. The TUI shows secrets as "set/unset" and
// submits empty for fields the operator did not change (SPEC §18.9); a naive
// overwrite would silently destroy the stored token.
func TestNotificationTargetRepoUpdatePreservesBlankSecret(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	repo := NewNotificationTargetRepo(store, secretFieldsFor)

	original := sampleTarget(t)
	if err := repo.Insert(ctx, original); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Caller submits an update where the secret field is empty — the
	// operator only meant to rename the target.
	updateCfg, err := json.Marshal(map[string]any{
		"server_url": "https://gotify.example.com",
		"token":      "",
		"priority":   7,
	})
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}
	updated := *original
	updated.Name = "Ops Gotify (renamed)"
	updated.Config = updateCfg
	updated.UpdatedAt = original.UpdatedAt.Add(time.Minute)

	if err := repo.Update(ctx, &updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	raw, err := repo.GetWithSecrets(ctx, original.ID)
	if err != nil {
		t.Fatalf("GetWithSecrets: %v", err)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(raw.Config, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg["token"] != "supersecret" {
		t.Errorf("token after blank update = %v, want preserved \"supersecret\"", cfg["token"])
	}
	if raw.Name != "Ops Gotify (renamed)" {
		t.Errorf("Name after update = %q, want renamed", raw.Name)
	}
	// Numeric fields and other public fields should reflect the update.
	if cfg["priority"] != float64(7) {
		t.Errorf("priority after update = %v, want 7", cfg["priority"])
	}
}

// TestNotificationTargetRepoUpdatePreservesOmittedSecret verifies that
// omitting a secret field entirely from the update payload also preserves the
// stored value — clients may send only the fields they wish to change.
func TestNotificationTargetRepoUpdatePreservesOmittedSecret(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	repo := NewNotificationTargetRepo(store, secretFieldsFor)

	original := sampleTarget(t)
	if err := repo.Insert(ctx, original); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// No "token" key at all in the update.
	updateCfg, err := json.Marshal(map[string]any{
		"server_url": "https://gotify.example.com",
		"priority":   3,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	updated := *original
	updated.Config = updateCfg
	updated.UpdatedAt = original.UpdatedAt.Add(time.Minute)
	if err := repo.Update(ctx, &updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	raw, err := repo.GetWithSecrets(ctx, original.ID)
	if err != nil {
		t.Fatalf("GetWithSecrets: %v", err)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(raw.Config, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg["token"] != "supersecret" {
		t.Errorf("token after omitted-secret update = %v, want preserved \"supersecret\"", cfg["token"])
	}
}

// TestNotificationTargetRepoUpdateRotatesSecret verifies that an update that
// provides a new non-empty secret writes it. Otherwise the operator could
// never rotate a leaked token.
func TestNotificationTargetRepoUpdateRotatesSecret(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	repo := NewNotificationTargetRepo(store, secretFieldsFor)

	original := sampleTarget(t)
	if err := repo.Insert(ctx, original); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	updateCfg, err := json.Marshal(map[string]any{
		"server_url": "https://gotify.example.com",
		"token":      "rotated",
		"priority":   5,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	updated := *original
	updated.Config = updateCfg
	updated.UpdatedAt = original.UpdatedAt.Add(time.Minute)
	if err := repo.Update(ctx, &updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	raw, err := repo.GetWithSecrets(ctx, original.ID)
	if err != nil {
		t.Fatalf("GetWithSecrets: %v", err)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(raw.Config, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg["token"] != "rotated" {
		t.Errorf("token after rotation update = %v, want \"rotated\"", cfg["token"])
	}
}

// TestNotificationTargetRepoUpdateNotFound verifies updating an unknown
// target returns ErrNotFound rather than silently affecting zero rows.
func TestNotificationTargetRepoUpdateNotFound(t *testing.T) {
	store := openMigrated(t)
	repo := NewNotificationTargetRepo(store, secretFieldsFor)

	tg := sampleTarget(t)
	if err := repo.Update(context.Background(), tg); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update(missing) = %v, want ErrNotFound", err)
	}
}

// TestNotificationTargetRepoSoftDelete verifies a soft-deleted target is
// hidden from Get and List but the row itself survives — notification_attempts
// rows reference target_id and would dangle if the row were physically
// removed.
func TestNotificationTargetRepoSoftDelete(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	repo := NewNotificationTargetRepo(store, secretFieldsFor)

	tg := sampleTarget(t)
	if err := repo.Insert(ctx, tg); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.SoftDelete(ctx, tg.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	if _, err := repo.Get(ctx, tg.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after SoftDelete = %v, want ErrNotFound", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List after SoftDelete returned %d, want 0", len(list))
	}

	// Second delete of an already-deleted target is ErrNotFound.
	if err := repo.SoftDelete(ctx, tg.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("SoftDelete twice = %v, want ErrNotFound", err)
	}

	// The row remains so historical attempts still resolve their target_id.
	var count int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM notification_targets WHERE id = ?", tg.ID).Scan(&count); err != nil {
		t.Fatalf("count row: %v", err)
	}
	if count != 1 {
		t.Errorf("notification_targets row count = %d, want 1 (soft-delete must not remove the row)", count)
	}
}

// TestNotificationTargetRepoNilSecretFields verifies the repo treats a nil
// SecretFieldsFunc as "no secret fields" — useful for tests and for kinds
// whose providers have no secret fields. Config is returned verbatim and
// updates pass through directly.
func TestNotificationTargetRepoNilSecretFields(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	repo := NewNotificationTargetRepo(store, nil)

	tg := sampleTarget(t)
	if err := repo.Insert(ctx, tg); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := repo.Get(ctx, tg.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(got.Config, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg["token"] != "supersecret" {
		t.Errorf("nil-secret-fields Get token = %v, want raw \"supersecret\"", cfg["token"])
	}
}

// TestNotificationAttemptRepoInsertAndListByTarget verifies attempts are
// stored and returned newest first per target. The TUI attempt list and the
// retry decision (whether to back off) both depend on this ordering.
func TestNotificationAttemptRepoInsertAndListByTarget(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	targetA := insertTarget(t, store)
	targetB := insertTarget(t, store)
	repo := NewNotificationAttemptRepo(store)

	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	// Three attempts for target A, one for target B.
	var aIDs []string
	for i := 0; i < 3; i++ {
		a := &notify.Attempt{
			ID:            monitor.NewID(),
			TargetID:      targetA,
			EventType:     notify.EventMonitorDown,
			Status:        notify.AttemptStatusFailure,
			AttemptNumber: i + 1,
			Error:         "connection refused",
			CreatedAt:     base.Add(time.Duration(i+1) * time.Minute),
		}
		if err := repo.Insert(ctx, a); err != nil {
			t.Fatalf("Insert A%d: %v", i, err)
		}
		aIDs = append(aIDs, a.ID)
	}
	sentAt := base.Add(time.Hour)
	bAttempt := &notify.Attempt{
		ID:            monitor.NewID(),
		TargetID:      targetB,
		EventType:     notify.EventManualTest,
		Status:        notify.AttemptStatusSuccess,
		AttemptNumber: 1,
		CreatedAt:     base.Add(2 * time.Minute),
		SentAt:        &sentAt,
	}
	if err := repo.Insert(ctx, bAttempt); err != nil {
		t.Fatalf("Insert B: %v", err)
	}

	// ListByTarget A returns its three attempts, newest first.
	gotA, err := repo.ListByTarget(ctx, targetA, 10)
	if err != nil {
		t.Fatalf("ListByTarget A: %v", err)
	}
	if len(gotA) != 3 {
		t.Fatalf("ListByTarget A returned %d, want 3", len(gotA))
	}
	for i, id := range []string{aIDs[2], aIDs[1], aIDs[0]} {
		if gotA[i].ID != id {
			t.Errorf("A position %d = %s, want %s", i, gotA[i].ID, id)
		}
	}
	if gotA[0].AttemptNumber != 3 || gotA[0].Status != notify.AttemptStatusFailure {
		t.Errorf("A[0] attempt mismatch: %+v", gotA[0])
	}
	if gotA[0].Error != "connection refused" {
		t.Errorf("A[0] error = %q, want \"connection refused\"", gotA[0].Error)
	}

	// ListByTarget B returns only B's attempt with the SentAt timestamp.
	gotB, err := repo.ListByTarget(ctx, targetB, 10)
	if err != nil {
		t.Fatalf("ListByTarget B: %v", err)
	}
	if len(gotB) != 1 || gotB[0].ID != bAttempt.ID {
		t.Errorf("ListByTarget B = %+v, want only %s", gotB, bAttempt.ID)
	}
	if gotB[0].SentAt == nil || !gotB[0].SentAt.Equal(sentAt) {
		t.Errorf("B SentAt = %v, want %v", gotB[0].SentAt, sentAt)
	}
	if gotB[0].Error != "" {
		t.Errorf("B Error = %q, want empty", gotB[0].Error)
	}
}

// TestNotificationAttemptRepoListByTargetLimit verifies a non-positive limit
// returns all rows and a positive limit caps the slice.
func TestNotificationAttemptRepoListByTargetLimit(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	targetID := insertTarget(t, store)
	repo := NewNotificationAttemptRepo(store)

	base := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 5; i++ {
		a := &notify.Attempt{
			ID:            monitor.NewID(),
			TargetID:      targetID,
			EventType:     notify.EventMonitorDown,
			Status:        notify.AttemptStatusFailure,
			AttemptNumber: i + 1,
			CreatedAt:     base.Add(time.Duration(i) * time.Second),
		}
		if err := repo.Insert(ctx, a); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	limited, err := repo.ListByTarget(ctx, targetID, 2)
	if err != nil {
		t.Fatalf("ListByTarget(limit=2): %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("ListByTarget(limit=2) returned %d, want 2", len(limited))
	}

	all, err := repo.ListByTarget(ctx, targetID, 0)
	if err != nil {
		t.Fatalf("ListByTarget(limit=0): %v", err)
	}
	if len(all) != 5 {
		t.Errorf("ListByTarget(limit=0) returned %d, want 5", len(all))
	}
}

// TestNotificationAttemptRepoListByTargetEmpty verifies an unknown target
// returns an empty slice, not an error — the TUI should render an empty list
// for a freshly-created target without any deliveries yet.
func TestNotificationAttemptRepoListByTargetEmpty(t *testing.T) {
	store := openMigrated(t)
	repo := NewNotificationAttemptRepo(store)

	got, err := repo.ListByTarget(context.Background(), monitor.NewID(), 10)
	if err != nil {
		t.Fatalf("ListByTarget: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListByTarget(unknown) returned %d, want 0", len(got))
	}
}

// TestNotificationAttemptRepoOptionalRefs verifies the optional monitor_id /
// incident_id / event_id fields round-trip via pointers, with NULL for the
// unset case (manual_test has no incident, for example).
func TestNotificationAttemptRepoOptionalRefs(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	targetID := insertTarget(t, store)
	monitorID := insertMonitor(t, store)
	repo := NewNotificationAttemptRepo(store)

	scoped := &notify.Attempt{
		ID:            monitor.NewID(),
		TargetID:      targetID,
		MonitorID:     &monitorID,
		EventType:     notify.EventMonitorDown,
		Status:        notify.AttemptStatusSuccess,
		AttemptNumber: 1,
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
	}
	if err := repo.Insert(ctx, scoped); err != nil {
		t.Fatalf("Insert scoped: %v", err)
	}
	unscoped := &notify.Attempt{
		ID:            monitor.NewID(),
		TargetID:      targetID,
		EventType:     notify.EventManualTest,
		Status:        notify.AttemptStatusSuccess,
		AttemptNumber: 1,
		CreatedAt:     scoped.CreatedAt.Add(time.Second),
	}
	if err := repo.Insert(ctx, unscoped); err != nil {
		t.Fatalf("Insert unscoped: %v", err)
	}

	got, err := repo.ListByTarget(ctx, targetID, 10)
	if err != nil {
		t.Fatalf("ListByTarget: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListByTarget returned %d, want 2", len(got))
	}
	// Newest first: unscoped (no monitor) precedes scoped.
	if got[0].MonitorID != nil {
		t.Errorf("unscoped MonitorID = %v, want nil", got[0].MonitorID)
	}
	if got[1].MonitorID == nil || *got[1].MonitorID != monitorID {
		t.Errorf("scoped MonitorID = %v, want %s", got[1].MonitorID, monitorID)
	}
}

// insertTarget persists a sample notification target so attempts can satisfy
// the target_id foreign key.
func insertTarget(t *testing.T, store *Store) string {
	t.Helper()

	tg := sampleTarget(t)
	if err := NewNotificationTargetRepo(store, secretFieldsFor).Insert(context.Background(), tg); err != nil {
		t.Fatalf("Insert target: %v", err)
	}
	return tg.ID
}
