package ipc

import (
	"context"
	"encoding/json"
	"net/http"
)

// StatusProvider supplies the data served by GET /v1/status (SPEC §10.5).
//
// It is implemented by the service application layer, which knows the version,
// start time, storage health, scheduler state, and monitor counts. The IPC
// package depends only on this interface so it carries no service internals.
type StatusProvider interface {
	// Status returns the current service status snapshot.
	Status(ctx context.Context) StatusResponse
}

// StatusHandler returns an http.HandlerFunc for GET /v1/status backed by p.
func StatusHandler(p StatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, p.Status(r.Context()))
	}
}

// writeJSON marshals v and writes it with the given HTTP status. On a marshal
// failure it writes a SPEC §10.3 internal_error envelope instead.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(EncodeError(NewAPIError(ErrInternal, "failed to encode response")))
		return
	}
	w.WriteHeader(status)
	w.Write(data)
}
