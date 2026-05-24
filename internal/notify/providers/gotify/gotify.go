// Package gotify implements the Gotify notification provider (SPEC §18.5). It
// POSTs a message to a Gotify server's /message endpoint, authenticating with
// an application token sent in the X-Gotify-Key header.
package gotify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/notify/providers/providerhttp"
)

// defaultPriority is sent when the config omits priority (SPEC §18.5 example).
const defaultPriority = 5

// Provider is the Gotify notification provider. The single *http.Client is safe
// for concurrent use across delivery workers; per-send timeouts come from the
// caller's context.
type Provider struct {
	client *http.Client
}

var _ notify.Provider = (*Provider)(nil)

// New returns a Gotify provider ready to send.
func New() *Provider { return &Provider{client: &http.Client{}} }

// Kind implements notify.Provider.
func (p *Provider) Kind() string { return "gotify" }

// DisplayName implements notify.Provider.
func (p *Provider) DisplayName() string { return "Gotify" }

// Fields describes the Gotify config form (SPEC §18.5). The application token
// is a required secret; priority is an optional number defaulting to 5.
func (p *Provider) Fields() []notify.Field {
	return []notify.Field{
		{
			Name:        "server_url",
			Label:       "Server URL",
			Type:        notify.FieldTypeURL,
			Required:    true,
			Description: "Base URL of the Gotify server, e.g. https://gotify.example.com.",
		},
		{
			Name:        "token",
			Label:       "Application Token",
			Type:        notify.FieldTypeSecretString,
			Required:    true,
			Secret:      true,
			Description: "Gotify application token used to post messages.",
		},
		{
			Name:        "priority",
			Label:       "Priority",
			Type:        notify.FieldTypeNumber,
			Default:     strconv.Itoa(defaultPriority),
			Description: "Message priority (higher is more urgent).",
		},
	}
}

type config struct {
	ServerURL string `json:"server_url"`
	Token     string `json:"token"`
	Priority  int    `json:"priority"`
}

// Validate enforces a present, well-formed absolute http(s) server URL and a
// non-empty token. Messages never echo the token, which is secret (SPEC §18.9).
func (p *Provider) Validate(_ context.Context, raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	return validateConfig(cfg)
}

// Send posts the rendered message to the Gotify /message endpoint.
func (p *Provider) Send(ctx context.Context, raw json.RawMessage, msg notify.Message) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	priority := cfg.Priority
	if priority == 0 {
		priority = defaultPriority
	}
	body, err := json.Marshal(payload{
		Title:    msg.Title,
		Message:  msg.Body,
		Priority: priority,
	})
	if err != nil {
		return fmt.Errorf("gotify: marshal payload: %w", err)
	}
	endpoint := strings.TrimRight(cfg.ServerURL, "/") + "/message"
	headers := map[string]string{"X-Gotify-Key": cfg.Token}
	if err := providerhttp.PostJSONWithHeaders(ctx, p.client, http.MethodPost, endpoint, body, headers); err != nil {
		return fmt.Errorf("gotify: %w", err)
	}
	return nil
}

// payload is the Gotify message body (SPEC §18.5). It carries only non-secret
// message fields; the token travels in the X-Gotify-Key header.
type payload struct {
	Title    string `json:"title"`
	Message  string `json:"message"`
	Priority int    `json:"priority"`
}

func parseConfig(raw json.RawMessage) (config, error) {
	var cfg config
	if len(raw) == 0 {
		return cfg, &notify.FieldError{Field: "server_url", Message: "must not be empty"}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("gotify: invalid config json: %w", err)
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
	if cfg.Token == "" {
		return &notify.FieldError{Field: "token", Message: "must not be empty"}
	}
	return nil
}
