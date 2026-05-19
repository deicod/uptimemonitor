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

// TestRunTUIConnectsAndPrintsStatus verifies the TUI bootstrap connects to a
// running service over its Unix socket and reports the service status. This
// matters because the TUI is purely a client (SPEC §9.2): if it cannot read
// status from the service the operator has no way to manage monitors.
func TestRunTUIConnectsAndPrintsStatus(t *testing.T) {
	cfg := testConfig(t)

	serviceCtx, stopService := context.WithCancel(context.Background())
	defer stopService()

	done := make(chan error, 1)
	go func() { done <- app.Run(serviceCtx, cfg) }()

	// Wait for the service to bind its socket before the TUI connects.
	waitForStatus(t, ipc.NewClient(cfg.SocketPath))

	var out bytes.Buffer
	if err := app.RunTUI(context.Background(), cfg, &out); err != nil {
		t.Fatalf("RunTUI returned error against a running service: %v", err)
	}
	if !strings.Contains(out.String(), "ready") {
		t.Errorf("RunTUI output does not report service state:\n%s", out.String())
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
	err := app.RunTUI(context.Background(), cfg, &out)
	if err == nil {
		t.Fatal("RunTUI succeeded with no service running")
	}
	if !strings.Contains(err.Error(), cfg.SocketPath) {
		t.Errorf("error %q does not name the socket path the TUI tried", err.Error())
	}
}
