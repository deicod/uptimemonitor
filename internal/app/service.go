// Package app wires uptimemonitor's components together into the runnable
// service and TUI processes. It owns the startup and shutdown sequencing
// (SPEC §9) so the cmd layer stays thin: cmd parses flags and config, app runs.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/deicod/uptimemonitor/internal/config"
	"github.com/deicod/uptimemonitor/internal/ipc"
	"github.com/deicod/uptimemonitor/internal/logging"
	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/pipeline"
	"github.com/deicod/uptimemonitor/internal/probe"
	"github.com/deicod/uptimemonitor/internal/scheduler"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
	"github.com/deicod/uptimemonitor/internal/store/tsdb"
	"github.com/deicod/uptimemonitor/internal/systemd"
	"github.com/deicod/uptimemonitor/internal/version"
)

// startupTimeout bounds how long Run waits for the IPC server to bind its
// socket before treating startup as failed.
const startupTimeout = 5 * time.Second

// Run executes the service startup sequence (SPEC §9.1), serves IPC requests
// until ctx is cancelled, then shuts down gracefully (SPEC §9.3).
//
// ctx is expected to be cancelled on SIGINT/SIGTERM by the caller. Run blocks
// until shutdown is complete and returns nil on a clean stop — including when
// the shutdown signal arrives mid-startup. Stores are closed in reverse open
// order (TSDB then SQLite) on every return path.
func Run(ctx context.Context, cfg *config.Config) error {
	logger := logging.Component(logging.New(cfg.LogLevel, os.Stderr), "service")

	if err := ensureDirs(cfg); err != nil {
		return err
	}

	sq, err := sqlite.Open(cfg.SQLitePath)
	if err != nil {
		return err
	}
	defer closeStore(logger, "sqlite", sq.Close)

	if err := sq.Migrate(); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	ts, err := tsdb.Open(cfg.TSDBPath, cfg.Retention.RawSamples)
	if err != nil {
		return err
	}
	defer closeStore(logger, "tsdb", ts.Close)

	eventRepo := sqlite.NewEventRepo(sq)
	stateRepo := sqlite.NewMonitorStateRepo(sq)
	incidentRepo := sqlite.NewIncidentRepo(sq)
	checkRepo := sqlite.NewCheckResultRepo(sq)

	monitorSvc := monitor.NewService(
		sqlite.NewMonitorRepo(sq),
		stateRepo,
		eventRepo,
	)
	pipe := pipeline.New(probe.NewDispatcher(), checkRepo, stateRepo, eventRepo, incidentRepo,
		logging.Component(logger, "pipeline"))
	sched := scheduler.New(pipe.Run, cfg.Service.CheckWorkers)

	// The OnChange observer keeps the scheduler's per-monitor schedule in sync
	// with monitor lifecycle events (M5.1, M7.5): deletes remove the entry,
	// every other change re-registers the (possibly updated) monitor so its
	// ticker is started, stopped, or rescheduled per its current enabled flag
	// and interval.
	monitorSvc.OnChange = func(c monitor.Change) {
		if c.Monitor == nil {
			return
		}
		if c.Kind == monitor.ChangeDeleted {
			sched.Remove(c.Monitor.ID)
			return
		}
		sched.Add(*c.Monitor)
	}

	provider := &statusProvider{
		sqlite:    sq,
		scheduler: sched,
		monitors:  monitorSvc,
		workers:   cfg.Service.CheckWorkers,
		startedAt: time.Now(),
		logger:    logging.Component(logger, "status"),
	}
	router := ipc.NewRouter(provider, monitorSvc, incidentRepo, eventRepo,
		ipc.WithManualChecker(sched), ipc.WithCheckResults(checkRepo))
	server := ipc.NewServer(cfg.SocketPath, router)

	// Start the scheduler before loading monitors so each Add starts its
	// ticker immediately (the scheduler only schedules tickers after Start).
	sched.Start(ctx)
	defer sched.Stop()

	existing, err := monitorSvc.List(ctx, monitor.MonitorFilter{})
	if err != nil {
		return fmt.Errorf("load existing monitors: %w", err)
	}
	for _, m := range existing {
		sched.Add(*m)
	}

	// The server is given its own cancellable context so every return path —
	// including a failed or aborted startup — stops the goroutine and lets it
	// remove the socket.
	serverCtx, cancelServer := context.WithCancel(ctx)
	defer cancelServer()

	// serverErr carries the server's single final result; serverDone is closed
	// once that result has been sent. serverErr is read exactly once, after
	// serverDone, so the two never race.
	serverErr := make(chan error, 1)
	serverDone := make(chan struct{})
	go func() {
		serverErr <- server.Start(serverCtx)
		close(serverDone)
	}()

	switch waitForSocket(ctx, cfg.SocketPath, serverDone) {
	case startupListening:
		// Signal ready and start the watchdog only once IPC is listening.
		if _, err := systemd.Ready(); err != nil {
			logger.Warn("systemd readiness notification failed", "error", err.Error())
		}
		systemd.StartWatchdog(ctx, logger)
		logger.Info("service started", "version", version.String(), "socket", cfg.SocketPath)

		select {
		case <-serverDone:
			// The server stopped on its own — an unexpected failure.
		case <-ctx.Done():
			logger.Info("shutdown signal received, stopping service")
		}
		cancelServer()
		<-serverDone
		if err := <-serverErr; err != nil {
			return fmt.Errorf("ipc server: %w", err)
		}
		logger.Info("service stopped")
		return nil

	case startupAborted:
		// A shutdown signal arrived before the server finished binding. This
		// is a clean stop, not an error.
		cancelServer()
		<-serverDone
		<-serverErr
		return nil

	default: // startupFailed
		cancelServer()
		<-serverDone
		if err := <-serverErr; err != nil {
			return fmt.Errorf("ipc server failed to start: %w", err)
		}
		return fmt.Errorf("ipc server did not start within %s", startupTimeout)
	}
}

// ensureDirs creates the data and runtime directories plus the parents of the
// SQLite database, TSDB, and socket paths (SPEC §9.1). MkdirAll is idempotent,
// so existing directories are left untouched.
func ensureDirs(cfg *config.Config) error {
	dirs := map[string]struct{}{
		cfg.DataDir:                  {},
		cfg.RuntimeDir:               {},
		filepath.Dir(cfg.SQLitePath): {},
		filepath.Dir(cfg.TSDBPath):   {},
		filepath.Dir(cfg.SocketPath): {},
	}
	for dir := range dirs {
		if dir == "" || dir == "." {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return nil
}

// startupResult reports how the IPC server's startup concluded.
type startupResult int

const (
	// startupListening: the socket is bound and serving.
	startupListening startupResult = iota
	// startupAborted: ctx was cancelled before the socket appeared.
	startupAborted
	// startupFailed: the server stopped before binding, or timed out.
	startupFailed
)

// waitForSocket blocks until the IPC socket file appears, the server goroutine
// finishes, the startup timeout elapses, or ctx is cancelled. On both the
// ctx-cancelled and server-finished paths it re-checks the socket so a server
// that bound in the same instant is still reported as listening.
func waitForSocket(ctx context.Context, path string, serverDone <-chan struct{}) startupResult {
	deadline := time.NewTimer(startupTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if socketExists(path) {
			return startupListening
		}
		select {
		case <-serverDone:
			if socketExists(path) {
				return startupListening
			}
			return startupFailed
		case <-deadline.C:
			return startupFailed
		case <-ctx.Done():
			if socketExists(path) {
				return startupListening
			}
			return startupAborted
		case <-ticker.C:
		}
	}
}

// closeStore closes a storage backend during shutdown, logging any error.
// A close failure cannot abort an already-terminating process, so it is
// surfaced via the log rather than returned (SPEC §9.3).
func closeStore(logger *slog.Logger, name string, close func() error) {
	if err := close(); err != nil {
		logger.Error("failed to close store", "store", name, "error", err.Error())
	}
}

// socketExists reports whether a file exists at path.
func socketExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// statusProvider implements ipc.StatusProvider, backing GET /v1/status with the
// service's live view of itself.
type statusProvider struct {
	sqlite    *sqlite.Store
	scheduler *scheduler.Scheduler
	monitors  *monitor.Service
	workers   int
	startedAt time.Time
	logger    *slog.Logger
}

// Status returns the current service status snapshot (SPEC §10.5).
func (p *statusProvider) Status(ctx context.Context) ipc.StatusResponse {
	return ipc.StatusResponse{
		Version:   version.String(),
		State:     "ready",
		StartedAt: p.startedAt,
		SQLite:    ipc.StoreHealth{OK: p.sqlite.DB().PingContext(ctx) == nil},
		TSDB:      ipc.StoreHealth{OK: true},
		Scheduler: ipc.SchedulerStatus{Running: p.scheduler.Running(), Workers: p.workers},
		Monitors:  p.monitorCounts(ctx),
	}
}

// monitorCounts counts active (enabled, non-deleted) monitors and the total of
// non-deleted monitors for the /v1/status payload. A list error is logged and
// reported as zero counts rather than failing the status call, because /v1/status
// is a liveness probe and must always respond.
func (p *statusProvider) monitorCounts(ctx context.Context) ipc.MonitorCounts {
	all, err := p.monitors.List(ctx, monitor.MonitorFilter{})
	if err != nil {
		p.logger.Error("list monitors for status", "error", err.Error())
		return ipc.MonitorCounts{}
	}
	counts := ipc.MonitorCounts{Total: len(all)}
	for _, m := range all {
		if m.Enabled {
			counts.Active++
		}
	}
	return counts
}
