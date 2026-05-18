package sqlite

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies any pending versioned migrations to the database. It is
// idempotent: already-applied migrations are skipped. Migration files are
// embedded in the binary at compile time (no runtime dependency on the atlas
// CLI — SPEC §13.2, decision 13).
//
// The function creates an atlas_schema_revisions table to track which
// migration files have already been applied. Each migration runs inside its
// own transaction to fail fast on errors.
func (s *Store) Migrate() error {
	return migrateFromDir(s.db, migrations)
}

// migrateFromDir is the implementation shared between the production Migrate
// (which uses the embedded FS) and tests (which may supply a custom FS with
// intentionally broken migrations).
func migrateFromDir(db *sql.DB, dir fs.FS) error {
	// Ensure the revisions tracking table exists.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS atlas_schema_revisions (
			version  TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return fmt.Errorf("migrate: create revisions table: %w", err)
	}

	// Discover migration files (skip atlas.sum and non-.sql files).
	files, err := collectMigrationFiles(dir)
	if err != nil {
		return fmt.Errorf("migrate: read migration dir: %w", err)
	}

	for _, f := range files {
		version := f // the filename is the version key

		// Check if already applied.
		var existing string
		err := db.QueryRow(
			"SELECT version FROM atlas_schema_revisions WHERE version = ?",
			version,
		).Scan(&existing)
		if err == nil {
			continue // already applied
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("migrate: check revision %s: %w", version, err)
		}

		// Read the migration content.
		content, err := fs.ReadFile(dir, f)
		if err != nil {
			return fmt.Errorf("migrate: read %s: %w", f, err)
		}

		// Execute within a transaction.
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migrate: begin tx for %s: %w", version, err)
		}

		if err := execStatements(tx, string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate: apply %s: %w", version, err)
		}

		// Record the revision.
		if _, err := tx.Exec(
			"INSERT INTO atlas_schema_revisions (version) VALUES (?)",
			version,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrate: record revision %s: %w", version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrate: commit %s: %w", version, err)
		}
	}

	return nil
}

// collectMigrationFiles returns sorted .sql filenames from dir (excluding
// atlas.sum and any non-.sql entries). The embed subdirectory prefix
// "migrations/" is stripped when present.
func collectMigrationFiles(dir fs.FS) ([]string, error) {
	var files []string

	err := fs.WalkDir(dir, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".sql") {
			return nil // skip atlas.sum and other non-SQL files
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

// execStatements splits a multi-statement SQL migration into individual
// statements separated by semicolons and executes each one. Empty statements
// (from trailing semicolons or comment-only lines) are skipped.
func execStatements(tx *sql.Tx, content string) error {
	stmts := strings.Split(content, ";")
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", truncate(stmt, 80), err)
		}
	}
	return nil
}

// truncate returns s shortened to n runes with "…" appended if necessary.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
