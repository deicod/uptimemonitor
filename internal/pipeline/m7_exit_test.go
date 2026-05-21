package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/pipeline"
	"github.com/deicod/uptimemonitor/internal/scheduler"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// switchableProber returns whichever outcome is currently set. The pipeline
// runs on scheduler worker goroutines, so reads and writes are guarded by a
// mutex; tests flip the outcome to drive the monitor through the
// success → failure → recovery cycle the M7 exit check exercises.
type switchableProber struct {
	mu      sync.Mutex
	outcome probeOutcome
	calls   int
}

func newSwitchableProber(initial probeOutcome) *switchableProber {
	return &switchableProber{outcome: initial}
}

func (p *switchableProber) set(out probeOutcome) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.outcome = out
}

func (p *switchableProber) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *switchableProber) Dispatch(_ context.Context, m monitor.Monitor) (monitor.CheckResult, error) {
	p.mu.Lock()
	out := p.outcome
	p.calls++
	p.mu.Unlock()

	now := time.Now().UTC()
	return monitor.CheckResult{
		ID:         monitor.NewID(),
		MonitorID:  m.ID,
		StartedAt:  now,
		FinishedAt: now,
		Duration:   1 * time.Millisecond,
		Success:    out.success,
		Error:      out.errStr,
	}, nil
}

// waitUntil polls cond until it is true or the timeout elapses; it mirrors the
// scheduler tests' helper rather than time.Sleep-based assertions, which would
// either flake under load or pad the runtime.
func waitUntil(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", msg)
}

// TestM7ExitCheck is the SPEC §28 acceptance check for milestone M7. It wires
// the real scheduler to the real pipeline against an on-disk SQLite database
// and a controllable prober, then drives the monitor through three phases:
//
//  1. Repeated success ticks — the scheduler must keep firing, the pipeline
//     must persist a check_result per tick, and the state must settle on up.
//  2. Flip to failure — the pipeline must transition the state to down, open
//     an incident, and append both a monitor_state_changed and an
//     incident_opened event.
//  3. Flip back to success — the pipeline must transition to up, resolve the
//     open incident, and append an incident_resolved event.
//
// If this test passes, every M7 acceptance bullet from SPEC §28 holds against
// real components, not mocks: "scheduler checks monitors repeatedly", "results
// stored in SQLite", "state transitions recorded", "incidents opened and
// resolved".
func TestM7ExitCheck(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// Swap the fixture's queued prober for a switchable one so this test can
	// flip outcomes mid-run rather than enqueueing a fixed script.
	prober := newSwitchableProber(probeOutcome{success: true})
	p := pipeline.New(
		prober,
		sqlite.NewCheckResultRepo(f.store),
		sqlite.NewMonitorStateRepo(f.store),
		sqlite.NewEventRepo(f.store),
		sqlite.NewIncidentRepo(f.store),
		&fakeSampleWriter{},
		discardLogger(),
	)

	sched := scheduler.New(p.Run, 2)
	sched.Start(ctx)
	t.Cleanup(sched.Stop)

	// A short interval keeps total runtime bounded while still exercising the
	// real time.Ticker path (a fake clock would not catch races against it).
	const interval = 25 * time.Millisecond

	cfg, err := json.Marshal(monitor.HTTPMonitorConfig{
		URL:               "https://example.com",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	m, err := f.svc.Create(ctx, &monitor.Monitor{
		Name:                 "M7 Exit",
		Type:                 monitor.MonitorTypeHTTP,
		Enabled:              true,
		Interval:             interval,
		Timeout:              5 * time.Second,
		Config:               cfg,
		NotificationsEnabled: false,
	})
	if err != nil {
		t.Fatalf("Create monitor: %v", err)
	}
	sched.Add(*m)

	stateRepo := sqlite.NewMonitorStateRepo(f.store)
	checkRepo := sqlite.NewCheckResultRepo(f.store)
	incidentRepo := sqlite.NewIncidentRepo(f.store)

	// --- Phase 1: repeated success ticks settle the state on up. ---
	waitUntil(t, func() bool {
		st, err := stateRepo.Get(ctx, m.ID)
		return err == nil && st.State == monitor.StateUp && st.ConsecutiveSuccesses >= 3
	}, 2*time.Second, "state up with ≥3 consecutive successes")

	checks, err := checkRepo.ListRecent(ctx, m.ID, 0)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(checks) < 3 {
		t.Fatalf("check_results = %d, want ≥3 (scheduler must tick repeatedly)", len(checks))
	}
	evts := f.monitorEvents(t, m.ID)
	if !hasEventType(evts, monitor.EventMonitorStateChanged) {
		t.Errorf("phase 1: missing monitor_state_changed event in %+v", evts)
	}

	// --- Phase 2: flip to failure → state down + incident open. ---
	prober.set(probeOutcome{success: false, errStr: "request failed"})

	waitUntil(t, func() bool {
		st, err := stateRepo.Get(ctx, m.ID)
		return err == nil && st.State == monitor.StateDown
	}, 2*time.Second, "state to flip to down")

	open, err := incidentRepo.FindOpenByMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("FindOpenByMonitor after failure: %v", err)
	}
	if open.Reason != "request failed" {
		t.Errorf("open incident reason = %q, want %q", open.Reason, "request failed")
	}

	evts = f.monitorEvents(t, m.ID)
	if !hasEventType(evts, monitor.EventIncidentOpened) {
		t.Errorf("phase 2: missing incident_opened event in %+v", evts)
	}

	// --- Phase 3: recover → state up + incident resolved. ---
	prober.set(probeOutcome{success: true})

	waitUntil(t, func() bool {
		_, err := incidentRepo.FindOpenByMonitor(ctx, m.ID)
		return errors.Is(err, sqlite.ErrNotFound)
	}, 2*time.Second, "open incident to be resolved")

	st, err := stateRepo.Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get state after recovery: %v", err)
	}
	if st.State != monitor.StateUp {
		t.Errorf("state after recovery = %q, want %q", st.State, monitor.StateUp)
	}

	list, err := incidentRepo.List(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("List incidents: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("incidents = %d, want exactly 1 over the cycle", len(list))
	}
	if list[0].ResolvedAt == nil || list[0].EndEventID == nil {
		t.Errorf("resolved incident missing resolution fields: %+v", list[0])
	}

	evts = f.monitorEvents(t, m.ID)
	if !hasEventType(evts, monitor.EventIncidentResolved) {
		t.Errorf("phase 3: missing incident_resolved event in %+v", evts)
	}

	if prober.callCount() < 3 {
		t.Errorf("prober call count = %d, want ≥3 (scheduler did not tick enough)",
			prober.callCount())
	}
}
