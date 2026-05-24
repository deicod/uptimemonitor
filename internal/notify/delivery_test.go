package notify

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// errSend is the sentinel a stubProvider returns to simulate a transport
// failure, so tests can assert the exact error propagates back via errors.Is.
var errSend = errors.New("stub send failure")

// stubProvider is a recording notify.Provider for delivery tests. results[i]
// is returned on the (i+1)-th Send; once the slice is exhausted the last entry
// repeats, so a single-element {errSend} models "always fails" and {errSend,
// nil} models "fails once then succeeds". It counts calls so tests can prove
// the retry loop stopped (or didn't).
type stubProvider struct {
	kind    string
	mu      sync.Mutex
	calls   int
	results []error
	msgs    []Message
}

func (s *stubProvider) Kind() string                                    { return s.kind }
func (s *stubProvider) DisplayName() string                             { return s.kind }
func (s *stubProvider) Fields() []Field                                 { return []Field{} }
func (s *stubProvider) Validate(context.Context, json.RawMessage) error { return nil }

func (s *stubProvider) Send(_ context.Context, _ json.RawMessage, msg Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.msgs = append(s.msgs, msg)
	if len(s.results) == 0 {
		return nil
	}
	idx := s.calls - 1
	if idx >= len(s.results) {
		idx = len(s.results) - 1
	}
	return s.results[idx]
}

func (s *stubProvider) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// stubTargets is an in-memory TargetLister.
type stubTargets struct {
	targets []*Target
	err     error
}

func (s *stubTargets) ListWithSecrets(context.Context) ([]*Target, error) {
	return s.targets, s.err
}

// stubAttempts is an in-memory AttemptRecorder. onInsert, if set, fires after
// each insert so an async (queue) test can wait for delivery to complete.
type stubAttempts struct {
	mu       sync.Mutex
	inserted []*Attempt
	onInsert func(*Attempt)
}

func (s *stubAttempts) Insert(_ context.Context, a *Attempt) error {
	s.mu.Lock()
	s.inserted = append(s.inserted, a)
	fn := s.onInsert
	s.mu.Unlock()
	if fn != nil {
		fn(a)
	}
	return nil
}

func (s *stubAttempts) all() []*Attempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Attempt, len(s.inserted))
	copy(out, s.inserted)
	return out
}

// recordingSleeper captures the backoff delays the pipeline waits between
// retries, and never actually sleeps, so retry tests run instantly.
type recordingSleeper struct {
	mu    sync.Mutex
	calls []time.Duration
}

func (r *recordingSleeper) sleep(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, d)
	return nil
}

func (r *recordingSleeper) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

const testRetryDelay = 5 * time.Second
const testMaxDelay = 60 * time.Second

func testCfg(maxAttempts int) RetryConfig {
	return RetryConfig{
		MaxAttempts:       maxAttempts,
		InitialRetryDelay: testRetryDelay,
		MaxRetryDelay:     testMaxDelay,
	}
}

func enabledTarget(id, kind string) *Target {
	return &Target{ID: id, Name: id, Kind: kind, Enabled: true, Config: json.RawMessage(`{}`)}
}

func newTestRegistry(t *testing.T, p Provider) *Registry {
	t.Helper()
	reg := NewRegistry()
	if err := reg.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return reg
}

// TestBackoffDelay pins the SPEC §18.7 schedule: bounded exponential backoff.
// The doubling matters so transient outages get progressively more breathing
// room, and the cap matters so a long outage never stretches the delay to
// minutes/hours and starves recovery notifications. A test that only checked
// the first delay would miss a missing cap, so every boundary is covered.
func TestBackoffDelay(t *testing.T) {
	cases := []struct {
		name    string
		attempt int
		initial time.Duration
		max     time.Duration
		want    time.Duration
	}{
		{"first wait is initial", 1, 5 * time.Second, 60 * time.Second, 5 * time.Second},
		{"second wait doubles", 2, 5 * time.Second, 60 * time.Second, 10 * time.Second},
		{"third wait doubles again", 3, 5 * time.Second, 60 * time.Second, 20 * time.Second},
		{"fourth wait doubles again", 4, 5 * time.Second, 60 * time.Second, 40 * time.Second},
		{"caps at max", 5, 5 * time.Second, 60 * time.Second, 60 * time.Second},
		{"stays capped", 6, 5 * time.Second, 60 * time.Second, 60 * time.Second},
		{"initial above max is capped", 1, 90 * time.Second, 60 * time.Second, 60 * time.Second},
		{"zero initial yields zero", 3, 0, 60 * time.Second, 0},
		{"attempt below one treated as first", 0, 5 * time.Second, 60 * time.Second, 5 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := backoffDelay(tc.attempt, tc.initial, tc.max); got != tc.want {
				t.Errorf("backoffDelay(%d, %v, %v) = %v, want %v",
					tc.attempt, tc.initial, tc.max, got, tc.want)
			}
		})
	}
}

// TestDeliverRetriesToMaxAttemptsThenFails covers the core retry contract
// (SPEC §18.7): a permanently failing provider is tried exactly MaxAttempts
// times, each try is recorded as a failure attempt with an increasing number,
// and the loop backs off between tries. Without retry the service would give up
// on the first transient blip; without a bound it would retry forever.
func TestDeliverRetriesToMaxAttemptsThenFails(t *testing.T) {
	prov := &stubProvider{kind: "fake", results: []error{errSend}}
	attempts := &stubAttempts{}
	sleeper := &recordingSleeper{}
	p := NewPipeline(newTestRegistry(t, prov),
		&stubTargets{targets: []*Target{enabledTarget("t1", "fake")}},
		attempts, testCfg(3), nil, WithSleep(sleeper.sleep))

	job := Job{Message: NewMonitorDownMessage("m1", "Mon 1", time.Now()), IncidentID: "I1"}
	p.deliver(context.Background(), job)

	if prov.callCount() != 3 {
		t.Fatalf("provider Send calls = %d, want 3", prov.callCount())
	}
	if sleeper.count() != 2 {
		t.Errorf("backoff waits = %d, want 2 (between 3 attempts)", sleeper.count())
	}
	recorded := attempts.all()
	if len(recorded) != 3 {
		t.Fatalf("recorded attempts = %d, want 3", len(recorded))
	}
	for i, a := range recorded {
		if a.Status != AttemptStatusFailure {
			t.Errorf("attempt %d status = %q, want %q", i, a.Status, AttemptStatusFailure)
		}
		if a.AttemptNumber != i+1 {
			t.Errorf("attempt %d number = %d, want %d", i, a.AttemptNumber, i+1)
		}
		if a.EventType != EventMonitorDown {
			t.Errorf("attempt %d event type = %q, want %q", i, a.EventType, EventMonitorDown)
		}
		if a.IncidentID == nil || *a.IncidentID != "I1" {
			t.Errorf("attempt %d incident id = %v, want I1", i, a.IncidentID)
		}
		if a.Error == "" {
			t.Errorf("attempt %d has empty error, want the send failure recorded", i)
		}
		if a.SentAt != nil {
			t.Errorf("attempt %d SentAt = %v, want nil for a failed send", i, a.SentAt)
		}
	}
}

// TestDeliverSuccessStopsRetrying proves the loop terminates on the first
// success (SPEC §18.7): a provider that fails once then succeeds must be called
// exactly twice — not a third time — and the recorded attempts must end in a
// success carrying a SentAt timestamp. A regression that kept retrying after a
// success would double-send recovery/down messages.
func TestDeliverSuccessStopsRetrying(t *testing.T) {
	prov := &stubProvider{kind: "fake", results: []error{errSend, nil}}
	attempts := &stubAttempts{}
	sleeper := &recordingSleeper{}
	p := NewPipeline(newTestRegistry(t, prov),
		&stubTargets{targets: []*Target{enabledTarget("t1", "fake")}},
		attempts, testCfg(3), nil, WithSleep(sleeper.sleep))

	p.deliver(context.Background(), Job{
		Message:    NewMonitorRecoveredMessage("m1", "Mon 1", time.Now()),
		IncidentID: "I1",
	})

	if prov.callCount() != 2 {
		t.Fatalf("provider Send calls = %d, want 2 (stop after success)", prov.callCount())
	}
	if sleeper.count() != 1 {
		t.Errorf("backoff waits = %d, want 1", sleeper.count())
	}
	recorded := attempts.all()
	if len(recorded) != 2 {
		t.Fatalf("recorded attempts = %d, want 2", len(recorded))
	}
	if recorded[0].Status != AttemptStatusFailure {
		t.Errorf("first attempt status = %q, want %q", recorded[0].Status, AttemptStatusFailure)
	}
	last := recorded[1]
	if last.Status != AttemptStatusSuccess {
		t.Errorf("final attempt status = %q, want %q", last.Status, AttemptStatusSuccess)
	}
	if last.SentAt == nil {
		t.Error("successful attempt SentAt = nil, want a timestamp")
	}
	if last.Error != "" {
		t.Errorf("successful attempt error = %q, want empty", last.Error)
	}
}

// TestDeliverSpamSuppression encodes SPEC §18.8: at most one delivery cycle per
// (incident, event type). The same down job re-delivered for an already-handled
// incident must be dropped, while a *different* incident and the *recovery* of
// the same incident must still go through — proving the guard keys on both the
// incident and the event type rather than blanket-suppressing a monitor.
func TestDeliverSpamSuppression(t *testing.T) {
	prov := &stubProvider{kind: "fake"}
	attempts := &stubAttempts{}
	p := NewPipeline(newTestRegistry(t, prov),
		&stubTargets{targets: []*Target{enabledTarget("t1", "fake")}},
		attempts, testCfg(3), nil)

	ctx := context.Background()
	down1 := Job{Message: NewMonitorDownMessage("m1", "Mon 1", time.Now()), IncidentID: "I1"}

	p.deliver(ctx, down1)
	p.deliver(ctx, down1) // duplicate down for I1 — must be suppressed
	if prov.callCount() != 1 {
		t.Fatalf("after duplicate down, Send calls = %d, want 1", prov.callCount())
	}

	// A different incident is independent and must be delivered.
	p.deliver(ctx, Job{Message: NewMonitorDownMessage("m1", "Mon 1", time.Now()), IncidentID: "I2"})
	if prov.callCount() != 2 {
		t.Fatalf("after second incident, Send calls = %d, want 2", prov.callCount())
	}

	// The recovery of I1 is a different event type — it must not be suppressed.
	p.deliver(ctx, Job{Message: NewMonitorRecoveredMessage("m1", "Mon 1", time.Now()), IncidentID: "I1"})
	if prov.callCount() != 3 {
		t.Fatalf("after recovery of I1, Send calls = %d, want 3", prov.callCount())
	}
	if got := len(attempts.all()); got != 3 {
		t.Errorf("recorded attempts = %d, want 3", got)
	}
}

// TestTestNotRetried covers SPEC §18.7's manual-test exception: a manual test
// is a single, user-initiated probe of one target and must never be retried,
// even when MaxAttempts is 3 and the send fails. The send error is returned so
// the test IPC endpoint can report the failure to the operator immediately.
func TestTestNotRetried(t *testing.T) {
	prov := &stubProvider{kind: "fake", results: []error{errSend}}
	attempts := &stubAttempts{}
	sleeper := &recordingSleeper{}
	p := NewPipeline(newTestRegistry(t, prov),
		&stubTargets{}, attempts, testCfg(3), nil, WithSleep(sleeper.sleep))

	target := enabledTarget("t1", "fake")
	msg := NewManualTestMessage("m1", "Mon 1", time.Now())
	err := p.Test(context.Background(), target, msg)

	if !errors.Is(err, errSend) {
		t.Fatalf("Test error = %v, want errSend", err)
	}
	if prov.callCount() != 1 {
		t.Errorf("provider Send calls = %d, want 1 (no retry for manual test)", prov.callCount())
	}
	if sleeper.count() != 0 {
		t.Errorf("backoff waits = %d, want 0", sleeper.count())
	}
	recorded := attempts.all()
	if len(recorded) != 1 {
		t.Fatalf("recorded attempts = %d, want 1", len(recorded))
	}
	if recorded[0].EventType != EventManualTest {
		t.Errorf("attempt event type = %q, want %q", recorded[0].EventType, EventManualTest)
	}
	if recorded[0].Status != AttemptStatusFailure {
		t.Errorf("attempt status = %q, want %q", recorded[0].Status, AttemptStatusFailure)
	}
}

// TestDeliverSkipsDisabledTargets pins SPEC §18.6: a job fans out to every
// globally-enabled target only. A disabled target must receive nothing, so an
// operator can silence one destination without deleting it.
func TestDeliverSkipsDisabledTargets(t *testing.T) {
	prov := &stubProvider{kind: "fake"}
	attempts := &stubAttempts{}
	disabled := enabledTarget("t2", "fake")
	disabled.Enabled = false
	p := NewPipeline(newTestRegistry(t, prov),
		&stubTargets{targets: []*Target{enabledTarget("t1", "fake"), disabled}},
		attempts, testCfg(3), nil)

	p.deliver(context.Background(), Job{
		Message:    NewMonitorDownMessage("m1", "Mon 1", time.Now()),
		IncidentID: "I1",
	})

	if prov.callCount() != 1 {
		t.Fatalf("provider Send calls = %d, want 1 (disabled target skipped)", prov.callCount())
	}
	recorded := attempts.all()
	if len(recorded) != 1 || recorded[0].TargetID != "t1" {
		t.Fatalf("recorded attempts = %+v, want one for t1", recorded)
	}
}

// TestEnqueueProcessesJob is the queue smoke test: a job handed to Enqueue is
// picked up by a worker and actually delivered (SPEC §18.6 in-memory queue with
// workers). It waits on an insert signal rather than a sleep so it stays
// deterministic, then Stop drains the workers.
func TestEnqueueProcessesJob(t *testing.T) {
	prov := &stubProvider{kind: "fake"}
	done := make(chan struct{}, 1)
	attempts := &stubAttempts{onInsert: func(*Attempt) {
		select {
		case done <- struct{}{}:
		default:
		}
	}}
	p := NewPipeline(newTestRegistry(t, prov),
		&stubTargets{targets: []*Target{enabledTarget("t1", "fake")}},
		attempts, testCfg(1), nil, WithWorkers(1))

	p.Start(context.Background())
	p.Enqueue(Job{Message: NewMonitorDownMessage("m1", "Mon 1", time.Now()), IncidentID: "I1"})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueued job was not delivered within 2s")
	}
	p.Stop()

	if prov.callCount() != 1 {
		t.Errorf("provider Send calls = %d, want 1", prov.callCount())
	}
}
