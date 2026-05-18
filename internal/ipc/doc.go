// Package ipc owns the API contract types shared between the uptimemonitor
// service and TUI. Request/response DTOs, error codes, and JSON envelope
// helpers live here so that both sides depend on the same type definitions
// (SPEC §10).
//
// No transport logic lives in this file — it holds only data structures and
// encoding utilities. Server and client implementations will be added in
// subsequent milestones (M3.2, M3.3).
package ipc
