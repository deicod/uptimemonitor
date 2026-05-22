// Package providerhttp holds the shared HTTP POST helper used by the
// JSON-over-HTTP notification providers (webhook, discord, slack, and the
// M9.6/M9.7 providers). Centralising the send path keeps secret redaction
// consistent: a target's endpoint URL is a secret (SPEC §18.9, §23) and must
// never appear in an error the delivery pipeline logs or persists.
package providerhttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

// PostJSON sends body to url as application/json using method (defaulting to
// POST when blank). It returns nil only when the response status is 2xx. On a
// non-2xx response the error names the status code but never the URL; transport
// errors are reduced to a coarse category for the same reason.
// context.Canceled and context.DeadlineExceeded are preserved (matchable with
// errors.Is) so the delivery pipeline can still distinguish shutdown from a
// timeout.
func PostJSON(ctx context.Context, client *http.Client, method, url string, body []byte) error {
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", sanitize(err))
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", sanitize(err))
	}
	defer resp.Body.Close()
	// Drain so the connection can be reused; the body is not inspected.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected response status %d", resp.StatusCode)
	}
	return nil
}

var (
	errTimeout = errors.New("request timed out")
	errDNS     = errors.New("dns resolution failed")
	errNetwork = errors.New("request failed")
)

// sanitize collapses a transport error into a category that carries no URL,
// host, or request payload. The context cancellation and deadline sentinels
// are returned unchanged so errors.Is keeps working for callers that classify
// shutdown vs. timeout; everything else maps to a fixed, value-free error.
func sanitize(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errTimeout
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return errDNS
	}
	return errNetwork
}
