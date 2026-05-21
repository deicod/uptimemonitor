package notify

import (
	"encoding/json"
	"time"
)

// Target is a configured notification destination — one row in
// notification_targets (SPEC §12.3). Kind selects the Provider that owns the
// Config JSON payload; Config is opaque here and is interpreted by the
// matching Provider.
type Target struct {
	ID        string
	Name      string
	Kind      string
	Enabled   bool
	Config    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// Attempt is the audit record for a single notification delivery try — one
// row in notification_attempts (SPEC §12.3). AttemptNumber starts at 1 and
// increases on retry; Status is "success", "failure", or "pending"; Error is
// the sanitised provider error when the send failed and the empty string
// otherwise. MonitorID / IncidentID / EventID are pointers so a manual_test
// (which has no incident) can leave them unset.
type Attempt struct {
	ID            string
	TargetID      string
	MonitorID     *string
	IncidentID    *string
	EventID       *string
	EventType     string
	Status        string
	AttemptNumber int
	Error         string
	CreatedAt     time.Time
	SentAt        *time.Time
}

// Attempt status values (SPEC §18.6).
const (
	AttemptStatusPending = "pending"
	AttemptStatusSuccess = "success"
	AttemptStatusFailure = "failure"
)
