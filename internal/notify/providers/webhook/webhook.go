// Package webhook implements the generic JSON webhook notification provider
// (SPEC §18.5). It POSTs a JSON document describing the notification to a
// configured URL.
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/notify/providers/providerhttp"
)

// Provider is the generic webhook notification provider. The single
// *http.Client is safe for concurrent use across delivery workers; per-send
// timeouts come from the caller's context.
type Provider struct {
	client *http.Client
}

var _ notify.Provider = (*Provider)(nil)

// New returns a webhook provider ready to send.
func New() *Provider { return &Provider{client: &http.Client{}} }

// Kind implements notify.Provider.
func (p *Provider) Kind() string { return "webhook" }

// DisplayName implements notify.Provider.
func (p *Provider) DisplayName() string { return "Webhook" }

// Fields describes the webhook config form (SPEC §18.4–18.5). The URL is a
// secret because webhook endpoints commonly embed an authentication token in
// the path or query string.
func (p *Provider) Fields() []notify.Field {
	return []notify.Field{
		{
			Name:        "url",
			Label:       "Webhook URL",
			Type:        notify.FieldTypeSecretString,
			Required:    true,
			Secret:      true,
			Description: "URL the JSON payload is posted to.",
		},
		{
			Name:        "method",
			Label:       "HTTP Method",
			Type:        notify.FieldTypeString,
			Required:    true,
			Default:     http.MethodPost,
			Description: "HTTP method used to post the payload.",
		},
	}
}

type config struct {
	URL    string `json:"url"`
	Method string `json:"method"`
}

// Validate enforces that url is present and a well-formed absolute http(s)
// URL. method advertises a default (POST), so a blank method is filled in at
// send time rather than rejected here. Error messages never echo the URL
// value, which is secret (SPEC §18.9).
func (p *Provider) Validate(_ context.Context, raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	return validateConfig(cfg)
}

// Send posts the rendered message as JSON to the configured URL.
func (p *Provider) Send(ctx context.Context, raw json.RawMessage, msg notify.Message) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	body, err := json.Marshal(payload{
		EventType:   msg.EventType,
		MonitorID:   msg.MonitorID,
		MonitorName: msg.MonitorName,
		State:       msg.State,
		Title:       msg.Title,
		Body:        msg.Body,
		URL:         msg.URL,
		Time:        msg.Time.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("webhook: marshal payload: %w", err)
	}
	if err := providerhttp.PostJSON(ctx, p.client, cfg.Method, cfg.URL, body); err != nil {
		return fmt.Errorf("webhook: %w", err)
	}
	return nil
}

// payload is the JSON document posted to the webhook endpoint. It carries only
// non-secret message fields (SPEC §18.9).
type payload struct {
	EventType   string `json:"event_type"`
	MonitorID   string `json:"monitor_id"`
	MonitorName string `json:"monitor_name"`
	State       string `json:"state,omitempty"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	URL         string `json:"url,omitempty"`
	Time        string `json:"time"`
}

func parseConfig(raw json.RawMessage) (config, error) {
	var cfg config
	if len(raw) == 0 {
		return cfg, &notify.FieldError{Field: "url", Message: "must not be empty"}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("webhook: invalid config json: %w", err)
	}
	return cfg, nil
}

func validateConfig(cfg config) error {
	if cfg.URL == "" {
		return &notify.FieldError{Field: "url", Message: "must not be empty"}
	}
	u, err := url.Parse(cfg.URL)
	switch {
	case err != nil:
		return &notify.FieldError{Field: "url", Message: "must be a valid URL"}
	case !u.IsAbs():
		return &notify.FieldError{Field: "url", Message: "must be an absolute URL"}
	case u.Scheme != "http" && u.Scheme != "https":
		return &notify.FieldError{Field: "url", Message: "scheme must be http or https"}
	case u.Host == "":
		return &notify.FieldError{Field: "url", Message: "must include a host"}
	}
	return nil
}
