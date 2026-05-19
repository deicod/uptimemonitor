package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// incidentColumns is the shared column list for incidents reads.
const incidentColumns = `id, monitor_id, started_at, resolved_at, ` +
	`start_event_id, end_event_id, reason`

// IncidentRepo provides open/resolve/list access to the incidents table
// (SPEC §11.5, §12.3), which records downtime periods. Raw SQL is confined to
// this package per the SPEC §5 architecture rule.
type IncidentRepo struct {
	db *sql.DB
}

// NewIncidentRepo binds an incident repository to an open store.
func NewIncidentRepo(s *Store) *IncidentRepo {
	return &IncidentRepo{db: s.db}
}

// Open records a new, unresolved incident. The caller supplies the ID,
// MonitorID, StartedAt, StartEventID, and Reason; ResolvedAt and EndEventID
// must be unset and are stored as NULL.
func (r *IncidentRepo) Open(ctx context.Context, in *monitor.Incident) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO incidents (`+incidentColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.ID, in.MonitorID, in.StartedAt.UTC().Format(timeLayout),
		nullTime(in.ResolvedAt), nullText(in.StartEventID),
		nullString(in.EndEventID), nullText(in.Reason),
	)
	if err != nil {
		return fmt.Errorf("sqlite: open incident %s: %w", in.ID, err)
	}
	return nil
}

// Resolve closes an open incident by stamping resolved_at and end_event_id.
// An already-resolved or missing incident yields ErrNotFound, so a caller
// cannot resolve the same incident twice.
func (r *IncidentRepo) Resolve(ctx context.Context, id string, resolvedAt time.Time, endEventID string) error {
	res, err := r.db.ExecContext(ctx,
		"UPDATE incidents SET resolved_at = ?, end_event_id = ? "+
			"WHERE id = ? AND resolved_at IS NULL",
		resolvedAt.UTC().Format(timeLayout), nullText(endEventID), id,
	)
	if err != nil {
		return fmt.Errorf("sqlite: resolve incident %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: resolve incident %s: rows affected: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns the incidents for one monitor, newest first. A non-positive
// limit returns all rows.
func (r *IncidentRepo) List(ctx context.Context, monitorID string, limit int) ([]*monitor.Incident, error) {
	if limit <= 0 {
		limit = -1 // SQLite treats a negative LIMIT as unbounded.
	}
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+incidentColumns+" FROM incidents WHERE monitor_id = ? "+
			"ORDER BY started_at DESC, id DESC LIMIT ?",
		monitorID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list incidents for %s: %w", monitorID, err)
	}
	defer rows.Close()

	var incidents []*monitor.Incident
	for rows.Next() {
		in, err := scanIncident(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list incidents for %s: %w", monitorID, err)
		}
		incidents = append(incidents, in)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list incidents for %s: %w", monitorID, err)
	}
	return incidents, nil
}

// FindOpenByMonitor returns the unresolved incident for a monitor, used by the
// state machine to decide whether a failure opens a new incident or extends an
// existing one. A monitor with no open incident yields ErrNotFound.
func (r *IncidentRepo) FindOpenByMonitor(ctx context.Context, monitorID string) (*monitor.Incident, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+incidentColumns+" FROM incidents "+
			"WHERE monitor_id = ? AND resolved_at IS NULL "+
			"ORDER BY started_at DESC LIMIT 1",
		monitorID,
	)
	in, err := scanIncident(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: find open incident for %s: %w", monitorID, err)
	}
	return in, nil
}

// scanIncident reads one incidents row into an Incident value.
func scanIncident(s scannable) (*monitor.Incident, error) {
	var (
		in           monitor.Incident
		startedAt    string
		resolvedAt   sql.NullString
		startEventID sql.NullString
		endEventID   sql.NullString
		reason       sql.NullString
	)
	if err := s.Scan(
		&in.ID, &in.MonitorID, &startedAt, &resolvedAt,
		&startEventID, &endEventID, &reason,
	); err != nil {
		return nil, err
	}

	in.EndEventID = stringPtr(endEventID)
	if startEventID.Valid {
		in.StartEventID = startEventID.String
	}
	if reason.Valid {
		in.Reason = reason.String
	}

	var err error
	if in.StartedAt, err = time.Parse(timeLayout, startedAt); err != nil {
		return nil, fmt.Errorf("parse started_at: %w", err)
	}
	if in.ResolvedAt, err = timePtr(resolvedAt); err != nil {
		return nil, fmt.Errorf("parse resolved_at: %w", err)
	}
	return &in, nil
}
