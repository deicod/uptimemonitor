package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/deicod/uptimemonitor/internal/notify"
)

func TestProviderImplementsInterface(t *testing.T) {
	var _ notify.Provider = New()
}

func TestKindAndFields(t *testing.T) {
	p := New()
	if p.Kind() != "discord" {
		t.Errorf("Kind() = %q, want discord", p.Kind())
	}
	f := p.Fields()
	if len(f) != 1 {
		t.Fatalf("Fields() len = %d, want 1", len(f))
	}
	if w := f[0]; w.Name != "webhook_url" || w.Type != notify.FieldTypeSecretString || !w.Required || !w.Secret {
		t.Errorf("webhook_url field = %+v", w)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct{ name, config, wantField string }{
		{"valid", `{"webhook_url":"https://discord.com/api/webhooks/1/abc"}`, ""},
		{"missing", `{}`, "webhook_url"},
		{"empty config", ``, "webhook_url"},
		{"relative", `{"webhook_url":"/x"}`, "webhook_url"},
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

// TestSendPostsContent verifies the Discord webhook body: a single "content"
// field combining the rendered title and body, POSTed as JSON. Discord replies
// 204 on success, which must count as delivered.
func TestSendPostsContent(t *testing.T) {
	var body []byte
	var method, ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		ct = r.Header.Get("Content-Type")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	msg := notify.Message{Title: "Monitor down: Example", Body: "It went down."}
	cfg := json.RawMessage(`{"webhook_url":"` + srv.URL + `"}`)
	if err := New().Send(context.Background(), cfg, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("method = %q, want POST", method)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var got struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if got.Content != "Monitor down: Example\n\nIt went down." {
		t.Errorf("content = %q", got.Content)
	}
}

func TestSendErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	cfg := json.RawMessage(`{"webhook_url":"` + srv.URL + `"}`)
	if err := New().Send(context.Background(), cfg, notify.Message{Title: "x"}); err == nil {
		t.Fatal("Send returned nil for a 500 response")
	}
}
