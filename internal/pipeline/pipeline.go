// Package pipeline wires a probe execution to its persistence side effects:
// it dispatches the probe, classifies the new monitor state via the state
// machine (SPEC §17), and writes the resulting check_result, state row,
// events, and incident open/resolve records (SPEC §17.3, §11.5–11.6).
//
// The Pipeline.Run method matches scheduler.CheckFunc so the scheduler can
// invoke it directly without knowing about persistence (SPEC §16).
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/store/tsdb"
)

// Prober executes one probe for a monitor and returns the observation as a
// CheckResult with no State field set — the pipeline assigns State after
// running the state machine. *probe.Dispatcher satisfies this interface; tests
// use a fake.
type Prober interface {
	Dispatch(ctx context.Context, m monitor.Monitor) (monitor.CheckResult, error)
}

// CheckResultRepo persists individual check observations (SPEC §12.3
// check_results). *sqlite.CheckResultRepo satisfies it.
type CheckResultRepo interface {
	Insert(ctx context.Context, cr *monitor.CheckResult) error
}

// StateRepo persists the per-monitor health snapshot row (SPEC §12.3
// monitor_states). *sqlite.MonitorStateRepo satisfies it.
type StateRepo interface {
	Get(ctx context.Context, monitorID string) (*monitor.MonitorStatus, error)
	Upsert(ctx context.Context, st *monitor.MonitorStatus) error
}

// EventRepo appends to the audit log (SPEC §11.6).
// *sqlite.EventRepo satisfies it.
type EventRepo interface {
	Insert(ctx context.Context, e *monitor.Event) error
}

// IncidentRepo opens and resolves incidents (SPEC §11.5).
// *sqlite.IncidentRepo satisfies it.
type IncidentRepo interface {
	Open(ctx context.Context, in *monitor.Incident) error
	Resolve(ctx context.Context, id string, resolvedAt time.Time, endEventID string) error
	FindOpenByMonitor(ctx context.Context, monitorID string) (*monitor.Incident, error)
}

// SampleWriter persists per-check time-series samples to the TSDB
// (SPEC §14.2–14.3). *tsdb.Store satisfies it.
type SampleWriter interface {
	WriteCheck(ctx context.Context, sample tsdb.CheckSample) error
}

// Notifier enqueues a notification job for asynchronous delivery
// (*notify.Pipeline satisfies it). The check pipeline calls it when a
// transition opens or resolves an incident (SPEC §17.3, §18.6).
type Notifier interface {
	Enqueue(job notify.Job)
}

// NotificationGate reports whether notifications are globally enabled, backing
// the settings-stored runtime toggle (SPEC §18.6, §6 decision 5). A transition
// is notified only when the gate is open, the monitor's NotificationsEnabled
// flag is set, and — downstream in the delivery pipeline — the target is
// enabled.
type NotificationGate interface {
	NotificationsEnabled(ctx context.Context) bool
}

// Pipeline executes one check and persists every consequence (SPEC §17.3): a
// check_result row, the new monitor_states row, a monitor_state_changed event
// on transitions, and the matching incident_opened / incident_resolved event
// plus incident row when the transition opens or resolves downtime. When a
// Notifier is wired (WithNotifications), it also queues monitor_down /
// monitor_recovered notifications on those transitions (SPEC §17.3, §18.6).
type Pipeline struct {
	prober    Prober
	checks    CheckResultRepo
	states    StateRepo
	events    EventRepo
	incidents IncidentRepo
	samples   SampleWriter
	logger    *slog.Logger
	notifier  Notifier
	gate      NotificationGate
}

// Option customises a Pipeline at construction.
type Option func(*Pipeline)

// WithNotifications wires notification delivery into the pipeline. When the
// notifier is nil the pipeline records transitions without notifying — the
// behaviour before M9.11. The gate may be nil, which is treated as "globally
// enabled" (the per-monitor and per-target flags still apply).
func WithNotifications(notifier Notifier, gate NotificationGate) Option {
	return func(p *Pipeline) {
		p.notifier = notifier
		p.gate = gate
	}
}

// New builds a Pipeline. All positional dependencies are required; the pipeline
// does no I/O until Run is called. A nil logger falls back to slog.Default so the
// pipeline always has somewhere to surface persistence errors.
func New(prober Prober, checks CheckResultRepo, states StateRepo, events EventRepo, incidents IncidentRepo, samples SampleWriter, logger *slog.Logger, opts ...Option) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	p := &Pipeline{
		prober:    prober,
		checks:    checks,
		states:    states,
		events:    events,
		incidents: incidents,
		samples:   samples,
		logger:    logger,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Run executes a single check for m and persists the outcome. It matches
// scheduler.CheckFunc so the scheduler invokes Run directly. The manual flag
// is informational: the state machine already encodes the "manual check on a
// paused monitor does not unpause" rule (SPEC §16.4) because paused+any leaves
// the state at paused, so Run does not branch on it.
//
// Persistence errors cannot be returned through scheduler.CheckFunc, so Run
// logs them at error level with the monitor_id and a "manual" flag. The
// scheduler keeps ticking and the next check retries; an operator watching
// logs sees the failure rather than a silent gap (SPEC §23).
func (p *Pipeline) Run(ctx context.Context, m monitor.Monitor, manual bool) {
	if err := p.run(ctx, m); err != nil {
		p.logger.Error("check pipeline failed",
			"monitor_id", m.ID,
			"manual", manual,
			"error", err.Error(),
		)
	}
}

func (p *Pipeline) run(ctx context.Context, m monitor.Monitor) error {
	cr, derr := p.prober.Dispatch(ctx, m)
	if derr != nil {
		// A dispatcher-level error means we never produced a Result (unknown
		// monitor type, malformed config). Record it as a failed check so the
		// pipeline still runs the state machine and the operator can see the
		// failure in the TUI rather than a silent gap.
		now := time.Now().UTC()
		cr = monitor.CheckResult{
			ID:         monitor.NewID(),
			MonitorID:  m.ID,
			StartedAt:  now,
			FinishedAt: now,
			Success:    false,
			Error:      "probe configuration error",
		}
	}

	status, err := p.states.Get(ctx, m.ID)
	if err != nil {
		return fmt.Errorf("pipeline: get state for %s: %w", m.ID, err)
	}

	input := monitor.InputFailure
	if cr.Success {
		input = monitor.InputSuccess
	}
	transition := monitor.NextState(status.State, input)
	cr.State = transition.To

	// Use the probe's own clock as "now" so the persisted timestamps are
	// internally consistent (event/incident times match the check's
	// finished_at). Fall back to time.Now for the synthesized
	// dispatcher-error CheckResult, whose FinishedAt is already set.
	now := cr.FinishedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if err := p.checks.Insert(ctx, &cr); err != nil {
		return fmt.Errorf("pipeline: insert check_result: %w", err)
	}

	// TSDB write is best-effort: SQLite is the authoritative record for state,
	// events, and incidents (SPEC §12, §14.1). A TSDB failure must not block
	// state transitions, so we log and continue rather than return.
	if err := p.samples.WriteCheck(ctx, tsdb.CheckSample{
		MonitorID:      m.ID,
		MonitorType:    string(m.Type),
		FinishedAt:     now,
		Success:        cr.Success,
		Duration:       cr.Duration,
		HTTPStatusCode: cr.HTTPStatusCode,
	}); err != nil {
		p.logger.Error("write tsdb samples",
			"monitor_id", m.ID,
			"error", err.Error(),
		)
	}

	if transition.EmitStateChange {
		data, _ := json.Marshal(map[string]string{
			"from": string(transition.From),
			"to":   string(transition.To),
		})
		if err := p.insertEvent(ctx, monitor.EventMonitorStateChanged, m.ID, data, now); err != nil {
			return err
		}
	}

	if transition.OpenIncident {
		evID := monitor.NewID()
		if err := p.insertEventWithID(ctx, evID, monitor.EventIncidentOpened, m.ID, nil, now); err != nil {
			return err
		}
		incidentID := monitor.NewID()
		if err := p.incidents.Open(ctx, &monitor.Incident{
			ID:           incidentID,
			MonitorID:    m.ID,
			StartedAt:    now,
			StartEventID: evID,
			Reason:       cr.Error,
		}); err != nil {
			return fmt.Errorf("pipeline: open incident: %w", err)
		}
		if transition.NotifyDown {
			p.enqueueNotification(ctx, m, notify.NewMonitorDownMessage(m.ID, m.Name, now), incidentID, evID)
		}
	}

	if transition.ResolveIncident {
		open, err := p.incidents.FindOpenByMonitor(ctx, m.ID)
		if err != nil {
			return fmt.Errorf("pipeline: find open incident: %w", err)
		}
		evID := monitor.NewID()
		if err := p.insertEventWithID(ctx, evID, monitor.EventIncidentResolved, m.ID, nil, now); err != nil {
			return err
		}
		if err := p.incidents.Resolve(ctx, open.ID, now, evID); err != nil {
			return fmt.Errorf("pipeline: resolve incident: %w", err)
		}
		if transition.NotifyRecovery {
			p.enqueueNotification(ctx, m, notify.NewMonitorRecoveredMessage(m.ID, m.Name, now), open.ID, evID)
		}
	}

	next := *status
	next.State = transition.To
	crID := cr.ID
	next.LastCheckID = &crID
	t := now
	next.LastCheckedAt = &t
	if cr.Success {
		next.LastSuccessAt = &t
		next.ConsecutiveSuccesses++
		next.ConsecutiveFailures = 0
	} else {
		next.LastFailureAt = &t
		next.ConsecutiveFailures++
		next.ConsecutiveSuccesses = 0
	}
	next.UpdatedAt = now
	if err := p.states.Upsert(ctx, &next); err != nil {
		return fmt.Errorf("pipeline: upsert monitor_state: %w", err)
	}
	return nil
}

// enqueueNotification queues msg for delivery, applying the global toggle (the
// gate) and the per-monitor NotificationsEnabled flag (SPEC §18.6, §6 decision
// 5); the per-target enabled check happens downstream in the delivery pipeline.
// It is a no-op when no notifier is wired, so a pipeline built without
// WithNotifications records transitions without notifying.
func (p *Pipeline) enqueueNotification(ctx context.Context, m monitor.Monitor, msg notify.Message, incidentID, eventID string) {
	if p.notifier == nil || !m.NotificationsEnabled {
		return
	}
	if p.gate != nil && !p.gate.NotificationsEnabled(ctx) {
		return
	}
	p.notifier.Enqueue(notify.Job{Message: msg, IncidentID: incidentID, EventID: eventID})
}

// insertEvent appends a monitor-scoped event with a freshly minted ID.
func (p *Pipeline) insertEvent(ctx context.Context, evType, monitorID string, data json.RawMessage, at time.Time) error {
	return p.insertEventWithID(ctx, monitor.NewID(), evType, monitorID, data, at)
}

// insertEventWithID appends a monitor-scoped event with the supplied ID so the
// caller can reference it (e.g. as an incident's start_event_id).
func (p *Pipeline) insertEventWithID(ctx context.Context, id, evType, monitorID string, data json.RawMessage, at time.Time) error {
	mID := monitorID
	if err := p.events.Insert(ctx, &monitor.Event{
		ID:        id,
		Type:      evType,
		MonitorID: &mID,
		Data:      data,
		CreatedAt: at,
	}); err != nil {
		return fmt.Errorf("pipeline: insert %s event: %w", evType, err)
	}
	return nil
}
