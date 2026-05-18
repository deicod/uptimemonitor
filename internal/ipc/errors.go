package ipc

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ErrorCode is one of the SPEC §10.3 error-code strings.
type ErrorCode string

// SPEC §10.3 error codes. These constants are locked to their string values;
// changing them is a wire-format breaking change.
const (
	ErrBadRequest         ErrorCode = "bad_request"
	ErrValidation         ErrorCode = "validation_error"
	ErrNotFound           ErrorCode = "not_found"
	ErrConflict           ErrorCode = "conflict"
	ErrInternal           ErrorCode = "internal_error"
	ErrServiceUnavailable ErrorCode = "service_unavailable"
	ErrProvider           ErrorCode = "provider_error"
)

// HTTPStatus maps an error code to its canonical HTTP status code. Unknown
// codes default to 500.
func (c ErrorCode) HTTPStatus() int {
	switch c {
	case ErrBadRequest:
		return http.StatusBadRequest
	case ErrValidation:
		return http.StatusUnprocessableEntity
	case ErrNotFound:
		return http.StatusNotFound
	case ErrConflict:
		return http.StatusConflict
	case ErrInternal:
		return http.StatusInternalServerError
	case ErrServiceUnavailable:
		return http.StatusServiceUnavailable
	case ErrProvider:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// APIError is the structured error returned in IPC responses (SPEC §10.3).
// It implements the error interface so it can be used directly in Go error
// chains and participates in errors.As unwrapping.
type APIError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Field   string    `json:"field,omitempty"`
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s (field %s)", e.Code, e.Message, e.Field)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewAPIError constructs an APIError. An optional field name can be supplied as
// the third argument; if omitted the Field is left empty.
func NewAPIError(code ErrorCode, message string, field ...string) *APIError {
	e := &APIError{Code: code, Message: message}
	if len(field) > 0 {
		e.Field = field[0]
	}
	return e
}

// ErrorEnvelope is the top-level JSON wrapper for error responses (SPEC §10.3):
//
//	{"error": {"code": "...", "message": "...", "field": "..."}}
type ErrorEnvelope struct {
	Error *APIError `json:"error"`
}

// EncodeError marshals an APIError into its JSON envelope. It never fails
// because APIError is always marshalable; a marshal panic would indicate a
// programming error.
func EncodeError(e *APIError) []byte {
	data, err := json.Marshal(ErrorEnvelope{Error: e})
	if err != nil {
		// This should never happen for the fixed APIError struct.
		panic(fmt.Sprintf("ipc: marshal error envelope: %v", err))
	}
	return data
}

// DecodeError parses a JSON error envelope and returns the contained APIError.
// It returns an error if the JSON is invalid or the envelope is missing the
// "error" key.
func DecodeError(data []byte) (*APIError, error) {
	var env ErrorEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("ipc: decode error envelope: %w", err)
	}
	if env.Error == nil {
		return nil, fmt.Errorf("ipc: error envelope missing \"error\" key")
	}
	return env.Error, nil
}
