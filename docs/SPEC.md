# Uptime Monitor Technical Specification

Status: Draft  
Version: 0.2  
Date: 2026-05-18  
Repository: `github.com/deicod/uptimemonitor`  
License: MIT  
Derived from: `docs/PRD.md` version 0.2  
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
│   │   ├── http.go
│   │   ├── result.go
│   │   └── runner.go
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

### 11.2 HTTP monitor config

```go
type HTTPMonitorConfig struct {
    URL               string `json:"url"`
    Method            string `json:"method"`
    ExpectedStatusMin int    `json:"expected_status_min"`
    ExpectedStatusMax int    `json:"expected_status_max"`
}
```

MVP validation:

- URL must be absolute.
- Scheme must be `http` or `https`.
- Method must be `GET`.
- Expected status range must be valid.
- Timeout must be positive.
- Interval must be positive.

### 11.3 Check result

```go
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
```

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
    http_status_code INTEGER
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

### 15.2 HTTP runner

MVP behavior:

- Supports `GET` only.
- Uses per-monitor timeout.
- Follows Go default redirect behavior initially.
- Records HTTP status code.
- Measures total duration.
- Classifies success by expected status range.

### 15.3 HTTP result classification

A check is successful when:

```text
request completes without transport error
and response status code is within configured expected range
```

Default expected range:

```text
200-299
```

### 15.4 Error handling

Transport errors should be stored as failed check results with a sanitized error string.

Do not store sensitive request data in error fields.

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

## 22. Containerization with ko

### 22.1 Image entrypoint

The ko-built image should run:

```text
uptimemonitor service
```

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
- HTTP monitor validation.
- HTTP probe classification.
- State transitions.
- Notification provider validation.
- Notification retry decisions.
- IPC handler validation.

### 24.2 Integration tests

Required areas:

- SQLite migrations on empty database.
- Service startup with temporary directories.
- Monitor create/update/delete over IPC.
- Manual check over IPC.
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

## 29. Revision history

```text
0.1 - Initial technical specification derived from PRD v0.2.
0.2 - Resolved all SPEC v0.1 open technical questions (§27). Added
      internal/logging to the repository layout (§5). Documented the custom
      duration decode hook for day and week suffixes (§8). Specified embedded,
      in-process migration application at startup with no runtime dependency on
      the atlas binary (§13). Corrected the ko build path (§25).
```
