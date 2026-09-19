package sqlite

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// TestMigrateCreatesAllTables verifies that applying migrations to an empty
// database creates every table defined in the SPEC §12.3 schema.
func TestMigrateCreatesAllTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.db")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Every SPEC §12.3 table must exist.
	wantTables := []string{
		"monitors",
		"monitor_states",
		"check_results",
		"incidents",
		"events",
		"notification_targets",
		"notification_attempts",
		"settings",
	}
	for _, table := range wantTables {
		var name string
		err := store.DB().QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
			table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found after migration: %v", table, err)
		}
	}
}

// TestMigrateIdempotent verifies that calling Migrate twice is a no-op the
// second time — the same revision is already applied.
func TestMigrateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.db")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate (first): %v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate (second): %v", err)
	}
}

// TestMigrateBrokenMigration verifies that a corrupt or invalid migration
// file causes Migrate to return an error rather than silently succeeding.
func TestMigrateBrokenMigration(t *testing.T) {
	dir := t.TempDir()

	// Write a broken migration and matching atlas.sum into a temp dir, then
	// point a store at it via a custom Migrate call.
	migrationsDir := filepath.Join(dir, "migrations")
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(migrationsDir, "20260101000000_broken.sql"),
		[]byte("CREATE TAABLE broken_syntax (id TEXT);"),
		0o644,
	); err != nil {
		t.Fatalf("write broken migration: %v", err)
	}
	// Write a valid-looking atlas.sum (the hash will not match the broken content,
	// but that's fine — the migration applier will detect the SQL error).
	if err := os.WriteFile(
		filepath.Join(migrationsDir, "atlas.sum"),
		[]byte("h1:fake\n20260101000000_broken.sql h1:fake\n"),
		0o644,
	); err != nil {
		t.Fatalf("write atlas.sum: %v", err)
	}

	dbPath := filepath.Join(dir, "config.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := migrateFromDir(store.DB(), os.DirFS(migrationsDir)); err == nil {
		t.Fatal("migrateFromDir with broken SQL returned nil error, want error")
	}
}

// TestMigrateReportsRollbackFailure verifies that when a migration fails and
// its transaction cannot be rolled back, the rollback error is reported next
// to the migration error instead of being dropped — otherwise an operator
// debugging a failed upgrade would never learn the rollback went wrong too.
func TestMigrateReportsRollbackFailure(t *testing.T) {
	dir := t.TempDir()

	// The leading ROLLBACK ends the transaction behind database/sql's back,
	// so after the next statement fails there is nothing left to roll back
	// and tx.Rollback itself errors.
	migrationsDir := filepath.Join(dir, "migrations")
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(migrationsDir, "20260101000000_unrollbackable.sql"),
		[]byte("ROLLBACK; SELECT * FROM missing_table;"),
		0o644,
	); err != nil {
		t.Fatalf("write migration: %v", err)
	}

	store, err := Open(filepath.Join(dir, "config.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	err = migrateFromDir(store.DB(), os.DirFS(migrationsDir))
	if err == nil {
		t.Fatal("migrateFromDir returned nil error, want error")
	}
	for _, want := range []string{"migrate: apply", "migrate: rollback:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// TestMigrateCheckResultDetailsUpgrade upgrades a database that holds only the
// v0.1.0 schema and data (SPEC §13.4): existing installations must keep their
// check history, with each recorded HTTP status moved into the details
// payload the TUI now reads, and the HTTP-only column gone.
func TestMigrateCheckResultDetailsUpgrade(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Apply only the initial migration, under the same version key the
	// embedded directory uses, so Migrate later sees it as already applied.
	const initial = "migrations/20260518202531_initial_schema.sql"
	content, err := fs.ReadFile(migrations, initial)
	if err != nil {
		t.Fatalf("read %s: %v", initial, err)
	}
	if err := migrateFromDir(store.DB(), fstest.MapFS{initial: {Data: content}}); err != nil {
		t.Fatalf("apply v0.1.0 schema: %v", err)
	}

	monitorID := insertMonitor(t, store)
	for _, row := range []struct {
		id, startedAt string
		status        any
		errText       any
	}{
		{"c-ok", "2026-05-20T10:00:00Z", 200, nil},
		{"c-500", "2026-05-20T10:01:00Z", 503, "status 503 outside expected range 200-299"},
		{"c-transport", "2026-05-20T10:02:00Z", nil, "request timed out"},
	} {
		if _, err := store.DB().Exec(`INSERT INTO check_results
			(id, monitor_id, started_at, finished_at, duration_ms, success, state, error, http_status_code)
			VALUES (?, ?, ?, ?, 42, 1, 'up', ?, ?)`,
			row.id, monitorID, row.startedAt, row.startedAt, row.errText, row.status); err != nil {
			t.Fatalf("seed %s: %v", row.id, err)
		}
	}

	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate (upgrade): %v", err)
	}

	cols := map[string]bool{}
	rows, err := store.DB().Query("SELECT name FROM pragma_table_info('check_results')")
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		cols[name] = true
	}
	if !cols["details"] || cols["http_status_code"] {
		t.Fatalf("check_results columns = %v, want details and no http_status_code", cols)
	}

	got, err := NewCheckResultRepo(store).ListRecent(context.Background(), monitorID, 0)
	if err != nil {
		t.Fatalf("ListRecent after upgrade: %v", err)
	}
	want := map[string]string{
		"c-ok":        `{"status_code":200}`,
		"c-500":       `{"status_code":503}`,
		"c-transport": "",
	}
	if len(got) != len(want) {
		t.Fatalf("rows after upgrade = %d, want %d", len(got), len(want))
	}
	for _, cr := range got {
		if string(cr.Details) != want[cr.ID] {
			t.Errorf("%s details = %q, want %q", cr.ID, cr.Details, want[cr.ID])
		}
		if cr.Duration.Milliseconds() != 42 {
			t.Errorf("%s duration = %v, want 42ms preserved", cr.ID, cr.Duration)
		}
	}
	if got[0].ID != "c-transport" || got[0].Error != "request timed out" {
		t.Errorf("newest row = %s %q, want c-transport with its error preserved", got[0].ID, got[0].Error)
	}
}
