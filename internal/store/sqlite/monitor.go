package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// timeLayout is the textual format for the TEXT timestamp columns in SPEC
// §12.3. RFC3339Nano keeps sub-second precision and sorts lexically.
const timeLayout = time.RFC3339Nano

// monitorColumns is the SELECT list for a monitors row, aliased to m so the
// state-filter join can disambiguate.
const monitorColumns = `m.id, m.name, m.type, m.enabled, m.interval_seconds, ` +
	`m.timeout_seconds, m.config_json, m.notifications_enabled, ` +
	`m.created_at, m.updated_at, m.deleted_at`

// MonitorRepo provides CRUD access to the monitors table (SPEC §12.1). Raw
// SQL is confined to this package per the SPEC §5 architecture rule.
type MonitorRepo struct {
	db *sql.DB
}

// NewMonitorRepo binds a monitor repository to an open store.
func NewMonitorRepo(s *Store) *MonitorRepo {
	return &MonitorRepo{db: s.db}
}

// MonitorFilter narrows a List query. A nil field means "no constraint".
type MonitorFilter struct {
	// Enabled, when set, keeps only monitors with the given enabled flag.
	Enabled *bool
	// State, when set, keeps only monitors whose current monitor_states row
	// has the given state.
	State *monitor.MonitorState
}

// Insert persists a new monitor. The caller supplies the ID and timestamps.
func (r *MonitorRepo) Insert(ctx context.Context, m *monitor.Monitor) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO monitors (
			id, name, type, enabled, interval_seconds, timeout_seconds,
			config_json, notifications_enabled, created_at, updated_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Name, string(m.Type), boolToInt(m.Enabled),
		seconds(m.Interval), seconds(m.Timeout), configJSON(m.Config),
		boolToInt(m.NotificationsEnabled),
		m.CreatedAt.UTC().Format(timeLayout), m.UpdatedAt.UTC().Format(timeLayout),
		nullTime(m.DeletedAt),
	)
	if err != nil {
		return fmt.Errorf("sqlite: insert monitor %s: %w", m.ID, err)
	}
	return nil
}

// Get returns the monitor with the given id. A soft-deleted or missing
// monitor yields ErrNotFound.
func (r *MonitorRepo) Get(ctx context.Context, id string) (*monitor.Monitor, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+monitorColumns+" FROM monitors m WHERE m.id = ? AND m.deleted_at IS NULL",
		id,
	)
	m, err := scanMonitor(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get monitor %s: %w", id, err)
	}
	return m, nil
}

// List returns the non-deleted monitors matching the filter, ordered by id
// (which is a ULID, so this is creation order).
func (r *MonitorRepo) List(ctx context.Context, f MonitorFilter) ([]*monitor.Monitor, error) {
	var (
		query strings.Builder
		args  []any
	)
	query.WriteString("SELECT " + monitorColumns + " FROM monitors m")
	if f.State != nil {
		query.WriteString(" JOIN monitor_states ms ON ms.monitor_id = m.id")
	}
	query.WriteString(" WHERE m.deleted_at IS NULL")
	if f.Enabled != nil {
		query.WriteString(" AND m.enabled = ?")
		args = append(args, boolToInt(*f.Enabled))
	}
	if f.State != nil {
		query.WriteString(" AND ms.state = ?")
		args = append(args, string(*f.State))
	}
	query.WriteString(" ORDER BY m.id ASC")

	rows, err := r.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list monitors: %w", err)
	}
	defer rows.Close()

	var monitors []*monitor.Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list monitors: %w", err)
		}
		monitors = append(monitors, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list monitors: %w", err)
	}
	return monitors, nil
}

// Update persists changes to an existing monitor's mutable fields. The id and
// created_at are not modified. A missing or soft-deleted monitor yields
// ErrNotFound.
func (r *MonitorRepo) Update(ctx context.Context, m *monitor.Monitor) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE monitors SET
			name = ?, type = ?, enabled = ?, interval_seconds = ?,
			timeout_seconds = ?, config_json = ?, notifications_enabled = ?,
			updated_at = ?
		WHERE id = ? AND deleted_at IS NULL`,
		m.Name, string(m.Type), boolToInt(m.Enabled),
		seconds(m.Interval), seconds(m.Timeout), configJSON(m.Config),
		boolToInt(m.NotificationsEnabled),
		m.UpdatedAt.UTC().Format(timeLayout), m.ID,
	)
	if err != nil {
		return fmt.Errorf("sqlite: update monitor %s: %w", m.ID, err)
	}
	return checkAffected(res, "update", m.ID)
}

// SoftDelete marks a monitor deleted by stamping deleted_at. The row itself
// is kept so that TSDB samples and incident history keep a valid referent
// (SPEC §6 decision 2). Deleting an already-deleted or missing monitor yields
// ErrNotFound.
func (r *MonitorRepo) SoftDelete(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(timeLayout)
	res, err := r.db.ExecContext(ctx,
		"UPDATE monitors SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		now, now, id,
	)
	if err != nil {
		return fmt.Errorf("sqlite: soft-delete monitor %s: %w", id, err)
	}
	return checkAffected(res, "soft-delete", id)
}

// scannable is satisfied by both *sql.Row and *sql.Rows.
type scannable interface {
	Scan(dest ...any) error
}

// scanMonitor reads one monitors row into a Monitor value.
func scanMonitor(s scannable) (*monitor.Monitor, error) {
	var (
		m            monitor.Monitor
		typ          string
		enabled      int
		notifEnabled int
		intervalSec  int64
		timeoutSec   int64
		config       string
		createdAt    string
		updatedAt    string
		deletedAt    sql.NullString
	)
	if err := s.Scan(
		&m.ID, &m.Name, &typ, &enabled, &intervalSec, &timeoutSec,
		&config, &notifEnabled, &createdAt, &updatedAt, &deletedAt,
	); err != nil {
		return nil, err
	}

	m.Type = monitor.MonitorType(typ)
	m.Enabled = enabled != 0
	m.NotificationsEnabled = notifEnabled != 0
	m.Interval = time.Duration(intervalSec) * time.Second
	m.Timeout = time.Duration(timeoutSec) * time.Second
	m.Config = []byte(config)

	var err error
	if m.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	if m.UpdatedAt, err = time.Parse(timeLayout, updatedAt); err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	if deletedAt.Valid {
		t, err := time.Parse(timeLayout, deletedAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse deleted_at: %w", err)
		}
		m.DeletedAt = &t
	}
	return &m, nil
}

// checkAffected turns a zero-rows-affected result into ErrNotFound.
func checkAffected(res sql.Result, op, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: %s monitor %s: rows affected: %w", op, id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// boolToInt maps a Go bool to SQLite's 0/1 integer encoding.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// seconds rounds a duration to whole seconds for the *_seconds columns.
func seconds(d time.Duration) int64 {
	return int64(d / time.Second)
}

// configJSON returns a non-empty JSON string for the NOT NULL config_json
// column, defaulting an unset config to an empty object.
func configJSON(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

// nullTime maps an optional timestamp to a nullable TEXT column value.
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(timeLayout)
}

// nullString maps an optional string to a nullable TEXT column value.
func nullString(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// nullText maps a plain string to a nullable TEXT column value, treating the
// empty string as SQL NULL.
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullInt maps an optional int to a nullable INTEGER column value.
func nullInt(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

// stringPtr converts a nullable TEXT column value to an optional string.
func stringPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	s := ns.String
	return &s
}

// timePtr parses a nullable TEXT timestamp column into an optional time.
func timePtr(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid {
		return nil, nil
	}
	t, err := time.Parse(timeLayout, ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
