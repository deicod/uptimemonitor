package notify

import "fmt"

// FieldError is a provider-config validation failure tied to a specific field
// name (e.g. "url", "webhook_url"). Returning the field lets the IPC layer
// surface it as validation_error.field when a notification target is created
// or tested (SPEC §10.3, §18.4). It mirrors monitor.FieldError but lives in
// this package so providers validate their config without depending on the
// monitor package.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}
