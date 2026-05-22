// Package slack implements the Slack incoming-webhook notification provider
// (SPEC §18.5). It POSTs a JSON message to a Slack incoming-webhook URL.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/notify/providers/providerhttp"
)

// Provider is the Slack notification provider.
type Provider struct {
	client *http.Client
}

var _ notify.Provider = (*Provider)(nil)

// New returns a Slack provider ready to send.
func New() *Provider { return &Provider{client: &http.Client{}} }

// Kind implements notify.Provider.
func (p *Provider) Kind() string { return "slack" }

// DisplayName implements notify.Provider.
func (p *Provider) DisplayName() string { return "Slack" }

// Fields describes the Slack config form (SPEC §18.5). The incoming-webhook
// URL carries a secret token and is therefore a secret field.
func (p *Provider) Fields() []notify.Field {
	return []notify.Field{
		{
			Name:        "webhook_url",
			Label:       "Webhook URL",
			Type:        notify.FieldTypeSecretString,
			Required:    true,
			Secret:      true,
			Description: "Slack incoming-webhook URL.",
		},
	}
}

type config struct {
	WebhookURL string `json:"webhook_url"`
}

// Validate enforces a present, well-formed absolute http(s) webhook URL.
// Messages never echo the URL value, which is secret (SPEC §18.9).
func (p *Provider) Validate(_ context.Context, raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	return validateConfig(cfg)
}

// Send posts the rendered message to the Slack webhook. Slack's "text" field
// is the only one MVP populates.
func (p *Provider) Send(ctx context.Context, raw json.RawMessage, msg notify.Message) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	body, err := json.Marshal(payload{Text: messageText(msg)})
	if err != nil {
		return fmt.Errorf("slack: marshal payload: %w", err)
	}
	if err := providerhttp.PostJSON(ctx, p.client, http.MethodPost, cfg.WebhookURL, body); err != nil {
		return fmt.Errorf("slack: %w", err)
	}
	return nil
}

// payload is the minimal Slack incoming-webhook body (SPEC §18.5).
type payload struct {
	Text string `json:"text"`
}

func parseConfig(raw json.RawMessage) (config, error) {
	var cfg config
	if len(raw) == 0 {
		return cfg, &notify.FieldError{Field: "webhook_url", Message: "must not be empty"}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("slack: invalid config json: %w", err)
	}
	return cfg, nil
}

func validateConfig(cfg config) error {
	if cfg.WebhookURL == "" {
		return &notify.FieldError{Field: "webhook_url", Message: "must not be empty"}
	}
	u, err := url.Parse(cfg.WebhookURL)
	switch {
	case err != nil:
		return &notify.FieldError{Field: "webhook_url", Message: "must be a valid URL"}
	case !u.IsAbs():
		return &notify.FieldError{Field: "webhook_url", Message: "must be an absolute URL"}
	case u.Scheme != "http" && u.Scheme != "https":
		return &notify.FieldError{Field: "webhook_url", Message: "scheme must be http or https"}
	case u.Host == "":
		return &notify.FieldError{Field: "webhook_url", Message: "must include a host"}
	}
	return nil
}

// messageText joins the rendered title and body into the single text field the
// chat providers expose. Title and body come from notify.Render; no secrets
// are involved (SPEC §18.9).
func messageText(msg notify.Message) string {
	switch {
	case msg.Title == "":
		return msg.Body
	case msg.Body == "":
		return msg.Title
	default:
		return msg.Title + "\n\n" + msg.Body
	}
}
