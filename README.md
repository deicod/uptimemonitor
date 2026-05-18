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

> Status: early development. See [`docs/PLAN.md`](docs/PLAN.md) for current
> progress.

## Building

Requires Go (see [`go.mod`](go.mod) for the minimum version).

```sh
make build      # compile ./bin/uptimemonitor with version metadata
make test       # run the test suite
make vet        # run go vet
make lint       # run golangci-lint
```

`go build ./...` also works for a plain build without version ldflags.

## Running

```sh
uptimemonitor service      # start the background service
uptimemonitor tui          # launch the terminal UI client
uptimemonitor --version    # print version, commit, and build date
uptimemonitor --help       # list commands and flags
```

The TUI connects to a running service over its local Unix socket.

## Documentation

- [Product Requirements (PRD)](docs/PRD.md) — what the product is and why.
- [Specification (SPEC)](docs/SPEC.md) — technical design and contracts.
- [Implementation Plan (PLAN)](docs/PLAN.md) — milestone-based task list.
- [Contributing](CONTRIBUTING.md) — how to work on the project.

## License

MIT — see [`LICENSE`](LICENSE).
