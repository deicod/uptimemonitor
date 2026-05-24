package ntfy

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
// topic are required, and token is an optional secret.
func TestKindAndFields(t *testing.T) {
	p := New()
	if p.Kind() != "ntfy" {
		t.Errorf("Kind() = %q, want ntfy", p.Kind())
	}
	f := p.Fields()
	if len(f) != 3 {
		t.Fatalf("Fields() len = %d, want 3", len(f))
	}
	if s := f[0]; s.Name != "server_url" || s.Type != notify.FieldTypeURL || !s.Required || s.Secret {
		t.Errorf("server_url field = %+v", s)
	}
	if tp := f[1]; tp.Name != "topic" || tp.Type != notify.FieldTypeString || !tp.Required || tp.Secret {
		t.Errorf("topic field = %+v", tp)
	}
	if tk := f[2]; tk.Name != "token" || tk.Type != notify.FieldTypeSecretString || tk.Required || !tk.Secret {
		t.Errorf("token field = %+v", tk)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct{ name, config, wantField string }{
		{"valid", `{"server_url":"https://ntfy.sh","topic":"alerts"}`, ""},
		{"valid with token", `{"server_url":"https://ntfy.sh","topic":"alerts","token":"tk_abc"}`, ""},
		{"missing server_url", `{"topic":"alerts"}`, "server_url"},
		{"missing topic", `{"server_url":"https://ntfy.sh"}`, "topic"},
		{"empty config", ``, "server_url"},
		{"relative server_url", `{"server_url":"/x","topic":"alerts"}`, "server_url"},
		{"bad scheme", `{"server_url":"ftp://ntfy.sh","topic":"alerts"}`, "server_url"},
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

// TestSendPostsToRoot verifies ntfy JSON publishing: a POST to the server root
// carrying the topic, title, and message, with no auth header when no token is
// configured.
func TestSendPostsToRoot(t *testing.T) {
	var path, method, ct, auth string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		method = r.Method
		ct = r.Header.Get("Content-Type")
		auth = r.Header.Get("Authorization")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	msg := notify.Message{Title: "Monitor down: Example", Body: "It went down."}
	// Trailing slash on server_url must be tolerated.
	cfg := json.RawMessage(`{"server_url":"` + srv.URL + `/","topic":"alerts"}`)
	if err := New().Send(context.Background(), cfg, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("method = %q, want POST", method)
	}
	if path != "/" {
		t.Errorf("path = %q, want / (JSON publishing posts to the root)", path)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if auth != "" {
		t.Errorf("Authorization = %q, want empty when no token configured", auth)
	}
	var got struct {
		Topic   string `json:"topic"`
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if got.Topic != "alerts" {
		t.Errorf("topic = %q, want alerts", got.Topic)
	}
	if got.Title != "Monitor down: Example" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Message != "It went down." {
		t.Errorf("message = %q", got.Message)
	}
}

// TestSendSetsBearerToken proves an optional token is sent as a Bearer auth
// header (ntfy's access-token scheme) and never appears in the body.
func TestSendSetsBearerToken(t *testing.T) {
	var auth string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := json.RawMessage(`{"server_url":"` + srv.URL + `","topic":"alerts","token":"tk_secret"}`)
	if err := New().Send(context.Background(), cfg, notify.Message{Title: "x", Body: "y"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if auth != "Bearer tk_secret" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer tk_secret")
	}
	if string(body) == "" || strings.Contains(string(body), "tk_secret") {
		t.Errorf("token must not appear in body: %q", body)
	}
}

func TestSendErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg := json.RawMessage(`{"server_url":"` + srv.URL + `","topic":"alerts"}`)
	if err := New().Send(context.Background(), cfg, notify.Message{Title: "x"}); err == nil {
		t.Fatal("Send returned nil for a 500 response")
	}
}
