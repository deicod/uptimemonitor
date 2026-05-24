# Container images with ko

[ko](https://ko.build) builds a minimal, distroless container image straight
from the Go source — no `Dockerfile` required (SPEC §22). The build is
configured by [`.ko.yaml`](../../.ko.yaml) at the repository root.

## Build

```sh
make ko-build                       # build into the local Docker daemon (ko.local)
KO_DOCKER_REPO=ghcr.io/you/uptimemonitor make ko-build   # build for a registry
```

`make ko-build` runs `ko build --local .`. Drop `--local` (set a real
`KO_DOCKER_REPO`) to push to a registry instead.

## Entrypoint: pass `service` as an argument

ko sets the image **entrypoint to the compiled binary** and leaves the command
(`CMD`) empty — it has no option to bake in a subcommand. The binary therefore
behaves like `uptimemonitor` with no arguments (it prints help) unless you pass
a subcommand at run time. To run the service, supply `service` as the argument:

```sh
docker run --rm <image> service
```

In Kubernetes, set it as `args` (leave `command` unset so ko's binary entrypoint
is preserved):

```yaml
containers:
  - name: uptimemonitor
    image: <image>
    args: ["service"]
```

> This matches SPEC §22.1: ko cannot set a default command/args, so the image
> runs the binary and `service` is supplied by the orchestrator. The behaviour
> is equivalent to running `uptimemonitor service`.

## Persistence

The service stores SQLite and the Prometheus TSDB under its data directory, so a
container deployment **must mount persistent storage** for it (SPEC §22.2).

The default paths (`/var/lib/uptimemonitor`, `/run/uptimemonitor`) are not
writable by the non-root image user, so either mount writable volumes at those
paths or point the service at a writable location with environment variables
(SPEC §8.4). For a quick local smoke run using the world-writable `/tmp`:

```sh
docker run --rm \
  -e UPTIMEMONITOR_DATA_DIR=/tmp/um/data \
  -e UPTIMEMONITOR_RUNTIME_DIR=/tmp/um/run \
  -e UPTIMEMONITOR_SQLITE_PATH=/tmp/um/data/config.db \
  -e UPTIMEMONITOR_TSDB_PATH=/tmp/um/data/tsdb \
  -e UPTIMEMONITOR_SOCKET_PATH=/tmp/um/run/uptimemonitor.sock \
  <image> service
```

## IPC / Unix-socket caveat

The service exposes its API only over a Unix domain socket — there is no TCP
listener in the MVP (SPEC §22.3, §10.1). The `tui` client connects to that
socket, so to manage monitors from a container you must reach the socket from
the same network/mount namespace: run the `tui` in the same container (or a
sidecar) with the socket on a shared volume, or `docker exec` into the running
container. Exposing the API over the network is out of scope for the MVP.
