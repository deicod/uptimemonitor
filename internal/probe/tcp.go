package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"syscall"
	"time"

	"github.com/deicod/uptimemonitor/internal/monitor"
)

// TCPRunner executes TCP port monitor probes (SPEC §15.2.2): a check succeeds
// when a TCP connection to the configured host and port is established within
// the monitor timeout. No application payload is exchanged. The runner holds
// no per-monitor state, so one instance is safe for concurrent use.
type TCPRunner struct {
	dialer net.Dialer
}

// NewTCPRunner returns a TCPRunner ready to execute checks. Host names are
// resolved by the dialer with the default resolver, and every resolved
// address is tried in turn within the monitor timeout.
func NewTCPRunner() *TCPRunner {
	return &TCPRunner{}
}

// Type reports that this runner handles TCP monitors.
func (r *TCPRunner) Type() monitor.MonitorType { return monitor.MonitorTypeTCP }

// Run dials the monitor's host and port and closes the connection as soon as
// it is established. Connection failures are failed Results; the error return
// is reserved for configuration that escaped validation (SPEC §15.4).
func (r *TCPRunner) Run(ctx context.Context, m monitor.Monitor) (Result, error) {
	var cfg monitor.TCPMonitorConfig
	if err := json.Unmarshal(m.Config, &cfg); err != nil {
		return Result{}, fmt.Errorf("decode tcp monitor config: %w", err)
	}
	if err := monitor.ValidateTCPConfig(&cfg); err != nil {
		return Result{}, fmt.Errorf("validate tcp monitor config: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()

	started := time.Now()
	conn, err := r.dialer.DialContext(runCtx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)))
	finished := time.Now()
	res := Result{
		StartedAt:  started,
		FinishedAt: finished,
		Duration:   finished.Sub(started),
	}
	if err != nil {
		res.Error = "connect: " + describeNetError(runCtx, err)
		res.Details = marshalDetails(TCPDetails{})
		return res, nil
	}
	remote := conn.RemoteAddr().String()
	_ = conn.Close()
	res.Success = true
	res.Details = marshalDetails(TCPDetails{RemoteAddr: remote})
	return res, nil
}

// describeNetError reduces a dial, read, or write error to a short cause that
// carries no addresses or request data (SPEC §15.4). ctx is the probe context:
// once it has expired or been cancelled, the socket error is only a symptom of
// that, so the context state wins.
func describeNetError(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok {
		switch {
		case dnsErr.IsNotFound:
			return "host lookup failed: no such host"
		case dnsErr.IsTimeout:
			return "host lookup timed out"
		}
		return "host lookup failed"
	}
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.Is(err, syscall.ECONNRESET):
		return "connection reset"
	case errors.Is(err, syscall.EHOSTUNREACH):
		return "host unreachable"
	case errors.Is(err, syscall.ENETUNREACH):
		return "network unreachable"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "connection closed"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timed out"
	}
	return "network error"
}
