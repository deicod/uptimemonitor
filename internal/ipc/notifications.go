package ipc

import (
	"net/http"

	"github.com/deicod/uptimemonitor/internal/notify"
)

// NotificationProviderResponse is the DTO for one entry in the providers list
// (SPEC §10.5 GET /v1/notifications/providers). The TUI consumes Fields
// verbatim to render its provider form (M9.12), so the JSON shape must
// match SPEC §18.4 — that comes from notify.Field's tags directly.
type NotificationProviderResponse struct {
	Kind        string         `json:"kind"`
	DisplayName string         `json:"display_name"`
	Fields      []notify.Field `json:"fields"`
}

// NotificationProvidersResponse is the top-level DTO returned by
// GET /v1/notifications/providers.
type NotificationProvidersResponse struct {
	Providers []NotificationProviderResponse `json:"providers"`
}

// NotificationProviderRegistry is the subset of notify.Registry the IPC
// providers endpoint needs. *notify.Registry satisfies it; the interface is
// declared here so handler tests can substitute a fake without depending on
// the registry's mutation API.
type NotificationProviderRegistry interface {
	List() []notify.Provider
}

// listProvidersHandler serves GET /v1/notifications/providers (SPEC §10.5,
// §18.3). The registry's List() is the authoritative ordering — the
// handler must not re-sort, so a deterministic registry order is the only
// guarantee the wire format is stable.
func listProvidersHandler(reg NotificationProviderRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		providers := reg.List()
		resp := NotificationProvidersResponse{
			Providers: make([]NotificationProviderResponse, 0, len(providers)),
		}
		for _, p := range providers {
			fields := p.Fields()
			if fields == nil {
				fields = []notify.Field{}
			}
			resp.Providers = append(resp.Providers, NotificationProviderResponse{
				Kind:        p.Kind(),
				DisplayName: p.DisplayName(),
				Fields:      fields,
			})
		}
		writeJSON(w, http.StatusOK, resp)
	}
}
