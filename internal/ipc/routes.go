package ipc

import "net/http"

// NewRouter assembles the /v1 route table and returns the *http.ServeMux to
// pass to NewServer.
//
// This function is the single place where the route table is assembled (SPEC
// §10.4), making it easy to see the full API surface. Monitor routes are
// registered only when monitors is non-nil, so callers that need just the
// status endpoint (e.g. status-only tests) may pass nil. Additional routes
// (incidents, notifications, etc.) are registered here as they are
// implemented in subsequent milestones.
func NewRouter(status StatusProvider, monitors MonitorService) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /v1/status", StatusHandler(status))
	if monitors != nil {
		mux.Handle("GET /v1/monitors", listMonitorsHandler(monitors))
		mux.Handle("POST /v1/monitors", createMonitorHandler(monitors))
		mux.Handle("GET /v1/monitors/{id}", getMonitorHandler(monitors))
		mux.Handle("PATCH /v1/monitors/{id}", updateMonitorHandler(monitors))
		mux.Handle("DELETE /v1/monitors/{id}", deleteMonitorHandler(monitors))
	}
	return mux
}
