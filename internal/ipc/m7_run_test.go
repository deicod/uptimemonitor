package ipc_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/ipc"
	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/pipeline"
	"github.com/deicod/uptimemonitor/internal/scheduler"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// fakeProber is a pipeline.Prober that always returns a successful synthetic
// check, so the integration test does not need a real HTTP target.
type fakeProber struct{ called int }

func (f *fakeProber) Dispatch(_ context.Context, m monitor.Monitor) (monitor.CheckResult, error) {
	f.called++
	now := time.Now().UTC()
	return monitor.CheckResult{
		ID:         monitor.NewID(),
		MonitorID:  m.ID,
		StartedAt:  now,
		FinishedAt: now,
		Duration:   time.Millisecond,
		Success:    true,
	}, nil
}

// TestManualCheckDisabledMonitorOverIPC walks the full M7.7 flow for a
// disabled monitor: the IPC client calls POST /v1/monitors/{id}/run, the
// scheduler enqueues a manual job that runs through the real pipeline, and the
// resulting check is readable via GET /v1/monitors/{id}/checks. The whole
// point of the test is the SPEC §16.4 guarantee — a manual check on a disabled
// monitor must not unpause it — so we assert both the recorded check state and
// the monitor_state row stay at "paused".
func TestManualCheckDisabledMonitorOverIPC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store, err := sqlite.Open(filepath.Join(t.TempDir(), "m7.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	monitorRepo := sqlite.NewMonitorRepo(store)
	stateRepo := sqlite.NewMonitorStateRepo(store)
	eventRepo := sqlite.NewEventRepo(store)
	incidentRepo := sqlite.NewIncidentRepo(store)
	checkRepo := sqlite.NewCheckResultRepo(store)

	svc := monitor.NewService(monitorRepo, stateRepo, eventRepo)
	prober := &fakeProber{}
	pipe := pipeline.New(prober, checkRepo, stateRepo, eventRepo, incidentRepo,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	sched := scheduler.New(pipe.Run, 2)

	// The OnChange observer must mirror service.go so the scheduler learns
	// about the monitor we are about to create.
	svc.OnChange = func(c monitor.Change) {
		if c.Monitor == nil {
			return
		}
		if c.Kind == monitor.ChangeDeleted {
			sched.Remove(c.Monitor.ID)
			return
		}
		sched.Add(*c.Monitor)
	}

	sched.Start(ctx)
	t.Cleanup(sched.Stop)

	cfg, _ := json.Marshal(monitor.HTTPMonitorConfig{
		URL:               "https://example.com",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	})
	created, err := svc.Create(ctx, &monitor.Monitor{
		Name:     "Paused Monitor",
		Type:     monitor.MonitorTypeHTTP,
		Enabled:  false, // starts paused; no ticker, only manual triggers
		Interval: 60 * time.Second,
		Timeout:  10 * time.Second,
		Config:   cfg,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Sanity: the monitor starts in the paused state.
	st, err := stateRepo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("state Get: %v", err)
	}
	if st.State != monitor.StatePaused {
		t.Fatalf("initial state = %q, want %q", st.State, monitor.StatePaused)
	}

	sock := filepath.Join(t.TempDir(), "m7.sock")
	mux := ipc.NewRouter(nil, svc, incidentRepo, eventRepo,
		ipc.WithManualChecker(sched), ipc.WithCheckResults(checkRepo))
	srv := ipc.NewServer(sock, mux)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()
	waitForServer(t, sock)

	client := ipc.NewClient(sock)

	resp, err := client.RunMonitor(ctx, created.ID)
	if err != nil {
		t.Fatalf("RunMonitor: %v", err)
	}
	if resp.Status != "queued" {
		t.Errorf("Status = %q, want queued", resp.Status)
	}

	// The manual job runs asynchronously through the worker pool, so poll for
	// the check_result to land rather than racing on timing. A short deadline
	// keeps the test fast while remaining tolerant of slow CI machines.
	var checks []ipc.CheckResultResponse
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		checks, err = client.RecentChecks(ctx, created.ID, 10)
		if err != nil {
			t.Fatalf("RecentChecks: %v", err)
		}
		if len(checks) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(checks) != 1 {
		t.Fatalf("checks = %d, want 1 (manual check never landed)", len(checks))
	}

	// The state machine must keep the monitor paused; the check itself is
	// labelled with the new (still paused) state so history reads stay
	// consistent (SPEC §17.2, §16.4).
	if checks[0].State != string(monitor.StatePaused) {
		t.Errorf("check state = %q, want %q", checks[0].State, monitor.StatePaused)
	}
	st, err = stateRepo.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("state Get after run: %v", err)
	}
	if st.State != monitor.StatePaused {
		t.Errorf("monitor state after manual check = %q, want %q (manual must not unpause)",
			st.State, monitor.StatePaused)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("server Start returned error: %v", err)
	}
}
