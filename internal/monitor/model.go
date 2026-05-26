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

// MonitorType values (SPEC §11.2). v0.2.0 adds TCP, ICMP ping, and DNS
// alongside the v0.1.0 HTTP type.
const (
	MonitorTypeHTTP MonitorType = "http"
	MonitorTypeTCP  MonitorType = "tcp"
	MonitorTypePing MonitorType = "ping"
	MonitorTypeDNS  MonitorType = "dns"
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
// (SPEC §11.2.1).
type HTTPMonitorConfig struct {
	URL               string       `json:"url"`
	Method            string       `json:"method"`
	ExpectedStatusMin int          `json:"expected_status_min"`
	ExpectedStatusMax int          `json:"expected_status_max"`
	BodyCap           int64        `json:"body_cap,omitempty"`
	Keyword           *HTTPKeyword `json:"keyword,omitempty"`
}

// HTTPKeyword is the optional body-content check for an HTTP monitor
// (SPEC §11.2.1, §15.2.1). When set, the keyword outcome is combined with the
// status-range classification to compute Success.
type HTTPKeyword struct {
	Mode  HTTPKeywordMode `json:"mode"`
	Value string          `json:"value"`
}

// HTTPKeywordMode selects how HTTPKeyword.Value is evaluated against the
// response body.
type HTTPKeywordMode string

// HTTPKeywordMode values (SPEC §11.2.1).
const (
	HTTPKeywordContains    HTTPKeywordMode = "contains"
	HTTPKeywordNotContains HTTPKeywordMode = "not_contains"
	HTTPKeywordRegex       HTTPKeywordMode = "regex"
)

// TCPMonitorConfig is the type-specific configuration for a TCP port monitor
// (SPEC §11.2.2).
type TCPMonitorConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// ICMPPingMonitorConfig is the type-specific configuration for an ICMP ping
// monitor (SPEC §11.2.3). PacketCount=0 is treated as the SPEC default of 1.
type ICMPPingMonitorConfig struct {
	Host        string `json:"host"`
	PacketCount int    `json:"packet_count,omitempty"`
}

// DNSMonitorConfig is the type-specific configuration for a DNS monitor
// (SPEC §11.2.4). An empty Resolver selects the system resolver.
type DNSMonitorConfig struct {
	Name          string            `json:"name"`
	RecordType    DNSRecordType     `json:"record_type"`
	Resolver      string            `json:"resolver,omitempty"`
	ExpectedValue *DNSExpectedValue `json:"expected_value,omitempty"`
}

// DNSRecordType is the DNS record class queried by a DNS monitor.
type DNSRecordType string

// DNSRecordType values (SPEC §11.2.4).
const (
	DNSRecordA     DNSRecordType = "A"
	DNSRecordAAAA  DNSRecordType = "AAAA"
	DNSRecordCNAME DNSRecordType = "CNAME"
	DNSRecordMX    DNSRecordType = "MX"
	DNSRecordTXT   DNSRecordType = "TXT"
	DNSRecordNS    DNSRecordType = "NS"
)

// DNSExpectedValue is the optional expected-value check for a DNS monitor
// (SPEC §11.2.4, §15.2.4). Positive conditions are existential ("at least one
// record matches"); negative conditions are universal ("no record matches the
// positive form").
type DNSExpectedValue struct {
	Condition DNSMatchCondition `json:"condition"`
	Value     string            `json:"value"`
}

// DNSMatchCondition selects how DNSExpectedValue.Value is compared against
// each returned record's zone-file textual form.
type DNSMatchCondition string

// DNSMatchCondition values (SPEC §11.2.4). All comparisons are case-sensitive
// byte comparisons.
const (
	DNSCondEquals        DNSMatchCondition = "equals"
	DNSCondNotEquals     DNSMatchCondition = "not_equals"
	DNSCondContains      DNSMatchCondition = "contains"
	DNSCondNotContains   DNSMatchCondition = "not_contains"
	DNSCondStartsWith    DNSMatchCondition = "starts_with"
	DNSCondNotStartsWith DNSMatchCondition = "not_starts_with"
	DNSCondEndsWith      DNSMatchCondition = "ends_with"
	DNSCondNotEndsWith   DNSMatchCondition = "not_ends_with"
)

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

// MonitorStatus is the current health snapshot of a monitor, persisted as one
// row per monitor in the monitor_states table (SPEC §12.3). It is upserted
// after every check; the consecutive counters drive the state machine.
type MonitorStatus struct {
	MonitorID            string
	State                MonitorState
	LastCheckID          *string
	LastCheckedAt        *time.Time
	LastSuccessAt        *time.Time
	LastFailureAt        *time.Time
	ConsecutiveSuccesses int
	ConsecutiveFailures  int
	UpdatedAt            time.Time
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
