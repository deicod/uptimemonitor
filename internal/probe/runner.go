package probe

import (
	"context"
	"fmt"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// Runner executes a single probe for monitors of a specific MonitorType
// (SPEC §15.1). Implementations are stateless with respect to a given
// monitor: each Run call is independent and must be safe to invoke
// concurrently for distinct monitors.
//
// Run returns a Result describing the observation and an error only for
// internal/programmer errors that prevent producing any Result at all
// (e.g. a malformed monitor config). Probe-level failures such as transport
// errors or out-of-range responses are reported through Result.Success and
// Result.Error, not via the error return (SPEC §15.3–15.4).
type Runner interface {
	Type() monitor.MonitorType
	Run(ctx context.Context, m monitor.Monitor) (Result, error)
}

// Dispatcher routes a check to the Runner registered for the monitor's type
// and assembles a monitor.CheckResult from the probe's observation (SPEC §15).
// State classification belongs to the state machine (M7.4), so Dispatch leaves
// the CheckResult.State field at its zero value for the pipeline to fill in.
type Dispatcher struct {
	runners map[monitor.MonitorType]Runner
}

// NewDispatcher returns a Dispatcher pre-registered with the MVP HTTP runner
// (SPEC §15.2). Tests and future monitor types can override or extend the
// registry via Register before the dispatcher is shared with other goroutines.
func NewDispatcher() *Dispatcher {
	d := &Dispatcher{runners: make(map[monitor.MonitorType]Runner)}
	d.Register(NewHTTPRunner())
	return d
}

// Register associates r with the MonitorType it advertises via Type(). The
// registry is not safe for concurrent mutation; call Register only during
// dispatcher setup, before passing the dispatcher to the scheduler.
func (d *Dispatcher) Register(r Runner) {
	d.runners[r.Type()] = r
}

// Dispatch finds the Runner for m.Type, executes it, and returns a
// monitor.CheckResult populated with the probe observation. An unknown
// MonitorType, or a Runner-level error, is returned via the error return so
// the pipeline can distinguish setup/programmer errors from real probe
// failures (which surface in the CheckResult itself).
func (d *Dispatcher) Dispatch(ctx context.Context, m monitor.Monitor) (monitor.CheckResult, error) {
	r, ok := d.runners[m.Type]
	if !ok {
		return monitor.CheckResult{}, fmt.Errorf("no runner registered for monitor type %q", m.Type)
	}
	res, err := r.Run(ctx, m)
	if err != nil {
		return monitor.CheckResult{}, err
	}
	return monitor.CheckResult{
		ID:             monitor.NewID(),
		MonitorID:      m.ID,
		StartedAt:      res.StartedAt,
		FinishedAt:     res.FinishedAt,
		Duration:       res.Duration,
		Success:        res.Success,
		Error:          res.Error,
		HTTPStatusCode: res.HTTPStatusCode,
	}, nil
}
