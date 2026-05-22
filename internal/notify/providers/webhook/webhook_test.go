package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deicod/uptimemonitor/internal/notify"
)

func TestProviderImplementsInterface(t *testing.T) {
	var _ notify.Provider = New()
}

// TestFields pins the SPEC §18.4/§10.5 field contract the TUI form renders
// against: url is a required secret, method is a string defaulting to POST.
func TestFields(t *testing.T) {
	fields := New().Fields()
	if len(fields) != 2 {
		t.Fatalf("Fields() len = %d, want 2", len(fields))
	}
	if u := fields[0]; u.Name != "url" || u.Type != notify.FieldTypeSecretString || !u.Required || !u.Secret {
		t.Errorf("url field = %+v", u)
	}
	if m := fields[1]; m.Name != "method" || m.Type != notify.FieldTypeString || m.Default != http.MethodPost {
		t.Errorf("method field = %+v", m)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name      string
		config    string
		wantField string // "" => expect success
	}{
		{"valid", `{"url":"https://example.com/hook","method":"POST"}`, ""},
		{"valid blank method", `{"url":"https://example.com/hook"}`, ""},
		{"missing url", `{"method":"POST"}`, "url"},
		{"empty config", ``, "url"},
		{"relative url", `{"url":"/hook"}`, "url"},
		{"bad scheme", `{"url":"ftp://example.com"}`, "url"},
	}
	p := New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.Validate(context.Background(), json.RawMessage(tc.config))
			if tc.wantField == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			var fe *notify.FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("Validate = %v, want *notify.FieldError", err)
			}
			if fe.Field != tc.wantField {
				t.Errorf("field = %q, want %q", fe.Field, tc.wantField)
			}
		})
	}
}

func TestValidateMalformedJSON(t *testing.T) {
	if err := New().Validate(context.Background(), json.RawMessage(`{bad`)); err == nil {
		t.Fatal("Validate(malformed json) = nil, want error")
	}
}

// TestSendPostsPayload checks the generic webhook contract: a JSON document of
// the non-secret message fields, POSTed with the JSON content type.
func TestSendPostsPayload(t *testing.T) {
	var gotBody []byte
	var gotMethod, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	msg := notify.Message{
		EventType:   notify.EventMonitorDown,
		MonitorID:   "01HMON",
		MonitorName: "Example",
		State:       "down",
		Title:       "Monitor down: Example",
		Body:        "It went down.",
		URL:         "https://example.com",
		Time:        time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC),
	}
	cfg := json.RawMessage(`{"url":"` + srv.URL + `"}`)
	if err := New().Send(context.Background(), cfg, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotCT != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotCT)
	}
	var got map[string]any
	if err := json.Unmarshal(gotBody, &got); err != nil {
		t.Fatalf("payload not JSON: %v (%q)", err, gotBody)
	}
	want := map[string]string{
		"event_type":   "monitor_down",
		"monitor_id":   "01HMON",
		"monitor_name": "Example",
		"state":        "down",
		"title":        "Monitor down: Example",
		"body":         "It went down.",
		"url":          "https://example.com",
		"time":         "2026-05-22T12:00:00Z",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("payload[%q] = %v, want %q", k, got[k], v)
		}
	}
}

// TestSendHonorsMethod proves the configurable verb reaches the wire.
func TestSendHonorsMethod(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
	}))
	defer srv.Close()
	cfg := json.RawMessage(`{"url":"` + srv.URL + `","method":"PUT"}`)
	if err := New().Send(context.Background(), cfg, notify.Message{}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
}

// TestSendErrorOnNon2xx: a non-2xx response is a delivery failure the pipeline
// must see as an error to retry.
func TestSendErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	cfg := json.RawMessage(`{"url":"` + srv.URL + `"}`)
	if err := New().Send(context.Background(), cfg, notify.Message{}); err == nil {
		t.Fatal("Send returned nil for a 502 response")
	}
}
