package notify

import (
	"errors"
	"testing"
)

// TestFieldError pins the wire-facing format and errors.As behaviour the IPC
// layer relies on: provider Validate failures must surface a named field so a
// target create/test can be mapped to validation_error.field (SPEC §10.3,
// §18.4). If Error() or the As target type drifts, that mapping breaks
// silently.
func TestFieldError(t *testing.T) {
	err := error(&FieldError{Field: "url", Message: "must not be empty"})
	if err.Error() != "url: must not be empty" {
		t.Errorf("Error() = %q, want %q", err.Error(), "url: must not be empty")
	}
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("errors.As(%v) failed", err)
	}
	if fe.Field != "url" {
		t.Errorf("Field = %q, want url", fe.Field)
	}
}
