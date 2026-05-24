package monitor

import (
	"errors"
	"testing"
	"time"
)

func validMonitor() *Monitor {
	return &Monitor{
		Name:     "example",
		Type:     MonitorTypeHTTP,
		Interval: 60 * time.Second,
		Timeout:  10 * time.Second,
	}
}

// Each invalid case must fail on the named field so the IPC layer can point
// the user at the exact input to correct.
func TestValidateMonitor(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*Monitor)
		wantField string // "" means the monitor should be accepted
	}{
		{"valid baseline", func(*Monitor) {}, ""},
		{"empty name", func(m *Monitor) { m.Name = "" }, "name"},
		// Control characters in a name would inject SMTP headers via the email
		// provider's Subject (CWE-93) and corrupt TUI rendering; reject them
		// here rather than relying on each consumer to sanitize.
		{"name with CRLF", func(m *Monitor) { m.Name = "a\r\nBcc: evil@example.com" }, "name"},
		{"name with NUL", func(m *Monitor) { m.Name = "a\x00b" }, "name"},
		{"name with tab", func(m *Monitor) { m.Name = "a\tb" }, "name"},
		{"name with unicode letters accepted", func(m *Monitor) { m.Name = "café—naïve" }, ""},
		{"unsupported type", func(m *Monitor) { m.Type = "tcp" }, "type"},
		{"zero interval", func(m *Monitor) { m.Interval = 0 }, "interval"},
		{"negative interval", func(m *Monitor) { m.Interval = -time.Second }, "interval"},
		{"zero timeout", func(m *Monitor) { m.Timeout = 0 }, "timeout"},
		{"negative timeout", func(m *Monitor) { m.Timeout = -time.Second }, "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validMonitor()
			tc.mutate(m)
			assertField(t, ValidateMonitor(m), tc.wantField)
		})
	}
}

func validHTTPConfig() *HTTPMonitorConfig {
	return &HTTPMonitorConfig{
		URL:               "https://example.com/health",
		Method:            "GET",
		ExpectedStatusMin: 200,
		ExpectedStatusMax: 299,
	}
}

// HTTP config rules from SPEC §11.2: absolute URL, http/https scheme, GET
// method, and a valid expected-status range.
func TestValidateHTTPConfig(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*HTTPMonitorConfig)
		wantField string
	}{
		{"valid baseline", func(*HTTPMonitorConfig) {}, ""},
		{"empty url", func(c *HTTPMonitorConfig) { c.URL = "" }, "url"},
		{"relative url", func(c *HTTPMonitorConfig) { c.URL = "/health" }, "url"},
		{"non-http scheme", func(c *HTTPMonitorConfig) { c.URL = "ftp://example.com" }, "url"},
		{"missing host", func(c *HTTPMonitorConfig) { c.URL = "http://" }, "url"},
		{"http scheme accepted", func(c *HTTPMonitorConfig) { c.URL = "http://example.com" }, ""},
		{"non-GET method", func(c *HTTPMonitorConfig) { c.Method = "POST" }, "method"},
		{"status min too low", func(c *HTTPMonitorConfig) { c.ExpectedStatusMin = 99 }, "expected_status_min"},
		{"status max too high", func(c *HTTPMonitorConfig) { c.ExpectedStatusMax = 600 }, "expected_status_max"},
		{"min greater than max", func(c *HTTPMonitorConfig) {
			c.ExpectedStatusMin = 300
			c.ExpectedStatusMax = 299
		}, "expected_status_max"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validHTTPConfig()
			tc.mutate(c)
			assertField(t, ValidateHTTPConfig(c), tc.wantField)
		})
	}
}

// assertField checks that err is nil when wantField is empty, or otherwise a
// *FieldError naming wantField.
func assertField(t *testing.T, err error, wantField string) {
	t.Helper()
	if wantField == "" {
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		return
	}
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("expected *FieldError, got %v", err)
	}
	if fe.Field != wantField {
		t.Errorf("error field = %q, want %q", fe.Field, wantField)
	}
}
