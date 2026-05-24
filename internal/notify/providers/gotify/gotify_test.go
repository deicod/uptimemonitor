package gotify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/deicod/uptimemonitor/internal/notify"
)

func TestProviderImplementsInterface(t *testing.T) {
	var _ notify.Provider = New()
}

// TestKindAndFields pins the SPEC §18.4/§18.5 field contract: server_url and
// token are required (token is a secret), priority is an optional number that
// defaults to 5.
func TestKindAndFields(t *testing.T) {
	p := New()
	if p.Kind() != "gotify" {
		t.Errorf("Kind() = %q, want gotify", p.Kind())
	}
	f := p.Fields()
	if len(f) != 3 {
		t.Fatalf("Fields() len = %d, want 3", len(f))
	}
	if s := f[0]; s.Name != "server_url" || s.Type != notify.FieldTypeURL || !s.Required || s.Secret {
		t.Errorf("server_url field = %+v", s)
	}
	if tk := f[1]; tk.Name != "token" || tk.Type != notify.FieldTypeSecretString || !tk.Required || !tk.Secret {
		t.Errorf("token field = %+v", tk)
	}
	if pr := f[2]; pr.Name != "priority" || pr.Type != notify.FieldTypeNumber || pr.Required || pr.Default != "5" {
		t.Errorf("priority field = %+v", pr)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct{ name, config, wantField string }{
		{"valid", `{"server_url":"https://gotify.example.com","token":"abc"}`, ""},
		{"valid with priority", `{"server_url":"https://gotify.example.com","token":"abc","priority":8}`, ""},
		{"missing server_url", `{"token":"abc"}`, "server_url"},
		{"missing token", `{"server_url":"https://gotify.example.com"}`, "token"},
		{"empty config", ``, "server_url"},
		{"relative server_url", `{"server_url":"/x","token":"abc"}`, "server_url"},
		{"bad scheme", `{"server_url":"ftp://gotify.example.com","token":"abc"}`, "server_url"},
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
			if !errors.As(err, &fe) || fe.Field != tc.wantField {
				t.Fatalf("Validate = %v, want *notify.FieldError(%s)", err, tc.wantField)
			}
		})
	}
}

func TestValidateMalformedJSON(t *testing.T) {
	if err := New().Validate(context.Background(), json.RawMessage(`{bad`)); err == nil {
		t.Fatal("Validate(malformed json) = nil, want error")
	}
}

// TestSendPostsMessage verifies the Gotify contract: a POST to /message with
// title, message, and priority, authenticated via the X-Gotify-Key header
// rather than a query string so the token never lands in request logs.
func TestSendPostsMessage(t *testing.T) {
	var path, method, ct, key, query string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		method = r.Method
		ct = r.Header.Get("Content-Type")
		key = r.Header.Get("X-Gotify-Key")
		query = r.URL.RawQuery
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	msg := notify.Message{Title: "Monitor down: Example", Body: "It went down."}
	cfg := json.RawMessage(`{"server_url":"` + srv.URL + `/","token":"app_token","priority":8}`)
	if err := New().Send(context.Background(), cfg, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("method = %q, want POST", method)
	}
	if path != "/message" {
		t.Errorf("path = %q, want /message", path)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if key != "app_token" {
		t.Errorf("X-Gotify-Key = %q, want app_token", key)
	}
	if strings.Contains(query, "app_token") {
		t.Errorf("token leaked into query string: %q", query)
	}
	var got struct {
		Title    string `json:"title"`
		Message  string `json:"message"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if got.Title != "Monitor down: Example" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Message != "It went down." {
		t.Errorf("message = %q", got.Message)
	}
	if got.Priority != 8 {
		t.Errorf("priority = %d, want 8", got.Priority)
	}
}

// TestSendDefaultsPriority proves a config without an explicit priority sends
// the documented default of 5 (SPEC §18.5).
func TestSendDefaultsPriority(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := json.RawMessage(`{"server_url":"` + srv.URL + `","token":"app_token"}`)
	if err := New().Send(context.Background(), cfg, notify.Message{Title: "x", Body: "y"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var got struct {
		Priority int `json:"priority"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if got.Priority != 5 {
		t.Errorf("priority = %d, want default 5", got.Priority)
	}
}

func TestSendErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg := json.RawMessage(`{"server_url":"` + srv.URL + `","token":"app_token"}`)
	if err := New().Send(context.Background(), cfg, notify.Message{Title: "x"}); err == nil {
		t.Fatal("Send returned nil for a 500 response")
	}
}
