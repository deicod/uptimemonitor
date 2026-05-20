package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// HTTPRunner executes HTTP monitor probes (SPEC §15.2). It is a thin wrapper
// around a single *http.Client so connections can be pooled across checks; the
// per-monitor timeout is applied via the request context rather than the
// client to keep one runner instance safe for concurrent use across distinct
// monitors.
type HTTPRunner struct {
	client *http.Client
}

// NewHTTPRunner returns an HTTPRunner ready to execute checks. The default
// http.Client follows Go's standard redirect policy (up to 10), matching the
// MVP behavior described in SPEC §15.2.
func NewHTTPRunner() *HTTPRunner {
	return &HTTPRunner{client: &http.Client{}}
}

// Type reports that this runner handles HTTP monitors.
func (r *HTTPRunner) Type() monitor.MonitorType { return monitor.MonitorTypeHTTP }

// Run executes a single HTTP check against m. Transport errors and
// out-of-range responses are returned as failed Results; the error return is
// reserved for malformed configuration that prevents producing a Result at
// all (SPEC §15.1).
func (r *HTTPRunner) Run(ctx context.Context, m monitor.Monitor) (Result, error) {
	var cfg monitor.HTTPMonitorConfig
	if err := json.Unmarshal(m.Config, &cfg); err != nil {
		return Result{}, fmt.Errorf("decode http monitor config: %w", err)
	}
	if err := monitor.ValidateHTTPConfig(&cfg); err != nil {
		return Result{}, fmt.Errorf("validate http monitor config: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(runCtx, cfg.Method, cfg.URL, nil)
	started := time.Now()
	if err != nil {
		finished := time.Now()
		return Result{
			StartedAt:  started,
			FinishedAt: finished,
			Duration:   finished.Sub(started),
			Error:      "invalid request",
		}, nil
	}

	resp, err := r.client.Do(req)
	if err != nil {
		finished := time.Now()
		return Result{
			StartedAt:  started,
			FinishedAt: finished,
			Duration:   finished.Sub(started),
			Error:      sanitizeTransportError(err),
		}, nil
	}
	defer resp.Body.Close()
	// Drain the body so the underlying connection can be reused on the next
	// check. The body content itself is not inspected (SPEC §15.3 classifies
	// by status code only for MVP).
	_, _ = io.Copy(io.Discard, resp.Body)

	finished := time.Now()
	status := resp.StatusCode
	res := Result{
		StartedAt:      started,
		FinishedAt:     finished,
		Duration:       finished.Sub(started),
		HTTPStatusCode: &status,
	}
	if status >= cfg.ExpectedStatusMin && status <= cfg.ExpectedStatusMax {
		res.Success = true
		return res, nil
	}
	res.Error = fmt.Sprintf("status %d outside expected range %d-%d", status, cfg.ExpectedStatusMin, cfg.ExpectedStatusMax)
	return res, nil
}

// sanitizeTransportError maps a transport-layer error to a short, human
// readable description that never includes the target URL, host, or any
// request payload (SPEC §15.4, §23). Categorising by error kind lets the TUI
// and logs show something useful while keeping potentially sensitive request
// data out of persisted check results.
func sanitizeTransportError(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "request timed out"
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok && urlErr.Timeout() {
		return "request timed out"
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return "dns resolution failed"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "request timed out"
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok {
		return "network error: " + opErr.Op
	}
	return "request failed"
}
