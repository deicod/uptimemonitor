# Uptime Monitor PRD

Status: Draft  
Version: 0.2  
Date: 2026-05-18  
Repository: `github.com/deicod/uptimemonitor`  
License: MIT  
Primary target: Linux with systemd  
Primary interface: terminal UI

## 1. Summary

Uptime Monitor is open-source uptime monitoring software written in Go.

It is conceptually inspired by Uptime Kuma, but it is not intended to be a clone, a web-first product, or a feature-for-feature reimplementation. The project focuses on a local-first, terminal-native monitoring experience with a long-running background service and a CLI/TUI interface.

The system is packaged as one binary named `uptimemonitor`. The binary provides a background service command and a terminal UI command. The service performs checks, persists state, sends notifications, and exposes a local IPC API. The TUI connects to the running service and provides the primary user experience.

The MVP uses SQLite for relational data and configuration, Prometheus TSDB for time-series probe results, Bubble Tea for the terminal UI, Cobra/Viper for CLI and configuration, Atlas for database migrations, and ko for container image builds.

## 2. Product positioning

Uptime Monitor is a lightweight, self-hosted, terminal-native uptime monitor for developers, operators, and homelab users who want a focused monitoring system without a browser-first dashboard or heavyweight observability stack.

It should feel like a reliable system utility: easy to install, easy to run, inspectable, and simple to operate over SSH.

## 3. Goals

The project aims to provide:

- A self-hosted uptime monitoring system written in Go.
- A terminal-first user experience using a Bubble Tea TUI.
- A long-running background service suitable for systemd.
- A clear separation between service logic and UI logic.
- Embedded storage without requiring PostgreSQL, Redis, external Prometheus, or other infrastructure.
- Built-in notifications as part of the MVP.
- An extensible Go-native notification provider system inspired by Uptime Kuma's broad provider catalog.
- A containerizable deployment path using ko.
- Local-first single-user operation for the initial release.
- A future-compatible secret-based access model for a later web UI.
- MIT-licensed open-source software that is easy to inspect, modify, and self-host.

## 4. Non-goals

The MVP does not aim to provide:

- A web UI.
- Hosted SaaS operation.
- Multi-user account management.
- Distributed monitoring from multiple regions.
- Remote agents.
- Multi-node clustering.
- Full Uptime Kuma feature parity.
- Uptime Kuma import compatibility.
- A Kubernetes operator.
- A public internet-facing API.
- Prometheus-compatible metrics export.
- A Dockerfile-first container workflow.

These may be considered later, but they should not shape the MVP unless they are required for clean future compatibility.

## 5. Target users

Primary users:

- Developers who want a simple self-hosted uptime monitor.
- Operators who prefer terminal tools over web dashboards.
- Homelab users who want lightweight service monitoring.
- Single-user server administrators who manage systems over SSH.

Secondary users:

- Go developers interested in extending the monitor.
- Users who want a minimal alternative to web-first uptime tools.
- Users who want systemd-native operation.
- Users who want a tool that can later gain a web UI without requiring accounts in the TUI path.

## 6. Product principles

### Terminal-native

The primary interface is the TUI. The TUI should be fast, keyboard-friendly, readable in common terminal sizes, and usable over SSH.

### Local-first

The MVP targets single-user local operation. The default deployment model is a local service plus local TUI client.

### Service-owned state

The background service owns persistent state. The TUI must not directly read from or write to SQLite or TSDB.

### Boring persistence

Embedded storage is preferred. SQLite stores relational data and configuration. Prometheus TSDB stores historical time-series check data.

### Conceptual similarity, not compatibility

The project may borrow product concepts from Uptime Kuma, such as monitors, heartbeats, notifications, history views, and status summaries. It should not attempt to duplicate the web UI or guarantee compatibility with Uptime Kuma data models.

### Configurable security boundary

The TUI should not require an account or secret for local use. A configurable secret should exist because a future web UI will need an authentication boundary.

### Containerizable by design

The project should be containerizable with ko. Container support is part of product scope, but systemd-managed local operation remains the primary MVP path.

## 7. User experience overview

The user installs one binary:

```text
uptimemonitor
```

The binary exposes at least two commands:

```text
uptimemonitor service
uptimemonitor tui
```

The expected local flow is:

```text
systemctl start uptimemonitor
uptimemonitor tui
```

The service runs continuously and performs checks. The TUI connects to the service over a local IPC channel and provides monitor management, configuration, status inspection, history inspection, and notification configuration.

## 8. MVP scope

The MVP includes:

- One Go binary.
- Cobra/Viper command scaffold initialized with `cobra-cli`.
- `uptimemonitor service` command.
- `uptimemonitor tui` command.
- systemd-compatible background service.
- Bubble Tea-based terminal UI.
- HTTP over Unix socket IPC between the TUI and service.
- SQLite-backed configuration and metadata storage using `modernc.org/sqlite`.
- Atlas-managed SQLite migrations.
- Prometheus TSDB-backed time-series storage.
- HTTP monitor type.
- Monitor creation primarily inside the TUI.
- Full configuration access inside the TUI.
- Monitor list view.
- Monitor detail view.
- Current status display.
- Recent check result display.
- Basic history visualization inspired by Uptime Kuma-style heartbeat/history views.
- Manual check trigger.
- Basic incident/state tracking.
- Built-in notification support with a provider registry.
- Initial Go-native notification providers for common webhook/chat/push/email workflows.
- Configurable secret for future web UI/API use.
- Local configuration file support.
- Basic logging.
- ko-based container image build path.

## 9. MVP exclusions

The MVP excludes:

- Web UI.
- User registration and login flows.
- TUI authentication.
- Public status pages.
- Distributed agents.
- Remote check locations.
- Prometheus metrics endpoint.
- Uptime Kuma import/export compatibility.
- Advanced analytics.
- Multi-tenant operation.
- Role-based access control.
- Complex escalation policies.

## 10. User-facing concepts

### Monitor

A configured target that should be checked repeatedly.

### Check result

The result of one probe execution for a monitor.

### Heartbeat

A compact historical representation of check status over time. In this project, heartbeat is a product concept, not necessarily a direct copy of Uptime Kuma internals.

### Incident

A period where a monitor is considered down or degraded.

### Event

A noteworthy monitor or system-level change, such as monitor creation, monitor update, state transition, notification failure, or service start.

### Notification target

A configured destination for alerts. Notification targets are backed by provider implementations such as webhook, email, ntfy, Gotify, Discord, Telegram, Slack, or other Uptime Kuma-like integrations. Provider support is Go-native and does not imply reuse of Uptime Kuma's JavaScript implementation or config schema.

### Secret

A configured shared secret intended for future web UI/API access. It should not be required for local TUI use in the MVP.

## 11. Functional requirements

### 11.1 Service command

The service command must:

- Load configuration.
- Validate configuration.
- Initialize SQLite.
- Run Atlas-managed migrations.
- Initialize Prometheus TSDB.
- Start the local IPC server.
- Start the monitor scheduler.
- Execute checks on schedule.
- Store check results.
- Update current monitor state.
- Detect state transitions.
- Create incidents and events.
- Send notifications for relevant state changes.
- Expose monitor, status, history, incident, event, and configuration data to the TUI.
- Shut down cleanly on termination signals.

### 11.2 TUI command

The TUI command must:

- Connect to the running service.
- Display service health.
- Display the monitor list.
- Display monitor details.
- Show current monitor state.
- Show recent check results.
- Show basic history visualization.
- Create monitors.
- Edit monitors.
- Delete monitors.
- Enable and disable monitors.
- Trigger manual checks.
- Configure notifications.
- Configure global settings.
- Confirm destructive actions before applying them.

### 11.3 Monitor management

The system must allow users to:

- Create HTTP monitors.
- List monitors.
- View monitor details.
- Update monitor configuration.
- Enable monitors.
- Disable monitors.
- Delete monitors.
- Trigger a check manually.

Monitor creation and editing should happen primarily in the TUI.

### 11.4 HTTP monitor

The first monitor type is HTTP.

An HTTP monitor must support:

- Display name.
- URL.
- Method, initially `GET`.
- Check interval.
- Timeout.
- Expected status code or status code range.
- Enable/disable flag.
- Notification enable/disable behavior.

The MVP may defer:

- Custom request headers.
- Request bodies.
- HTTP authentication.
- TLS certificate expiry checks.
- Keyword matching.
- Redirect policy configuration.
- Proxy configuration.

### 11.5 Check execution

The service must:

- Execute checks according to each monitor interval.
- Respect monitor timeout settings.
- Record success or failure.
- Record response duration.
- Record relevant HTTP response metadata.
- Avoid overlapping checks for the same monitor unless explicitly supported later.
- Persist enough information to show current status and recent history.

### 11.6 State tracking

The service must track these monitor states:

```text
unknown
up
down
paused
```

The service must detect state transitions such as:

```text
unknown -> up
up -> down
down -> up
up -> paused
paused -> up
```

State transitions must be stored as events. Down periods should create or update incidents.

### 11.7 Notifications

Notifications are part of the MVP.

The product should support a provider model similar in spirit to Uptime Kuma's notification-provider catalog: many provider integrations, each with its own configuration, exposed through a common notification workflow. This project should implement providers natively in Go rather than reusing Uptime Kuma's JavaScript code. Uptime Kuma is a reference for expected provider breadth, not a compatibility contract.

The MVP must include the notification provider framework and a practical initial provider set.

Required MVP providers:

1. Generic webhook.
2. SMTP/email.
3. ntfy.
4. Gotify.
5. Discord webhook.
6. Telegram bot.
7. Slack webhook.

Recommended post-MVP provider families:

- Incident management providers, such as PagerDuty, Opsgenie, or similar services.
- Push notification providers, such as Pushover or similar services.
- Chat providers, such as Matrix, Microsoft Teams, Mattermost, Rocket.Chat, or similar services.
- Mobile/app notification providers.
- Additional webhook-compatible services.

Provider support requirements:

- Configure notification targets in the TUI.
- Show provider-specific required and optional fields in the TUI.
- Validate provider configuration before saving where practical.
- Send a test notification for each configured target.
- Send notification when a monitor transitions to `down`.
- Send notification when a monitor recovers to `up`.
- Record notification send attempts.
- Record notification failures.
- Avoid notification spam from repeated failures.
- Allow notifications to be enabled or disabled globally.
- Allow notification targets to be enabled or disabled individually.

Provider implementation requirements:

- Providers must use a common service-side interface.
- Providers must be registered by stable provider kind, such as `webhook`, `email`, `ntfy`, `gotify`, `discord`, `telegram`, or `slack`.
- Provider configuration must be stored as structured data.
- Provider secrets must not be logged.
- Provider send behavior must be owned by the service, not the TUI.
- The TUI may trigger test notifications, but the service performs delivery.

Advanced notification features are post-MVP:

- Escalation policies.
- Notification schedules.
- Per-user notification preferences.
- Complex templating.
- Multiple severity levels.
- Full Uptime Kuma notification-provider parity.
- Uptime Kuma notification configuration import.

### 11.8 Configuration

The project uses Cobra and Viper.

Configuration must be available through:

- Config file.
- Environment variables.
- Command flags where appropriate.

The default config format should be YAML.

Recommended default paths:

```text
/etc/uptimemonitor/config.yaml
/var/lib/uptimemonitor/config.db
/var/lib/uptimemonitor/tsdb
/run/uptimemonitor/uptimemonitor.sock
```

Example configuration shape:

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

retention:
  raw_samples: 30d
  aggregated_history: 365d

notifications:
  enabled: true
  default_providers:
    - webhook
    - email
    - ntfy
    - gotify
    - discord
    - telegram
    - slack
```

## 12. TUI requirements

The TUI should expose all configuration needed for MVP operation.

### 12.1 Monitor list screen

Displays:

- Monitor name.
- Monitor type.
- Current state.
- Last check time.
- Last response time.
- Compact recent history indicator.
- Notification enabled/disabled indicator.

### 12.2 Monitor detail screen

Displays:

- Monitor configuration.
- Current state.
- Recent check results.
- Recent incidents.
- Recent events.
- Basic latency/history view.
- Notification configuration summary.

### 12.3 Monitor form screen

Allows creating and editing HTTP monitors.

The form should support all MVP HTTP monitor fields.

### 12.4 Notification settings screen

Allows:

- Creating notification targets.
- Choosing a provider kind.
- Editing provider-specific configuration.
- Validating required provider fields.
- Deleting notification targets.
- Sending a test notification.
- Viewing recent notification attempts and failures.
- Enabling/disabling notifications globally.
- Enabling/disabling individual notification targets.
- Enabling/disabling notifications per monitor, if supported by the first implementation.

### 12.5 Service status screen

Displays:

- Service health.
- Service version.
- Storage status.
- Number of monitors.
- Scheduler state.
- IPC connection status.
- Config file path.
- Data directory path.

### 12.6 Confirmation UX

Destructive actions must require confirmation.

Actions requiring confirmation include:

- Delete monitor.
- Delete notification target.
- Clear history.
- Clear incidents.
- Reset configuration.

## 13. History and visualization requirements

The MVP should include basic historical visibility, not advanced analytics.

The history experience should be inspired by Uptime Kuma's compact dashboard/status-page approach, especially its heartbeat-style visual summaries. Uptime Kuma's public UI uses dashboard, status page, settings, and notification-oriented views, and a public draft PR explored configurable heartbeat ranges such as auto, 6 hours, 12 hours, 24 hours, 7 days, 30 days, 60 days, 90 days, 180 days, and 365 days with aggregation for longer ranges.

For this project, MVP history should include:

- Recent check table.
- Compact status history indicator.
- Basic latency values.
- Current uptime summary.
- Clear indication of up/down/unknown/paused states.

Recommended initial ranges:

```text
last 1 hour
last 6 hours
last 24 hours
last 7 days
last 30 days
```

Post-MVP ranges may include:

```text
last 60 days
last 90 days
last 180 days
last 365 days
custom range
```

The first TUI implementation may use text-based indicators rather than graphical charts.

## 14. Retention requirements

The product must define data retention early because check data grows continuously.

Uptime Kuma stores persistent data under a configurable data directory and has data management concepts around retention, database maintenance, clearing statistics, and backup/export behavior. This project should use that as product inspiration, but not duplicate the implementation.

Recommended MVP retention model:

- Raw check samples retained for 30 days by default.
- Aggregated history retained for 365 days by default.
- Retention values configurable globally.
- Per-monitor retention deferred until post-MVP.
- Manual history clearing deferred unless easy to include safely.

The SPEC should define how raw TSDB samples and any aggregated summaries are stored, compacted, and deleted.

## 15. Security requirements

The MVP security model is local-first.

Requirements:

- TUI access must not require an account.
- TUI access must not require the configured secret.
- The service IPC socket should rely on local filesystem permissions.
- The service should not expose a public HTTP API by default.
- A configurable secret must exist for future web UI/API use.
- The secret should be optional for MVP local operation.
- The secret should not be logged.
- The secret should be loadable from config or environment.

Future web UI work should decide whether the secret becomes:

- A bootstrap admin secret.
- A bearer token.
- A setup-only enrollment secret.
- A replacement for full account login in single-user mode.

## 16. Packaging and deployment requirements

### 16.1 Local binary

The project should be buildable as a local Go binary.

### 16.2 systemd

The primary deployment target is Linux with systemd.

The project should provide:

- Example systemd unit.
- Clear default data paths.
- Clear runtime socket path.
- Restart-safe service behavior.

### 16.3 Container image

The project should be containerizable using ko.

Requirements:

- The repository should support ko-based image builds.
- The container image should run the same `uptimemonitor` binary.
- The service command should be usable as the container entrypoint.
- Persistent data should be mounted as a volume.
- Container operation should not become the primary design constraint for the MVP.

## 17. Storage requirements

### 17.1 SQLite

SQLite stores relational and configuration data.

SQLite should store:

- Monitor configuration.
- Notification targets.
- Notification provider kind and provider-specific configuration.
- Notification attempt history.
- Current monitor state.
- Incidents.
- Events.
- Service settings.
- Secret metadata or secret hash if needed later.

The selected driver is `modernc.org/sqlite`.

### 17.2 Atlas migrations

Atlas is the selected migration tool.

The product requires:

- Versioned migrations.
- Repeatable service startup behavior.
- Clear failure behavior if migrations cannot run.
- Migration linting in development workflows where practical.

Implementation details belong in the SPEC.

### 17.3 Prometheus TSDB

Prometheus TSDB is the selected embedded time-series storage.

TSDB should store:

- Probe success/failure samples.
- Probe duration samples.
- HTTP status samples.
- Historical time-series data needed for TUI history views.

The SPEC should define metric names, labels, retention, compaction, and query access patterns.

## 18. Non-functional requirements

### Reliability

The service should be safe to restart. It must recover monitor configuration after restart and continue scheduled checks.

### Performance

The MVP should comfortably support dozens to hundreds of monitors on a small Linux machine.

### Operability

The service should provide useful logs, predictable file locations, and clear startup errors.

### Portability

The primary target is Linux. Other platforms may work, but systemd integration is Linux-specific.

### Simplicity

The project should avoid unnecessary infrastructure dependencies.

### Testability

Core service behavior should be testable without running the TUI. TUI behavior should be testable at the model/update level where practical.

## 19. Success criteria

The MVP is successful when a user can:

- Build or install the binary.
- Start the background service.
- Open the TUI.
- Create an HTTP monitor inside the TUI.
- See the monitor checked repeatedly.
- See whether the target is up or down.
- See recent latency and check history.
- Configure at least one notification target from the MVP provider set.
- Send a test notification.
- Receive a down notification.
- Receive a recovery notification.
- Inspect recent notification delivery failures in the TUI.
- Restart the service without losing monitor configuration.
- Run the service under systemd.
- Build a container image using ko.

## 20. Future scope

Potential future features:

- Web UI.
- TCP monitors.
- DNS monitors.
- ICMP ping monitors.
- TLS certificate expiry checks.
- Keyword/content checks.
- Custom HTTP headers.
- Request body support.
- Additional notification integrations beyond the MVP provider set.
- Broader Uptime Kuma-like notification-provider coverage.
- Import/export.
- Backup/restore.
- Public status pages.
- Remote API.
- Token-based access.
- Full accounts.
- Multi-user support.
- Uptime Kuma import compatibility.
- More advanced incident analytics.
- Maintenance windows.
- Per-monitor retention.
- Custom history ranges.

## 21. Open questions

The following questions remain for the SPEC or later product decisions:

1. Should global retention defaults be exactly 30 days raw and 365 days aggregated, or should those values change before implementation?
2. Should the TUI include textual history indicators only in the MVP, or should it include richer terminal charts?
3. Should the configured secret be stored directly, hashed, or loaded only from environment/secrets files?
4. Should container deployments use the same Unix socket model, a local TCP listener, or both?
5. Should notification settings be global-only in MVP, or configurable per monitor from the start?
6. Which post-MVP notification providers should be prioritized after webhook, email, ntfy, Gotify, Discord, Telegram, and Slack?

## 22. Decisions captured

These decisions are considered accepted for PRD v0.1:

- The project is open-source software, not a commercial product.
- The license is MIT.
- The module path is `github.com/deicod/uptimemonitor`.
- The implementation language is Go.
- The project is conceptually similar to Uptime Kuma, not compatible with it.
- The project uses Cobra/Viper and is initialized with `cobra-cli`.
- The project uses Bubble Tea for the TUI.
- The project has one binary with two MVP commands: `service` and `tui`.
- The service is intended to run under systemd.
- The TUI talks to the service instead of accessing databases directly.
- The IPC protocol is HTTP over Unix socket.
- SQLite stores configuration and relational data.
- The SQLite driver is `modernc.org/sqlite`.
- Atlas manages database migrations.
- Prometheus TSDB stores time-series data.
- Notifications are part of MVP.
- MVP notification providers are generic webhook, SMTP/email, ntfy, Gotify, Discord webhook, Telegram bot, and Slack webhook.
- Notification providers are Go-native implementations inspired by Uptime Kuma's provider breadth, not copied JavaScript providers or a compatibility guarantee.
- Single-user local operation is the MVP target.
- TUI use requires no account and no secret.
- A configurable secret exists for future web UI/API use.
- The software should be containerizable with ko.
- The TUI is the primary place for monitor creation and configuration.
- The TUI should expose all MVP configuration.
- Destructive TUI actions require confirmation.
- Prometheus metrics export is not part of the MVP.

## 23. Research notes

The following public references informed this PRD:

- Uptime Kuma describes itself as an easy-to-use self-hosted monitoring tool and presents dashboard, status page, settings, and notification-oriented UI concepts: <https://github.com/louislam/uptime-kuma>
- Uptime Kuma maintains a large notification-provider directory; this PRD uses that as inspiration for a broad, extensible provider architecture implemented natively in Go: <https://github.com/louislam/uptime-kuma/tree/master/server/notification-providers>
- Uptime Kuma documents a configurable data directory, default data path, SQLite database file, and environment-variable configuration model: <https://github.com/louislam/uptime-kuma/wiki/Environment-Variables>
- Uptime Kuma data management documentation discusses retention policies, database maintenance, statistics clearing, backup/export, and data directory structure: <https://deepwiki.com/louislam/uptime-kuma/11-data-management-and-backup>
- A Uptime Kuma draft PR explored configurable heartbeat bar ranges including auto, 6 hours, 12 hours, 24 hours, 7 days, 30 days, 60 days, 90 days, 180 days, and 365 days, with aggregation for longer ranges: <https://github.com/louislam/uptime-kuma/pull/5916>
- Prometheus TSDB is the local time-series database library used for Prometheus v2 storage: <https://github.com/prometheus/prometheus/tree/main/tsdb>
- Atlas supports declarative and versioned database migration workflows: <https://atlasgo.io/docs>
- ko is a Go container image builder intended for simple Go applications without many OS-level dependencies: <https://ko.build/>
- `modernc.org/sqlite` is a `database/sql` SQLite driver using a CGo-free port of SQLite: <https://pkg.go.dev/modernc.org/sqlite>
