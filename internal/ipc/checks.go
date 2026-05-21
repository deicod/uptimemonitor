package ipc

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
	"github.com/deicod/uptimemonitor/internal/store/sqlite"
)

// CheckResultResponse is the DTO for a single check observation
// (SPEC §11.3, §10.5).
type CheckResultResponse struct {
	ID             string    `json:"id"`
	MonitorID      string    `json:"monitor_id"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	DurationMs     int64     `json:"duration_ms"`
	Success        bool      `json:"success"`
	State          string    `json:"state"`
	Error          string    `json:"error,omitempty"`
	HTTPStatusCode *int      `json:"http_status_code,omitempty"`
}

// CheckResultListResponse is the DTO returned by GET /v1/monitors/{id}/checks.
type CheckResultListResponse struct {
	Checks []CheckResultResponse `json:"checks"`
}

// RunMonitorResponse is the DTO returned by POST /v1/monitors/{id}/run.
// "queued" indicates the manual check has been accepted for asynchronous
// execution (SPEC §10.5).
type RunMonitorResponse struct {
	Status string `json:"status"`
}

// ManualChecker enqueues an out-of-band check for a monitor. It is implemented
// by *scheduler.Scheduler. The interface is declared here so handler tests can
// substitute a fake and the IPC package does not import the scheduler.
//
// ManualTrigger returns false when the monitor is unknown to the scheduler, a
// check is already in flight for it (no-overlap rule), or the scheduler is
// shutting down (SPEC §16.3–16.4).
type ManualChecker interface {
	ManualTrigger(id string) bool
}

// CheckResultReader reads recent check observations for a monitor.
// *sqlite.CheckResultRepo satisfies it; declared here for the same reason as
// the other reader interfaces in this package.
type CheckResultReader interface {
	ListRecent(ctx context.Context, monitorID string, limit int) ([]*monitor.CheckResult, error)
}

// runMonitorHandler serves POST /v1/monitors/{id}/run. It returns 202 Accepted
// with {"status":"queued"} when the check has been enqueued. The monitor's
// existence is checked first so a missing id yields 404 before the scheduler is
// touched; a false from ManualTrigger means the scheduler refused the trigger
// (in-flight check or shutdown) and is reported as a conflict so the operator
// knows their click did not start a new check.
//
// Per SPEC §16.4 manual checks may run on disabled monitors without unpausing
// them — this handler does not branch on Enabled because the scheduler and
// state machine already encode that rule (paused stays paused on any check
// outcome).
func runMonitorHandler(svc MonitorService, checker ManualChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		if !checker.ManualTrigger(id) {
			writeAPIError(w, NewAPIError(ErrConflict, "a check is already in progress for this monitor"))
			return
		}
		writeJSON(w, http.StatusAccepted, RunMonitorResponse{Status: "queued"})
	}
}

// listMonitorChecksHandler serves GET /v1/monitors/{id}/checks?limit= (SPEC
// §10.5). The monitor must exist; ?limit= is optional and falls back to
// defaultListLimit so a missing query parameter does not return an unbounded
// result set.
func listMonitorChecksHandler(svc MonitorService, repo CheckResultReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, err := svc.Get(r.Context(), id); err != nil {
			writeAPIError(w, mapServiceError(err))
			return
		}
		limit, apiErr := parseLimit(r.URL.Query())
		if apiErr != nil {
			writeAPIError(w, apiErr)
			return
		}
		checks, err := repo.ListRecent(r.Context(), id, limit)
		if err != nil {
			writeAPIError(w, mapRepoError(err))
			return
		}
		resp := CheckResultListResponse{Checks: make([]CheckResultResponse, 0, len(checks))}
		for _, c := range checks {
			resp.Checks = append(resp.Checks, checkResultToResponse(c))
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// mapRepoError mirrors mapServiceError for repository-only errors, where a
// FieldError cannot appear.
func mapRepoError(err error) *APIError {
	if errors.Is(err, sqlite.ErrNotFound) {
		return NewAPIError(ErrNotFound, "not found")
	}
	return NewAPIError(ErrInternal, "an internal error occurred")
}

func checkResultToResponse(c *monitor.CheckResult) CheckResultResponse {
	return CheckResultResponse{
		ID:             c.ID,
		MonitorID:      c.MonitorID,
		StartedAt:      c.StartedAt,
		FinishedAt:     c.FinishedAt,
		DurationMs:     c.Duration.Milliseconds(),
		Success:        c.Success,
		State:          string(c.State),
		Error:          c.Error,
		HTTPStatusCode: c.HTTPStatusCode,
	}
}
