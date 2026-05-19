package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// eventColumns is the shared column list for events reads.
const eventColumns = `id, type, monitor_id, data_json, created_at`

// EventRepo provides insert and list access to the events audit log
// (SPEC §11.6, §12.3). Raw SQL is confined to this package per the SPEC §5
// architecture rule.
type EventRepo struct {
	db *sql.DB
}

// NewEventRepo binds an event repository to an open store.
func NewEventRepo(s *Store) *EventRepo {
	return &EventRepo{db: s.db}
}

// Insert appends a single event to the audit log. The caller supplies the ID,
// type, and created_at. A nil MonitorID records a service-scoped event.
func (r *EventRepo) Insert(ctx context.Context, e *monitor.Event) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO events (`+eventColumns+`)
		VALUES (?, ?, ?, ?, ?)`,
		e.ID, e.Type, nullString(e.MonitorID), configJSON(e.Data),
		e.CreatedAt.UTC().Format(timeLayout),
	)
	if err != nil {
		return fmt.Errorf("sqlite: insert event %s: %w", e.ID, err)
	}
	return nil
}

// List returns the most recent events across all monitors, newest first. A
// non-positive limit returns all rows.
func (r *EventRepo) List(ctx context.Context, limit int) ([]*monitor.Event, error) {
	if limit <= 0 {
		limit = -1 // SQLite treats a negative LIMIT as unbounded.
	}
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+eventColumns+" FROM events ORDER BY created_at DESC, id DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list events: %w", err)
	}
	return scanEvents(rows, "list events")
}

// ListByMonitor returns the most recent events scoped to one monitor, newest
// first. A non-positive limit returns all rows.
func (r *EventRepo) ListByMonitor(ctx context.Context, monitorID string, limit int) ([]*monitor.Event, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+eventColumns+" FROM events WHERE monitor_id = ? "+
			"ORDER BY created_at DESC, id DESC LIMIT ?",
		monitorID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list events for %s: %w", monitorID, err)
	}
	return scanEvents(rows, "list events for "+monitorID)
}

// scanEvents drains a rows cursor into Event values, closing it before return.
func scanEvents(rows *sql.Rows, op string) ([]*monitor.Event, error) {
	defer rows.Close()

	var events []*monitor.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: %s: %w", op, err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: %s: %w", op, err)
	}
	return events, nil
}

// scanEvent reads one events row into an Event value.
func scanEvent(s scannable) (*monitor.Event, error) {
	var (
		e         monitor.Event
		monitorID sql.NullString
		dataJSON  string
		createdAt string
	)
	if err := s.Scan(&e.ID, &e.Type, &monitorID, &dataJSON, &createdAt); err != nil {
		return nil, err
	}

	e.MonitorID = stringPtr(monitorID)
	e.Data = []byte(dataJSON)

	var err error
	if e.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	return &e, nil
}
