package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
)

// Client is an HTTP client that communicates with the uptimemonitor service
// over a Unix domain socket. It provides a typed request helper that handles
// JSON encoding/decoding and maps error envelopes to Go errors.
type Client struct {
	socketPath string
	httpClient *http.Client
}

// NewClient creates a Client that dials the given Unix socket path.
func NewClient(socketPath string) *Client {
	return &Client{
		socketPath: socketPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socketPath)
				},
			},
		},
	}
}

// SocketPath returns the Unix socket path this client is configured to dial.
func (c *Client) SocketPath() string {
	return c.socketPath
}

// Do performs an HTTP request to the service over the Unix socket.
//
// The method and path are used as-is (path should include the /v1 prefix).
// If body is non-nil it is JSON-encoded as the request body. If result is
// non-nil and the response is 2xx, the response body is JSON-decoded into it.
//
// On non-2xx responses, Do attempts to decode a SPEC §10.3 error envelope and
// returns an *APIError. If the response body is not valid JSON, a synthetic
// *APIError with the HTTP status is returned instead.
//
// Connection failures (missing socket, connection refused) are returned as
// *ConnectionError with a user-friendly message.
func (c *Client) Do(ctx context.Context, method, path string, body any, result any) error {
	// Build the request body.
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("ipc: marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	// The host is ignored by the Unix transport; we use a fixed value for
	// clarity in logs/traces.
	url := "http://uptimemonitor" + path

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("ipc: create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return c.wrapConnError(err)
	}
	defer resp.Body.Close()

	// Read the full body so we can inspect it.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("ipc: read response body: %w", err)
	}

	// 2xx → success.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if result != nil && len(respBody) > 0 {
			if err := json.Unmarshal(respBody, result); err != nil {
				return fmt.Errorf("ipc: decode response: %w", err)
			}
		}
		return nil
	}

	// Non-2xx → try to decode the error envelope.
	apiErr, decErr := DecodeError(respBody)
	if decErr == nil {
		return apiErr
	}

	// The body wasn't a valid error envelope (e.g. plain text). Build a
	// synthetic APIError so the caller can always use errors.As.
	return &APIError{
		Code:    errorCodeFromHTTPStatus(resp.StatusCode),
		Message: truncateBody(string(respBody), 200),
	}
}

// Status fetches the service status from GET /v1/status (SPEC §10.5).
func (c *Client) Status(ctx context.Context) (StatusResponse, error) {
	var resp StatusResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/status", nil, &resp); err != nil {
		return StatusResponse{}, err
	}
	return resp, nil
}

// ConnectionError is returned when the client cannot reach the service socket.
// It wraps the underlying network error and provides a user-friendly message
// (SPEC §8.5).
type ConnectionError struct {
	SocketPath string
	Err        error
}

// Error returns a human-readable message suitable for display in the TUI.
func (e *ConnectionError) Error() string {
	return fmt.Sprintf("cannot connect to uptimemonitor service at %s: %v", e.SocketPath, e.Err)
}

// Unwrap returns the underlying error for use with errors.Is / errors.As.
func (e *ConnectionError) Unwrap() error {
	return e.Err
}

// wrapConnError inspects err and, if it looks like a connection failure,
// wraps it in a *ConnectionError. Otherwise it returns a generic ipc error.
func (c *Client) wrapConnError(err error) error {
	// The net/http package wraps dial errors in *url.Error, which in turn
	// wraps *net.OpError. errors.As traverses the chain for us.
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return &ConnectionError{
			SocketPath: c.socketPath,
			Err:        err,
		}
	}
	// Fallback: wrap as a connection error anyway if it smells like one.
	// Context cancellation and other transport errors still get wrapped as
	// connection errors since the caller can always inspect Unwrap().
	return &ConnectionError{
		SocketPath: c.socketPath,
		Err:        err,
	}
}

// errorCodeFromHTTPStatus maps an HTTP status code to the closest SPEC §10.3
// error code, used as a fallback when the response body is not a valid JSON
// error envelope.
func errorCodeFromHTTPStatus(status int) ErrorCode {
	switch status {
	case http.StatusBadRequest:
		return ErrBadRequest
	case http.StatusUnprocessableEntity:
		return ErrValidation
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusConflict:
		return ErrConflict
	case http.StatusServiceUnavailable:
		return ErrServiceUnavailable
	case http.StatusBadGateway:
		return ErrProvider
	default:
		return ErrInternal
	}
}

// truncateBody truncates s to maxLen, appending "…" if truncated.
func truncateBody(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
