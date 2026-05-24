package monitor

import (
	"fmt"
	"net/url"
	"unicode"
)

// FieldError is a validation failure tied to a specific field. Naming the
// field lets callers (e.g. the IPC layer) map it to a validation_error.field.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// ValidateMonitor checks the type-agnostic monitor fields (SPEC §11.1–11.2).
// Type-specific configuration is validated separately, e.g. ValidateHTTPConfig.
func ValidateMonitor(m *Monitor) error {
	switch {
	case m.Name == "":
		return &FieldError{"name", "must not be empty"}
	case hasControlChar(m.Name):
		// A name is a display string carried into notification payloads (e.g.
		// the email Subject) and the TUI. Control characters there enable
		// header injection (CWE-93) and break rendering, so reject them at the
		// source rather than trusting every downstream consumer to sanitize.
		return &FieldError{"name", "must not contain control characters"}
	case m.Type != MonitorTypeHTTP:
		return &FieldError{"type", fmt.Sprintf("unsupported monitor type %q", m.Type)}
	case m.Interval <= 0:
		return &FieldError{"interval", "must be positive"}
	case m.Timeout <= 0:
		return &FieldError{"timeout", "must be positive"}
	}
	return nil
}

// hasControlChar reports whether s contains any Unicode control character
// (C0/C1, including CR, LF, NUL, and tab). Printable Unicode text — accented
// letters, dashes, emoji — is allowed.
func hasControlChar(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// ValidateHTTPConfig checks an HTTP monitor's type-specific configuration
// against the SPEC §11.2 MVP rules.
func ValidateHTTPConfig(c *HTTPMonitorConfig) error {
	u, err := url.Parse(c.URL)
	switch {
	case c.URL == "":
		return &FieldError{"url", "must not be empty"}
	case err != nil:
		return &FieldError{"url", "must be a valid URL"}
	case !u.IsAbs():
		return &FieldError{"url", "must be an absolute URL"}
	case u.Scheme != "http" && u.Scheme != "https":
		return &FieldError{"url", "scheme must be http or https"}
	case u.Host == "":
		return &FieldError{"url", "must include a host"}
	case c.Method != "GET":
		return &FieldError{"method", "must be GET"}
	case c.ExpectedStatusMin < 100 || c.ExpectedStatusMin > 599:
		return &FieldError{"expected_status_min", "must be between 100 and 599"}
	case c.ExpectedStatusMax < 100 || c.ExpectedStatusMax > 599:
		return &FieldError{"expected_status_max", "must be between 100 and 599"}
	case c.ExpectedStatusMin > c.ExpectedStatusMax:
		return &FieldError{"expected_status_max", "must not be less than expected_status_min"}
	}
	return nil
}
