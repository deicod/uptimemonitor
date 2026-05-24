package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/app"
	"github.com/deicod/uptimemonitor/internal/config"
	"github.com/deicod/uptimemonitor/internal/ipc"
	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/notify/providers/fake"
)

// TestEndToEndSmoke is the SPEC §24.4 end-to-end smoke test (M10.5). It drives a
// real service over its Unix socket through the whole MVP path: a monitor goes
// up against a live HTTP server, goes down when that server stops, and the
// resulting incident produces exactly the one down notification an operator
// depends on. The fake provider (injected via app.WithProviders) records the
// delivery so the test asserts it without any real network notification.
//
// This is the single test that proves the scheduler, probe, state machine,
// incident logic, and notification pipeline are wired together correctly inside
// app.Run — every piece is covered in isolation elsewhere, but only this test
// exercises the seams between them.
func TestEndToEndSmoke(t *testing.T) {
	cfg := testConfig(t)
	// testConfig leaves Notifications zero-valued (globally off). The down
	// notification only fires when the global gate is open, so enable it here
	// with a fast retry policy so a transient failure would not stall the test.
	cfg.Notifications = config.NotificationConfig{
		Enabled:           true,
		MaxAttempts:       3,
		InitialRetryDelay: 10 * time.Millisecond,
		MaxRetryDelay:     20 * time.Millisecond,
	}

	// The fake provider records every send; it is the test's window into the
	// delivery pipeline. Injecting it is the only production seam this test needs.
	fakeProv := fake.New()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, cfg, app.WithProviders(fakeProv)) }()

	client := ipc.NewClient(cfg.SocketPath)
	waitForStatus(t, client)

	// A local HTTP server stands in for the monitored endpoint: 200 while up.
	// Close it at most once (guarded) — the test also closes it mid-flow to
	// drive the monitor down.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srvClosed := false
	closeSrv := func() {
		if !srvClosed {
			srvClosed = true
			srv.Close()
		}
	}
	defer closeSrv()

	monCfg, err := json.Marshal(map[string]any{
		"url":                 srv.URL,
		"method":              "GET",
		"expected_status_min": 200,
		"expected_status_max": 299,
	})
	if err != nil {
		t.Fatalf("marshal monitor config: %v", err)
	}
	// A 60s interval keeps the scheduler from firing during the test, so every
	// check is one this test triggered — the up→down sequence stays deterministic.
	mon, err := client.CreateMonitor(context.Background(), ipc.CreateMonitorRequest{
		Name:                 "E2E Smoke",
		Type:                 "http",
		Enabled:              true,
		Interval:             ipc.Duration(60 * time.Second),
		Timeout:              ipc.Duration(5 * time.Second),
		Config:               monCfg,
		NotificationsEnabled: true,
	})
	if err != nil {
		t.Fatalf("CreateMonitor: %v", err)
	}

	// An enabled fake target receives the fan-out down notification. The fake
	// provider needs no config.
	if _, err := client.CreateNotificationTarget(context.Background(), ipc.CreateNotificationTargetRequest{
		Name:    "Fake target",
		Kind:    "fake",
		Enabled: true,
	}); err != nil {
		t.Fatalf("CreateNotificationTarget: %v", err)
	}

	// Manual check against the live server → up. unknown→up opens no incident
	// and sends nothing, so no notification is expected yet.
	triggerManualCheck(t, client, mon.ID)
	waitForLatestCheck(t, client, mon.ID, "up")

	// Take the server down. The no-overlap rule guarantees this manual check
	// sees the persisted "up" state, so the failure drives up→down: an incident
	// opens and a monitor_down notification is queued.
	closeSrv()
	triggerManualCheck(t, client, mon.ID)
	waitForLatestCheck(t, client, mon.ID, "down")

	// The end of the SPEC §24.4 path: the fake provider recorded the down
	// notification exactly because the incident opened.
	waitForFakeDownSend(t, fakeProv)

	// SIGTERM-equivalent: a clean shutdown must still succeed after all this.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on clean shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("service did not shut down within timeout")
	}
}

// triggerManualCheck enqueues a manual check, retrying while the scheduler
// reports a conflict. A conflict means a prior check for this monitor is still
// finishing (the no-overlap rule, SPEC §16.3); retrying until it clears both
// starts the next check and guarantees it observes the previous check's
// persisted state.
func triggerManualCheck(t *testing.T, c *ipc.Client, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, err := c.RunMonitor(context.Background(), id)
		if err == nil {
			return
		}
		var apiErr *ipc.APIError
		if errors.As(err, &apiErr) && apiErr.Code == ipc.ErrConflict {
			if time.Now().After(deadline) {
				t.Fatalf("RunMonitor(%s) kept returning conflict", id)
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		t.Fatalf("RunMonitor(%s): %v", id, err)
	}
}

// waitForLatestCheck polls the recent-checks endpoint until the most recent
// check for the monitor is in wantState, or the deadline passes. Manual checks
// run asynchronously, so a poll bridges the gap between triggering and the
// result landing in SQLite.
func waitForLatestCheck(t *testing.T, c *ipc.Client, id, wantState string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		checks, err := c.RecentChecks(context.Background(), id, 1)
		if err == nil && len(checks) > 0 && checks[0].State == wantState {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("monitor %s latest check never reached state %q (checks=%+v, err=%v)", id, wantState, checks, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForFakeDownSend polls the fake provider until it has recorded a
// monitor_down send, or the deadline passes. Delivery is asynchronous (enqueue
// → worker → provider), so the recorded send appears shortly after the down
// transition is persisted.
func waitForFakeDownSend(t *testing.T, p *fake.Provider) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, s := range p.Sends() {
			if s.Message.EventType == notify.EventMonitorDown {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake provider never recorded a %s notification (got %d sends)", notify.EventMonitorDown, len(p.Sends()))
		}
		time.Sleep(20 * time.Millisecond)
	}
}
