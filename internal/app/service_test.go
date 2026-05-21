package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/app"
	"github.com/deicod/uptimemonitor/internal/config"
	"github.com/deicod/uptimemonitor/internal/ipc"
)

// testConfig returns a Config rooted under a temp directory so the service can
// be started without touching any real system paths.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	runDir := filepath.Join(dir, "run")
	return &config.Config{
		DataDir:    dataDir,
		RuntimeDir: runDir,
		SQLitePath: filepath.Join(dataDir, "config.db"),
		TSDBPath:   filepath.Join(dataDir, "tsdb"),
		SocketPath: filepath.Join(runDir, "uptimemonitor.sock"),
		LogLevel:   "error",
		Service: config.ServiceConfig{
			CheckWorkers:    4,
			DefaultInterval: 60 * time.Second,
			Timeout:         10 * time.Second,
			ShutdownTimeout: 10 * time.Second,
		},
		Retention: config.RetentionConfig{
			RawSamples:        30 * 24 * time.Hour,
			AggregatedHistory: 365 * 24 * time.Hour,
		},
	}
}

// waitForStatus polls /v1/status until it succeeds or the deadline passes. The
// service starts asynchronously, so a short poll bridges the startup gap.
func waitForStatus(t *testing.T, c *ipc.Client) ipc.StatusResponse {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := c.Status(context.Background())
		if err == nil {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("service never became reachable: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestServiceLifecycle exercises the full startup sequence (SPEC §9.1): the
// service opens its stores, serves /v1/status over the Unix socket, and on a
// shutdown signal exits cleanly leaving no socket behind (SPEC §9.3). The test
// matters because an operator relies on SIGTERM producing a clean exit — a
// leaked socket would block the next start.
func TestServiceLifecycle(t *testing.T) {
	cfg := testConfig(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, cfg) }()

	client := ipc.NewClient(cfg.SocketPath)
	status := waitForStatus(t, client)

	if status.State != "ready" {
		t.Errorf("status.State = %q, want %q", status.State, "ready")
	}
	if !status.SQLite.OK {
		t.Error("status.SQLite.OK = false, want true")
	}
	if !status.TSDB.OK {
		t.Error("status.TSDB.OK = false, want true")
	}
	// The scheduler must be reported as running once startup has reached
	// "ready"; otherwise the check pipeline is not wired and monitors created
	// over IPC would never be probed (M7.6).
	if !status.Scheduler.Running {
		t.Error("status.Scheduler.Running = false, want true")
	}
	if status.Scheduler.Workers != cfg.Service.CheckWorkers {
		t.Errorf("status.Scheduler.Workers = %d, want %d",
			status.Scheduler.Workers, cfg.Service.CheckWorkers)
	}
	// A fresh service has no monitors; both counts must start at zero or the
	// status endpoint would mislead operators.
	if status.Monitors.Total != 0 || status.Monitors.Active != 0 {
		t.Errorf("initial status.Monitors = %+v, want zero counts", status.Monitors)
	}

	// Creating one enabled monitor must lift Total and Active in /v1/status —
	// this exercises the live monitor.Service → statusProvider wiring.
	cfgJSON, err := json.Marshal(map[string]any{
		"url":                 "https://example.com",
		"method":              "GET",
		"expected_status_min": 200,
		"expected_status_max": 299,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if _, err := client.CreateMonitor(context.Background(), ipc.CreateMonitorRequest{
		Name:                 "Example",
		Type:                 "http",
		Enabled:              true,
		Interval:             ipc.Duration(60 * time.Second),
		Timeout:              ipc.Duration(10 * time.Second),
		Config:               cfgJSON,
		NotificationsEnabled: true,
	}); err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}
	after, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status after create: %v", err)
	}
	if after.Monitors.Total != 1 || after.Monitors.Active != 1 {
		t.Errorf("after create, status.Monitors = %+v, want {Total:1 Active:1}", after.Monitors)
	}

	// Simulate SIGTERM: cancelling ctx triggers graceful shutdown.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on clean shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("service did not shut down within timeout")
	}

	if _, err := os.Stat(cfg.SocketPath); !os.IsNotExist(err) {
		t.Errorf("socket %s was not removed on shutdown (stat err: %v)", cfg.SocketPath, err)
	}
}

// TestServiceFailsOnIPCBindReturnsWithoutHanging exercises the startupFailed
// branch of Run: the IPC server cannot bind because the socket path is an
// existing directory, but the parent ctx remains active. Run must still
// return promptly. Without the retention cleaner being given a cancellable
// sub-context, the deferred wait on the cleaner goroutine deadlocks because
// the cleaner's hourly ticker only exits on ctx.Done().
func TestServiceFailsOnIPCBindReturnsWithoutHanging(t *testing.T) {
	cfg := testConfig(t)
	// Pre-create the socket path as a directory so the IPC server's
	// stale-socket cleanup fails and Start returns an error before
	// listen ever succeeds — driving Run through startupFailed.
	if err := os.MkdirAll(cfg.SocketPath, 0o755); err != nil {
		t.Fatalf("pre-create socket dir: %v", err)
	}
	// Put a file inside so os.Remove on the directory fails.
	if err := os.WriteFile(filepath.Join(cfg.SocketPath, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed dir: %v", err)
	}

	// Use a parent ctx that we do NOT cancel; the fix must not require it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, cfg) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run returned nil despite IPC bind failure")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s — retention goroutine likely deadlocked the deferred wait")
	}
}

// TestServiceFailsOnUnusableSocketDir verifies startup fails fast when a
// required directory cannot be created, rather than reporting a false ready.
func TestServiceFailsOnUnusableSocketDir(t *testing.T) {
	cfg := testConfig(t)
	// Point the socket at a path whose parent is a regular file: MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker file: %v", err)
	}
	cfg.SocketPath = filepath.Join(blocker, "sub", "uptimemonitor.sock")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := app.Run(ctx, cfg); err == nil {
		t.Fatal("Run succeeded despite an uncreatable socket directory")
	}
}
