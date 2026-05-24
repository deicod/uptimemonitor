// Package ntfy implements the ntfy notification provider (SPEC §18.5). It
// publishes a message to an ntfy server using the JSON publishing API: a POST
// to the server root carrying the topic, title, and message in the body.
package ntfy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/notify/providers/providerhttp"
)

// Provider is the ntfy notification provider. The single *http.Client is safe
// for concurrent use across delivery workers; per-send timeouts come from the
// caller's context.
type Provider struct {
	client *http.Client
}

var _ notify.Provider = (*Provider)(nil)

// New returns an ntfy provider ready to send.
func New() *Provider { return &Provider{client: &http.Client{}} }

// Kind implements notify.Provider.
func (p *Provider) Kind() string { return "ntfy" }

// DisplayName implements notify.Provider.
func (p *Provider) DisplayName() string { return "ntfy" }

// Fields describes the ntfy config form (SPEC §18.5). The token is optional —
// public topics need no auth — but is secret when present.
func (p *Provider) Fields() []notify.Field {
	return []notify.Field{
		{
			Name:        "server_url",
			Label:       "Server URL",
			Type:        notify.FieldTypeURL,
			Required:    true,
			Description: "Base URL of the ntfy server, e.g. https://ntfy.sh.",
		},
		{
			Name:        "topic",
			Label:       "Topic",
			Type:        notify.FieldTypeString,
			Required:    true,
			Description: "Topic the message is published to.",
		},
		{
			Name:        "token",
			Label:       "Access Token",
			Type:        notify.FieldTypeSecretString,
			Secret:      true,
			Description: "Optional ntfy access token for protected topics.",
		},
	}
}

type config struct {
	ServerURL string `json:"server_url"`
	Topic     string `json:"topic"`
	Token     string `json:"token"`
}

// Validate enforces a present, well-formed absolute http(s) server URL and a
// non-empty topic. The token is optional. Messages never echo the token, which
// is secret (SPEC §18.9).
func (p *Provider) Validate(_ context.Context, raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	return validateConfig(cfg)
}

// Send publishes the rendered message via the ntfy JSON publishing API.
func (p *Provider) Send(ctx context.Context, raw json.RawMessage, msg notify.Message) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	body, err := json.Marshal(payload{
		Topic:   cfg.Topic,
		Title:   msg.Title,
		Message: msg.Body,
	})
	if err != nil {
		return fmt.Errorf("ntfy: marshal payload: %w", err)
	}
	var headers map[string]string
	if cfg.Token != "" {
		headers = map[string]string{"Authorization": "Bearer " + cfg.Token}
	}
	endpoint := strings.TrimRight(cfg.ServerURL, "/")
	if err := providerhttp.PostJSONWithHeaders(ctx, p.client, http.MethodPost, endpoint, body, headers); err != nil {
		return fmt.Errorf("ntfy: %w", err)
	}
	return nil
}

// payload is the ntfy JSON publishing body (SPEC §18.5). It carries only
// non-secret message fields; the token travels in the Authorization header.
type payload struct {
	Topic   string `json:"topic"`
	Title   string `json:"title,omitempty"`
	Message string `json:"message"`
}

func parseConfig(raw json.RawMessage) (config, error) {
	var cfg config
	if len(raw) == 0 {
		return cfg, &notify.FieldError{Field: "server_url", Message: "must not be empty"}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("ntfy: invalid config json: %w", err)
	}
	return cfg, nil
}

func validateConfig(cfg config) error {
	if cfg.ServerURL == "" {
		return &notify.FieldError{Field: "server_url", Message: "must not be empty"}
	}
	u, err := url.Parse(cfg.ServerURL)
	switch {
	case err != nil:
		return &notify.FieldError{Field: "server_url", Message: "must be a valid URL"}
	case !u.IsAbs():
		return &notify.FieldError{Field: "server_url", Message: "must be an absolute URL"}
	case u.Scheme != "http" && u.Scheme != "https":
		return &notify.FieldError{Field: "server_url", Message: "scheme must be http or https"}
	case u.Host == "":
		return &notify.FieldError{Field: "server_url", Message: "must include a host"}
	}
	if cfg.Topic == "" {
		return &notify.FieldError{Field: "topic", Message: "must not be empty"}
	}
	return nil
}
