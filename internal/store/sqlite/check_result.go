package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// checkResultColumns is the shared column list for check_results reads/writes.
const checkResultColumns = `id, monitor_id, started_at, finished_at, ` +
	`duration_ms, success, state, error, http_status_code`

// CheckResultRepo provides insert, recent-list, and prune access to the
// check_results table (SPEC §12.3). Raw SQL is confined to this package per
// the SPEC §5 architecture rule.
type CheckResultRepo struct {
	db *sql.DB
}

// NewCheckResultRepo binds a check-result repository to an open store.
func NewCheckResultRepo(s *Store) *CheckResultRepo {
	return &CheckResultRepo{db: s.db}
}

// Insert persists a single check result. The caller supplies the ID.
func (r *CheckResultRepo) Insert(ctx context.Context, cr *monitor.CheckResult) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO check_results (`+checkResultColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cr.ID, cr.MonitorID,
		cr.StartedAt.UTC().Format(timeLayout), cr.FinishedAt.UTC().Format(timeLayout),
		cr.Duration.Milliseconds(), boolToInt(cr.Success), string(cr.State),
		nullText(cr.Error), nullInt(cr.HTTPStatusCode),
	)
	if err != nil {
		return fmt.Errorf("sqlite: insert check_result %s: %w", cr.ID, err)
	}
	return nil
}

// ListRecent returns the most recent check results for a monitor, newest
// first. A non-positive limit returns all rows.
func (r *CheckResultRepo) ListRecent(ctx context.Context, monitorID string, limit int) ([]*monitor.CheckResult, error) {
	if limit <= 0 {
		limit = -1 // SQLite treats a negative LIMIT as unbounded.
	}
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+checkResultColumns+" FROM check_results "+
			"WHERE monitor_id = ? ORDER BY started_at DESC LIMIT ?",
		monitorID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list check_results for %s: %w", monitorID, err)
	}
	defer rows.Close()

	var results []*monitor.CheckResult
	for rows.Next() {
		cr, err := scanCheckResult(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list check_results for %s: %w", monitorID, err)
		}
		results = append(results, cr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list check_results for %s: %w", monitorID, err)
	}
	return results, nil
}

// PruneOlderThan deletes check results whose started_at is before cutoff and
// returns the number of rows removed. This implements the SPEC §12.5 retention
// of recent check summaries.
func (r *CheckResultRepo) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		"DELETE FROM check_results WHERE started_at < ?",
		cutoff.UTC().Format(timeLayout),
	)
	if err != nil {
		return 0, fmt.Errorf("sqlite: prune check_results: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: prune check_results: rows affected: %w", err)
	}
	return n, nil
}

// scanCheckResult reads one check_results row into a CheckResult value.
func scanCheckResult(s scannable) (*monitor.CheckResult, error) {
	var (
		cr         monitor.CheckResult
		startedAt  string
		finishedAt string
		durationMs int64
		success    int
		state      string
		errText    sql.NullString
		statusCode sql.NullInt64
	)
	if err := s.Scan(
		&cr.ID, &cr.MonitorID, &startedAt, &finishedAt,
		&durationMs, &success, &state, &errText, &statusCode,
	); err != nil {
		return nil, err
	}

	cr.Duration = time.Duration(durationMs) * time.Millisecond
	cr.Success = success != 0
	cr.State = monitor.MonitorState(state)
	if errText.Valid {
		cr.Error = errText.String
	}
	if statusCode.Valid {
		code := int(statusCode.Int64)
		cr.HTTPStatusCode = &code
	}

	var err error
	if cr.StartedAt, err = time.Parse(timeLayout, startedAt); err != nil {
		return nil, fmt.Errorf("parse started_at: %w", err)
	}
	if cr.FinishedAt, err = time.Parse(timeLayout, finishedAt); err != nil {
		return nil, fmt.Errorf("parse finished_at: %w", err)
	}
	return &cr, nil
}
