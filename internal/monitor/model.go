// Package monitor defines the core domain model for uptime monitors and the
// data produced by checking them.
package monitor

import (
	"crypto/rand"
	"encoding/json"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// MonitorType identifies the kind of probe a monitor performs.
type MonitorType string

// MonitorType values. Only HTTP monitors are supported in the MVP.
const (
	MonitorTypeHTTP MonitorType = "http"
)

// MonitorState is the current health classification of a monitor.
type MonitorState string

// MonitorState values (SPEC §11.4).
const (
	// StateUnknown means no successful classification has occurred yet.
	StateUnknown MonitorState = "unknown"
	// StateUp means the latest check met the success criteria.
	StateUp MonitorState = "up"
	// StateDown means the latest check did not meet the success criteria.
	StateDown MonitorState = "down"
	// StatePaused means the monitor is disabled or intentionally paused.
	StatePaused MonitorState = "paused"
)

// Event type values (SPEC §11.6).
const (
	EventServiceStarted            = "service_started"
	EventServiceStopped            = "service_stopped"
	EventMonitorCreated            = "monitor_created"
	EventMonitorUpdated            = "monitor_updated"
	EventMonitorDeleted            = "monitor_deleted"
	EventMonitorEnabled            = "monitor_enabled"
	EventMonitorDisabled           = "monitor_disabled"
	EventMonitorStateChanged       = "monitor_state_changed"
	EventIncidentOpened            = "incident_opened"
	EventIncidentResolved          = "incident_resolved"
	EventNotificationTargetCreated = "notification_target_created"
	EventNotificationTargetUpdated = "notification_target_updated"
	EventNotificationTargetDeleted = "notification_target_deleted"
	EventNotificationSent          = "notification_sent"
	EventNotificationFailed        = "notification_failed"
)

// Monitor is a configured target that is checked on an interval (SPEC §11.1).
type Monitor struct {
	ID                   string
	Name                 string
	Type                 MonitorType
	Enabled              bool
	Interval             time.Duration
	Timeout              time.Duration
	Config               json.RawMessage
	NotificationsEnabled bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
	DeletedAt            *time.Time
}

// HTTPMonitorConfig is the type-specific configuration for an HTTP monitor
// (SPEC §11.2).
type HTTPMonitorConfig struct {
	URL               string `json:"url"`
	Method            string `json:"method"`
	ExpectedStatusMin int    `json:"expected_status_min"`
	ExpectedStatusMax int    `json:"expected_status_max"`
}

// CheckResult is the outcome of a single probe execution (SPEC §11.3).
type CheckResult struct {
	ID             string
	MonitorID      string
	StartedAt      time.Time
	FinishedAt     time.Time
	Duration       time.Duration
	Success        bool
	State          MonitorState
	Error          string
	HTTPStatusCode *int
}

// Incident is an open or resolved period of downtime for a monitor
// (SPEC §11.5).
type Incident struct {
	ID           string
	MonitorID    string
	StartedAt    time.Time
	ResolvedAt   *time.Time
	StartEventID string
	EndEventID   *string
	Reason       string
}

// Event is an audit-log entry, optionally scoped to a monitor (SPEC §11.6).
type Event struct {
	ID        string
	Type      string
	MonitorID *string
	Data      json.RawMessage
	CreatedAt time.Time
}

// entropy guards a single monotonic ULID entropy source. ulid.MonotonicEntropy
// is not safe for concurrent use, so NewID serialises access.
var (
	entropyMu sync.Mutex
	entropy   = ulid.Monotonic(rand.Reader, 0)
)

// NewID returns a fresh ULID as a string. IDs are lexically sortable by
// creation time, so sorting IDs orders the records chronologically
// (SPEC §6 decision 1).
func NewID() string {
	entropyMu.Lock()
	defer entropyMu.Unlock()
	return ulid.MustNew(ulid.Now(), entropy).String()
}
