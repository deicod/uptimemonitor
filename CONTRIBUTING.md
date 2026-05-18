# Contributing

Thanks for your interest in Uptime Monitor.

## Workflow

Work is organized as milestone-based tasks in [`docs/PLAN.md`](docs/PLAN.md).
Pick a task whose dependencies are complete, implement it, and flip its
checkbox to `[x]` as part of the same change.

All production code is built **test-first** (red-green-refactor):

1. Write the failing test(s) that capture the required behaviour.
2. Write the minimum implementation that makes them pass.
3. Refactor with tests green.

## Before submitting

A change is ready only when all of the following are clean:

```sh
make test    # go test ./...
make vet     # go vet ./...
make fmt     # gofmt -w . — leaves no diff
make build   # go build succeeds
```

## Conventions

- `cmd/` stays thin; business logic lives in `internal/`.
- Raw SQL lives only in `internal/store` (and migration files).
- `internal/tui` must not import `internal/store/*`.
- Secrets are never logged and never returned in API responses by default.
- Match the surrounding code's style.

See [`docs/SPEC.md`](docs/SPEC.md) for the full architecture and rules, and
[`CLAUDE.md`](CLAUDE.md) for the project's working principles.

## License

By contributing, you agree that your contributions are licensed under the
MIT License (see [`LICENSE`](LICENSE)).
