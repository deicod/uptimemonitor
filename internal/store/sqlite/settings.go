package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SettingsRepo provides key/value JSON access to the settings table
// (SPEC §12.3), which holds global service toggles such as
// notifications-enabled. Raw SQL is confined to this package per the SPEC §5
// architecture rule.
type SettingsRepo struct {
	db *sql.DB
}

// NewSettingsRepo binds a settings repository to an open store.
func NewSettingsRepo(s *Store) *SettingsRepo {
	return &SettingsRepo{db: s.db}
}

// Set writes valueJSON under key, overwriting any existing value. key is the
// primary key, so callers can update a toggle without first checking for it.
func (r *SettingsRepo) Set(ctx context.Context, key string, valueJSON []byte) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO settings (key, value_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value_json = excluded.value_json,
			updated_at = excluded.updated_at`,
		key, string(valueJSON), time.Now().UTC().Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("sqlite: set setting %s: %w", key, err)
	}
	return nil
}

// Get returns the JSON value stored under key. An unset key yields
// ErrNotFound, letting callers distinguish "never configured" from a stored
// value.
func (r *SettingsRepo) Get(ctx context.Context, key string) ([]byte, error) {
	var valueJSON string
	err := r.db.QueryRowContext(ctx,
		"SELECT value_json FROM settings WHERE key = ?", key,
	).Scan(&valueJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get setting %s: %w", key, err)
	}
	return []byte(valueJSON), nil
}
