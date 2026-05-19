package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// newMonitorRepo opens a migrated temp database and returns a monitor
// repository bound to it.
func newMonitorRepo(t *testing.T) *MonitorRepo {
	t.Helper()

	store, err := Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return NewMonitorRepo(store)
}

// sampleMonitor builds a valid in-memory monitor for repository tests.
func sampleMonitor(t *testing.T) *monitor.Monitor {
	t.Helper()

	cfg, err := json.Marshal(monitor.HTTPMonitorConfig{
		URL:               "https://example.com",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	return &monitor.Monitor{
		ID:                   monitor.NewID(),
		Name:                 "Example",
		Type:                 monitor.MonitorTypeHTTP,
		Enabled:              true,
		Interval:             60 * time.Second,
		Timeout:              10 * time.Second,
		Config:               cfg,
		NotificationsEnabled: true,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

// TestMonitorRepoRoundTrip verifies that a monitor inserted into the
// repository is returned unchanged by Get — config persistence matters
// because the scheduler decodes it to drive the probe.
func TestMonitorRepoRoundTrip(t *testing.T) {
	repo := newMonitorRepo(t)
	ctx := context.Background()

	want := sampleMonitor(t)
	if err := repo.Insert(ctx, want); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ID != want.ID || got.Name != want.Name || got.Type != want.Type {
		t.Errorf("identity mismatch: got %+v want %+v", got, want)
	}
	if got.Enabled != want.Enabled || got.NotificationsEnabled != want.NotificationsEnabled {
		t.Errorf("flag mismatch: got %+v want %+v", got, want)
	}
	if got.Interval != want.Interval || got.Timeout != want.Timeout {
		t.Errorf("duration mismatch: got interval=%s timeout=%s want interval=%s timeout=%s",
			got.Interval, got.Timeout, want.Interval, want.Timeout)
	}
	if string(got.Config) != string(want.Config) {
		t.Errorf("config mismatch: got %s want %s", got.Config, want.Config)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Errorf("timestamp mismatch: got %+v want %+v", got, want)
	}
	if got.DeletedAt != nil {
		t.Errorf("DeletedAt = %v, want nil", got.DeletedAt)
	}
}

// TestMonitorRepoGetNotFound verifies a missing id is reported as ErrNotFound
// rather than a generic database error, so callers can map it to a 404.
func TestMonitorRepoGetNotFound(t *testing.T) {
	repo := newMonitorRepo(t)

	_, err := repo.Get(context.Background(), monitor.NewID())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrNotFound", err)
	}
}

// TestMonitorRepoUpdate verifies an updated monitor's new field values are
// persisted and the row is re-read with them.
func TestMonitorRepoUpdate(t *testing.T) {
	repo := newMonitorRepo(t)
	ctx := context.Background()

	m := sampleMonitor(t)
	if err := repo.Insert(ctx, m); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	m.Name = "Renamed"
	m.Enabled = false
	m.Interval = 120 * time.Second
	m.UpdatedAt = m.UpdatedAt.Add(time.Minute)
	if err := repo.Update(ctx, m); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Renamed" || got.Enabled != false || got.Interval != 120*time.Second {
		t.Errorf("update not persisted: got %+v", got)
	}
	if !got.UpdatedAt.Equal(m.UpdatedAt) {
		t.Errorf("UpdatedAt = %s, want %s", got.UpdatedAt, m.UpdatedAt)
	}
}

// TestMonitorRepoUpdateNotFound verifies updating an unknown monitor returns
// ErrNotFound instead of silently affecting zero rows.
func TestMonitorRepoUpdateNotFound(t *testing.T) {
	repo := newMonitorRepo(t)

	m := sampleMonitor(t)
	if err := repo.Update(context.Background(), m); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(missing) error = %v, want ErrNotFound", err)
	}
}

// TestMonitorRepoSoftDelete verifies a soft-deleted monitor is hidden from
// Get and from the default List, satisfying the SPEC §6 decision 2 soft-delete
// semantics (the row survives so TSDB samples keep their referent).
func TestMonitorRepoSoftDelete(t *testing.T) {
	repo := newMonitorRepo(t)
	ctx := context.Background()

	m := sampleMonitor(t)
	if err := repo.Insert(ctx, m); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := repo.SoftDelete(ctx, m.ID); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}

	if _, err := repo.Get(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after SoftDelete error = %v, want ErrNotFound", err)
	}

	list, err := repo.List(ctx, MonitorFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("List returned %d monitors, want 0 after soft-delete", len(list))
	}

	// A second delete of the already-deleted monitor is ErrNotFound.
	if err := repo.SoftDelete(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("SoftDelete twice error = %v, want ErrNotFound", err)
	}

	// The row itself still exists so foreign-key referents remain valid.
	var count int
	if err := repo.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM monitors WHERE id = ?", m.ID).Scan(&count); err != nil {
		t.Fatalf("count row: %v", err)
	}
	if count != 1 {
		t.Errorf("monitors row count = %d, want 1 (soft-delete must not remove the row)", count)
	}
}

// TestMonitorRepoListFilters verifies the enabled and state filters select
// only matching monitors. The state filter joins monitor_states; that table
// is exercised here with raw inserts since its repository lands in M4.4.
func TestMonitorRepoListFilters(t *testing.T) {
	repo := newMonitorRepo(t)
	ctx := context.Background()

	enabledUp := sampleMonitor(t)
	enabledUp.Enabled = true

	disabledDown := sampleMonitor(t)
	disabledDown.Enabled = false

	for _, m := range []*monitor.Monitor{enabledUp, disabledDown} {
		if err := repo.Insert(ctx, m); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	insertState := func(monitorID string, state monitor.MonitorState) {
		_, err := repo.db.ExecContext(ctx,
			"INSERT INTO monitor_states (monitor_id, state, updated_at) VALUES (?, ?, ?)",
			monitorID, string(state), time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			t.Fatalf("insert monitor_state: %v", err)
		}
	}
	insertState(enabledUp.ID, monitor.StateUp)
	insertState(disabledDown.ID, monitor.StateDown)

	// No filter: both monitors.
	all, err := repo.List(ctx, MonitorFilter{})
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List(all) returned %d, want 2", len(all))
	}

	// Enabled filter.
	enabledTrue := true
	gotEnabled, err := repo.List(ctx, MonitorFilter{Enabled: &enabledTrue})
	if err != nil {
		t.Fatalf("List(enabled): %v", err)
	}
	if len(gotEnabled) != 1 || gotEnabled[0].ID != enabledUp.ID {
		t.Errorf("List(enabled=true) = %v, want only %s", gotEnabled, enabledUp.ID)
	}

	// State filter.
	stateDown := monitor.StateDown
	gotDown, err := repo.List(ctx, MonitorFilter{State: &stateDown})
	if err != nil {
		t.Fatalf("List(state): %v", err)
	}
	if len(gotDown) != 1 || gotDown[0].ID != disabledDown.ID {
		t.Errorf("List(state=down) = %v, want only %s", gotDown, disabledDown.ID)
	}
}
