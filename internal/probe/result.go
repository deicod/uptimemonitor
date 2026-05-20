// Package probe defines the runner interface and result type that every
// monitor probe implementation produces. It is intentionally storage- and
// transport-agnostic: a Result describes only what a probe observed, leaving
// state-machine classification, persistence, and ID assignment to higher
// layers (SPEC §15.1, §17).
package probe

import "time"

// Result is the outcome of a single probe execution (SPEC §15). A zero Result
// represents a failed check with no extra information; runners populate the
// fields they own. The probe layer never assigns IDs or monitor state — the
// check pipeline derives those from the Result and the previous MonitorStatus.
type Result struct {
	// StartedAt is when the probe began executing.
	StartedAt time.Time `json:"started_at"`
	// FinishedAt is when the probe completed, regardless of success.
	FinishedAt time.Time `json:"finished_at"`
	// Duration is the wall-clock time the probe took. It is recorded
	// separately from the timestamps so callers do not have to recompute it
	// and so probes that wrap an external clock can report a meaningful
	// duration even when the timestamps are quantised.
	Duration time.Duration `json:"duration"`
	// Success reports whether the probe met its success criteria
	// (SPEC §15.3). Transport errors and out-of-range responses are both
	// failures.
	Success bool `json:"success"`
	// Error is a sanitised, human-readable description of the failure when
	// Success is false. It must not contain secrets or raw request data
	// (SPEC §15.4, §23).
	Error string `json:"error,omitempty"`
	// HTTPStatusCode is the HTTP response status, when one was received.
	// It is a pointer so the absence of a status (e.g. on a transport
	// error) can be distinguished from a real 0 status.
	HTTPStatusCode *int `json:"http_status_code,omitempty"`
}
