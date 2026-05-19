package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// openMigrated opens a migrated temp database and returns the store so a test
// can build several repositories that share the same connection.
func openMigrated(t *testing.T) *Store {
	t.Helper()

	store, err := Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return store
}

// insertMonitor persists a sample monitor so that state and check-result rows
// have a valid foreign-key referent (foreign_keys is ON per SPEC §12.4).
func insertMonitor(t *testing.T, store *Store) string {
	t.Helper()

	m := sampleMonitor(t)
	if err := NewMonitorRepo(store).Insert(context.Background(), m); err != nil {
		t.Fatalf("Insert monitor: %v", err)
	}
	return m.ID
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

// TestMonitorStateRepoUpsertGet verifies that Upsert both inserts a new state
// row and overwrites an existing one — the scheduler upserts after every
// check, so a second call for the same monitor must replace, not duplicate.
func TestMonitorStateRepoUpsertGet(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	monitorID := insertMonitor(t, store)
	repo := NewMonitorStateRepo(store)

	now := time.Now().UTC().Truncate(time.Second)
	want := &monitor.MonitorStatus{
		MonitorID:            monitorID,
		State:                monitor.StateUp,
		LastCheckID:          strPtr(monitor.NewID()),
		LastCheckedAt:        &now,
		LastSuccessAt:        &now,
		ConsecutiveSuccesses: 3,
		ConsecutiveFailures:  0,
		UpdatedAt:            now,
	}
	if err := repo.Upsert(ctx, want); err != nil {
		t.Fatalf("Upsert (insert): %v", err)
	}

	got, err := repo.Get(ctx, monitorID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != want.State || got.ConsecutiveSuccesses != 3 {
		t.Errorf("inserted state mismatch: got %+v", got)
	}
	if got.LastCheckID == nil || *got.LastCheckID != *want.LastCheckID {
		t.Errorf("LastCheckID = %v, want %v", got.LastCheckID, want.LastCheckID)
	}
	if got.LastCheckedAt == nil || !got.LastCheckedAt.Equal(now) {
		t.Errorf("LastCheckedAt = %v, want %v", got.LastCheckedAt, now)
	}
	if got.LastFailureAt != nil {
		t.Errorf("LastFailureAt = %v, want nil", got.LastFailureAt)
	}

	// A second Upsert for the same monitor must overwrite the row.
	later := now.Add(time.Minute)
	want.State = monitor.StateDown
	want.ConsecutiveSuccesses = 0
	want.ConsecutiveFailures = 1
	want.LastFailureAt = &later
	want.UpdatedAt = later
	if err := repo.Upsert(ctx, want); err != nil {
		t.Fatalf("Upsert (update): %v", err)
	}

	got, err = repo.Get(ctx, monitorID)
	if err != nil {
		t.Fatalf("Get after second upsert: %v", err)
	}
	if got.State != monitor.StateDown || got.ConsecutiveFailures != 1 || got.ConsecutiveSuccesses != 0 {
		t.Errorf("overwrite failed: got %+v", got)
	}
	if got.LastFailureAt == nil || !got.LastFailureAt.Equal(later) {
		t.Errorf("LastFailureAt = %v, want %v", got.LastFailureAt, later)
	}

	var count int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM monitor_states WHERE monitor_id = ?", monitorID).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("monitor_states row count = %d, want 1 (upsert must not duplicate)", count)
	}
}

// TestMonitorStateRepoGetNotFound verifies a monitor without a state row is
// reported as ErrNotFound so callers can distinguish it from a query failure.
func TestMonitorStateRepoGetNotFound(t *testing.T) {
	store := openMigrated(t)
	repo := NewMonitorStateRepo(store)

	if _, err := repo.Get(context.Background(), monitor.NewID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
	}
}

// sampleCheckResult builds a check result for the given monitor at started_at.
func sampleCheckResult(monitorID string, startedAt time.Time, success bool) *monitor.CheckResult {
	cr := &monitor.CheckResult{
		ID:         monitor.NewID(),
		MonitorID:  monitorID,
		StartedAt:  startedAt,
		FinishedAt: startedAt.Add(150 * time.Millisecond),
		Duration:   150 * time.Millisecond,
		Success:    success,
	}
	if success {
		cr.State = monitor.StateUp
		cr.HTTPStatusCode = intPtr(200)
	} else {
		cr.State = monitor.StateDown
		cr.Error = "connection refused"
	}
	return cr
}

// TestCheckResultRepoRoundTrip verifies every persisted field — including the
// nullable error and status code — survives an insert/list round-trip, since
// the TUI renders these directly.
func TestCheckResultRepoRoundTrip(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	monitorID := insertMonitor(t, store)
	repo := NewCheckResultRepo(store)

	start := time.Now().UTC().Truncate(time.Second)
	ok := sampleCheckResult(monitorID, start, true)
	fail := sampleCheckResult(monitorID, start.Add(time.Minute), false)
	for _, cr := range []*monitor.CheckResult{ok, fail} {
		if err := repo.Insert(ctx, cr); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	got, err := repo.ListRecent(ctx, monitorID, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListRecent returned %d, want 2", len(got))
	}

	// Most recent first: the failed check.
	gotFail, gotOK := got[0], got[1]
	if gotFail.ID != fail.ID || gotOK.ID != ok.ID {
		t.Fatalf("ordering wrong: got %s,%s", gotFail.ID, gotOK.ID)
	}
	if gotFail.Success || gotFail.State != monitor.StateDown || gotFail.Error != "connection refused" {
		t.Errorf("failed result mismatch: got %+v", gotFail)
	}
	if gotFail.HTTPStatusCode != nil {
		t.Errorf("failed result HTTPStatusCode = %v, want nil", gotFail.HTTPStatusCode)
	}
	if !gotOK.Success || gotOK.Duration != 150*time.Millisecond {
		t.Errorf("ok result mismatch: got %+v", gotOK)
	}
	if gotOK.HTTPStatusCode == nil || *gotOK.HTTPStatusCode != 200 {
		t.Errorf("ok result HTTPStatusCode = %v, want 200", gotOK.HTTPStatusCode)
	}
	if gotOK.Error != "" {
		t.Errorf("ok result Error = %q, want empty", gotOK.Error)
	}
	if !gotOK.StartedAt.Equal(ok.StartedAt) || !gotOK.FinishedAt.Equal(ok.FinishedAt) {
		t.Errorf("ok result timestamps mismatch: got %+v", gotOK)
	}
}

// TestCheckResultRepoListRecentOrdering verifies results come back newest
// first and that the limit caps the slice — the detail screen shows only the
// latest few checks.
func TestCheckResultRepoListRecentOrdering(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	monitorID := insertMonitor(t, store)
	repo := NewCheckResultRepo(store)

	base := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	var ids []string
	for i := 0; i < 5; i++ {
		cr := sampleCheckResult(monitorID, base.Add(time.Duration(i)*time.Minute), true)
		if err := repo.Insert(ctx, cr); err != nil {
			t.Fatalf("Insert: %v", err)
		}
		ids = append(ids, cr.ID)
	}

	got, err := repo.ListRecent(ctx, monitorID, 3)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListRecent(limit=3) returned %d, want 3", len(got))
	}
	// Newest three, in descending started_at order: ids[4], ids[3], ids[2].
	want := []string{ids[4], ids[3], ids[2]}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d = %s, want %s", i, got[i].ID, id)
		}
	}
}

// TestCheckResultRepoPruneOlderThan verifies the retention prune removes rows
// before the cutoff and keeps newer ones (SPEC §12.5, 30-day retention).
func TestCheckResultRepoPruneOlderThan(t *testing.T) {
	store := openMigrated(t)
	ctx := context.Background()
	monitorID := insertMonitor(t, store)
	repo := NewCheckResultRepo(store)

	now := time.Now().UTC().Truncate(time.Second)
	old := sampleCheckResult(monitorID, now.Add(-40*24*time.Hour), true)
	recent := sampleCheckResult(monitorID, now.Add(-1*time.Hour), true)
	for _, cr := range []*monitor.CheckResult{old, recent} {
		if err := repo.Insert(ctx, cr); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	cutoff := now.Add(-30 * 24 * time.Hour)
	deleted, err := repo.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Errorf("PruneOlderThan deleted %d rows, want 1", deleted)
	}

	got, err := repo.ListRecent(ctx, monitorID, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(got) != 1 || got[0].ID != recent.ID {
		t.Errorf("after prune got %d rows, want only %s", len(got), recent.ID)
	}
}
