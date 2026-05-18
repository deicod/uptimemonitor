// Package sqlite owns uptimemonitor's relational persistence (SPEC §12). It
// opens the SQLite database, applies the connection pragmas from SPEC §12.4,
// and exposes the *sql.DB handle to the repository layer. No raw SQL lives
// outside this package except migration files (SPEC §5).
package sqlite

import (
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"
)

// pragmas are applied to every pooled connection via the DSN so that
// per-connection settings (notably foreign_keys) hold for all of them, not
// just the one that ran an Exec (SPEC §12.4).
var pragmas = []string{
	"foreign_keys(1)",
	"journal_mode(WAL)",
	"synchronous(NORMAL)",
	"busy_timeout(5000)",
}

// Store wraps the SQLite connection pool for the service's relational data.
type Store struct {
	db *sql.DB
}

// Open opens (creating if absent) the SQLite database at path with the SPEC
// §12.4 pragmas applied to every connection, and verifies connectivity.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite: database path is empty")
	}

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite: ping %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// DB returns the underlying connection pool for use by repositories.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the connection pool.
func (s *Store) Close() error {
	return s.db.Close()
}

// dsn builds a modernc.org/sqlite DSN that applies the connection pragmas on
// every new connection.
func dsn(path string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	return "file:" + path + "?" + q.Encode()
}
