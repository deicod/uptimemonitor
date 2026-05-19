package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// MonitorStateRepo provides upsert/get access to the monitor_states table
// (SPEC §12.3), which holds one current-health row per monitor. Raw SQL is
// confined to this package per the SPEC §5 architecture rule.
type MonitorStateRepo struct {
	db *sql.DB
}

// NewMonitorStateRepo binds a monitor-state repository to an open store.
func NewMonitorStateRepo(s *Store) *MonitorStateRepo {
	return &MonitorStateRepo{db: s.db}
}

// Upsert inserts the state row for a monitor or, if one already exists,
// overwrites it. monitor_id is the primary key, so the scheduler can call
// this after every check without accumulating duplicate rows.
func (r *MonitorStateRepo) Upsert(ctx context.Context, st *monitor.MonitorStatus) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO monitor_states (
			monitor_id, state, last_check_id, last_checked_at,
			last_success_at, last_failure_at,
			consecutive_successes, consecutive_failures, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(monitor_id) DO UPDATE SET
			state = excluded.state,
			last_check_id = excluded.last_check_id,
			last_checked_at = excluded.last_checked_at,
			last_success_at = excluded.last_success_at,
			last_failure_at = excluded.last_failure_at,
			consecutive_successes = excluded.consecutive_successes,
			consecutive_failures = excluded.consecutive_failures,
			updated_at = excluded.updated_at`,
		st.MonitorID, string(st.State), nullString(st.LastCheckID),
		nullTime(st.LastCheckedAt), nullTime(st.LastSuccessAt), nullTime(st.LastFailureAt),
		st.ConsecutiveSuccesses, st.ConsecutiveFailures,
		st.UpdatedAt.UTC().Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("sqlite: upsert monitor_state %s: %w", st.MonitorID, err)
	}
	return nil
}

// Get returns the current state row for a monitor. A monitor with no state
// row yields ErrNotFound.
func (r *MonitorStateRepo) Get(ctx context.Context, monitorID string) (*monitor.MonitorStatus, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT monitor_id, state, last_check_id, last_checked_at,
			last_success_at, last_failure_at,
			consecutive_successes, consecutive_failures, updated_at
		FROM monitor_states WHERE monitor_id = ?`, monitorID)
	st, err := scanMonitorStatus(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get monitor_state %s: %w", monitorID, err)
	}
	return st, nil
}

// scanMonitorStatus reads one monitor_states row into a MonitorStatus value.
func scanMonitorStatus(s scannable) (*monitor.MonitorStatus, error) {
	var (
		st            monitor.MonitorStatus
		state         string
		lastCheckID   sql.NullString
		lastCheckedAt sql.NullString
		lastSuccessAt sql.NullString
		lastFailureAt sql.NullString
		updatedAt     string
	)
	if err := s.Scan(
		&st.MonitorID, &state, &lastCheckID, &lastCheckedAt,
		&lastSuccessAt, &lastFailureAt,
		&st.ConsecutiveSuccesses, &st.ConsecutiveFailures, &updatedAt,
	); err != nil {
		return nil, err
	}

	st.State = monitor.MonitorState(state)
	st.LastCheckID = stringPtr(lastCheckID)

	var err error
	if st.LastCheckedAt, err = timePtr(lastCheckedAt); err != nil {
		return nil, fmt.Errorf("parse last_checked_at: %w", err)
	}
	if st.LastSuccessAt, err = timePtr(lastSuccessAt); err != nil {
		return nil, fmt.Errorf("parse last_success_at: %w", err)
	}
	if st.LastFailureAt, err = timePtr(lastFailureAt); err != nil {
		return nil, fmt.Errorf("parse last_failure_at: %w", err)
	}
	if st.UpdatedAt, err = time.Parse(timeLayout, updatedAt); err != nil {
		return nil, fmt.Errorf("parse updated_at: %w", err)
	}
	return &st, nil
}
