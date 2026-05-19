package ipc

import "net/http"

// NewRouter assembles the /v1 route table and returns the *http.ServeMux to
// pass to NewServer.
//
// This function is the single place where the route table is assembled (SPEC
// §10.4), making it easy to see the full API surface. Additional routes
// (monitors, etc.) are registered here as they are implemented in subsequent
// milestones (M5.2, etc.).
func NewRouter(status StatusProvider) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/status", StatusHandler(status))
	return mux
}
