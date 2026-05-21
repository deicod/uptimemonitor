package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/pipeline"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// discardLogger returns a logger that drops every record. Tests don't assert
// on log output, so silencing it keeps `go test -v` output focused on test
// assertions instead of pipeline traces.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fakeProber returns a deterministic CheckResult per call so a test can drive
// the pipeline through a specific success/failure sequence without booting a
// real HTTP server.
type fakeProber struct {
	queue []probeOutcome
	calls int
}

// probeOutcome describes the next CheckResult the prober should synthesize.
// We use a small struct rather than passing a full CheckResult so tests stay
// readable: most fields (IDs, timestamps) are mechanical and the pipeline
// derives them anyway.
type probeOutcome struct {
	success bool
	errStr  string
}

func (f *fakeProber) Dispatch(_ context.Context, m monitor.Monitor) (monitor.CheckResult, error) {
	if f.calls >= len(f.queue) {
		// Reuse the last queued outcome if a test fires more checks than it
		// queued. This keeps tests resilient to harmless extra runs.
		f.calls++
		return f.next(f.queue[len(f.queue)-1], m), nil
	}
	out := f.queue[f.calls]
	f.calls++
	return f.next(out, m), nil
}

func (f *fakeProber) next(out probeOutcome, m monitor.Monitor) monitor.CheckResult {
	now := time.Now().UTC()
	return monitor.CheckResult{
		ID:         monitor.NewID(),
		MonitorID:  m.ID,
		StartedAt:  now,
		FinishedAt: now,
		Duration:   5 * time.Millisecond,
		Success:    out.success,
		Error:      out.errStr,
	}
}

// pipelineFixture bundles everything an integration test needs: an open,
// migrated SQLite store, a monitor service to insert seed data, the pipeline
// under test, and the fake prober the test drives.
type pipelineFixture struct {
	store    *sqlite.Store
	svc      *monitor.Service
	pipeline *pipeline.Pipeline
	prober   *fakeProber
}

func newFixture(t *testing.T) *pipelineFixture {
	t.Helper()

	store, err := sqlite.Open(filepath.Join(t.TempDir(), "pipeline.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	monitors := sqlite.NewMonitorRepo(store)
	states := sqlite.NewMonitorStateRepo(store)
	events := sqlite.NewEventRepo(store)
	incidents := sqlite.NewIncidentRepo(store)
	checks := sqlite.NewCheckResultRepo(store)

	svc := monitor.NewService(monitors, states, events)
	prober := &fakeProber{}
	p := pipeline.New(prober, checks, states, events, incidents, discardLogger())

	return &pipelineFixture{store: store, svc: svc, pipeline: p, prober: prober}
}

// createMonitor seeds an enabled HTTP monitor through the real monitor.Service
// so the integration test exercises the same setup path as a live IPC create.
func (f *pipelineFixture) createMonitor(t *testing.T) *monitor.Monitor {
	t.Helper()

	cfg, err := json.Marshal(monitor.HTTPMonitorConfig{
		URL:               "https://example.com",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	m, err := f.svc.Create(context.Background(), &monitor.Monitor{
		Name:                 "Example",
		Type:                 monitor.MonitorTypeHTTP,
		Enabled:              true,
		Interval:             60 * time.Second,
		Timeout:              10 * time.Second,
		Config:               cfg,
		NotificationsEnabled: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return m
}

// monitorEvents returns events for one monitor newest-first via the real repo.
func (f *pipelineFixture) monitorEvents(t *testing.T, monitorID string) []*monitor.Event {
	t.Helper()
	evts, err := sqlite.NewEventRepo(f.store).ListByMonitor(context.Background(), monitorID, 0)
	if err != nil {
		t.Fatalf("ListByMonitor: %v", err)
	}
	return evts
}

// hasEventType reports whether any event in evts has the given type. Tests use
// it rather than asserting positional types so that adding tangential events
// (e.g. monitor_created from the seed) does not require updating every test.
func hasEventType(evts []*monitor.Event, typ string) bool {
	for _, e := range evts {
		if e.Type == typ {
			return true
		}
	}
	return false
}

// TestPipeline_SuccessTransitionsToUp covers the unknown→up path: the seed
// monitor starts at unknown, the first successful check flips its state to up,
// records a check_result, and emits a monitor_state_changed event. No incident
// is opened on this transition (SPEC §17.3 "unknown -> up").
func TestPipeline_SuccessTransitionsToUp(t *testing.T) {
	f := newFixture(t)
	m := f.createMonitor(t)
	ctx := context.Background()

	f.prober.queue = []probeOutcome{{success: true}}
	f.pipeline.Run(ctx, *m, false)

	state, err := sqlite.NewMonitorStateRepo(f.store).Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get state: %v", err)
	}
	if state.State != monitor.StateUp {
		t.Errorf("state = %q, want %q", state.State, monitor.StateUp)
	}
	if state.ConsecutiveSuccesses != 1 || state.ConsecutiveFailures != 0 {
		t.Errorf("counters = (%d, %d), want (1, 0)", state.ConsecutiveSuccesses, state.ConsecutiveFailures)
	}
	if state.LastSuccessAt == nil {
		t.Error("LastSuccessAt not stamped on successful check")
	}

	checks, err := sqlite.NewCheckResultRepo(f.store).ListRecent(ctx, m.ID, 0)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(checks) != 1 {
		t.Fatalf("check_results = %d, want 1", len(checks))
	}
	if !checks[0].Success || checks[0].State != monitor.StateUp {
		t.Errorf("check_result = %+v, want success=true state=up", checks[0])
	}

	evts := f.monitorEvents(t, m.ID)
	if !hasEventType(evts, monitor.EventMonitorStateChanged) {
		t.Errorf("missing monitor_state_changed event in %+v", evts)
	}
	if hasEventType(evts, monitor.EventIncidentOpened) {
		t.Errorf("unexpected incident_opened event on first success")
	}
}

// TestPipeline_FailureOpensIncidentAndEvent covers the unknown→down path: a
// failing first check transitions to down, opens an incident, and emits both a
// monitor_state_changed and an incident_opened event. The incident's
// start_event_id must point at the incident_opened event so the audit log can
// follow the link.
func TestPipeline_FailureOpensIncidentAndEvent(t *testing.T) {
	f := newFixture(t)
	m := f.createMonitor(t)
	ctx := context.Background()

	f.prober.queue = []probeOutcome{{success: false, errStr: "request failed"}}
	f.pipeline.Run(ctx, *m, false)

	state, err := sqlite.NewMonitorStateRepo(f.store).Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get state: %v", err)
	}
	if state.State != monitor.StateDown {
		t.Errorf("state = %q, want %q", state.State, monitor.StateDown)
	}
	if state.ConsecutiveFailures != 1 || state.ConsecutiveSuccesses != 0 {
		t.Errorf("counters = (%d, %d), want (0, 1)", state.ConsecutiveSuccesses, state.ConsecutiveFailures)
	}
	if state.LastFailureAt == nil {
		t.Error("LastFailureAt not stamped on failed check")
	}

	evts := f.monitorEvents(t, m.ID)
	if !hasEventType(evts, monitor.EventMonitorStateChanged) {
		t.Errorf("missing monitor_state_changed event in %+v", evts)
	}
	if !hasEventType(evts, monitor.EventIncidentOpened) {
		t.Errorf("missing incident_opened event in %+v", evts)
	}

	open, err := sqlite.NewIncidentRepo(f.store).FindOpenByMonitor(ctx, m.ID)
	if err != nil {
		t.Fatalf("FindOpenByMonitor: %v", err)
	}
	if open.Reason != "request failed" {
		t.Errorf("incident reason = %q, want %q", open.Reason, "request failed")
	}
	// The link from incident → event must resolve to an incident_opened entry.
	var startEvent *monitor.Event
	for _, e := range evts {
		if e.ID == open.StartEventID {
			startEvent = e
			break
		}
	}
	if startEvent == nil {
		t.Fatalf("start_event_id %q does not match any event", open.StartEventID)
	}
	if startEvent.Type != monitor.EventIncidentOpened {
		t.Errorf("start_event_id type = %q, want %q", startEvent.Type, monitor.EventIncidentOpened)
	}
}

// TestPipeline_RecoveryResolvesIncident covers down→up after a prior failure:
// the open incident is resolved (resolved_at + end_event_id stamped), an
// incident_resolved event is appended, and FindOpenByMonitor no longer returns
// a row. This is the SPEC §17.3 down→up path that drives the recovery
// notification once M9 is wired.
func TestPipeline_RecoveryResolvesIncident(t *testing.T) {
	f := newFixture(t)
	m := f.createMonitor(t)
	ctx := context.Background()

	// First, drive the monitor down so an incident is open.
	f.prober.queue = []probeOutcome{
		{success: false, errStr: "request failed"},
		{success: true},
	}
	f.pipeline.Run(ctx, *m, false)

	if _, err := sqlite.NewIncidentRepo(f.store).FindOpenByMonitor(ctx, m.ID); err != nil {
		t.Fatalf("precondition: incident should be open after failure: %v", err)
	}

	// Now recover.
	f.pipeline.Run(ctx, *m, false)

	state, err := sqlite.NewMonitorStateRepo(f.store).Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get state: %v", err)
	}
	if state.State != monitor.StateUp {
		t.Errorf("state after recovery = %q, want %q", state.State, monitor.StateUp)
	}
	if state.ConsecutiveSuccesses != 1 || state.ConsecutiveFailures != 0 {
		t.Errorf("counters reset incorrectly = (%d, %d), want (1, 0)",
			state.ConsecutiveSuccesses, state.ConsecutiveFailures)
	}

	// No open incident remains.
	if _, err := sqlite.NewIncidentRepo(f.store).FindOpenByMonitor(ctx, m.ID); !errors.Is(err, sqlite.ErrNotFound) {
		t.Errorf("FindOpenByMonitor after recovery = %v, want ErrNotFound", err)
	}

	// The incident still lists, now with resolution fields set.
	list, err := sqlite.NewIncidentRepo(f.store).List(ctx, m.ID, 10)
	if err != nil {
		t.Fatalf("List incidents: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("incidents = %d, want 1", len(list))
	}
	in := list[0]
	if in.ResolvedAt == nil {
		t.Error("ResolvedAt not stamped on recovery")
	}
	if in.EndEventID == nil {
		t.Error("EndEventID not stamped on recovery")
	}

	evts := f.monitorEvents(t, m.ID)
	if !hasEventType(evts, monitor.EventIncidentResolved) {
		t.Errorf("missing incident_resolved event in %+v", evts)
	}
	// The end_event_id should point at the incident_resolved entry.
	if in.EndEventID != nil {
		var endEvent *monitor.Event
		for _, e := range evts {
			if e.ID == *in.EndEventID {
				endEvent = e
				break
			}
		}
		if endEvent == nil {
			t.Fatalf("end_event_id %q does not match any event", *in.EndEventID)
		}
		if endEvent.Type != monitor.EventIncidentResolved {
			t.Errorf("end_event_id type = %q, want %q", endEvent.Type, monitor.EventIncidentResolved)
		}
	}
}

// TestPipeline_SteadyStateNoOp covers the up→up "silent" path (SPEC §18.8 spam
// rule): repeated successes after the first must not emit a state-changed
// event or open new incidents, even though check_results keep accumulating.
// Without this, every successful tick would generate notification noise once
// M9 is wired.
func TestPipeline_SteadyStateNoOp(t *testing.T) {
	f := newFixture(t)
	m := f.createMonitor(t)
	ctx := context.Background()

	f.prober.queue = []probeOutcome{{success: true}, {success: true}}
	f.pipeline.Run(ctx, *m, false)
	f.pipeline.Run(ctx, *m, false)

	state, err := sqlite.NewMonitorStateRepo(f.store).Get(ctx, m.ID)
	if err != nil {
		t.Fatalf("Get state: %v", err)
	}
	if state.ConsecutiveSuccesses != 2 {
		t.Errorf("ConsecutiveSuccesses = %d, want 2", state.ConsecutiveSuccesses)
	}

	checks, err := sqlite.NewCheckResultRepo(f.store).ListRecent(ctx, m.ID, 0)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("check_results = %d, want 2", len(checks))
	}

	evts := f.monitorEvents(t, m.ID)
	var stateChanges int
	for _, e := range evts {
		if e.Type == monitor.EventMonitorStateChanged {
			stateChanges++
		}
	}
	if stateChanges != 1 {
		t.Errorf("monitor_state_changed events = %d, want 1 (only the first up transition)", stateChanges)
	}
}
