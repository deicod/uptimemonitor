package app_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/app"
	"github.com/deicod/uptimemonitor/internal/ipc"
)

// TestRunTUILaunchesAgainstRunningService verifies the TUI bootstrap connects
// to a running service and launches the Bubble Tea UI, then exits cleanly when
// its context is cancelled. The TUI is purely a client (SPEC §9.2): it must
// come up against a live service so the operator can manage monitors.
func TestRunTUILaunchesAgainstRunningService(t *testing.T) {
	cfg := testConfig(t)

	serviceCtx, stopService := context.WithCancel(context.Background())
	defer stopService()

	done := make(chan error, 1)
	go func() { done <- app.Run(serviceCtx, cfg) }()

	// Wait for the service to bind its socket before the TUI connects.
	waitForStatus(t, ipc.NewClient(cfg.SocketPath))

	tuiCtx, stopTUI := context.WithCancel(context.Background())
	var out bytes.Buffer
	tuiDone := make(chan error, 1)
	go func() {
		tuiDone <- app.RunTUI(tuiCtx, cfg, strings.NewReader(""), &out)
	}()

	// Give the program a moment to start, then cancel its context. A cancelled
	// context is a clean shutdown, so RunTUI must return without error.
	time.Sleep(300 * time.Millisecond)
	stopTUI()

	select {
	case err := <-tuiDone:
		if err != nil {
			t.Fatalf("RunTUI returned error against a running service: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunTUI did not exit after its context was cancelled")
	}

	stopService()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("service did not shut down within timeout")
	}
}

// TestRunTUIServiceDown verifies that when no service is listening the TUI
// bootstrap fails with a readable error rather than a raw socket error — the
// cmd layer turns that error into a non-zero exit.
func TestRunTUIServiceDown(t *testing.T) {
	cfg := testConfig(t)

	var out bytes.Buffer
	err := app.RunTUI(context.Background(), cfg, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("RunTUI succeeded with no service running")
	}
	if !strings.Contains(err.Error(), cfg.SocketPath) {
		t.Errorf("error %q does not name the socket path the TUI tried", err.Error())
	}
}
