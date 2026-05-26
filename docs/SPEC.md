# Uptime Monitor Technical Specification

Status: Draft  
Version: 0.4  
Date: 2026-05-26  
Repository: `github.com/deicod/uptimemonitor`  
License: MIT  
Derived from: `docs/PRD.md` version 0.3  
Primary target: Linux with systemd  
Primary interface: Bubble Tea terminal UI

## 1. Purpose

This specification defines the initial technical design for Uptime Monitor.

The document is intended to guide implementation. It translates the PRD into architecture, package layout, process behavior, storage design, IPC contracts, notification delivery, and implementation milestones.

The SPEC should be treated as a living document. Implementation may refine details, but changes that alter product behavior should be reflected back into the PRD.

## 2. System overview

Uptime Monitor is implemented as one Go module and one primary binary:

```text
uptimemonitor
```

The binary exposes two MVP commands:

```text
uptimemonitor service
uptimemonitor tui
```

The service is the authoritative process. It owns persistence, scheduling, probe execution, monitor state, incidents, events, notification delivery, and local IPC.

The TUI is a client process. It owns presentation, keyboard interaction, local forms, and user workflows. It must not access SQLite or Prometheus TSDB directly.

## 3. Technical decisions

The following decisions are accepted for this SPEC:

- Language: Go.
- Module path: `github.com/deicod/uptimemonitor`.
- License: MIT.
- CLI framework: Cobra.
- Configuration framework: Viper.
- Scaffold tool: `cobra-cli`.
- TUI framework: Bubble Tea.
- Relational storage: SQLite.
- SQLite driver: `modernc.org/sqlite`.
- Migration tool: Atlas.
- Time-series storage: Prometheus TSDB.
- IPC protocol: HTTP over Unix domain socket.
- Notification providers: Go-native implementations.
- Container build path: ko.
- Primary supervisor: systemd.
- MVP monitor type: HTTP.
- Release 0.2.0 monitor types: HTTP (with optional keyword check), TCP port, ICMP ping (unprivileged), DNS.
- MVP access model: local single-user, no TUI login.
- Future access boundary: configurable secret for future web UI/API.
- Prometheus metrics export: excluded from MVP.
- Entity IDs: ULID.
- Development task runner: Make (`Makefile`).

## 4. Non-goals for this SPEC

This SPEC does not define:

- A web UI.
- Public remote API exposure.
- Multi-user account flows.
- Uptime Kuma import compatibility.
- Prometheus metrics export.
- Distributed agents.
- Kubernetes operator behavior.
- Full notification-provider parity with Uptime Kuma.

## 5. Repository layout

Recommended initial layout:

```text
.
├── cmd/
│   ├── root.go
│   ├── service.go
│   └── tui.go
├── internal/
│   ├── app/
│   │   ├── service.go
│   │   └── tui.go
│   ├── config/
│   │   ├── config.go
│   │   └── validate.go
│   ├── ipc/
│   │   ├── client.go
│   │   ├── errors.go
│   │   ├── handlers.go
│   │   ├── routes.go
│   │   ├── server.go
│   │   └── types.go
│   ├── logging/
│   │   └── logging.go
│   ├── monitor/
│   │   ├── model.go
│   │   ├── service.go
│   │   ├── state.go
│   │   └── validate.go
│   ├── notify/
│   │   ├── delivery.go
│   │   ├── message.go
│   │   ├── provider.go
│   │   ├── registry.go
│   │   ├── template.go
│   │   └── providers/
│   │       ├── discord/
│   │       ├── email/
│   │       ├── fake/
│   │       ├── gotify/
│   │       ├── ntfy/
│   │       ├── providerhttp/
│   │       ├── slack/
│   │       ├── telegram/
│   │       └── webhook/
│   ├── probe/
│   │   ├── details.go
│   │   ├── dns.go
│   │   ├── http.go
│   │   ├── ping.go
│   │   ├── result.go
│   │   ├── runner.go
│   │   └── tcp.go
│   ├── scheduler/
│   │   ├── scheduler.go
│   │   └── worker.go
│   ├── store/
│   │   ├── sqlite/
│   │   │   ├── migrations/
│   │   │   ├── queries.go
│   │   │   ├── schema.sql
│   │   │   └── store.go
│   │   └── tsdb/
│   │       ├── query.go
│   │       ├── series.go
│   │       └── store.go
│   ├── systemd/
│   │   └── notify.go
│   ├── tui/
│   │   ├── app.go
│   │   ├── keys.go
│   │   ├── model.go
│   │   ├── screens/
│   │   ├── update.go
│   │   └── view.go
│   └── version/
│       └── version.go
├── deployments/
│   ├── systemd/
│   │   └── uptimemonitor.service
│   └── ko/
│       └── README.md
├── docs/
│   ├── PRD.md
│   └── SPEC.md
├── main.go
├── go.mod
├── go.sum
├── LICENSE
└── README.md
```

Rules:

- `cmd/` contains thin Cobra command wiring only.
- `internal/app/` composes dependencies for each command.
- `internal/store/sqlite/` owns relational persistence.
- `internal/store/tsdb/` owns Prometheus TSDB access.
- `internal/ipc/` owns API contracts between service and TUI.
- `internal/tui/` owns Bubble Tea models and screens.
- `internal/notify/` owns notification delivery and provider registration.
- `internal/logging/` owns `log/slog` logger construction.
- No package outside `store` should contain raw SQL except migration files.
- The TUI must not import `internal/store/*`.

## 6. Bootstrap commands

Initial scaffold:

```sh
go mod init github.com/deicod/uptimemonitor
cobra-cli init --license MIT --viper
cobra-cli add service
cobra-cli add tui
```

The generated Cobra files should be reviewed and simplified. Cobra commands should delegate to typed application entrypoints rather than containing implementation logic.

## 7. Commands

### 7.1 Root command

```text
uptimemonitor
```

Responsibilities:

- Print help when no subcommand is provided.
- Define persistent config flags.
- Initialize Viper binding.
- Expose version information.

Recommended flags:

```text
--config string      path to config file
--log-level string   log level override
--version            print version
```

### 7.2 Service command

```text
uptimemonitor service
```

Responsibilities:

1. Load and validate configuration.
2. Initialize logger.
3. Initialize SQLite.
4. Run Atlas migrations.
5. Initialize Prometheus TSDB.
6. Initialize notification registry.
7. Load monitors and notification targets.
8. Start IPC server on Unix socket.
9. Start scheduler and workers.
10. Send systemd readiness notification when ready.
11. Block until shutdown signal.
12. Shut down gracefully.

Recommended flags:

```text
--config string
--socket-path string
--data-dir string
--migrate bool
```

Default behavior: migrations run automatically on service startup.

### 7.3 TUI command

```text
uptimemonitor tui
```

Responsibilities:

1. Load client-side configuration.
2. Connect to the service over Unix socket.
3. Fetch initial service status.
4. Start Bubble Tea program.
5. Perform all reads and writes through IPC.

Recommended flags:

```text
--config string
--socket-path string
```

The TUI must not require the configured secret in MVP.

## 8. Configuration

### 8.1 Config file format

Default format: YAML.

Example:

```yaml
data_dir: /var/lib/uptimemonitor
runtime_dir: /run/uptimemonitor
sqlite_path: /var/lib/uptimemonitor/config.db
tsdb_path: /var/lib/uptimemonitor/tsdb
socket_path: /run/uptimemonitor/uptimemonitor.sock
log_level: info
secret: ""

service:
  check_workers: 16
  default_interval: 60s
  timeout: 10s
  shutdown_timeout: 10s

retention:
  raw_samples: 30d
  aggregated_history: 365d

notifications:
  enabled: true
  max_attempts: 3
  initial_retry_delay: 5s
  max_retry_delay: 60s
```

### 8.2 Config struct

Recommended shape:

```go
type Config struct {
    DataDir    string        `mapstructure:"data_dir"`
    RuntimeDir string        `mapstructure:"runtime_dir"`
    SQLitePath string        `mapstructure:"sqlite_path"`
    TSDBPath   string        `mapstructure:"tsdb_path"`
    SocketPath string        `mapstructure:"socket_path"`
    LogLevel   string        `mapstructure:"log_level"`
    Secret     string        `mapstructure:"secret"`
    Service    ServiceConfig `mapstructure:"service"`
    Retention  RetentionConfig `mapstructure:"retention"`
    Notifications NotificationConfig `mapstructure:"notifications"`
}

type ServiceConfig struct {
    CheckWorkers    int           `mapstructure:"check_workers"`
    DefaultInterval time.Duration `mapstructure:"default_interval"`
    Timeout         time.Duration `mapstructure:"timeout"`
    ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
}

type RetentionConfig struct {
    RawSamples        time.Duration `mapstructure:"raw_samples"`
    AggregatedHistory time.Duration `mapstructure:"aggregated_history"`
}

type NotificationConfig struct {
    Enabled           bool          `mapstructure:"enabled"`
    MaxAttempts       int           `mapstructure:"max_attempts"`
    InitialRetryDelay time.Duration `mapstructure:"initial_retry_delay"`
    MaxRetryDelay     time.Duration `mapstructure:"max_retry_delay"`
}
```

Duration fields are decoded with a custom `mapstructure` decode hook registered
with Viper before `Unmarshal`. Go's `time.ParseDuration` does not accept day or
week suffixes, so values such as `raw_samples: 30d` and `aggregated_history: 365d`
require the hook to translate `d` and `w` suffixes into a `time.Duration`.

### 8.3 Defaults

Recommended defaults:

```text
data_dir: /var/lib/uptimemonitor
runtime_dir: /run/uptimemonitor
sqlite_path: /var/lib/uptimemonitor/config.db
tsdb_path: /var/lib/uptimemonitor/tsdb
socket_path: /run/uptimemonitor/uptimemonitor.sock
log_level: info
service.check_workers: 16
service.default_interval: 60s
service.timeout: 10s
service.shutdown_timeout: 10s
retention.raw_samples: 30d
retention.aggregated_history: 365d
notifications.enabled: true
notifications.max_attempts: 3
notifications.initial_retry_delay: 5s
notifications.max_retry_delay: 60s
```

### 8.4 Environment variables

Use prefix:

```text
UPTIMEMONITOR_
```

Examples:

```text
UPTIMEMONITOR_DATA_DIR=/var/lib/uptimemonitor
UPTIMEMONITOR_SOCKET_PATH=/run/uptimemonitor/uptimemonitor.sock
UPTIMEMONITOR_SECRET=...
UPTIMEMONITOR_NOTIFICATIONS_ENABLED=true
```

### 8.5 Validation rules

The service must fail fast if:

- Data directory is empty.
- Runtime directory is empty.
- SQLite path is empty.
- TSDB path is empty.
- Socket path is empty.
- Worker count is less than 1.
- Default interval is less than 1 second.
- Timeout is less than 1 second.
- Timeout is greater than or equal to default interval by default.
- Notification retry settings are invalid.

The TUI should show readable service connection errors if the socket is missing or inaccessible.

## 9. Process model

### 9.1 Service lifecycle

Startup sequence:

```text
load config
validate config
initialize logging
ensure data/runtime directories
open sqlite
run migrations
open tsdb
initialize stores
initialize notification registry
load active monitors
load notification targets
start ipc server
start scheduler
start workers
signal ready
wait for shutdown
stop accepting ipc requests
stop scheduler
wait for running probes
flush notification attempts
close tsdb
close sqlite
exit
```

### 9.2 TUI lifecycle

Startup sequence:

```text
load config
create ipc client
connect to socket
fetch service status
fetch monitor list
start Bubble Tea program
poll or refresh via commands
send user mutations via ipc
render results
exit on user command or fatal connection error
```

### 9.3 Shutdown behavior

On SIGINT or SIGTERM, the service should:

- Stop scheduling new checks.
- Allow running checks to complete within shutdown timeout.
- Cancel checks exceeding shutdown timeout.
- Stop notification workers after in-flight delivery attempts complete or timeout.
- Close stores.
- Remove stale Unix socket if owned by the process.

## 10. IPC API

### 10.1 Transport

Use HTTP over Unix domain socket.

Default socket path:

```text
/run/uptimemonitor/uptimemonitor.sock
```

The server listens only on the Unix socket in MVP. It must not expose TCP by default.

### 10.2 Encoding

Request and response bodies use JSON.

Timestamps use RFC3339 with nanoseconds where useful.

Durations are encoded as strings for config-like APIs, for example:

```json
"60s"
```

Durations may be encoded as milliseconds for history data if it simplifies the TUI. The API must be consistent per field.

### 10.3 Error response

Standard error body:

```json
{
  "error": {
    "code": "validation_error",
    "message": "interval must be at least 1s",
    "field": "interval"
  }
}
```

Recommended error codes:

```text
bad_request
validation_error
not_found
conflict
internal_error
service_unavailable
provider_error
```

### 10.4 API versioning

Prefix all endpoints with:

```text
/v1
```

Breaking changes require a new version prefix.

### 10.5 Endpoints

#### Service status

```text
GET /v1/status
```

Response:

```json
{
  "version": "0.1.0-dev",
  "state": "ready",
  "started_at": "2026-05-18T12:00:00Z",
  "sqlite": { "ok": true },
  "tsdb": { "ok": true },
  "scheduler": { "running": true, "workers": 16 },
  "monitors": { "total": 3, "active": 2 }
}
```

#### Monitor list

```text
GET /v1/monitors
```

Query parameters:

```text
state=up|down|unknown|paused
enabled=true|false
```

#### Create monitor

```text
POST /v1/monitors
```

Request:

```json
{
  "name": "Example Website",
  "type": "http",
  "enabled": true,
  "interval": "60s",
  "timeout": "10s",
  "config": {
    "url": "https://example.com",
    "method": "GET",
    "expected_status_min": 200,
    "expected_status_max": 299
  },
  "notifications_enabled": true
}
```

For `type` values other than `http`, the `config` object matches the
type-specific struct documented in §11.2: `TCPMonitorConfig` (§11.2.2),
`ICMPPingMonitorConfig` (§11.2.3), or `DNSMonitorConfig` (§11.2.4).

#### Get monitor

```text
GET /v1/monitors/{id}
```

#### Update monitor

```text
PATCH /v1/monitors/{id}
```

Partial update. The service validates the resulting monitor.

#### Delete monitor

```text
DELETE /v1/monitors/{id}
```

Deletion is soft-delete in SQLite for the MVP (SPEC v0.1 open question 2, now resolved): the row is retained with `deleted_at` set and hidden from default listings. TSDB samples remain until retention removes them.

#### Trigger manual check

```text
POST /v1/monitors/{id}/run
```

Response:

```json
{
  "check_id": "01HX...",
  "queued": true
}
```

#### Recent check results

```text
GET /v1/monitors/{id}/checks?limit=50
```

Returns recent persisted check summaries from SQLite or a combined SQLite/TSDB view.

#### History

```text
GET /v1/monitors/{id}/history?range=24h&resolution=auto
```

Response:

```json
{
  "monitor_id": "01HX...",
  "range": "24h",
  "resolution": "5m",
  "points": [
    {
      "start": "2026-05-18T11:55:00Z",
      "end": "2026-05-18T12:00:00Z",
      "state": "up",
      "success_ratio": 1.0,
      "avg_duration_ms": 123
    }
  ]
}
```

#### Incidents

```text
GET /v1/incidents
GET /v1/monitors/{id}/incidents
```

#### Events

```text
GET /v1/events
GET /v1/monitors/{id}/events
```

#### Notification targets

```text
GET    /v1/notifications/targets
POST   /v1/notifications/targets
GET    /v1/notifications/targets/{id}
PATCH  /v1/notifications/targets/{id}
DELETE /v1/notifications/targets/{id}
POST   /v1/notifications/targets/{id}/test
GET    /v1/notifications/attempts
```

#### Provider metadata

```text
GET /v1/notifications/providers
```

Returns provider kinds and TUI field metadata.

Example:

```json
{
  "providers": [
    {
      "kind": "webhook",
      "display_name": "Webhook",
      "fields": [
        { "name": "url", "type": "secret_string", "required": true },
        { "name": "method", "type": "string", "required": true, "default": "POST" }
      ]
    }
  ]
}
```

## 11. Domain model

### 11.1 Monitor

```go
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
```

### 11.2 Monitor config payloads

Each monitor type defines a Go struct that marshals to/from the
`Monitor.Config` JSON. Runner contracts are in §15.2; result payloads are in
§15.3.

`MonitorType` is one of:

```go
const (
    MonitorTypeHTTP MonitorType = "http"
    MonitorTypeTCP  MonitorType = "tcp"
    MonitorTypePing MonitorType = "ping"
    MonitorTypeDNS  MonitorType = "dns"
)
```

#### 11.2.1 HTTP

```go
type HTTPMonitorConfig struct {
    URL               string       `json:"url"`
    Method            string       `json:"method"`
    ExpectedStatusMin int          `json:"expected_status_min"`
    ExpectedStatusMax int          `json:"expected_status_max"`
    BodyCap           int64        `json:"body_cap,omitempty"` // bytes; default 1<<20 (1 MiB)
    Keyword           *HTTPKeyword `json:"keyword,omitempty"`
}

type HTTPKeyword struct {
    Mode  HTTPKeywordMode `json:"mode"`  // contains | not_contains | regex
    Value string          `json:"value"` // literal substring or RE2 pattern
}

type HTTPKeywordMode string

const (
    HTTPKeywordContains    HTTPKeywordMode = "contains"
    HTTPKeywordNotContains HTTPKeywordMode = "not_contains"
    HTTPKeywordRegex       HTTPKeywordMode = "regex"
)
```

Validation:

- URL must be absolute; scheme must be `http` or `https`.
- Method must be `GET` (other methods deferred).
- Expected status range: `100 ≤ min ≤ max ≤ 599`; when both fields are zero,
  the runner uses `200`/`299`.
- `BodyCap`: when zero, the runner uses the default 1 MiB; when non-zero,
  must be `≥ 1024` and `≤ 16<<20` (16 MiB).
- `Keyword.Value`: non-empty when `Keyword` is set. For `regex`, the value
  must compile under `regexp.Compile` at validation time.

#### 11.2.2 TCP

```go
type TCPMonitorConfig struct {
    Host string `json:"host"`
    Port int    `json:"port"`
}
```

Validation:

- `Host`: non-empty; either a DNS name (RFC 1035 syntax) or a textual IP
  address.
- `Port`: `1 ≤ port ≤ 65535`.

#### 11.2.3 ICMP ping

```go
type ICMPPingMonitorConfig struct {
    Host        string `json:"host"`
    PacketCount int    `json:"packet_count,omitempty"` // default 1, max 5
}
```

Validation:

- `Host`: non-empty; DNS name or IPv4 textual address. The validator rejects
  hosts that resolve only to IPv6, with an error pointing at the IPv6
  deferral, so the operator sees a setup problem rather than a flapping
  monitor.
- `PacketCount`: when zero, treated as 1; when non-zero, must be in `[1, 5]`.

#### 11.2.4 DNS

```go
type DNSMonitorConfig struct {
    Name          string            `json:"name"`        // FQDN
    RecordType    DNSRecordType     `json:"record_type"` // A | AAAA | CNAME | MX | TXT | NS
    Resolver      string            `json:"resolver,omitempty"` // optional host:port
    ExpectedValue *DNSExpectedValue `json:"expected_value,omitempty"`
}

type DNSRecordType string

const (
    DNSRecordA     DNSRecordType = "A"
    DNSRecordAAAA  DNSRecordType = "AAAA"
    DNSRecordCNAME DNSRecordType = "CNAME"
    DNSRecordMX    DNSRecordType = "MX"
    DNSRecordTXT   DNSRecordType = "TXT"
    DNSRecordNS    DNSRecordType = "NS"
)

type DNSExpectedValue struct {
    Condition DNSMatchCondition `json:"condition"`
    Value     string            `json:"value"`
}

type DNSMatchCondition string

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
```

Validation:

- `Name`: non-empty; valid FQDN syntax.
- `RecordType`: must be one of the listed constants.
- `Resolver`: when set, must parse as `host:port` with port in `[1, 65535]`.
- `ExpectedValue.Value`: non-empty when `ExpectedValue` is set.

#### 11.2.5 Common validation

These rules apply to every monitor type:

- `Name`: non-empty; must not contain control characters (it is carried into
  notification payloads such as the email Subject and into TUI rendering, so
  control characters are rejected at the source to prevent header injection
  (CWE-93) and rendering corruption).
- `Type`: must be one of `http`, `tcp`, `ping`, `dns`.
- `Interval`: positive.
- `Timeout`: positive; less than `Interval` by default.
- `Config`: must decode cleanly into the type-specific struct above and pass
  its validation.

### 11.3 Check result

```go
type CheckResult struct {
    ID         string
    MonitorID  string
    StartedAt  time.Time
    FinishedAt time.Time
    Duration   time.Duration
    Success    bool
    State      MonitorState
    Error      string
    Details    json.RawMessage // type-specific payload; see §15.3
}
```

The v0.1.0 `HTTPStatusCode *int` field is removed in v0.4. HTTP status code
now lives inside `Details` as `HTTPDetails.StatusCode`. Migration 0002
(§13.4) backfills existing rows.

### 11.4 Monitor states

```text
unknown
up
down
paused
```

State semantics:

- `unknown`: no successful classification yet.
- `up`: latest check meets success criteria.
- `down`: latest check does not meet success criteria.
- `paused`: monitor is disabled or intentionally paused.

### 11.5 Incident

```go
type Incident struct {
    ID          string
    MonitorID   string
    StartedAt   time.Time
    ResolvedAt  *time.Time
    StartEventID string
    EndEventID   *string
    Reason      string
}
```

### 11.6 Event

```go
type Event struct {
    ID        string
    Type      string
    MonitorID *string
    Data      json.RawMessage
    CreatedAt time.Time
}
```

Recommended event types:

```text
service_started
service_stopped
monitor_created
monitor_updated
monitor_deleted
monitor_enabled
monitor_disabled
monitor_state_changed
incident_opened
incident_resolved
notification_target_created
notification_target_updated
notification_target_deleted
notification_sent
notification_failed
```

## 12. SQLite storage

### 12.1 Database role

SQLite is the source of truth for:

- Monitor configuration.
- Notification configuration.
- Current monitor state.
- Recent check summaries.
- Incidents.
- Events.
- Notification attempts.
- Service settings.

Prometheus TSDB is the source of truth for historical time-series samples.

### 12.2 ID format

Use string IDs.

Decision: entity IDs are ULIDs (SPEC v0.1 open question 1, now resolved). ULIDs
are 26 characters, time-ordered, and lexically sortable.

Requirement: IDs must be lexically sortable by creation time.

### 12.3 Schema draft

Initial schema draft:

```sql
CREATE TABLE monitors (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    interval_seconds INTEGER NOT NULL,
    timeout_seconds INTEGER NOT NULL,
    config_json TEXT NOT NULL,
    notifications_enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE TABLE monitor_states (
    monitor_id TEXT PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    last_check_id TEXT,
    last_checked_at TEXT,
    last_success_at TEXT,
    last_failure_at TEXT,
    consecutive_successes INTEGER NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL
);

CREATE TABLE check_results (
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    started_at TEXT NOT NULL,
    finished_at TEXT NOT NULL,
    duration_ms INTEGER NOT NULL,
    success INTEGER NOT NULL,
    state TEXT NOT NULL,
    error TEXT,
    details TEXT  -- type-specific JSON; see §15.3
);

CREATE INDEX idx_check_results_monitor_started
    ON check_results(monitor_id, started_at DESC);

CREATE TABLE incidents (
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    started_at TEXT NOT NULL,
    resolved_at TEXT,
    start_event_id TEXT,
    end_event_id TEXT,
    reason TEXT
);

CREATE INDEX idx_incidents_monitor_started
    ON incidents(monitor_id, started_at DESC);

CREATE TABLE events (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    monitor_id TEXT REFERENCES monitors(id) ON DELETE SET NULL,
    data_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);

CREATE INDEX idx_events_created
    ON events(created_at DESC);

CREATE INDEX idx_events_monitor_created
    ON events(monitor_id, created_at DESC);

CREATE TABLE notification_targets (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    config_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
);

CREATE TABLE notification_attempts (
    id TEXT PRIMARY KEY,
    target_id TEXT NOT NULL REFERENCES notification_targets(id),
    monitor_id TEXT REFERENCES monitors(id) ON DELETE SET NULL,
    incident_id TEXT REFERENCES incidents(id) ON DELETE SET NULL,
    event_id TEXT REFERENCES events(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    status TEXT NOT NULL,
    attempt_number INTEGER NOT NULL,
    error TEXT,
    created_at TEXT NOT NULL,
    sent_at TEXT
);

CREATE INDEX idx_notification_attempts_target_created
    ON notification_attempts(target_id, created_at DESC);

CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
```

### 12.4 SQLite pragmas

Recommended on open:

```sql
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA busy_timeout = 5000;
```

### 12.5 Check result retention in SQLite

SQLite should keep recent check summaries for fast TUI display.

Recommended MVP:

- Keep recent `check_results` for 30 days.
- TSDB remains authoritative for numeric historical samples within retention.
- If this duplication becomes problematic, reduce SQLite check retention later.

## 13. Atlas migrations

### 13.1 Migration mode

Use versioned migrations committed to the repository.

Recommended path:

```text
internal/store/sqlite/migrations
```

### 13.2 Service startup

On startup, the service should:

1. Open SQLite.
2. Acquire migration lock if necessary.
3. Apply pending migrations.
4. Fail startup on migration failure.

Migrations are applied by the service itself, in process. Migration files are
embedded into the binary with `embed.FS` and applied by an in-process applier, so
the runtime has no dependency on the `atlas` binary; this is required because the
ko-built container image (§22) is distroless. The `atlas` CLI is used only at
development time — for `atlas migrate diff`, `atlas migrate lint`, and generating
new migration files.

### 13.3 Development workflow

Recommended scripts or make targets:

```text
make migrate-new NAME=create_monitors
make migrate-lint
make migrate-apply
```

Exact Atlas commands should be defined when the repository build tooling is added.

### 13.4 v0.2.0 migration: check_result details

The v0.1.0 schema (§12.3) stored HTTP-specific data in
`check_results.http_status_code`. v0.2.0 replaces this column with a typed
JSON payload to support TCP, ICMP, DNS, and HTTP-keyword data without
widening the row per type.

Migration `0002_check_result_details.sql`:

```sql
ALTER TABLE check_results ADD COLUMN details TEXT;
UPDATE check_results
   SET details = json_object('status_code', http_status_code)
 WHERE http_status_code IS NOT NULL;
ALTER TABLE check_results DROP COLUMN http_status_code;
```

`ALTER TABLE … DROP COLUMN` requires SQLite ≥ 3.35.0, which `modernc.org/sqlite`
satisfies. Existing rows produced by the v0.1.0 HTTP runner are preserved
through the backfill: `http_status_code = N` becomes
`details = {"status_code": N}`. New runners emit their own typed payloads
(§15.3).

## 14. Prometheus TSDB storage

### 14.1 TSDB role

Prometheus TSDB stores numeric time-series samples for historical analysis and TUI history views.

### 14.2 Series model

Recommended metric names:

```text
uptimemonitor_probe_success
uptimemonitor_probe_duration_seconds
uptimemonitor_probe_http_status_code
```

Recommended labels:

```text
monitor_id
monitor_type
```

Optional labels:

```text
phase
```

Avoid high-cardinality labels such as URL, error message, or monitor name. Store those in SQLite.

The `monitor_type` label takes one of `http`, `tcp`, `ping`, `dns`.
`uptimemonitor_probe_http_status_code` is emitted only for HTTP monitors; the
other types record their type-specific observations through `CheckResult.Details`
(§15.3) rather than additional TSDB series, to avoid metric sprawl. Adding
per-type series (e.g. ICMP RTT histograms, DNS rcode counters) is deferred to
a later release.

### 14.3 Samples

For each check:

```text
uptimemonitor_probe_success{monitor_id="...", monitor_type="http"} 0|1
uptimemonitor_probe_duration_seconds{monitor_id="...", monitor_type="http"} duration_seconds
uptimemonitor_probe_http_status_code{monitor_id="...", monitor_type="http"} status_code
```

For failed checks without HTTP status, omit `uptimemonitor_probe_http_status_code` or write `0`. Omission is preferred because `0` is not an HTTP status code.

### 14.4 Retention

MVP defaults:

```text
raw samples: 30 days
aggregated history: 365 days
```

Implementation decision:

- Raw samples live in Prometheus TSDB.
- Aggregated history for MVP ranges is computed on demand by querying and
  bucketing TSDB samples; there is no separate aggregated-history store (SPEC
  v0.1 open question 3, now resolved).
- A persisted aggregation store may be revisited post-MVP if on-demand queries
  become too slow for the longer ranges.

### 14.5 History query ranges

MVP supported ranges:

```text
1h
6h
24h
7d
30d
```

Recommended automatic resolution:

```text
1h: 1m
6h: 5m
24h: 15m
7d: 1h
30d: 6h
```

### 14.6 Compaction and cleanup

The service should perform retention cleanup on startup and periodically while running.

Recommended cleanup interval:

```text
1h
```

Exact Prometheus TSDB block and compaction behavior should be validated during implementation.

## 15. Probe execution

### 15.1 Probe interface

```go
type Runner interface {
    Type() monitor.MonitorType
    Run(ctx context.Context, m monitor.Monitor) (probe.Result, error)
}
```

Each Runner is stateless across calls and safe to invoke concurrently for
distinct monitors. `Run` returns a `probe.Result` describing the observation;
per-check failures (transport errors, out-of-range responses, lookup failures,
ICMP timeouts) are reported through `Result.Success` and `Result.Error`. The
error return value is reserved for Runner-level problems (e.g. malformed
monitor config that escaped validation, or a missing OS facility such as the
unprivileged ICMP socket) — see §15.4.

```go
type Result struct {
    StartedAt  time.Time
    FinishedAt time.Time
    Duration   time.Duration
    Success    bool
    Error      string          // sanitized; no secrets, no raw request data
    Details    json.RawMessage // type-specific; see §15.3
}
```

### 15.2 Runner registry

The probe Dispatcher routes a check to the Runner registered for the
monitor's `MonitorType`. v0.2.0 ships four runners:

```text
http  -> HTTP runner (§15.2.1)
tcp   -> TCP port runner (§15.2.2)
ping  -> ICMP ping runner (§15.2.3)
dns   -> DNS runner (§15.2.4)
```

`NewDispatcher()` registers all four. Tests may override the registry by
calling `Register` before sharing the dispatcher across goroutines.

#### 15.2.1 HTTP runner

Behavior:

- Supports `GET` only (other methods deferred).
- Uses per-monitor timeout.
- Follows Go's default redirect behavior.
- Reads up to `HTTPMonitorConfig.BodyCap` bytes of the body (default 1 MiB)
  when a keyword check is configured; otherwise drains and discards the body
  within the timeout.
- Records HTTP status code in `HTTPDetails` (§15.3).
- Classifies success when (a) the request completed without transport error,
  (b) the response status code falls within the configured expected range
  (default 200–299), and (c) the keyword check passes when configured.

Keyword check (`HTTPMonitorConfig.Keyword`, optional):

- `contains`: success requires the read body to contain `Value` (literal byte
  match).
- `not_contains`: success requires the read body to NOT contain `Value`.
- `regex`: success requires the compiled `Value` (RE2 syntax) to match
  anywhere in the read body. The regex is compiled at monitor validation
  time; case sensitivity is controlled by the pattern (e.g. `(?i)…`).

If the body exceeds `BodyCap`, the read truncates at `BodyCap` and the
keyword check evaluates the prefix; the remainder of the connection is
drained within the monitor timeout to avoid TCP teardown noise.

#### 15.2.2 TCP port runner

Behavior:

- Resolves `TCPMonitorConfig.Host` with the default `net.Resolver`.
- Dials TCP to `Host:Port` within the per-monitor timeout.
- Closes the connection on success; no application-layer payload is
  exchanged.
- Records the resolved address in `TCPDetails`.
- Classifies success as a successful connect within the timeout.

#### 15.2.3 ICMP ping runner

Behavior:

- Uses unprivileged ICMP datagram sockets via `golang.org/x/net/icmp` and
  `golang.org/x/net/ipv4` (IPv4 only in v0.2.0; IPv6 deferred).
- Resolves `ICMPPingMonitorConfig.Host` to an IPv4 address.
- Sends `PacketCount` echo requests (default 1, bounded ≤ 5) back-to-back,
  waiting at most `timeout/PacketCount` for each reply.
- Records resolved address, packets sent, packets received, and best RTT in
  `ICMPPingDetails`; `BestRTTMs` is omitted when no reply arrived.
- Classifies success as receiving at least one reply within the timeout.

Operational requirement (`ping_group_range`):

- The runner opens `unix.SOCK_DGRAM` with `IPPROTO_ICMP`. Linux permits this
  without `CAP_NET_RAW` when the process's GID is within
  `net.ipv4.ping_group_range`.
- The systemd unit (§21) and `deployments/` docs document configuring
  `net.ipv4.ping_group_range` to include the service group.
- If the unprivileged ICMP socket cannot be opened, the runner returns a
  Runner-level error (not a per-check failure) so the operator sees a setup
  problem rather than a flapping "down" state. The service logs the error
  and marks the monitor as misconfigured until the operator fixes the
  sysctl.

#### 15.2.4 DNS runner

Behavior:

- If `DNSMonitorConfig.Resolver` is set (e.g. `1.1.1.1:53`), the runner uses
  a `net.Resolver{PreferGo: true, Dial: ...}` that dials UDP to the
  configured address. Otherwise the runner uses the system resolver.
- Issues exactly one query for `Name` of `RecordType` within the per-monitor
  timeout.
- Records resolver, rcode string (`NOERROR`, `NXDOMAIN`, `SERVFAIL`, …),
  answer count, and the first up-to-10 record values (zone-file textual
  form) in `DNSDetails`.
- Classifies success as: no error rcode, a non-empty answer set of the
  requested record type, and (when configured) the expected-value check
  passes.

Expected-value check (`DNSMonitorConfig.ExpectedValue`, optional):

- Each returned record is serialized to its zone-file textual form (e.g.
  `1.2.3.4` for A; `mail.example.com.` for CNAME/NS; `10 mail.example.com.`
  for MX; the joined character-string contents for TXT).
- Supported conditions (case-sensitive, byte comparisons):
  - `equals` / `not_equals`
  - `contains` / `not_contains`
  - `starts_with` / `not_starts_with`
  - `ends_with` / `not_ends_with`
- Positive conditions (`equals`, `contains`, `starts_with`, `ends_with`) are
  satisfied when at least one record string meets them.
- Negative conditions (`not_equals`, `not_contains`, `not_starts_with`,
  `not_ends_with`) are satisfied when no record string meets the
  corresponding positive form.

### 15.3 Result.Details payloads

Each runner populates `Result.Details` with a marshaled type-specific
struct. The TUI and IPC consumers read this verbatim and select a renderer
based on the parent monitor's `Type`.

```go
// monitor.MonitorTypeHTTP -> HTTPDetails
type HTTPDetails struct {
    StatusCode     *int  `json:"status_code,omitempty"`
    KeywordMatched *bool `json:"keyword_matched,omitempty"` // present only when a keyword check ran
}

// monitor.MonitorTypeTCP -> TCPDetails
type TCPDetails struct {
    RemoteAddr string `json:"remote_addr"` // resolved host:port
}

// monitor.MonitorTypePing -> ICMPPingDetails
type ICMPPingDetails struct {
    RemoteAddr      string `json:"remote_addr"`
    PacketsSent     int    `json:"packets_sent"`
    PacketsReceived int    `json:"packets_received"`
    BestRTTMs       *int64 `json:"best_rtt_ms,omitempty"` // omitted when no reply arrived
}

// monitor.MonitorTypeDNS -> DNSDetails
type DNSDetails struct {
    Resolver    string   `json:"resolver"`    // "system" or "host:port"
    RCode       string   `json:"rcode"`       // NOERROR, NXDOMAIN, ...
    AnswerCount int      `json:"answer_count"`
    Records     []string `json:"records,omitempty"` // first up-to-10, zone-file form
}
```

The check_result row stores Details as the `details TEXT` column (§12.3). A
nil `Details` is allowed; every v0.2.0 runner sets a value. IPC consumers
must understand the schema for their monitor type; the service returns
Details verbatim and does not normalize across types.

### 15.4 Error handling

Transport errors are stored as failed check results with a sanitized error
string. Do not store sensitive request data, secrets, or raw query
contents in error fields.

The Runner interface distinguishes per-check failures from Runner-level
errors:

- **Per-check failure** — returned via `Result.Success = false` and a
  sanitized `Result.Error`. Use for: HTTP transport errors, status out of
  range, keyword mismatch; TCP connect refused or timeout; ICMP timeouts
  (no reply within budget); DNS lookup errors, NXDOMAIN, SERVFAIL, empty
  answer set, expected-value mismatch.
- **Runner-level error** — returned via the second return value of `Run`.
  Use for: unrecoverable setup problems such as malformed monitor config
  that escaped validation, or the unprivileged ICMP socket failing to open
  on a host whose `ping_group_range` excludes the service group. These
  surface as service-level errors in logs and as a misconfigured indicator
  in the TUI, not as flapping monitor state.

## 16. Scheduler

### 16.1 Responsibilities

The scheduler must:

- Load enabled monitors.
- Schedule checks by interval.
- Queue checks to workers.
- Prevent overlapping checks for the same monitor.
- Support manual check triggers.
- Update schedules when monitors are created, changed, enabled, disabled, or deleted.

### 16.2 Worker model

Use a bounded worker pool.

Config:

```text
service.check_workers
```

Default:

```text
16
```

### 16.3 No-overlap rule

If a monitor check is already running when its next interval arrives, skip or delay the new run.

SPEC v0.1 recommendation: skip and record an internal event only if skip frequency becomes useful to expose.

### 16.4 Manual checks

Manual checks should use the same runner and persistence path as scheduled checks.

Manual checks may run even if the monitor is disabled only if explicitly allowed by the API. MVP recommendation: allow manual check for disabled monitors but do not change paused state unless the monitor is enabled.

## 17. State machine

### 17.1 States

```text
unknown
up
down
paused
```

### 17.2 Transitions

Recommended behavior:

```text
unknown + success -> up
unknown + failure -> down
up + success -> up
up + failure -> down
down + failure -> down
down + success -> up
any + disabled -> paused
paused + enabled -> unknown
```

### 17.3 Transition side effects

On `up -> down`:

- Create `monitor_state_changed` event.
- Open incident.
- Queue `monitor_down` notifications.

On `down -> up`:

- Create `monitor_state_changed` event.
- Resolve open incident.
- Queue `monitor_recovered` notifications.

On `unknown -> down`:

- Create event.
- Open incident.
- Queue `monitor_down` notifications.

On `unknown -> up`:

- Create event.
- No incident.
- No recovery notification.

On pause:

- Create event.
- Do not send down or recovery notification.

## 18. Notification system

### 18.1 Provider interface

Recommended interface:

```go
type Provider interface {
    Kind() string
    DisplayName() string
    Fields() []Field
    Validate(ctx context.Context, config json.RawMessage) error
    Send(ctx context.Context, config json.RawMessage, msg Message) error
}
```

### 18.2 Message model

```go
type Message struct {
    EventType   string
    MonitorID   string
    MonitorName string
    State       string
    Title       string
    Body        string
    URL         string
    Time        time.Time
    Metadata    map[string]string
}
```

MVP event types:

```text
monitor_down
monitor_recovered
manual_test
```

### 18.3 Provider registry

```go
type Registry struct {
    providers map[string]Provider
}
```

Provider kinds required for MVP:

```text
webhook
email
ntfy
gotify
discord
telegram
slack
```

### 18.4 Provider config metadata

The provider must expose field metadata for the TUI.

```go
type Field struct {
    Name        string
    Label       string
    Type        FieldType
    Required    bool
    Secret      bool
    Default     string
    Description string
}
```

Field types:

```text
string
secret_string
url
number
bool
select
textarea
```

### 18.5 Required provider configs

#### Webhook

```json
{
  "url": "https://example.com/webhook",
  "method": "POST"
}
```

#### Email

```json
{
  "host": "smtp.example.com",
  "port": 587,
  "username": "alerts@example.com",
  "password": "secret",
  "from": "alerts@example.com",
  "to": "admin@example.com",
  "starttls": true
}
```

#### ntfy

```json
{
  "server_url": "https://ntfy.sh",
  "topic": "my-topic",
  "token": "optional"
}
```

#### Gotify

```json
{
  "server_url": "https://gotify.example.com",
  "token": "secret",
  "priority": 5
}
```

#### Discord

```json
{
  "webhook_url": "https://discord.com/api/webhooks/..."
}
```

#### Telegram

```json
{
  "bot_token": "secret",
  "chat_id": "123456"
}
```

#### Slack

```json
{
  "webhook_url": "https://hooks.slack.com/services/..."
}
```

All examples above use example values and must not appear as real defaults.

### 18.6 Delivery pipeline

```text
state transition occurs
event is written
incident is opened/resolved if needed
notification job is created
worker loads enabled targets
provider sends message
attempt is recorded
retry if needed
final status is recorded
```

The "notification job" is held in an in-memory queue; MVP retry state is tracked
through the `notification_attempts` table only, with no separate persistent job
table (SPEC v0.1 open question 4, now resolved). In-flight jobs that have not
completed at shutdown are flushed during graceful shutdown (§9.3); jobs are not
durable across a crash in the MVP.

Each job is delivered to every globally-enabled notification target; the MVP has
no per-monitor target selection. Whether a monitor emits notifications at all is
governed by the monitor's `notifications_enabled` flag together with the global
`notifications.enabled` setting (SPEC v0.1 open question 5, now resolved).

### 18.7 Retry policy

MVP defaults:

```text
max_attempts: 3
initial_retry_delay: 5s
max_retry_delay: 60s
```

Use bounded exponential backoff.

Do not retry manual test notifications unless explicitly requested.

### 18.8 Spam prevention

The service should not send repeated down notifications on every failed check while a monitor is already down.

MVP sends:

- One down notification when incident opens.
- One recovery notification when incident resolves.
- Manual test notifications when requested.

### 18.9 Secret handling

Provider secrets may be stored in SQLite for MVP.

Requirements:

- Do not log secret fields.
- Do not return secret values from API responses by default.
- TUI should display secret fields as set/unset.
- Updating a target may preserve existing secret values when fields are left blank.

## 19. TUI architecture

### 19.1 Bubble Tea structure

The TUI should be screen-based.

Recommended screens:

```text
service status
monitor list
monitor detail
monitor form
notification target list
notification target form
notification attempt list
settings
confirmation dialog
error dialog
```

### 19.2 Model ownership

TUI models should store view state and cached API responses only.

No persistent writes happen locally.

### 19.3 API commands

Bubble Tea commands should call IPC client methods.

Example:

```go
func fetchMonitorsCmd(client *ipc.Client) tea.Cmd {
    return func() tea.Msg {
        monitors, err := client.ListMonitors(context.Background())
        if err != nil {
            return errMsg{err: err}
        }
        return monitorsLoadedMsg{monitors: monitors}
    }
}
```

### 19.4 Destructive confirmations

Required confirmations:

- Delete monitor.
- Delete notification target.
- Clear history.
- Clear incidents.
- Reset configuration.

Confirmation should include the affected object name when available.

### 19.5 History visualization

MVP should support text-based history indicators.

Possible representation:

```text
up:      ▪ or +
down:    x
unknown: ?
paused:  -
```

Use only characters that render reliably in common terminals unless a compatibility mode is added.

## 20. Security model

### 20.1 MVP local security

MVP security relies on:

- Unix socket filesystem permissions.
- Data directory permissions.
- Config file permissions.
- Service user isolation under systemd.

The TUI does not require account login or secret authentication.

### 20.2 Future secret

A configurable secret exists for future web UI/API use.

MVP requirements:

- Config key exists.
- Env var override exists.
- Secret is not required for TUI use.
- Secret is not logged.

Decision (SPEC v0.1 open question 7, now resolved):

- For the MVP the secret is loaded only from config or environment; it is not
  persisted, and no hash is stored in SQLite.
- Storing a derived hash in SQLite may be revisited when web UI/API work begins.

### 20.3 IPC access

Unix socket permissions should restrict access.

Recommended socket mode:

```text
0660
```

Recommended ownership:

```text
uptimemonitor:uptimemonitor
```

Local group membership can grant access to the TUI.

## 21. systemd integration

### 21.1 Unit file

Recommended unit:

```ini
[Unit]
Description=Uptime Monitor
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=uptimemonitor
Group=uptimemonitor
ExecStart=/usr/bin/uptimemonitor service --config /etc/uptimemonitor/config.yaml
Restart=on-failure
RestartSec=5s
WatchdogSec=30s
RuntimeDirectory=uptimemonitor
StateDirectory=uptimemonitor

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/uptimemonitor /run/uptimemonitor

[Install]
WantedBy=multi-user.target
```

### 21.2 Readiness

The service should notify readiness after:

- Config is loaded.
- SQLite is migrated.
- TSDB is open.
- IPC server is listening.
- Scheduler is running.

### 21.3 Watchdog

If watchdog support is enabled by systemd, the service should periodically report liveness.

### 21.4 ICMP ping prerequisites

The unprivileged ICMP runner (§15.2.3) opens an ICMP datagram socket which
requires the service group to be inside `net.ipv4.ping_group_range`.

Operators running ICMP ping monitors should drop a sysctl file alongside the
unit, for example:

```ini
# /etc/sysctl.d/60-uptimemonitor-ping.conf
net.ipv4.ping_group_range = 0 2147483647
```

A hardened deployment can replace the upper bound with the service GID range
instead of `2147483647`. The `deployments/` directory ships an example
drop-in alongside `uptimemonitor.service`.

This requirement applies only to hosts running ICMP ping monitors; HTTP,
TCP, and DNS monitors do not need it. The service does not modify sysctls
itself.

## 22. Containerization with ko

### 22.1 Image entrypoint

ko sets the image entrypoint to the compiled `uptimemonitor` binary and leaves
the container command empty; it has no mechanism to bake in a subcommand. The
service is therefore selected by supplying `service` as the runtime argument,
not by the image alone:

```text
docker run <image> service
```

In an orchestrator, pass it as the container arguments (for example, Kubernetes
`args: ["service"]`) and leave the command unset so ko's binary entrypoint is
preserved. The deployment behaviour is equivalent to running
`uptimemonitor service`.

### 22.2 Persistence

Container deployment must mount persistent data.

Recommended mount:

```text
/var/lib/uptimemonitor
```

### 22.3 Runtime directory

Containers may not use `/run/uptimemonitor` the same way as systemd.

Decision (SPEC v0.1 open question 6, now resolved):

- The MVP uses the Unix socket only, inside the container as elsewhere.
- No TCP listener is added in the MVP. An optional local TCP listener may be
  considered post-MVP, together with container TUI access patterns.

## 23. Logging

Use Go `log/slog` unless a later implementation reason requires a different logger.

Requirements:

- Structured logs.
- Configurable log level.
- No secrets in logs.
- Startup logs include effective paths.
- Probe errors are summarized.
- Notification failures include provider kind and target ID, not secret config.

Recommended fields:

```text
component
monitor_id
target_id
provider_kind
event_type
error
```

## 24. Testing strategy

### 24.1 Unit tests

Required areas:

- Config loading and validation.
- Monitor validation per type (HTTP, TCP, ICMP ping, DNS — §11.2).
- HTTP probe classification (status range + keyword check; §15.2.1).
- DNS expected-value condition matrix (eight conditions × positive/negative
  semantics; §15.2.4).
- State transitions.
- Notification provider validation.
- Notification retry decisions.
- IPC handler validation.
- Per-type `Details` marshaling/unmarshaling (§15.3).

### 24.2 Integration tests

Required areas:

- SQLite migrations on empty database, including migration 0002 backfill from
  a v0.1.0 dataset (§13.4).
- Service startup with temporary directories.
- Monitor create/update/delete over IPC for every type.
- Manual check over IPC.
- TCP runner against `net.Listen("tcp", "127.0.0.1:0")` loopback listener.
- DNS runner against an in-process DNS server (e.g. `github.com/miekg/dns`)
  exposing canned A / AAAA / MX / TXT / CNAME / NS responses.
- ICMP runner integration test is skipped by default and gated on a build
  tag or env var (e.g. `UPTIMEMONITOR_TEST_ICMP=1`) because it requires
  `ping_group_range` to be configured; CI defaults to skip.
- Notification test path with fake provider.
- TSDB sample write and query.

### 24.3 TUI tests

Test Bubble Tea update logic where practical:

- Monitor list loading.
- Form validation messages.
- Confirmation dialogs.
- Error message rendering state.

### 24.4 End-to-end smoke test

A basic smoke test should:

1. Start service with temp directories.
2. Create a monitor through IPC.
3. Run a local test HTTP server.
4. Trigger manual check.
5. Verify state becomes `up`.
6. Stop test server.
7. Trigger manual check.
8. Verify state becomes `down`.
9. Verify notification attempt with fake provider.

## 25. Build and quality checks

Recommended development commands:

```text
go test ./...
go vet ./...
gofmt
go mod tidy
atlas migrate lint
```

Optional later:

```text
golangci-lint run
ko build .
```

The development command set is encoded in a `Makefile` (SPEC v0.1 open question 8, now resolved).

## 26. Implementation milestones

### Milestone 1: Project scaffold

Deliver:

- Go module.
- Cobra/Viper scaffold.
- `service` command placeholder.
- `tui` command placeholder.
- Config loading.
- README.
- MIT license.
- PRD and SPEC docs.

### Milestone 2: Service foundation

Deliver:

- Config validation.
- SQLite open.
- Atlas migration setup.
- TSDB open.
- IPC status endpoint.
- Graceful shutdown.

### Milestone 3: Monitor CRUD

Deliver:

- SQLite monitor schema.
- IPC monitor endpoints.
- TUI monitor list.
- TUI monitor form.
- Create/edit/delete monitor flows.

### Milestone 4: HTTP checks and scheduler

Deliver:

- HTTP probe runner.
- Scheduler.
- Worker pool.
- Manual check trigger.
- State transitions.
- Recent check persistence.

### Milestone 5: History

Deliver:

- TSDB writes.
- History query endpoint.
- TUI recent history indicator.
- Basic retention cleanup.

### Milestone 6: Notifications

Deliver:

- Notification registry.
- Fake provider for tests.
- Webhook provider.
- Email provider.
- ntfy provider.
- Gotify provider.
- Discord provider.
- Telegram provider.
- Slack provider.
- TUI notification configuration.
- Test notification flow.
- Down/recovery notification flow.

### Milestone 7: Packaging

Deliver:

- systemd unit.
- ko build documentation.
- Example config.
- Install/run documentation.

### Milestone 8: Additional monitor types (v0.2.0)

Deliver:

- Migration `0002_check_result_details.sql` replacing
  `check_results.http_status_code` with `details TEXT` and backfilling
  existing rows (§13.4).
- `probe.Result.Details` and per-type Details structs (§15.3).
- HTTP runner extended with body cap and keyword check (§15.2.1).
- TCP port runner (§15.2.2).
- ICMP ping runner using unprivileged datagram sockets (§15.2.3); systemd
  unit and `deployments/` docs updated for `ping_group_range`.
- DNS runner (§15.2.4) with optional custom resolver and 8-condition
  expected-value check.
- Per-type `monitor.Config` validation (§11.2).
- Dispatcher registers all four runners by default.
- IPC schema updated to expose `Details` on check-result responses and
  accept type-specific configs on monitor create/update.
- TUI monitor form: type selector + type-specific field groups; monitor
  detail screen renders per-type summary lines from `Details`.
- End-to-end smoke test extended to cover all four types (with the ICMP
  variant gated behind the env-var/build-tag described in §24.2).

## 27. Resolved technical questions

The technical questions left open by SPEC v0.1 are resolved as follows in SPEC
v0.2. Each resolution is also reflected in the section noted.

1. Monitor IDs use ULID (§3, §12.2).
2. Monitor deletion is soft-delete: the row is retained with `deleted_at` set and
   hidden from default listings, and TSDB samples expire through retention
   (§10.5, §12.3).
3. Aggregated history is computed on demand from TSDB; the MVP has no separate
   aggregated-history store (§14.4).
4. Retry state uses the `notification_attempts` table plus an in-memory job
   queue; no separate job table is added (§18.6).
5. The MVP uses global enabled targets plus a per-monitor `notifications_enabled`
   on/off flag; there is no per-monitor target selection (§18.6).
6. Container deployments use the Unix socket only; no TCP IPC is added in the MVP
   (§22.3).
7. The future secret is loaded only from config/env in the MVP and is not stored
   or hashed in SQLite (§20.2).
8. Development commands are encoded in a `Makefile` (§3, §25).

### 27.1 Resolved in SPEC v0.4

These decisions are accepted for SPEC v0.4 (derived from PRD v0.3 §22):

1. Release 0.2.0 adds three monitor types — TCP port, ICMP ping (unprivileged),
   DNS — and extends HTTP with optional keyword matching (§3, §11.2, §15.2).
2. HTTP keyword matching is an extension of the HTTP monitor type, with three
   modes: `contains`, `not_contains`, `regex` (§11.2.1, §15.2.1). The `regex`
   mode uses Go's RE2 (`regexp.Compile`); case sensitivity is controlled by the
   pattern, not by an external flag.
3. ICMP ping uses unprivileged ICMP datagram sockets via Linux's
   `net.ipv4.ping_group_range`. Operators configure the sysctl at install
   time; the service does not modify it (§15.2.3, §21.4).
4. `probe.Result` carries type-specific data via a `Details json.RawMessage`
   payload (§15.3); the v0.1.0 `check_results.http_status_code` column is
   replaced by `details TEXT` through migration 0002 (§13.4).
5. DNS expected-value checks support eight conditions; positive conditions
   are existential ("at least one record matches"), negative conditions are
   universal ("no record matches the positive form"); all comparisons are
   case-sensitive byte comparisons against the zone-file textual form of
   each record (§15.2.4).
6. Per-type TSDB metrics are deferred; v0.2.0 keeps the three existing series
   (`uptimemonitor_probe_success`, `_duration_seconds`,
   `_http_status_code` HTTP-only) and stores per-type observations in
   `Details` (§14.2).

## 28. Acceptance criteria for the MVP implementation

An implementation satisfies this SPEC when:

- `uptimemonitor service` starts with valid config.
- Service creates/opens SQLite and TSDB storage.
- Service applies migrations.
- Service exposes `/v1/status` over Unix socket.
- `uptimemonitor tui` connects to the service.
- A user can create an HTTP monitor in the TUI.
- The scheduler checks the monitor repeatedly.
- Results are stored in SQLite and TSDB.
- State transitions are recorded.
- Incidents are opened and resolved.
- A user can configure MVP notification providers in the TUI.
- A user can send test notifications.
- Down and recovery notifications are sent once per incident lifecycle.
- Destructive TUI actions require confirmation.
- The service can run under systemd.
- The project can build a container image using ko.

### 28.1 v0.2.0 acceptance criteria

In addition to the MVP criteria above, v0.2.0 is satisfied when:

- A user can create monitors of types `http`, `tcp`, `ping`, and `dns`
  through the TUI; the form exposes the type-specific fields documented in
  §11.2.
- The HTTP monitor form accepts an optional keyword check (mode + value);
  invalid `regex` patterns are rejected at save time, not at run time.
- The DNS monitor form accepts an optional expected-value check and the
  eight conditions in §15.2.4.
- A configured ICMP ping monitor produces successful checks on a host whose
  `net.ipv4.ping_group_range` covers the service group, and returns a
  Runner-level error (logged, surfaced in the TUI as misconfigured) on a
  host where the socket cannot be opened.
- Migration `0002_check_result_details.sql` applies cleanly to a v0.1.0
  database and backfills `http_status_code` rows into `details` as
  `{"status_code": N}`.
- Per-type summary lines (HTTP status code, TCP remote address, ICMP RTT,
  DNS rcode + records) are visible on the monitor detail screen, sourced
  from `Details`.
- Existing TSDB queries continue to work; no new series are introduced.

## 29. Revision history

```text
0.1 - Initial technical specification derived from PRD v0.2.
0.2 - Resolved all SPEC v0.1 open technical questions (§27). Added
      internal/logging to the repository layout (§5). Documented the custom
      duration decode hook for day and week suffixes (§8). Specified embedded,
      in-process migration application at startup with no runtime dependency on
      the atlas binary (§13). Corrected the ko build path (§25).
0.3 - Clarified the ko image entrypoint (§22.1): ko sets the binary as the
      container entrypoint with an empty command, so the `service` subcommand is
      supplied as a runtime argument by the deployment rather than baked into
      the image.
0.4 - Added three monitor types and an HTTP keyword extension for release
      0.2.0. Restructured §11.2 into per-type config payloads (§11.2.1–§11.2.5)
      and replaced `CheckResult.HTTPStatusCode` with `Details json.RawMessage`
      (§11.3, §15.3). Replaced `check_results.http_status_code` with
      `details TEXT` and documented migration 0002 (§12.3, §13.4). Reorganised
      §15 into Runner registry + per-type runners (HTTP+keyword, TCP, ICMP
      ping, DNS) and distinguished per-check failures from Runner-level errors
      (§15.4). Added §21.4 documenting the `net.ipv4.ping_group_range`
      requirement for unprivileged ICMP. Added §27.1 resolutions, §28.1
      acceptance criteria, and Milestone 8 (§26). Probe directory layout in §5
      expanded to `details.go`, `dns.go`, `http.go`, `ping.go`, `tcp.go`.
```
