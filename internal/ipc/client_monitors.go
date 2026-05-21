package ipc

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// MonitorListFilter holds the optional filters accepted by ListMonitors,
// mirroring the state/enabled query parameters of GET /v1/monitors (SPEC
// §10.5). A zero value applies no filter.
type MonitorListFilter struct {
	// State, when non-empty, restricts the result to monitors in that state
	// (up, down, unknown, paused).
	State string
	// Enabled, when non-nil, restricts the result to enabled/disabled monitors.
	Enabled *bool
}

// query encodes the filter as URL query parameters; an empty filter yields "".
func (f MonitorListFilter) query() string {
	q := url.Values{}
	if f.State != "" {
		q.Set("state", f.State)
	}
	if f.Enabled != nil {
		q.Set("enabled", strconv.FormatBool(*f.Enabled))
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// ListMonitors fetches monitors from GET /v1/monitors, applying the given
// filter (SPEC §10.5).
func (c *Client) ListMonitors(ctx context.Context, filter MonitorListFilter) ([]MonitorResponse, error) {
	var resp MonitorListResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/monitors"+filter.query(), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Monitors, nil
}

// CreateMonitor creates a monitor via POST /v1/monitors (SPEC §10.5).
func (c *Client) CreateMonitor(ctx context.Context, req CreateMonitorRequest) (MonitorResponse, error) {
	var resp MonitorResponse
	if err := c.Do(ctx, http.MethodPost, "/v1/monitors", req, &resp); err != nil {
		return MonitorResponse{}, err
	}
	return resp, nil
}

// GetMonitor fetches a single monitor via GET /v1/monitors/{id} (SPEC §10.5).
func (c *Client) GetMonitor(ctx context.Context, id string) (MonitorResponse, error) {
	var resp MonitorResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/monitors/"+url.PathEscape(id), nil, &resp); err != nil {
		return MonitorResponse{}, err
	}
	return resp, nil
}

// UpdateMonitor applies a partial update via PATCH /v1/monitors/{id} (SPEC
// §10.5).
func (c *Client) UpdateMonitor(ctx context.Context, id string, req UpdateMonitorRequest) (MonitorResponse, error) {
	var resp MonitorResponse
	if err := c.Do(ctx, http.MethodPatch, "/v1/monitors/"+url.PathEscape(id), req, &resp); err != nil {
		return MonitorResponse{}, err
	}
	return resp, nil
}

// DeleteMonitor soft-deletes a monitor via DELETE /v1/monitors/{id} (SPEC
// §10.5). A successful delete has no response body.
func (c *Client) DeleteMonitor(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, "/v1/monitors/"+url.PathEscape(id), nil, nil)
}

// RunMonitor enqueues a manual check via POST /v1/monitors/{id}/run (SPEC
// §10.5). The response body's status is "queued" when the check has been
// accepted for asynchronous execution.
func (c *Client) RunMonitor(ctx context.Context, id string) (RunMonitorResponse, error) {
	var resp RunMonitorResponse
	if err := c.Do(ctx, http.MethodPost, "/v1/monitors/"+url.PathEscape(id)+"/run", nil, &resp); err != nil {
		return RunMonitorResponse{}, err
	}
	return resp, nil
}

// History fetches a monitor's time-series history via
// GET /v1/monitors/{id}/history?range= (SPEC §10.5). The range must be in the
// SPEC §14.5 supported set; the server returns a validation_error otherwise.
func (c *Client) History(ctx context.Context, id, rangeStr string) (HistoryResponse, error) {
	path := "/v1/monitors/" + url.PathEscape(id) + "/history?range=" + url.QueryEscape(rangeStr)
	var resp HistoryResponse
	if err := c.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return HistoryResponse{}, err
	}
	return resp, nil
}

// RecentChecks fetches the most recent check_results for a monitor via
// GET /v1/monitors/{id}/checks (SPEC §10.5). A non-positive limit omits the
// query parameter and the server applies its default.
func (c *Client) RecentChecks(ctx context.Context, id string, limit int) ([]CheckResultResponse, error) {
	path := "/v1/monitors/" + url.PathEscape(id) + "/checks"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var resp CheckResultListResponse
	if err := c.Do(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Checks, nil
}
