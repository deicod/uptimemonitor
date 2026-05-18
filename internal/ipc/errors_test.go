package ipc

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// ---------- error-code constants are stable ----------

func TestErrorCodeConstants(t *testing.T) {
	// The SPEC §10.3 error codes must remain stable so existing clients
	// don't break.  This test locks them to their string values.
	tests := []struct {
		code ErrorCode
		want string
	}{
		{ErrBadRequest, "bad_request"},
		{ErrValidation, "validation_error"},
		{ErrNotFound, "not_found"},
		{ErrConflict, "conflict"},
		{ErrInternal, "internal_error"},
		{ErrServiceUnavailable, "service_unavailable"},
		{ErrProvider, "provider_error"},
	}
	for _, tt := range tests {
		if string(tt.code) != tt.want {
			t.Errorf("ErrorCode = %q, want %q", tt.code, tt.want)
		}
	}
}

// ---------- ErrorCode → HTTP status mapping ----------

func TestErrorCodeHTTPStatus(t *testing.T) {
	tests := []struct {
		code ErrorCode
		want int
	}{
		{ErrBadRequest, http.StatusBadRequest},
		{ErrValidation, http.StatusUnprocessableEntity},
		{ErrNotFound, http.StatusNotFound},
		{ErrConflict, http.StatusConflict},
		{ErrInternal, http.StatusInternalServerError},
		{ErrServiceUnavailable, http.StatusServiceUnavailable},
		{ErrProvider, http.StatusBadGateway},
		{ErrorCode("unknown_code"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		got := tt.code.HTTPStatus()
		if got != tt.want {
			t.Errorf("%s.HTTPStatus() = %d, want %d", tt.code, got, tt.want)
		}
	}
}

// ---------- APIError implements the error interface ----------

func TestAPIErrorIsError(t *testing.T) {
	var _ error = &APIError{}
	e := &APIError{Code: ErrNotFound, Message: "monitor not found"}
	if e.Error() == "" {
		t.Fatal("Error() returned empty string")
	}
}

// ---------- APIError round-trip (encode → decode) ----------

func TestAPIErrorRoundTrip(t *testing.T) {
	original := &APIError{
		Code:    ErrValidation,
		Message: "interval must be at least 1s",
		Field:   "interval",
	}

	data, err := json.Marshal(ErrorEnvelope{Error: original})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := DecodeError(data)
	if err != nil {
		t.Fatalf("DecodeError: %v", err)
	}

	if got.Code != original.Code {
		t.Errorf("Code = %q, want %q", got.Code, original.Code)
	}
	if got.Message != original.Message {
		t.Errorf("Message = %q, want %q", got.Message, original.Message)
	}
	if got.Field != original.Field {
		t.Errorf("Field = %q, want %q", got.Field, original.Field)
	}
}

func TestAPIErrorRoundTripNoField(t *testing.T) {
	original := &APIError{
		Code:    ErrInternal,
		Message: "unexpected failure",
	}

	data, err := json.Marshal(ErrorEnvelope{Error: original})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := DecodeError(data)
	if err != nil {
		t.Fatalf("DecodeError: %v", err)
	}

	if got.Code != original.Code {
		t.Errorf("Code = %q, want %q", got.Code, original.Code)
	}
	if got.Message != original.Message {
		t.Errorf("Message = %q, want %q", got.Message, original.Message)
	}
	if got.Field != "" {
		t.Errorf("Field = %q, want empty", got.Field)
	}
}

// ---------- EncodeError produces the standard envelope ----------

func TestEncodeError(t *testing.T) {
	apiErr := &APIError{
		Code:    ErrNotFound,
		Message: "monitor not found",
	}

	data := EncodeError(apiErr)

	// Parse back and verify the envelope shape.
	var env ErrorEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if env.Error == nil {
		t.Fatal("envelope .error is nil")
	}
	if env.Error.Code != ErrNotFound {
		t.Errorf("Code = %q, want %q", env.Error.Code, ErrNotFound)
	}
}

// ---------- DecodeError on invalid JSON ----------

func TestDecodeErrorInvalidJSON(t *testing.T) {
	_, err := DecodeError([]byte(`{not json`))
	if err == nil {
		t.Fatal("DecodeError(invalid) = nil, want error")
	}
}

// ---------- DecodeError on missing .error key ----------

func TestDecodeErrorMissingKey(t *testing.T) {
	_, err := DecodeError([]byte(`{}`))
	if err == nil {
		t.Fatal("DecodeError({}) = nil, want error")
	}
}

// ---------- APIError participates in errors.As ----------

func TestAPIErrorAs(t *testing.T) {
	apiErr := &APIError{Code: ErrConflict, Message: "already exists"}
	wrapped := errors.Join(errors.New("context"), apiErr)

	var target *APIError
	if !errors.As(wrapped, &target) {
		t.Fatal("errors.As failed to find *APIError")
	}
	if target.Code != ErrConflict {
		t.Errorf("Code = %q, want %q", target.Code, ErrConflict)
	}
}

// ---------- NewAPIError helper ----------

func TestNewAPIError(t *testing.T) {
	e := NewAPIError(ErrValidation, "bad value", "name")
	if e.Code != ErrValidation {
		t.Errorf("Code = %q, want %q", e.Code, ErrValidation)
	}
	if e.Message != "bad value" {
		t.Errorf("Message = %q, want %q", e.Message, "bad value")
	}
	if e.Field != "name" {
		t.Errorf("Field = %q, want %q", e.Field, "name")
	}
}

func TestNewAPIErrorNoField(t *testing.T) {
	e := NewAPIError(ErrInternal, "boom")
	if e.Field != "" {
		t.Errorf("Field = %q, want empty", e.Field)
	}
}
