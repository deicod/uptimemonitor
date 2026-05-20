package probe

import (
	"context"

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
