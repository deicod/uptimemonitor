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

// Pipeline executes one check and persists every consequence (SPEC §17.3): a
// check_result row, the new monitor_states row, a monitor_state_changed event
// on transitions, and the matching incident_opened / incident_resolved event
// plus incident row when the transition opens or resolves downtime. Queueing
// notifications is deferred to M9.11.
type Pipeline struct {
	prober    Prober
	checks    CheckResultRepo
	states    StateRepo
	events    EventRepo
	incidents IncidentRepo
	logger    *slog.Logger
}

// New builds a Pipeline. All dependencies are required; the pipeline does no
// I/O until Run is called. A nil logger falls back to slog.Default so the
// pipeline always has somewhere to surface persistence errors.
func New(prober Prober, checks CheckResultRepo, states StateRepo, events EventRepo, incidents IncidentRepo, logger *slog.Logger) *Pipeline {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pipeline{
		prober:    prober,
		checks:    checks,
		states:    states,
		events:    events,
		incidents: incidents,
		logger:    logger,
	}
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
		if err := p.incidents.Open(ctx, &monitor.Incident{
			ID:           monitor.NewID(),
			MonitorID:    m.ID,
			StartedAt:    now,
			StartEventID: evID,
			Reason:       cr.Error,
		}); err != nil {
			return fmt.Errorf("pipeline: open incident: %w", err)
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
