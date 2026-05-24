package notify

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// Pipeline delivers notifications to configured targets (SPEC §18.6). It owns an
// in-memory job queue served by a bounded worker pool: the check pipeline calls
// Enqueue when an incident opens or resolves, a worker fans the job out to every
// enabled target, and each send is retried with bounded exponential backoff and
// recorded as a notification_attempts row.
//
// Spam suppression (SPEC §18.8) is enforced here as a backstop: at most one
// delivery cycle per (incident, event type), so a repeated enqueue for an
// already-handled incident is dropped even if the caller's transition logic
// double-fires.
//
// Secrets never reach the logs (SPEC §18.9, §23): the pipeline logs only target
// IDs, provider kinds, event types and summarised errors — never a target's
// Config, which holds tokens and passwords.
type Pipeline struct {
	registry *Registry
	targets  TargetLister
	attempts AttemptRecorder
	log      *slog.Logger
	cfg      RetryConfig
	workers  int
	// sleep waits for d or until ctx is cancelled; it is a field so tests can
	// substitute an instant, recording implementation.
	sleep func(ctx context.Context, d time.Duration) error

	jobs     chan Job
	wg       sync.WaitGroup
	done     chan struct{}
	stopOnce sync.Once

	mu   sync.Mutex
	seen map[string]bool
}

// RetryConfig is the bounded-exponential-backoff policy (SPEC §18.7). It mirrors
// the relevant fields of config.NotificationConfig; the service maps one onto
// the other so the notify package stays independent of the config package.
type RetryConfig struct {
	MaxAttempts       int
	InitialRetryDelay time.Duration
	MaxRetryDelay     time.Duration
}

// Job is one unit of fan-out work: deliver Message to every enabled target.
// IncidentID links the resulting attempts to their incident and keys spam
// suppression (empty for events with no incident); EventID is recorded on each
// attempt for the audit trail.
type Job struct {
	Message    Message
	IncidentID string
	EventID    string
}

// TargetLister loads notification targets with their (unredacted) secret config
// so the pipeline can authenticate to each provider. NotificationTargetRepo's
// ListWithSecrets satisfies this; the interface keeps notify free of an import
// cycle with the store package.
type TargetLister interface {
	ListWithSecrets(ctx context.Context) ([]*Target, error)
}

// AttemptRecorder persists a single delivery attempt. NotificationAttemptRepo's
// Insert satisfies this.
type AttemptRecorder interface {
	Insert(ctx context.Context, a *Attempt) error
}

// Option customises a Pipeline at construction.
type Option func(*Pipeline)

// WithWorkers sets the number of delivery workers. Values below 1 are ignored.
func WithWorkers(n int) Option {
	return func(p *Pipeline) {
		if n > 0 {
			p.workers = n
		}
	}
}

// WithSleep substitutes the backoff wait function. Used by tests to avoid real
// delays; a nil fn is ignored.
func WithSleep(fn func(ctx context.Context, d time.Duration) error) Option {
	return func(p *Pipeline) {
		if fn != nil {
			p.sleep = fn
		}
	}
}

const (
	defaultWorkers = 4
	jobQueueSize   = 256
)

// NewPipeline builds a delivery pipeline. reg resolves a target's kind to its
// provider; targets and attempts are the persistence ports; cfg is the retry
// policy; log may be nil (a discard logger is used).
func NewPipeline(reg *Registry, targets TargetLister, attempts AttemptRecorder, cfg RetryConfig, log *slog.Logger, opts ...Option) *Pipeline {
	p := &Pipeline{
		registry: reg,
		targets:  targets,
		attempts: attempts,
		log:      ensureLogger(log),
		cfg:      cfg,
		workers:  defaultWorkers,
		sleep:    contextSleep,
		jobs:     make(chan Job, jobQueueSize),
		done:     make(chan struct{}),
		seen:     map[string]bool{},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Start launches the worker pool. Deliveries use ctx as their base context, so
// cancelling it interrupts in-flight sends and backoff waits (SPEC §9.3).
func (p *Pipeline) Start(ctx context.Context) {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.run(ctx)
	}
}

// run is one worker loop. It exits when the base context is cancelled or Stop
// closes done; a delivery already in progress is allowed to finish.
func (p *Pipeline) run(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-p.done:
			return
		case <-ctx.Done():
			return
		case job := <-p.jobs:
			p.deliver(ctx, job)
		}
	}
}

// Enqueue submits a job for asynchronous delivery. It returns without blocking
// once the pipeline is stopped, so a late enqueue during shutdown is dropped
// rather than panicking on a closed channel.
func (p *Pipeline) Enqueue(job Job) {
	select {
	case p.jobs <- job:
	case <-p.done:
	}
}

// Stop signals the workers to finish and waits for in-flight deliveries to
// complete. Queued-but-unstarted jobs are abandoned (MVP jobs are not durable,
// SPEC §18.6). Stop is idempotent.
func (p *Pipeline) Stop() {
	p.stopOnce.Do(func() { close(p.done) })
	p.wg.Wait()
}

// Test delivers a one-off manual_test message to a single target and records
// the attempt. It never retries (SPEC §18.7) and is exempt from spam
// suppression; it returns the provider's send error so the test IPC endpoint
// can report the outcome. Unlike fan-out delivery it ignores target.Enabled, so
// an operator can verify a target's config before enabling it.
func (p *Pipeline) Test(ctx context.Context, target *Target, msg Message) error {
	prov, err := p.registry.Lookup(target.Kind)
	if err != nil {
		return err
	}
	rendered, err := Render(msg)
	if err != nil {
		return err
	}
	return p.sendWithRetry(ctx, prov, target, rendered, attemptLink{})
}

// deliver fans one job out to every enabled target. A job whose incident has
// already been handled is suppressed; a render or target-load failure aborts
// the whole job; a single unknown-provider target is skipped without affecting
// the others.
func (p *Pipeline) deliver(ctx context.Context, job Job) {
	if !p.claim(job) {
		p.log.Debug("notification suppressed; already delivered for incident",
			"incident_id", job.IncidentID, "event_type", job.Message.EventType)
		return
	}
	rendered, err := Render(job.Message)
	if err != nil {
		p.log.Error("render notification message",
			"event_type", job.Message.EventType, "monitor_id", job.Message.MonitorID, "error", err)
		return
	}
	targets, err := p.targets.ListWithSecrets(ctx)
	if err != nil {
		p.log.Error("load notification targets", "error", err)
		return
	}
	link := attemptLink{incidentID: job.IncidentID, eventID: job.EventID}
	for _, t := range targets {
		if !t.Enabled {
			continue
		}
		prov, err := p.registry.Lookup(t.Kind)
		if err != nil {
			p.log.Error("resolve notification provider",
				"target_id", t.ID, "provider_kind", t.Kind, "error", err)
			continue
		}
		if err := p.sendWithRetry(ctx, prov, t, rendered, link); err != nil {
			p.log.Warn("notification delivery failed",
				"target_id", t.ID, "provider_kind", t.Kind,
				"event_type", rendered.EventType, "error", err)
		}
	}
}

// attemptLink carries the incident/event identifiers recorded on each attempt.
type attemptLink struct {
	incidentID string
	eventID    string
}

// sendWithRetry delivers msg to one target, recording one attempt per try and
// returning the last send error (nil on success). It retries failed sends with
// bounded exponential backoff up to cfg.MaxAttempts; a manual test is delivered
// exactly once regardless of MaxAttempts (SPEC §18.7).
func (p *Pipeline) sendWithRetry(ctx context.Context, prov Provider, target *Target, msg Message, link attemptLink) error {
	maxAttempts := p.cfg.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if msg.EventType == EventManualTest {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		sendErr := prov.Send(ctx, target.Config, msg)
		p.recordAttempt(ctx, target, msg, link, attempt, sendErr)
		if sendErr == nil {
			return nil
		}
		lastErr = sendErr
		if attempt == maxAttempts {
			break
		}
		if err := p.sleep(ctx, backoffDelay(attempt, p.cfg.InitialRetryDelay, p.cfg.MaxRetryDelay)); err != nil {
			// The base context was cancelled (shutdown); stop retrying.
			return err
		}
	}
	return lastErr
}

// recordAttempt persists one attempt. A failed audit insert is logged (without
// secrets) but never propagated: the send already happened, and an unrecorded
// attempt must not crash a worker or trigger a phantom retry.
func (p *Pipeline) recordAttempt(ctx context.Context, t *Target, msg Message, link attemptLink, attemptNumber int, sendErr error) {
	now := time.Now().UTC()
	a := &Attempt{
		ID:            newID(),
		TargetID:      t.ID,
		MonitorID:     nonEmpty(msg.MonitorID),
		IncidentID:    nonEmpty(link.incidentID),
		EventID:       nonEmpty(link.eventID),
		EventType:     msg.EventType,
		AttemptNumber: attemptNumber,
		CreatedAt:     now,
	}
	if sendErr == nil {
		a.Status = AttemptStatusSuccess
		sent := now
		a.SentAt = &sent
	} else {
		a.Status = AttemptStatusFailure
		// Provider errors are written to be free of secrets (SPEC §18.9); they
		// are stored verbatim as the attempt's failure reason.
		a.Error = sendErr.Error()
	}
	if err := p.attempts.Insert(ctx, a); err != nil {
		p.log.Error("record notification attempt",
			"target_id", t.ID, "provider_kind", t.Kind, "error", err)
	}
}

// claim reserves a (incident, event type) pair for delivery, returning false if
// it was already claimed. Jobs without an incident (e.g. manual tests) are never
// suppressed. The reserved set lives only in memory; MVP suppression does not
// survive a restart (SPEC §18.6).
func (p *Pipeline) claim(job Job) bool {
	if job.IncidentID == "" {
		return true
	}
	key := job.IncidentID + ":" + job.Message.EventType
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.seen[key] {
		return false
	}
	p.seen[key] = true
	return true
}

// backoffDelay returns the wait before the (attempt+1)-th try: initial doubled
// for each prior attempt, capped at maxDelay (SPEC §18.7). attempt is 1-based. A
// non-positive initial disables waiting.
func backoffDelay(attempt int, initial, maxDelay time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if initial <= 0 {
		return 0
	}
	d := initial
	for i := 1; i < attempt; i++ {
		if maxDelay > 0 && d >= maxDelay {
			return maxDelay
		}
		d *= 2
	}
	if maxDelay > 0 && d > maxDelay {
		return maxDelay
	}
	return d
}

// contextSleep waits for d or until ctx is cancelled, returning ctx.Err() if
// cancelled first. It is the default Pipeline.sleep.
func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// nonEmpty returns a pointer to s, or nil when s is empty, so optional foreign
// keys (monitor/incident/event) are stored as SQL NULL rather than "".
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ensureLogger(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// idMu guards idGen: ulid.MonotonicEntropy is not safe for concurrent use.
// newID lives here rather than reusing monitor.NewID so the notify package
// stays independent of the monitor package (mirroring the FieldError split),
// which keeps the future check-pipeline wiring free of an import cycle.
var (
	idMu  sync.Mutex
	idGen = ulid.Monotonic(rand.Reader, 0)
)

func newID() string {
	idMu.Lock()
	defer idMu.Unlock()
	return ulid.MustNew(ulid.Now(), idGen).String()
}
