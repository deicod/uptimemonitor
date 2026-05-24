# Uptime Monitor Implementation Plan

Status: Draft
Version: 0.1
Date: 2026-05-18
Repository: `github.com/deicod/uptimemonitor`
License: MIT
Derived from: `docs/PRD.md` v0.2 and `docs/SPEC.md` v0.2

## 1. Purpose

This document turns the SPEC into an executable, milestone-based task list.

Each task is a self-contained unit of work intended to be implemented by a single
agent in a single focused session. Every task carries a `[ ]` checkbox. When a
task is finished and verified, the implementing agent changes `[ ]` to `[x]` in
this file as part of the same change.

The plan is a living document. If implementation reveals that a task is wrong,
missing, or mis-scoped, update this plan; if behaviour diverges from the SPEC,
reflect the change back into `docs/SPEC.md` (and `docs/PRD.md` if product-facing),
per SPEC §1.

## 2. How to use this plan

- Work milestones in order (M0 → M10). Within a milestone, work tasks in number
  order unless a task says otherwise; some tasks are explicitly parallelizable.
- A task lists its dependencies (`deps:`). Do not start a task until its
  dependencies are `[x]`.
- Each task names the exact SPEC/PRD sections and existing packages to read
  (`Context:`). Read only those — not the whole codebase.
- On completion, flip the task checkbox to `[x]` and ensure the Definition of
  Done (§5) holds.

## 3. Test-driven development (mandatory)

All production code in this project is built test-first. Every task follows the
red-green-refactor loop:

1. **Red** — write the failing test(s) that capture the task's required
   behaviour. The `Tests first:` line in each task states what to cover.
2. **Green** — write the minimum implementation that makes those tests pass.
3. **Refactor** — clean up names, structure, and duplication with tests green.

Guidance on test layers (see SPEC §24):

- **Strict test-first, pure unit tests** — config validation, monitor/HTTP
  validation, HTTP result classification, the state machine, notification
  provider `Validate`, retry/backoff decisions, message templating, IPC error
  mapping. These are deterministic logic; write the table-driven test before any
  implementation.
- **Test-first at the integration boundary** — SQLite repositories, migrations,
  TSDB read/write, IPC handler ↔ client round-trips, the check pipeline. Use
  `t.TempDir()` for storage and `net/http/httptest` for HTTP; no real network.
- **Test-alongside** — Bubble Tea screens. Test `Update` logic and message
  handling (and `View` via golden output where practical); a fake IPC client
  stands in for the service. `teatest` may be used for fuller flows.

Test tooling: standard `testing`, `net/http/httptest`, `t.TempDir()`, a fake IPC
client, a fake notification provider, and an in-process SMTP server for the email
provider. Tests must be hermetic and runnable via `go test ./...`.

## 4. Task sizing and context budget

Each task is deliberately scoped so the implementing agent only needs to load the
context named in its `Context:` line — a handful of SPEC sections plus a few
named packages — to do the work. That context, plus the agent's own reasoning,
tool output, new tests, and new implementation, must fit comfortably within a
**200k-token context window**.

If, while working, a task's required context plus its implementation will not fit
that budget, stop and split the task into smaller tasks in this plan rather than
proceeding with partial context.

## 5. Definition of Done (every task)

A task is `[x]` only when all of the following hold:

- Tests were written first, fail without the change, and pass with it.
- `go test ./...`, `go vet ./...`, and `gofmt -l` (no diff) are clean.
- `go build ./...` succeeds.
- Architectural rules hold (SPEC §5): `cmd/` stays thin; raw SQL lives only in
  `internal/store` (and migration files); `internal/tui` does not import
  `internal/store/*`.
- Secrets are never logged and never returned in API responses by default
  (SPEC §18.9, §23).
- The task checkbox is flipped to `[x]`; any SPEC divergence is noted for a SPEC
  update.

## 6. Decisions adopted by this plan

These resolve the open questions from SPEC §27 and PRD §21, plus a few additional
technical clarifications. All are now reflected in the SPEC (v0.2 — see SPEC §27
and the sections it references) and are restated here as a quick reference for
implementers. PRD §21 still lists the overlapping product-level questions;
reconcile them when the PRD is next revised.

1. **IDs** — ULID (`github.com/oklog/ulid/v2`); string, lexically sortable. (SPEC Q1)
2. **Monitor deletion** — soft-delete in SQLite; TSDB samples expire via
   retention. (SPEC Q2)
3. **Aggregated history** — computed on demand from TSDB for MVP ranges; no
   separate aggregation store. (SPEC Q3)
4. **Notification retry state** — `notification_attempts` table plus an in-memory
   job queue; no separate job table. (SPEC Q4)
5. **Per-monitor notifications** — global targets plus a per-monitor
   `notifications_enabled` on/off and per-target `enabled`; no per-monitor target
   selection in MVP. (SPEC Q5 / PRD Q5)
6. **Container IPC** — Unix socket only for MVP; no TCP listener. (SPEC Q6 / PRD Q4)
7. **Secret** — loaded from config/env only; not persisted or hashed in SQLite for
   MVP. (SPEC Q7 / PRD Q3)
8. **Task runner** — `Makefile`. (SPEC Q8)
9. **Retention defaults** — 30d raw samples, 365d aggregated, as in the SPEC. (PRD Q1)
10. **TUI history** — text/glyph indicators only for MVP; no terminal charts. (PRD Q2)
11. **Human durations** — values like `30d` use a custom mapstructure decode hook,
    since Go's `time.ParseDuration` rejects the `d` suffix.
12. **Logging package** — `internal/logging` hosts `log/slog` setup; it is part of
    the SPEC §5 repository layout.
13. **Migration runtime** — the `atlas` CLI is a dev-time tool only (`migrate diff`,
    `migrate lint`); the service applies embedded migration files at startup with
    no runtime dependency on the atlas binary.
14. Post-MVP provider prioritisation (PRD Q6) is out of scope for this plan.

## 7. Milestones

| ID | Milestone | SPEC §26 | Depends on |
|----|-----------|----------|------------|
| M0 | Scaffold cleanup & developer tooling | M1 | — |
| M1 | Configuration | M1 | M0 |
| M2 | Logging & storage foundations | M2 | M1 |
| M3 | Service lifecycle & IPC status | M2 | M2 |
| M4 | Domain model & SQLite repositories | M3 | M3 |
| M5 | Monitor management over IPC | M3 | M4 |
| M6 | TUI foundation & monitor screens | M3 | M5 |
| M7 | Probe execution, scheduler & state machine | M4 | M5 (M6 for M7.8) |
| M8 | History & retention | M5 | M7 |
| M9 | Notifications | M6 | M7, M6 |
| M10 | Packaging, deployment & end-to-end | M7 | M9 |

Current repository state (already present): Go module, the `cobra-cli`
service/tui scaffold (`cmd/*.go`, `main.go`), `LICENSE` (MIT), `.github/FUNDING.yml`,
and `docs/`. M0 cleans up that scaffold rather than creating it from scratch.

---

## M0 — Scaffold cleanup & developer tooling

Goal: the existing scaffold is production-shaped, the repo builds cleanly, and dev
tooling is in place.

- [x] **M0.1 — Clean up the Cobra scaffold** — *deps: —*
  Replace placeholder `Short`/`Long` text in `cmd/root.go`, `cmd/service.go`,
  `cmd/tui.go` with real descriptions; remove the generated `--toggle` flag and
  the `$HOME/.uptimemonitor.yaml` search in `initConfig` (config wiring lands in
  M1). Keep commands compiling and `--help` accurate; commands stay thin.
  *Tests first:* smoke test asserting `uptimemonitor --help` lists `service` and
  `tui` and that both subcommands run.
  *Context:* SPEC §6, §7; `cmd/*.go`, `main.go`.

- [x] **M0.2 — Version package & `--version`** — *deps: M0.1*
  Add `internal/version/version.go` (`Version`, `Commit`, `Date` vars defaulting
  to `"dev"`, plus a `String()` helper); wire a `--version` output into the root
  command, populated via `-ldflags -X`.
  *Tests first:* unit test for `version.String()`; smoke test for `uptimemonitor
  --version`.
  *Context:* SPEC §5 (version package), §7.1.

- [x] **M0.3 — Build & quality tooling** — *deps: M0.2*
  Add a `Makefile` with `build`, `test`, `vet`, `fmt`, `lint`, `tidy`,
  `migrate-new`, `migrate-lint`, `migrate-apply`, and `ko-build` targets (SPEC
  §13.3, §25); `build` injects version ldflags. Add `.gitignore`, `.golangci.yml`,
  `.editorconfig`.
  *Tests first:* n/a (tooling) — verify `make build`, `make test`, `make vet` run
  clean.
  *Context:* SPEC §13.3, §25; §6 (decisions).

- [x] **M0.4 — README & contributor docs** — *deps: M0.3*
  Write `README.md` (summary, build/run, links to PRD/SPEC/PLAN); confirm
  `LICENSE` is MIT; add a short `CONTRIBUTING.md`.
  *Tests first:* n/a — verify links resolve.
  *Context:* PRD §1–2, §7; SPEC §5.

- [x] **M0.5 — CI pipeline** — *deps: M0.3*
  Add `.github/workflows/ci.yml` running `go build ./...`, `go vet ./...`,
  `go test ./...`, and a `gofmt` check. (An `atlas migrate lint` step is added in
  M2 once migrations exist.)
  *Tests first:* n/a — verify the workflow passes on the current tree.
  *Context:* SPEC §25.

- [x] **M0.6 — M0 exit check** — *deps: M0.1–M0.5*
  Verify: repo builds; `--help`/`--version` correct; `make test`/`make vet`
  green; CI green. Mark all M0 tasks complete.

## M1 — Configuration

Goal: typed configuration loaded and validated from file, env, and flags.

- [x] **M1.1 — Config structs & duration decoding** — *deps: M0.2*
  Define `Config`, `ServiceConfig`, `RetentionConfig`, `NotificationConfig` in
  `internal/config/config.go` (SPEC §8.2). Add a mapstructure decode hook that
  accepts human durations including the `d`/`w` suffixes used by
  `raw_samples`/`aggregated_history`.
  *Tests first:* duration-hook table tests (`60s`, `10s`, `30d`, `365d`,
  invalid input); struct decode from a YAML fixture.
  *Context:* SPEC §8.1–8.2; §6 decision 11.

- [x] **M1.2 — Config loading (file, env, flags, defaults)** — *deps: M1.1*
  Implement `Load(...)` in `internal/config`: Viper with the `UPTIMEMONITOR_` env
  prefix, all defaults (SPEC §8.3), config-file discovery
  (`/etc/uptimemonitor/config.yaml`), and binding for `--config`, `--log-level`,
  `--socket-path`, `--data-dir`.
  *Tests first:* defaults applied with no file; env overrides file; flag overrides
  env; explicit `--config` honoured.
  *Context:* SPEC §8.1, §8.3, §8.4; §7.1–7.2.

- [x] **M1.3 — Config validation** — *deps: M1.1*
  Implement every SPEC §8.5 failure rule in `internal/config/validate.go`;
  return field-aware errors.
  *Tests first:* table-driven, one case per rule plus a valid baseline.
  *Context:* SPEC §8.5; `internal/config/config.go`.

- [x] **M1.4 — Wire config into commands** — *deps: M1.2, M1.3*
  Load and validate config in the root command's `PersistentPreRunE`, expose it
  to `service`/`tui`; remove the leftover scaffold `initConfig`.
  *Tests first:* command smoke tests — invalid config → non-zero exit with a
  readable error; valid config → command proceeds.
  *Context:* SPEC §7; `internal/config`.

- [x] **M1.5 — M1 exit check** — *deps: M1.1–M1.4*
  Verify config loads from file/env/flags with correct precedence and validation
  fails fast. Mark all M1 tasks complete.

## M2 — Logging & storage foundations

Goal: logging and both storage engines open, migrate, and close cleanly.

- [x] **M2.1 — Structured logging** — *deps: M1.1*
  Add `internal/logging/logging.go`: build a `*slog.Logger` from the configured
  log level, with a component helper and secret-redaction guidance (SPEC §23).
  *Tests first:* level parsing, handler selection, `With("component", …)` helper.
  *Context:* SPEC §23; §6 decision 12.

- [x] **M2.2 — SQLite store: open & pragmas** — *deps: M1.1*
  Add `internal/store/sqlite/store.go` (open via `modernc.org/sqlite`, apply
  pragmas from SPEC §12.4, expose the handle, `Close()`) and
  `internal/store/sqlite/schema.sql` containing the SPEC §12.3 schema as the
  declarative source for Atlas.
  *Tests first:* open a temp DB; assert `foreign_keys` on and `journal_mode=wal`;
  close cleanly.
  *Context:* SPEC §12.1–12.4.

- [x] **M2.3 — Atlas migrations & startup applier** — *deps: M2.2*
  Generate the initial versioned migration into
  `internal/store/sqlite/migrations/` from `schema.sql` (`atlas migrate diff`);
  embed migrations with `embed.FS`; implement a `Migrate` function applied at
  startup that runs pending migrations, is idempotent, and fails fast on error
  (no runtime dependency on the atlas binary). Add `atlas.hcl` and an
  `atlas migrate lint` CI step.
  *Tests first:* apply to an empty temp DB → all tables exist; re-run is a no-op;
  a broken migration → error returned.
  *Context:* SPEC §12.3, §13; §6 decision 13.

- [x] **M2.4 — Prometheus TSDB store: open & close** — *deps: M1.1*
  Add `internal/store/tsdb/store.go`: open the Prometheus `tsdb.DB` at
  `tsdb_path` with retention from config, expose appender/querier accessors and
  `Close()`.
  *Tests first:* open a temp TSDB; append one sample; close and reopen.
  *Context:* SPEC §14.1–14.4.

- [x] **M2.5 — M2 exit check** — *deps: M2.1–M2.4*
  Verify logging, SQLite (with migrations), and TSDB all open and close against
  temp directories. Mark all M2 tasks complete.

## M3 — Service lifecycle & IPC status

Goal: the service starts, exposes `/v1/status` over a Unix socket, shuts down
gracefully, and the TUI command can connect.

- [x] **M3.1 — IPC contract types & errors** — *deps: M2.1*
  Add `internal/ipc/types.go` (request/response DTOs — at minimum the status
  response) and `internal/ipc/errors.go` (error codes from SPEC §10.3, JSON
  envelope encode/decode helpers).
  *Tests first:* error envelope round-trips; error-code constants are stable.
  *Context:* SPEC §10.1–10.4.

- [x] **M3.2 — IPC server over Unix socket** — *deps: M3.1*
  Add `internal/ipc/server.go` and `routes.go`: an HTTP server bound to a Unix
  socket, `/v1` route prefix, JSON middleware, socket mode `0660`, stale-socket
  removal on start, and socket removal on stop.
  *Tests first:* server starts on a temp socket; handles a pre-existing stale
  socket; unknown route → `not_found` envelope; shutdown removes the socket.
  *Context:* SPEC §10.1, §10.3–10.4, §20.3, §9.3.

- [x] **M3.3 — IPC client** — *deps: M3.1*
  Add `internal/ipc/client.go`: an HTTP client dialing the Unix socket, a typed
  request helper, error-envelope decoding into Go errors, and a friendly
  connection-failure error when the socket is missing.
  *Tests first:* against an httptest server on a temp socket — success decode,
  error envelope → typed error, missing socket → readable error.
  *Context:* SPEC §10; §8.5 (connection errors).

- [x] **M3.4 — `/v1/status` endpoint** — *deps: M3.2, M3.3*
  Add `internal/ipc/handlers.go` with a status handler returning the SPEC §10.5
  status shape, backed by a `StatusProvider` interface (version, uptime, storage
  health, scheduler, monitor counts). Add the client `Status()` method.
  *Tests first:* handler returns the expected JSON; client `Status()` decodes it.
  *Context:* SPEC §10.5 (service status).

- [x] **M3.5 — systemd readiness & watchdog** — *deps: M2.1*
  Add `internal/systemd/notify.go`: `sd_notify` `READY=1`, a `WATCHDOG=1` pinger,
  and no-op behaviour when not run under systemd.
  *Tests first:* notify no-ops when `NOTIFY_SOCKET` is unset; writes the correct
  payload to a fake socket when set.
  *Context:* SPEC §21.2–21.3.

- [x] **M3.6 — Service application lifecycle** — *deps: M3.4, M3.5*
  Add `internal/app/service.go`: the full startup sequence (SPEC §9.1) —
  config → logging → directories → SQLite → migrate → TSDB → stores → IPC server →
  systemd ready → block on signal → graceful shutdown (SPEC §9.3). Wire
  `cmd/service.go` to call it.
  *Tests first:* integration — start with temp dirs, `/v1/status` reachable over
  the socket, SIGTERM → clean exit with the socket removed.
  *Context:* SPEC §9.1, §9.3, §7.2.

- [x] **M3.7 — TUI service-connection bootstrap** — *deps: M3.3, M3.6*
  Add `internal/app/tui.go`: load config, build the IPC client, fetch status, and
  surface a readable error if the service is down. Wire `cmd/tui.go` (no Bubble
  Tea yet — connect and print status; replaced in M6).
  *Tests first:* integration — service up → prints status; service down →
  friendly error and non-zero exit.
  *Context:* SPEC §7.3, §9.2.

- [x] **M3.8 — M3 exit check** — *deps: M3.1–M3.7*
  Verify (SPEC §28 partial): service starts with valid config, opens SQLite and
  TSDB, applies migrations, serves `/v1/status` over the socket, and the TUI
  command connects. Mark all M3 tasks complete.

## M4 — Domain model & SQLite repositories

Goal: domain types and all monitor-side SQLite repositories, fully tested.

- [x] **M4.1 — Domain types & ID generation** — *deps: M3.1*
  Add `internal/monitor/model.go`: `Monitor`, `MonitorType`, `MonitorState`,
  `CheckResult`, `Incident`, `Event` (SPEC §11) with state constants; add a ULID
  ID helper.
  *Tests first:* state constant values; the ID generator produces unique,
  lexically sortable IDs.
  *Context:* SPEC §11; §6 decision 1.

- [x] **M4.2 — Monitor & HTTP-config validation** — *deps: M4.1*
  Add `internal/monitor/validate.go`: monitor field validation plus
  `HTTPMonitorConfig` validation (SPEC §11.2 rules).
  *Tests first:* table-driven — absolute URL, `http`/`https` scheme, method `GET`,
  valid status range, positive timeout/interval; valid baseline.
  *Context:* SPEC §11.1–11.2.

- [x] **M4.3 — Monitor repository (SQLite)** — *deps: M2.3, M4.1*
  Implement monitor CRUD in `internal/store/sqlite`: insert, get, list (filters:
  `state`, `enabled`), update, soft-delete.
  *Tests first:* integration on a temp migrated DB — round-trip, list filters,
  soft-delete hidden from the default list.
  *Context:* SPEC §12.1–12.3; §6 decision 2.

- [x] **M4.4 — Monitor-state & check-result repositories** — *deps: M4.3*
  Implement `monitor_states` upsert/get and `check_results` insert / recent-list
  by monitor / prune-older-than.
  *Tests first:* integration — state upsert, recent ordering by `started_at
  DESC`, prune.
  *Context:* SPEC §12.3, §12.5.

- [x] **M4.5 — Event & incident repositories** — *deps: M4.3*
  Implement `events` insert/list (global and by monitor) and `incidents`
  open/resolve/list plus find-open-by-monitor.
  *Tests first:* integration — event ordering, incident open→resolve lifecycle,
  open-incident lookup.
  *Context:* SPEC §11.5–11.6, §12.3.

- [x] **M4.6 — Settings repository** — *deps: M4.3*
  Implement `settings` key/value JSON get/set (used for global toggles such as
  notifications-enabled).
  *Tests first:* integration — set/get/overwrite.
  *Context:* SPEC §12.3.

- [x] **M4.7 — M4 exit check** — *deps: M4.1–M4.6*
  Verify domain types and all repositories pass integration tests on a temp
  migrated DB. Mark all M4 tasks complete.

## M5 — Monitor management over IPC

Goal: full monitor CRUD plus incident/event reads over IPC.

- [x] **M5.1 — Monitor service layer** — *deps: M4.2, M4.5*
  Add `internal/monitor/service.go`: create/get/list/update/delete/enable/disable
  orchestrating the repositories; validates input; writes events
  (`monitor_created`, etc.); initialises `monitor_states` to `unknown`/`paused`;
  exposes a nil-able `OnChange` observer hook so the scheduler can subscribe in M7
  without M5 depending on M7.
  *Tests first:* integration — create persists monitor + state + event; update
  re-validates; delete soft-deletes and emits an event; enable/disable flips state
  and emits an event.
  *Context:* SPEC §11, §17 (paused semantics), §11.6 (events).

- [x] **M5.2 — Monitor IPC endpoints** — *deps: M3.2, M5.1*
  Add monitor handlers and routes: `GET`/`POST /v1/monitors`,
  `GET`/`PATCH`/`DELETE /v1/monitors/{id}` (SPEC §10.5). `PATCH` is
  partial-then-validate. Map domain/validation errors to error codes.
  *Tests first:* handler tests — create happy path, `validation_error` shape,
  `not_found`, partial update.
  *Context:* SPEC §10.5 (monitors), §10.3.

- [x] **M5.3 — IPC client monitor methods** — *deps: M3.3, M5.2*
  Add `ListMonitors`, `CreateMonitor`, `GetMonitor`, `UpdateMonitor`,
  `DeleteMonitor` to the IPC client.
  *Tests first:* against a test server — each method round-trips; error envelope →
  typed error.
  *Context:* SPEC §10.5 (monitors).

- [x] **M5.4 — Incident & event IPC endpoints + client** — *deps: M5.2, M5.3*
  Add `GET /v1/incidents`, `GET /v1/monitors/{id}/incidents`, `GET /v1/events`,
  `GET /v1/monitors/{id}/events` with matching client methods.
  *Tests first:* handler and client tests for each route.
  *Context:* SPEC §10.5 (incidents, events).

- [x] **M5.5 — M5 exit check** — *deps: M5.1–M5.4*
  Verify an integration test covering create → list → get → update → delete over
  IPC plus incident/event reads. Mark all M5 tasks complete.

## M6 — TUI foundation & monitor screens

Goal: a working Bubble Tea TUI that can view and manage monitors over IPC. Add
the Bubble Tea / Bubbles / Lipgloss dependencies in M6.1.

- [x] **M6.1 — Bubble Tea application shell** — *deps: M5.3*
  Add `internal/tui/app.go`, `model.go`, `update.go`, `view.go`, `keys.go`: a root
  model with a screen router/stack, a global keymap, the async IPC `tea.Cmd`
  pattern (SPEC §19.1–19.3), a status bar, and routing of errors to an error
  dialog. Launch it from `internal/app/tui.go`.
  *Tests first:* `Update` transitions between screens on keys; an IPC `tea.Cmd`
  produces loaded/error messages using a fake client.
  *Context:* SPEC §19.1–19.3.

- [x] **M6.2 — Service status screen** — *deps: M6.1*
  Add a status screen under `internal/tui/screens/` rendering the SPEC §12.5
  fields from `/v1/status`.
  *Tests first:* `Update` stores fetched status; `View` renders key fields.
  *Context:* SPEC §12.5; §10.5 (status).

- [x] **M6.3 — Monitor list screen** — *deps: M6.1*
  Add a monitor list screen with the SPEC §12.1 columns, refresh, selection, and
  navigation to detail/form. (The manual-check key is wired in M7.8.)
  *Tests first:* list populates from a fake client; selection moves; navigation
  messages are emitted.
  *Context:* SPEC §12.1, §19.

- [x] **M6.4 — Monitor detail screen** — *deps: M6.3, M5.4*
  Add a monitor detail screen per SPEC §12.2: config, current state, recent
  checks, incidents, events (history is a placeholder until M8; notification
  summary a placeholder until M9).
  *Tests first:* detail loads monitor + incidents + events via a fake client and
  renders state.
  *Context:* SPEC §12.2.

- [x] **M6.5 — Monitor form screen** — *deps: M6.3*
  Add a create/edit form for HTTP monitors with all SPEC §11.4 fields; map server
  `validation_error.field` to the matching form field.
  *Tests first:* field navigation; client-side required checks; server
  `validation_error` mapped to the correct field.
  *Context:* SPEC §12.3, §11.4, §11.2.

- [x] **M6.6 — Confirmation & error dialogs** — *deps: M6.4, M6.5*
  Add a reusable confirmation dialog (shows the affected object name) and an
  error dialog (SPEC §12.6, §19.4); route delete-monitor through confirmation.
  *Tests first:* confirm → action message, cancel → dismiss; error dialog renders
  the message.
  *Context:* SPEC §12.6, §19.4.

- [x] **M6.7 — M6 exit check** — *deps: M6.1–M6.6*
  Verify (SPEC §28): a user can create, view, edit, and delete an HTTP monitor in
  the TUI with destructive actions confirmed. Mark all M6 tasks complete.

## M7 — Probe execution, scheduler & state machine

Goal: monitors are checked on schedule, state transitions and incidents are
recorded, and manual checks work.

- [x] **M7.1 — Probe result & runner interface** — *deps: M4.1*
  Add `internal/probe/result.go` (`Result`) and the `Runner` interface (SPEC
  §15.1).
  *Tests first:* `Result` zero-value and round-trip.
  *Context:* SPEC §15.1.

- [x] **M7.2 — HTTP probe runner** — *deps: M7.1, M4.2*
  Add `internal/probe/http.go`: `GET` with per-monitor timeout, status capture,
  total-duration measurement, success classification by expected range, and
  sanitised transport errors (SPEC §15.2–15.4).
  *Tests first:* `httptest` — in-range status → success; out-of-range → failure;
  timeout → failure; bad host → sanitised error.
  *Context:* SPEC §15.2–15.4.

- [x] **M7.3 — Probe dispatcher** — *deps: M7.2*
  Add `internal/probe/runner.go`: select a runner by `MonitorType`, decode the
  monitor config, execute, and build a `CheckResult`.
  *Tests first:* dispatch to the HTTP runner; unknown type → error.
  *Context:* SPEC §15.

- [x] **M7.4 — Monitor state machine** — *deps: M4.1*
  Add `internal/monitor/state.go`: a pure transition function (SPEC §17.2) plus a
  side-effect descriptor (emit event? open/resolve incident? queue down/recovery
  notification?) per SPEC §17.3.
  *Tests first:* exhaustive table covering every transition, including
  `unknown→up`/`unknown→down` and pause.
  *Context:* SPEC §17.

- [x] **M7.5 — Scheduler & worker pool** — *deps: M5.1*
  Add `internal/scheduler/scheduler.go` and `worker.go`: per-monitor interval
  scheduling, a bounded worker pool (`check_workers`), the no-overlap rule (skip),
  a manual-trigger queue, and dynamic add/update/remove/enable/disable (SPEC §16).
  *Tests first:* fires on interval; respects the worker bound; no overlapping runs
  per monitor; manual trigger runs; schedule updates apply.
  *Context:* SPEC §16.

- [x] **M7.6 — Check pipeline integration** — *deps: M7.3, M7.4, M7.5, M4.4, M4.5*
  Wire scheduler → dispatcher → persistence: store `check_results`, update
  `monitor_states` (consecutive counters), apply the state machine, write events,
  and open/resolve incidents (SPEC §17.3). Register the scheduler as the monitor
  service's `OnChange` observer (M5.1) and start it in `internal/app/service.go`.
  *Tests first:* integration — success → state `up`; failure → state `down` +
  incident + event; recovery → incident resolved + event.
  *Context:* SPEC §17.3, §16, §11.5–11.6.

- [x] **M7.7 — Manual-check & recent-checks IPC** — *deps: M7.6, M5.3*
  Add `POST /v1/monitors/{id}/run` (queued response) and
  `GET /v1/monitors/{id}/checks?limit=` (SPEC §10.5) with client methods; allow
  manual checks for disabled monitors without unpausing them (SPEC §16.4).
  *Tests first:* handler and client tests; manual check on a disabled monitor
  does not change `paused` state.
  *Context:* SPEC §10.5, §16.4.

- [x] **M7.8 — TUI live state & manual checks** — *deps: M7.7, M6.4*
  Show live state and recent checks in the monitor list/detail screens; add a
  manual-check key that calls `/run` and refreshes.
  *Tests first:* `Update` handles run → refresh; recent checks render.
  *Context:* SPEC §12.1–12.2.

- [x] **M7.9 — M7 exit check** — *deps: M7.1–M7.8*
  Verify (SPEC §28): the scheduler checks monitors repeatedly, results are stored,
  transitions are recorded, and incidents open and resolve. Mark all M7 tasks
  complete.

## M8 — History & retention

Goal: time-series samples are written, history is queryable, retention runs, and
the TUI shows a heartbeat-style history view.

- [x] **M8.1 — TSDB sample writes** — *deps: M7.6, M2.4*
  Add `internal/store/tsdb/series.go`: write `uptimemonitor_probe_success`,
  `uptimemonitor_probe_duration_seconds`, and `uptimemonitor_probe_http_status_code`
  with `monitor_id`/`monitor_type` labels (SPEC §14.2–14.3; omit the status sample
  when there is none). Wire it into the check pipeline (M7.6).
  *Tests first:* append per check and query raw samples back; a failed check omits
  the status series.
  *Context:* SPEC §14.2–14.3.

- [x] **M8.2 — TSDB history queries & aggregation** — *deps: M8.1*
  Add `internal/store/tsdb/query.go`: a range query bucketed to the resolution
  for each range (SPEC §14.5), producing history points (`state`,
  `success_ratio`, `avg_duration_ms`).
  *Tests first:* seed samples, query each range, assert bucket count, resolution,
  and aggregates.
  *Context:* SPEC §14.5, §10.5 (history); §6 decision 3.

- [x] **M8.3 — History IPC endpoint + client** — *deps: M8.2, M5.3*
  Add `GET /v1/monitors/{id}/history?range=&resolution=` (SPEC §10.5) validating
  the range against the supported set, with a client method.
  *Tests first:* handler maps ranges → resolutions; invalid range →
  `validation_error`; client decodes the response.
  *Context:* SPEC §10.5 (history), §14.5.

- [x] **M8.4 — Retention cleanup** — *deps: M8.1, M4.4*
  Add startup and periodic (1h) cleanup: TSDB raw-sample retention and SQLite
  `check_results` pruning at 30 days (SPEC §12.5, §14.4, §14.6); hook it into
  `internal/app/service.go`.
  *Tests first:* samples/rows past retention are removed; recent data is kept.
  *Context:* SPEC §14.4, §14.6, §12.5.

- [x] **M8.5 — TUI history visualization** — *deps: M8.3, M6.4*
  Add a heartbeat-style history indicator and a range selector to the monitor
  detail screen (SPEC §13, §19.5 glyphs).
  *Tests first:* `Update` loads history per range; `View` renders a glyph row for
  up/down/unknown/paused.
  *Context:* SPEC §13, §19.5; §6 decision 10.

- [x] **M8.6 — M8 exit check** — *deps: M8.1–M8.5*
  Verify samples are written per check, history is queryable across all MVP
  ranges, retention runs, and the TUI shows history. Mark all M8 tasks complete.

## M9 — Notifications

Goal: the notification framework, all seven MVP providers, the delivery pipeline,
and TUI configuration. Tasks M9.5–M9.8 are independent and parallelizable once
M9.1–M9.4 are done.

- [x] **M9.1 — Provider interface, fields & message model** — *deps: M3.1*
  Add `internal/notify/provider.go` (`Provider`, `Field`, `FieldType`) and
  `internal/notify/message.go` (`Message`, MVP event types) per SPEC §18.1–18.2,
  §18.4.
  *Tests first:* field-type constants; message construction helpers.
  *Context:* SPEC §18.1–18.2, §18.4.

- [x] **M9.2 — Registry & provider-metadata endpoint** — *deps: M9.1, M3.2*
  Add `internal/notify/registry.go` and `GET /v1/notifications/providers` (SPEC
  §18.3, §10.5) with a client method.
  *Tests first:* register/lookup; unknown kind → error; the endpoint returns field
  metadata.
  *Context:* SPEC §18.3, §10.5 (providers).

- [x] **M9.3 — Message templating** — *deps: M9.1*
  Add `internal/notify/template.go`: render title/body for `monitor_down`,
  `monitor_recovered`, and `manual_test` (SPEC §18.2) with no secrets in output.
  *Tests first:* each event type renders the expected text.
  *Context:* SPEC §18.2.

- [x] **M9.4 — Notification repositories (SQLite)** — *deps: M2.3, M4.1*
  Implement `notification_targets` CRUD with soft-delete and `notification_attempts`
  insert / list-by-target (SPEC §12.3); never return secret fields by default;
  preserve a stored secret when an update leaves the field blank (SPEC §18.9).
  *Tests first:* integration — target CRUD, attempt insert/list, blank-secret
  update preserves the stored secret.
  *Context:* SPEC §12.3, §18.9.

- [x] **M9.5 — Fake + HTTP-webhook-family providers** — *deps: M9.1*
  Add `internal/notify/providers/`: `fake` (records sends, for tests), `webhook`,
  `discord`, and `slack` — JSON-over-HTTP `POST` (SPEC §18.5).
  *Tests first:* each `Validate` rejects missing required fields; each `Send` posts
  the expected payload (`httptest`).
  *Context:* SPEC §18.5 (webhook, discord, slack), §18.1.

- [x] **M9.6 — ntfy & Gotify providers** — *deps: M9.1*
  Add `providers/ntfy` and `providers/gotify` (SPEC §18.5).
  *Tests first:* `Validate` and `Send` via `httptest`, including token/auth-header
  handling.
  *Context:* SPEC §18.5 (ntfy, gotify).

- [x] **M9.7 — Telegram provider** — *deps: M9.1*
  Add `providers/telegram` (bot `sendMessage`, SPEC §18.5).
  *Tests first:* `Validate` and `Send` via `httptest`.
  *Context:* SPEC §18.5 (telegram).

- [x] **M9.8 — Email/SMTP provider** — *deps: M9.1*
  Add `providers/email`: SMTP send with STARTTLS and auth (SPEC §18.5).
  *Tests first:* `Validate`; `Send` against an in-process SMTP test server.
  *Context:* SPEC §18.5 (email).

- [x] **M9.9 — Delivery pipeline, retry & spam control** — *deps: M9.2, M9.3, M9.4*
  Add `internal/notify/delivery.go`: an in-memory job queue with workers, loading
  of enabled targets, send via provider, attempt recording, bounded exponential
  backoff (SPEC §18.7), one-down/one-recovery per incident (SPEC §18.8), no retry
  for manual tests, and secret redaction in logs (SPEC §18.9, §23).
  *Tests first:* retries to `max_attempts` then fails; success stops retrying; the
  spam rule prevents repeat down notifications; manual tests are not retried.
  *Context:* SPEC §18.6–18.9; §6 decision 4.

- [x] **M9.10 — Notification IPC endpoints + client** — *deps: M9.9, M5.3*
  Add notification-target CRUD, `POST /v1/notifications/targets/{id}/test`, and
  `GET /v1/notifications/attempts` (SPEC §10.5) with client methods; secrets are
  never returned.
  *Tests first:* handler and client tests; the test endpoint invokes delivery with
  the fake provider.
  *Context:* SPEC §10.5 (notifications), §18.9.

- [x] **M9.11 — Wire notifications into state transitions** — *deps: M9.9, M7.6*
  In the check pipeline, queue `monitor_down` on incident open and
  `monitor_recovered` on resolve, respecting the global toggle (settings), the
  per-monitor `notifications_enabled` flag, and per-target `enabled`.
  *Tests first:* integration with the fake provider — down → attempt, recovery →
  attempt; disabled monitor or global toggle off → no attempt.
  *Context:* SPEC §17.3, §18.6, §18.8; PRD §11.7; §6 decision 5.

- [x] **M9.12 — TUI notification screens** — *deps: M9.10, M6.6*
  Add a notification-target list, a provider-driven target form (fields from
  `/v1/notifications/providers`, secrets shown as set/unset), an attempts list, a
  global enable toggle, delete-with-confirm, and send-test (SPEC §12.4, §18.9).
  *Tests first:* the form renders provider fields; secrets show as set/unset;
  test-send and delete flows work.
  *Context:* SPEC §12.4, §18.4, §18.9.

- [x] **M9.13 — M9 exit check** — *deps: M9.1–M9.12*
  Verify (SPEC §28): a user can configure MVP providers in the TUI, send test
  notifications, and receive down/recovery notifications once per incident
  lifecycle. Mark all M9 tasks complete.

## M10 — Packaging, deployment & end-to-end

Goal: the project is installable, runnable under systemd, containerizable with ko,
and verified end to end.

- [ ] **M10.1 — systemd unit** — *deps: M3.6*
  Add `deployments/systemd/uptimemonitor.service` (SPEC §21.1) with the hardening
  directives; confirm `Type=notify` matches M3.5.
  *Tests first:* n/a — manual-verify note that the service starts under systemd.
  *Context:* SPEC §21.

- [ ] **M10.2 — ko container build** — *deps: M3.6*
  Add ko configuration (`.ko.yaml`), `deployments/ko/README.md`, and a
  `make ko-build` target; the image entrypoint is `uptimemonitor service` (SPEC
  §22). Document the container Unix-socket caveat.
  *Tests first:* n/a — verify `ko build --local` produces an image that starts the
  service.
  *Context:* SPEC §22; §6 decision 6.

- [ ] **M10.3 — Example config & config docs** — *deps: M1.2*
  Add an example `config.yaml` (SPEC §8.1) and document environment variables and
  default paths.
  *Tests first:* n/a — verify the example loads and validates.
  *Context:* SPEC §8.

- [ ] **M10.4 — README completion & install/run docs** — *deps: M10.1, M10.2, M10.3*
  Complete `README.md`: install, systemd setup, TUI usage, container usage, and a
  config reference.
  *Tests first:* n/a — verify documented commands.
  *Context:* PRD §7, §19; SPEC §21–22.

- [ ] **M10.5 — End-to-end smoke test** — *deps: M9.11*
  Implement SPEC §24.4: start the service with temp directories, create a monitor
  over IPC, run a local test HTTP server, trigger a manual check → `up`, stop the
  server, trigger a check → `down`, and assert a notification attempt via the fake
  provider. Make it runnable in CI.
  *Tests first:* the smoke test itself is the test.
  *Context:* SPEC §24.4.

- [ ] **M10.6 — Acceptance verification** — *deps: M10.1–M10.5*
  Walk the full SPEC §28 and PRD §19 checklists, fix any gaps, and confirm all
  `make` quality gates and CI are green. Mark all M10 tasks complete.

---

## 8. SPEC §28 acceptance criteria → milestone mapping

| Acceptance criterion | Milestone |
|----------------------|-----------|
| `service` starts with valid config | M3 |
| Creates/opens SQLite and TSDB storage | M2, M3 |
| Applies migrations | M2, M3 |
| Exposes `/v1/status` over the Unix socket | M3 |
| `tui` connects to the service | M3, M6 |
| Create an HTTP monitor in the TUI | M6 |
| Scheduler checks the monitor repeatedly | M7 |
| Results stored in SQLite and TSDB | M7, M8 |
| State transitions recorded | M7 |
| Incidents opened and resolved | M7 |
| Configure MVP notification providers in the TUI | M9 |
| Send test notifications | M9 |
| Down/recovery notifications once per incident lifecycle | M9 |
| Destructive TUI actions require confirmation | M6 |
| Service runs under systemd | M10 |
| Container image builds with ko | M10 |

## 9. Revision history

```text
0.1 - Initial implementation plan derived from PRD v0.2 and SPEC v0.2.
```
