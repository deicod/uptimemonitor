// Package notify defines the notification provider contract, field metadata,
// and message model that the delivery pipeline (M9.9) and the per-provider
// implementations (M9.5–M9.8) build on.
//
// SPEC §18.1–18.4.
package notify

import (
	"context"
	"encoding/json"
)

// FieldType names a provider-config field kind. The TUI uses it to render
// the right input widget and the validation layer uses it to decide how to
// parse the raw value. The string values are part of the wire contract
// exposed by GET /v1/notifications/providers (SPEC §10.5, §18.4).
type FieldType string

// FieldType values (SPEC §18.4).
const (
	FieldTypeString       FieldType = "string"
	FieldTypeSecretString FieldType = "secret_string"
	FieldTypeURL          FieldType = "url"
	FieldTypeNumber       FieldType = "number"
	FieldTypeBool         FieldType = "bool"
	FieldTypeSelect       FieldType = "select"
	FieldTypeTextarea     FieldType = "textarea"
)

// Field describes one provider-config field. Providers return their full
// field list from Fields() so the TUI can render a form without hard-coding
// provider-specific layouts (SPEC §18.4).
type Field struct {
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	Type        FieldType `json:"type"`
	Required    bool      `json:"required"`
	Secret      bool      `json:"secret"`
	Default     string    `json:"default,omitempty"`
	Description string    `json:"description,omitempty"`
}

// Provider is the contract every notification backend (webhook, email,
// telegram, …) implements. Concrete providers live under
// internal/notify/providers and are registered with the Registry (M9.2).
//
// Config is passed as json.RawMessage because the shape is provider-specific
// and is persisted as JSON in notification_targets.config (SPEC §12.3).
type Provider interface {
	// Kind is the stable string identifier persisted on each target and
	// used to look the provider up in the Registry (SPEC §18.3).
	Kind() string
	// DisplayName is a human-readable label for the TUI.
	DisplayName() string
	// Fields describes the provider-config form (SPEC §18.4).
	Fields() []Field
	// Validate checks that config has all required fields populated and
	// well-formed before persistence. It must not perform network I/O.
	Validate(ctx context.Context, config json.RawMessage) error
	// Send delivers msg using config. Send is invoked by the delivery
	// pipeline (M9.9) and is the only Provider method that performs I/O.
	Send(ctx context.Context, config json.RawMessage, msg Message) error
}
