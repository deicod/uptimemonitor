package ipc

import "net/http"

// NewRouter creates a *http.ServeMux pre-configured with the /v1 route prefix
// and any base routes. Endpoint-specific handlers (status, monitors, etc.) are
// registered by the caller before passing the mux to NewServer.
//
// This function exists as the single place where the route table is assembled
// (SPEC §10.4), making it easy to see the full API surface.
func NewRouter() *http.ServeMux {
	mux := http.NewServeMux()
	// Base routes are empty for now. Handlers will be registered here as
	// they are implemented in subsequent milestones (M3.4, M5.2, etc.).
	return mux
}
