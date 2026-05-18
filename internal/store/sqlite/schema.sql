-- schema.sql is the declarative source of truth for the uptimemonitor SQLite
-- database (SPEC §12.3). Atlas reads this file to diff and generate versioned
-- migrations; the service itself applies the embedded migration files at
-- startup and never executes this file directly.

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
