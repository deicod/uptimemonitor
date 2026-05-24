# Uptime Monitor

Self-hosted, terminal-native uptime monitoring written in Go.

Uptime Monitor periodically probes HTTP endpoints, tracks their state and
incidents, and delivers notifications. It ships as a single binary,
`uptimemonitor`, providing a long-lived background **service** that owns
persistence, scheduling, and notification delivery, plus a Bubble Tea **TUI**
client that manages monitors over a local Unix socket.

It is conceptually inspired by Uptime Kuma, but it is local-first and
terminal-native rather than a browser-first dashboard. The primary target is
Linux with systemd.

## Features

- HTTP monitors with per-monitor interval, timeout, and expected status range.
- Scheduler with a bounded worker pool and manual "check now" triggers.
- State machine with incidents (down/recovery) and an event audit log.
- History via an embedded Prometheus TSDB, shown as a heartbeat row in the TUI.
- Seven notification providers: webhook, Discord, Slack, ntfy, Gotify, Telegram,
  and email/SMTP.
- A keyboard-driven terminal UI; all reads and writes go through the service.

## Building

Requires Go (see [`go.mod`](go.mod) for the minimum version).

```sh
make build      # compile ./bin/uptimemonitor with version metadata
make test       # run the test suite
make vet        # run go vet
make lint       # run golangci-lint
```

`go build ./...` also works for a plain build without version ldflags.

## Installation

Build the binary and install it, the example config, and the systemd unit:

```sh
make build
sudo install -m 0755 bin/uptimemonitor /usr/bin/uptimemonitor

# Dedicated service user (matches the systemd unit).
sudo useradd --system --no-create-home --shell /usr/sbin/nologin uptimemonitor

# Configuration.
sudo install -D -m 0640 -o uptimemonitor -g uptimemonitor \
  config.example.yaml /etc/uptimemonitor/config.yaml

# systemd unit.
sudo install -D -m 0644 \
  deployments/systemd/uptimemonitor.service \
  /etc/systemd/system/uptimemonitor.service
sudo systemctl daemon-reload
sudo systemctl enable --now uptimemonitor
```

The unit runs as the `uptimemonitor` user with systemd hardening
(`ProtectSystem=strict`, `NoNewPrivileges`, `PrivateTmp`) and uses
`StateDirectory`/`RuntimeDirectory` to create and own `/var/lib/uptimemonitor`
and `/run/uptimemonitor`. It is `Type=notify`, so systemd marks the service
active only once SQLite is migrated, the TSDB is open, the IPC socket is
listening, and the scheduler is running. `WatchdogSec=30s` enables liveness
pings.

## Running

```sh
uptimemonitor service      # start the background service
uptimemonitor tui          # launch the terminal UI client
uptimemonitor --version    # print version, commit, and build date
uptimemonitor --help       # list commands and flags
```

The TUI connects to a running service over its local Unix socket. To use the
TUI as a non-root user, add that user to the `uptimemonitor` group so it can
access the socket (mode `0660`).

### TUI usage

The TUI is keyboard-driven; each screen shows its available keys in the status
bar.

- **Global:** `ctrl+c` quit, `esc` back, `↑/k` and `↓/j` move, `r` refresh.
- **Home:** `s` status, `m` monitors, `N` notifications.
- **Monitor list:** `n` new, `enter` detail, `e` edit, `d` delete, `c` check now.
- **Monitor detail:** `1`–`5` select the history range, `c` check now, `e` edit.
- **Notifications:** `n` new target, `t` send test, `d` delete, `a` attempts,
  `g` toggle the global notifications switch.
- **Confirm dialog:** `y`/`enter` confirm, `n` cancel. Destructive actions
  (deleting a monitor or target) always ask first.

## Configuration

The service reads YAML configuration, environment variables, and flags, in
increasing order of precedence:

```text
built-in defaults  <  config file  <  environment  <  command flags
```

The config file is `--config <path>` when given, otherwise
`/etc/uptimemonitor/config.yaml` if present. A copy with every value documented
is in [`config.example.yaml`](config.example.yaml).

Every key has an environment override with the `UPTIMEMONITOR_` prefix; nested
keys join with underscores (e.g. `service.check_workers` →
`UPTIMEMONITOR_SERVICE_CHECK_WORKERS`). Durations accept `h`/`m`/`s` plus the
`d` (day) and `w` (week) suffixes used by the retention settings.

### Reference

| Key | Env var | Default | Meaning |
|-----|---------|---------|---------|
| `data_dir` | `UPTIMEMONITOR_DATA_DIR` | `/var/lib/uptimemonitor` | Persistent data root |
| `runtime_dir` | `UPTIMEMONITOR_RUNTIME_DIR` | `/run/uptimemonitor` | Runtime (socket) directory |
| `sqlite_path` | `UPTIMEMONITOR_SQLITE_PATH` | `/var/lib/uptimemonitor/config.db` | SQLite database file |
| `tsdb_path` | `UPTIMEMONITOR_TSDB_PATH` | `/var/lib/uptimemonitor/tsdb` | Prometheus TSDB directory |
| `socket_path` | `UPTIMEMONITOR_SOCKET_PATH` | `/run/uptimemonitor/uptimemonitor.sock` | IPC Unix socket |
| `log_level` | `UPTIMEMONITOR_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `secret` | `UPTIMEMONITOR_SECRET` | `""` | Reserved for a future web UI/API; not required by the TUI |
| `service.check_workers` | `UPTIMEMONITOR_SERVICE_CHECK_WORKERS` | `16` | Concurrent probe workers |
| `service.default_interval` | `UPTIMEMONITOR_SERVICE_DEFAULT_INTERVAL` | `60s` | Default probe interval |
| `service.timeout` | `UPTIMEMONITOR_SERVICE_TIMEOUT` | `10s` | Per-probe timeout (must be `< default_interval`) |
| `service.shutdown_timeout` | `UPTIMEMONITOR_SERVICE_SHUTDOWN_TIMEOUT` | `10s` | Grace period for in-flight checks |
| `retention.raw_samples` | `UPTIMEMONITOR_RETENTION_RAW_SAMPLES` | `30d` | Raw sample / `check_results` retention |
| `retention.aggregated_history` | `UPTIMEMONITOR_RETENTION_AGGREGATED_HISTORY` | `365d` | Aggregated history horizon |
| `notifications.enabled` | `UPTIMEMONITOR_NOTIFICATIONS_ENABLED` | `true` | Global notifications switch |
| `notifications.max_attempts` | `UPTIMEMONITOR_NOTIFICATIONS_MAX_ATTEMPTS` | `3` | Delivery attempts before giving up |
| `notifications.initial_retry_delay` | `UPTIMEMONITOR_NOTIFICATIONS_INITIAL_RETRY_DELAY` | `5s` | First backoff delay |
| `notifications.max_retry_delay` | `UPTIMEMONITOR_NOTIFICATIONS_MAX_RETRY_DELAY` | `60s` | Backoff cap |

Flags `--config`, `--log-level`, `--socket-path`, and `--data-dir` override the
matching keys. The service fails fast with a field-aware error on invalid
configuration.

## Containers (ko)

A distroless image builds straight from source with [ko](https://ko.build):

```sh
make ko-build      # build into the local Docker daemon
```

The image entrypoint is the binary, so pass `service` as the argument
(`docker run <image> service`) and mount persistent storage. See
[`deployments/ko/README.md`](deployments/ko/README.md) for registry pushes,
volume layout, and the Unix-socket caveat.

## Documentation

- [Product Requirements (PRD)](docs/PRD.md) — what the product is and why.
- [Specification (SPEC)](docs/SPEC.md) — technical design and contracts.
- [Implementation Plan (PLAN)](docs/PLAN.md) — milestone-based task list.
- [Contributing](CONTRIBUTING.md) — how to work on the project.

## License

MIT — see [`LICENSE`](LICENSE).
