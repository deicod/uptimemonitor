package sqlite

import (
	"path/filepath"
	"testing"
)

// TestOpenAppliesPragmas verifies that a freshly opened database has the
// SPEC §12.4 pragmas in effect. foreign_keys must be ON so that the schema's
// ON DELETE rules are enforced, and journal_mode must be WAL so concurrent
// readers do not block the probe-result writer.
func TestOpenAppliesPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.db")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	var foreignKeys int
	if err := store.DB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	var journalMode string
	if err := store.DB().QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}
}

// TestOpenEmptyPath rejects an empty path rather than silently opening an
// in-memory or working-directory database.
func TestOpenEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("Open(\"\") = nil error, want error")
	}
}

// TestClose confirms a clean close of an opened database.
func TestClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.db")

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
