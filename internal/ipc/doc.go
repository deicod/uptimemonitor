// Package ipc owns the API contract types, server, and routing shared between
// the uptimemonitor service and TUI. Request/response DTOs, error codes, JSON
// envelope helpers, the Unix-socket HTTP server, and route wiring all live here
// so that both sides depend on the same type definitions (SPEC §10).
//
// Server and route setup are in server.go and routes.go; data structures and
// encoding utilities are in types.go and errors.go. Client implementation will
// be added in a subsequent milestone (M3.3).
package ipc
