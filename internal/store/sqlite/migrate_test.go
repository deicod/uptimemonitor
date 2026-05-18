package sqlite

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateCreatesAllTables verifies that applying migrations to an empty
// database creates every table defined in the SPEC §12.3 schema.
func TestMigrateCreatesAllTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.db")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

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
	t.Cleanup(func() { store.Close() })

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
	t.Cleanup(func() { store.Close() })

	if err := migrateFromDir(store.DB(), os.DirFS(migrationsDir)); err == nil {
		t.Fatal("migrateFromDir with broken SQL returned nil error, want error")
	}
}
