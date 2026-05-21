package ipc

import (
	"context"
	"net/http"
)

// NotificationProviders fetches the available notification providers from
// GET /v1/notifications/providers (SPEC §10.5, §18.3). The TUI provider form
// (M9.12) consumes the returned field metadata to render its inputs.
func (c *Client) NotificationProviders(ctx context.Context) ([]NotificationProviderResponse, error) {
	var resp NotificationProvidersResponse
	if err := c.Do(ctx, http.MethodGet, "/v1/notifications/providers", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Providers, nil
}
