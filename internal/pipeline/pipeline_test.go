package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/notify"
	fakeprov "github.com/deicod/uptimemonitor/internal/notify/providers/fake"
	"github.com/deicod/uptimemonitor/internal/pipeline"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
	"github.com/deicod/uptimemonitor/internal/store/tsdb"
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
	status  *int
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
		ID:             monitor.NewID(),
		MonitorID:      m.ID,
		StartedAt:      now,
		FinishedAt:     now,
		Duration:       5 * time.Millisecond,
		Success:        out.success,
		Error:          out.errStr,
		HTTPStatusCode: out.status,
	}
}

// fakeSampleWriter records every CheckSample the pipeline writes so tests can
// assert that TSDB samples are produced per check. It satisfies
// pipeline.SampleWriter without needing a real TSDB on disk.
type fakeSampleWriter struct {
	mu      sync.Mutex
	samples []tsdb.CheckSample
}

func (f *fakeSampleWriter) WriteCheck(_ context.Context, s tsdb.CheckSample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples = append(f.samples, s)
	return nil
}

func (f *fakeSampleWriter) Samples() []tsdb.CheckSample {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]tsdb.CheckSample, len(f.samples))
	copy(out, f.samples)
	return out
}

// pipelineFixture bundles everything an integration test needs: an open,
// migrated SQLite store, a monitor service to insert seed data, the pipeline
// under test, and the fake prober the test drives.
type pipelineFixture struct {
	store    *sqlite.Store
	svc      *monitor.Service
	pipeline *pipeline.Pipeline
	prober   *fakeProber
	samples  *fakeSampleWriter
}

func newFixture(t *testing.T, opts ...pipeline.Option) *pipelineFixture {
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
	samples := &fakeSampleWriter{}
	p := pipeline.New(prober, checks, states, events, incidents, samples, discardLogger(), opts...)

	return &pipelineFixture{store: store, svc: svc, pipeline: p, prober: prober, samples: samples}
}

// fakeNotifier records enqueued jobs synchronously so the gating tests can
// assert exactly when the pipeline decides to notify, without the async
// delivery worker pool in the picture.
type fakeNotifier struct {
	mu   sync.Mutex
	jobs []notify.Job
}

func (n *fakeNotifier) Enqueue(job notify.Job) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.jobs = append(n.jobs, job)
}

func (n *fakeNotifier) Jobs() []notify.Job {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]notify.Job, len(n.jobs))
	copy(out, n.jobs)
	return out
}

// fakeGate is a constant global notifications toggle.
type fakeGate struct{ enabled bool }

func (g fakeGate) NotificationsEnabled(context.Context) bool { return g.enabled }

// waitForSends polls the fake provider until it has recorded at least want
// sends or the deadline passes. Delivery runs on the notify pipeline's worker
// pool, so the assertion has to wait for the async hand-off.
func waitForSends(t *testing.T, fp *fakeprov.Provider, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(fp.Sends()) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected at least %d sends, got %d within timeout", want, len(fp.Sends()))
}

// TestPipeline_QueuesDownAndRecoveryNotifications covers the SPEC §17.3 wiring:
// a down transition queues a monitor_down job linked to the freshly-opened
// incident, and the recovery queues a monitor_recovered job against that same
// incident. The link matters because the delivery pipeline keys spam
// suppression on (incident, event type) (SPEC §18.8).
func TestPipeline_QueuesDownAndRecoveryNotifications(t *testing.T) {
	notifier := &fakeNotifier{}
	f := newFixture(t, pipeline.WithNotifications(notifier, fakeGate{enabled: true}))
	m := f.createMonitor(t)
	ctx := context.Background()

	f.prober.queue = []probeOutcome{{success: false, errStr: "boom"}, {success: true}}
	f.pipeline.Run(ctx, *m, false) // down
	f.pipeline.Run(ctx, *m, false) // recovery

	jobs := notifier.Jobs()
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2 (down + recovery)", len(jobs))
	}
	down := jobs[0]
	if down.Message.EventType != notify.EventMonitorDown {
		t.Errorf("job[0] type = %q, want %q", down.Message.EventType, notify.EventMonitorDown)
	}
	if down.IncidentID == "" || down.EventID == "" {
		t.Errorf("down job missing incident/event link: %+v", down)
	}
	if down.Message.MonitorID != m.ID || down.Message.MonitorName != m.Name {
		t.Errorf("down job message identity = %+v, want id=%s name=%s", down.Message, m.ID, m.Name)
	}
	rec := jobs[1]
	if rec.Message.EventType != notify.EventMonitorRecovered {
		t.Errorf("job[1] type = %q, want %q", rec.Message.EventType, notify.EventMonitorRecovered)
	}
	if rec.IncidentID != down.IncidentID {
		t.Errorf("recovery incident = %q, want same as down %q", rec.IncidentID, down.IncidentID)
	}
}

// TestPipeline_NoNotificationOnFirstSuccess pins that unknown→up neither opens
// an incident nor notifies (SPEC §17.3 "unknown -> up").
func TestPipeline_NoNotificationOnFirstSuccess(t *testing.T) {
	notifier := &fakeNotifier{}
	f := newFixture(t, pipeline.WithNotifications(notifier, fakeGate{enabled: true}))
	m := f.createMonitor(t)

	f.prober.queue = []probeOutcome{{success: true}}
	f.pipeline.Run(context.Background(), *m, false)

	if jobs := notifier.Jobs(); len(jobs) != 0 {
		t.Errorf("jobs on unknown→up = %d, want 0", len(jobs))
	}
}

// TestPipeline_NoNotificationWhenMonitorDisabled covers the per-monitor opt-out
// (SPEC §18.6, §6 decision 5): a monitor with NotificationsEnabled=false still
// transitions and opens incidents but must not enqueue notifications.
func TestPipeline_NoNotificationWhenMonitorDisabled(t *testing.T) {
	notifier := &fakeNotifier{}
	f := newFixture(t, pipeline.WithNotifications(notifier, fakeGate{enabled: true}))
	m := f.createMonitor(t)
	m.NotificationsEnabled = false

	f.prober.queue = []probeOutcome{{success: false, errStr: "boom"}}
	f.pipeline.Run(context.Background(), *m, false)

	if jobs := notifier.Jobs(); len(jobs) != 0 {
		t.Errorf("jobs for notifications-disabled monitor = %d, want 0", len(jobs))
	}
}

// TestPipeline_NoNotificationWhenGloballyDisabled covers the global toggle
// (SPEC §18.6): with the gate closed, no transition notifies.
func TestPipeline_NoNotificationWhenGloballyDisabled(t *testing.T) {
	notifier := &fakeNotifier{}
	f := newFixture(t, pipeline.WithNotifications(notifier, fakeGate{enabled: false}))
	m := f.createMonitor(t)

	f.prober.queue = []probeOutcome{{success: false, errStr: "boom"}}
	f.pipeline.Run(context.Background(), *m, false)

	if jobs := notifier.Jobs(); len(jobs) != 0 {
		t.Errorf("jobs with global toggle off = %d, want 0", len(jobs))
	}
}

// TestPipeline_DeliversToFakeProviderAndRecordsAttempt is the M9.11 end-to-end
// check: a down then recovery transition, run through the real notify delivery
// pipeline, reaches the fake provider and records one attempt per delivery.
func TestPipeline_DeliversToFakeProviderAndRecordsAttempt(t *testing.T) {
	f := newFixture(t)
	m := f.createMonitor(t)
	ctx := context.Background()

	reg := notify.NewRegistry()
	fp := fakeprov.New()
	if err := reg.Register(fp); err != nil {
		t.Fatalf("Register: %v", err)
	}
	targetRepo := sqlite.NewNotificationTargetRepo(f.store, reg.SecretFields)
	attemptRepo := sqlite.NewNotificationAttemptRepo(f.store)
	now := time.Now().UTC()
	if err := targetRepo.Insert(ctx, &notify.Target{
		ID: monitor.NewID(), Name: "Ops", Kind: "fake", Enabled: true,
		Config: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Insert target: %v", err)
	}

	notifyPipe := notify.NewPipeline(reg, targetRepo, attemptRepo, notify.RetryConfig{MaxAttempts: 1}, discardLogger())
	notifyPipe.Start(ctx)
	defer notifyPipe.Stop()

	p := pipeline.New(f.prober,
		sqlite.NewCheckResultRepo(f.store), sqlite.NewMonitorStateRepo(f.store),
		sqlite.NewEventRepo(f.store), sqlite.NewIncidentRepo(f.store), f.samples, discardLogger(),
		pipeline.WithNotifications(notifyPipe, fakeGate{enabled: true}))

	f.prober.queue = []probeOutcome{{success: false, errStr: "boom"}, {success: true}}

	p.Run(ctx, *m, false) // down
	waitForSends(t, fp, 1)
	if got := fp.Sends()[0].Message.EventType; got != notify.EventMonitorDown {
		t.Errorf("first send = %q, want %q", got, notify.EventMonitorDown)
	}

	p.Run(ctx, *m, false) // recovery
	waitForSends(t, fp, 2)
	if got := fp.Sends()[1].Message.EventType; got != notify.EventMonitorRecovered {
		t.Errorf("second send = %q, want %q", got, notify.EventMonitorRecovered)
	}

	attempts, err := attemptRepo.ListRecent(ctx, 0)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts recorded = %d, want 2", len(attempts))
	}
	for _, a := range attempts {
		if a.Status != notify.AttemptStatusSuccess {
			t.Errorf("attempt %s status = %q, want success", a.ID, a.Status)
		}
	}
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

// TestPipeline_WritesTSDBSamplesPerCheck covers SPEC §14.2–14.3: every check
// must produce a TSDB sample labelled with the monitor's id and type. Without
// this, the history view (M8.5) would have no raw data to plot. The fakeProber
// installs a deterministic HTTP status so the test can also assert that the
// status code carries through to the sample (and is dropped on transport
// failure in a sibling test).
func TestPipeline_WritesTSDBSamplesPerCheck(t *testing.T) {
	f := newFixture(t)
	m := f.createMonitor(t)
	ctx := context.Background()

	status := 200
	f.prober.queue = []probeOutcome{{success: true, status: &status}}
	f.pipeline.Run(ctx, *m, false)

	got := f.samples.Samples()
	if len(got) != 1 {
		t.Fatalf("samples written = %d, want 1", len(got))
	}
	s := got[0]
	if s.MonitorID != m.ID {
		t.Errorf("sample.MonitorID = %q, want %q", s.MonitorID, m.ID)
	}
	if s.MonitorType != string(monitor.MonitorTypeHTTP) {
		t.Errorf("sample.MonitorType = %q, want %q", s.MonitorType, monitor.MonitorTypeHTTP)
	}
	if !s.Success {
		t.Errorf("sample.Success = false, want true")
	}
	if s.HTTPStatusCode == nil || *s.HTTPStatusCode != 200 {
		t.Errorf("sample.HTTPStatusCode = %v, want 200", s.HTTPStatusCode)
	}
}

// TestPipeline_FailedCheckSampleHasNoStatus covers the SPEC §14.3 rule that a
// failed check without an HTTP status must omit the status field — the
// pipeline forwards the probe's nil HTTPStatusCode untouched so the TSDB
// writer can suppress the status series.
func TestPipeline_FailedCheckSampleHasNoStatus(t *testing.T) {
	f := newFixture(t)
	m := f.createMonitor(t)
	ctx := context.Background()

	f.prober.queue = []probeOutcome{{success: false, errStr: "dial tcp: timeout"}}
	f.pipeline.Run(ctx, *m, false)

	got := f.samples.Samples()
	if len(got) != 1 {
		t.Fatalf("samples written = %d, want 1", len(got))
	}
	if got[0].HTTPStatusCode != nil {
		t.Errorf("sample.HTTPStatusCode = %v, want nil for transport failure", *got[0].HTTPStatusCode)
	}
	if got[0].Success {
		t.Errorf("sample.Success = true, want false")
	}
}
