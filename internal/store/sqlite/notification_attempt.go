package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/deicod/uptimemonitor/internal/notify"
)

// notificationAttemptColumns is the shared column list for attempt reads.
const notificationAttemptColumns = `id, target_id, monitor_id, incident_id, ` +
	`event_id, event_type, status, attempt_number, error, created_at, sent_at`

// NotificationAttemptRepo provides insert and list-by-target access to the
// notification_attempts table (SPEC §12.3), the audit log of delivery tries
// that also backs MVP retry state (SPEC §18.6, §6 decision 4). Raw SQL is
// confined to this package per the SPEC §5 architecture rule.
type NotificationAttemptRepo struct {
	db *sql.DB
}

// NewNotificationAttemptRepo binds an attempt repository to an open store.
func NewNotificationAttemptRepo(s *Store) *NotificationAttemptRepo {
	return &NotificationAttemptRepo{db: s.db}
}

// Insert records a single delivery attempt. The caller supplies the ID and
// timestamps. A nil MonitorID/IncidentID/EventID is stored as NULL — a
// manual_test, for example, has no incident.
func (r *NotificationAttemptRepo) Insert(ctx context.Context, a *notify.Attempt) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO notification_attempts (`+notificationAttemptColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TargetID, nullString(a.MonitorID), nullString(a.IncidentID),
		nullString(a.EventID), a.EventType, a.Status, a.AttemptNumber,
		nullText(a.Error), a.CreatedAt.UTC().Format(timeLayout), nullTime(a.SentAt),
	)
	if err != nil {
		return fmt.Errorf("sqlite: insert notification attempt %s: %w", a.ID, err)
	}
	return nil
}

// ListByTarget returns the attempts for one target, newest first. A
// non-positive limit returns all rows. An unknown target yields an empty
// slice, not an error.
func (r *NotificationAttemptRepo) ListByTarget(ctx context.Context, targetID string, limit int) ([]*notify.Attempt, error) {
	if limit <= 0 {
		limit = -1 // SQLite treats a negative LIMIT as unbounded.
	}
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+notificationAttemptColumns+" FROM notification_attempts "+
			"WHERE target_id = ? ORDER BY created_at DESC, id DESC LIMIT ?",
		targetID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list notification attempts for %s: %w", targetID, err)
	}
	defer rows.Close()

	var out []*notify.Attempt
	for rows.Next() {
		a, err := scanNotificationAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list notification attempts for %s: %w", targetID, err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list notification attempts for %s: %w", targetID, err)
	}
	return out, nil
}

// ListRecent returns the most recent attempts across all targets, newest
// first. A non-positive limit returns all rows. It backs the global
// GET /v1/notifications/attempts endpoint (SPEC §10.5), which — unlike
// ListByTarget — is not scoped to a single target.
func (r *NotificationAttemptRepo) ListRecent(ctx context.Context, limit int) ([]*notify.Attempt, error) {
	if limit <= 0 {
		limit = -1 // SQLite treats a negative LIMIT as unbounded.
	}
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+notificationAttemptColumns+" FROM notification_attempts "+
			"ORDER BY created_at DESC, id DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list recent notification attempts: %w", err)
	}
	defer rows.Close()

	var out []*notify.Attempt
	for rows.Next() {
		a, err := scanNotificationAttempt(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: list recent notification attempts: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list recent notification attempts: %w", err)
	}
	return out, nil
}

// scanNotificationAttempt reads one notification_attempts row into an Attempt.
func scanNotificationAttempt(s scannable) (*notify.Attempt, error) {
	var (
		a          notify.Attempt
		monitorID  sql.NullString
		incidentID sql.NullString
		eventID    sql.NullString
		errText    sql.NullString
		createdAt  string
		sentAt     sql.NullString
	)
	if err := s.Scan(
		&a.ID, &a.TargetID, &monitorID, &incidentID, &eventID,
		&a.EventType, &a.Status, &a.AttemptNumber, &errText, &createdAt, &sentAt,
	); err != nil {
		return nil, err
	}

	a.MonitorID = stringPtr(monitorID)
	a.IncidentID = stringPtr(incidentID)
	a.EventID = stringPtr(eventID)
	if errText.Valid {
		a.Error = errText.String
	}

	var err error
	if a.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	if a.SentAt, err = timePtr(sentAt); err != nil {
		return nil, fmt.Errorf("parse sent_at: %w", err)
	}
	return &a, nil
}
