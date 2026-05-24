package telegram

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

// TestKindAndFields pins the SPEC §18.4/§18.5 field contract: bot_token is a
// required secret, chat_id is a required string.
func TestKindAndFields(t *testing.T) {
	p := New()
	if p.Kind() != "telegram" {
		t.Errorf("Kind() = %q, want telegram", p.Kind())
	}
	f := p.Fields()
	if len(f) != 2 {
		t.Fatalf("Fields() len = %d, want 2", len(f))
	}
	if bt := f[0]; bt.Name != "bot_token" || bt.Type != notify.FieldTypeSecretString || !bt.Required || !bt.Secret {
		t.Errorf("bot_token field = %+v", bt)
	}
	if ci := f[1]; ci.Name != "chat_id" || ci.Type != notify.FieldTypeString || !ci.Required || ci.Secret {
		t.Errorf("chat_id field = %+v", ci)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct{ name, config, wantField string }{
		{"valid", `{"bot_token":"123:abc","chat_id":"42"}`, ""},
		{"missing bot_token", `{"chat_id":"42"}`, "bot_token"},
		{"missing chat_id", `{"bot_token":"123:abc"}`, "chat_id"},
		{"empty config", ``, "bot_token"},
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

// TestSendPostsMessage verifies the Telegram contract: a POST to
// /bot<token>/sendMessage carrying chat_id and a combined title/body text. The
// bot token rides in the URL path (Telegram's scheme) and must never appear in
// the JSON body.
func TestSendPostsMessage(t *testing.T) {
	var path, method, ct string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		method = r.Method
		ct = r.Header.Get("Content-Type")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New()
	p.baseURL = srv.URL
	msg := notify.Message{Title: "Monitor down: Example", Body: "It went down."}
	cfg := json.RawMessage(`{"bot_token":"123:secret","chat_id":"42"}`)
	if err := p.Send(context.Background(), cfg, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if method != http.MethodPost {
		t.Errorf("method = %q, want POST", method)
	}
	if path != "/bot123:secret/sendMessage" {
		t.Errorf("path = %q, want /bot123:secret/sendMessage", path)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if strings.Contains(string(body), "123:secret") {
		t.Errorf("bot token leaked into body: %q", body)
	}
	var got struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if got.ChatID != "42" {
		t.Errorf("chat_id = %q, want 42", got.ChatID)
	}
	if !strings.Contains(got.Text, "Monitor down: Example") || !strings.Contains(got.Text, "It went down.") {
		t.Errorf("text = %q, want title and body", got.Text)
	}
}

// TestSendBodyOnly proves a message with no title still sends its body as the
// text, with no leading blank lines.
func TestSendBodyOnly(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New()
	p.baseURL = srv.URL
	cfg := json.RawMessage(`{"bot_token":"123:secret","chat_id":"42"}`)
	if err := p.Send(context.Background(), cfg, notify.Message{Body: "Just the body."}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var got struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if got.Text != "Just the body." {
		t.Errorf("text = %q, want %q", got.Text, "Just the body.")
	}
}

func TestSendErrorOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	p := New()
	p.baseURL = srv.URL
	cfg := json.RawMessage(`{"bot_token":"123:secret","chat_id":"42"}`)
	if err := p.Send(context.Background(), cfg, notify.Message{Title: "x"}); err == nil {
		t.Fatal("Send returned nil for a 400 response")
	}
}
