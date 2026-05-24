// Package telegram implements the Telegram notification provider (SPEC §18.5).
// It posts a message through the Bot API sendMessage method: a JSON POST to
// /bot<bot_token>/sendMessage carrying the chat ID and message text. The bot
// token rides in the URL path per Telegram's scheme, so error sanitisation in
// providerhttp (which never echoes the URL) keeps it out of logs (SPEC §18.9).
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/deicod/uptimemonitor/internal/notify"
	"github.com/deicod/uptimemonitor/internal/notify/providers/providerhttp"
)

// defaultBaseURL is the Telegram Bot API root. It seeds Provider.baseURL, which
// tests override to point Send at an httptest server.
const defaultBaseURL = "https://api.telegram.org"

// Provider is the Telegram notification provider. The single *http.Client is
// safe for concurrent use across delivery workers; per-send timeouts come from
// the caller's context.
type Provider struct {
	client  *http.Client
	baseURL string
}

var _ notify.Provider = (*Provider)(nil)

// New returns a Telegram provider ready to send against the public Bot API.
func New() *Provider {
	return &Provider{client: &http.Client{}, baseURL: defaultBaseURL}
}

// Kind implements notify.Provider.
func (p *Provider) Kind() string { return "telegram" }

// DisplayName implements notify.Provider.
func (p *Provider) DisplayName() string { return "Telegram" }

// Fields describes the Telegram config form (SPEC §18.5). The bot token is a
// required secret; the chat ID is a required string (it may be a numeric ID or
// an @channel handle, so it is not constrained to digits).
func (p *Provider) Fields() []notify.Field {
	return []notify.Field{
		{
			Name:        "bot_token",
			Label:       "Bot Token",
			Type:        notify.FieldTypeSecretString,
			Required:    true,
			Secret:      true,
			Description: "Telegram bot token from @BotFather.",
		},
		{
			Name:        "chat_id",
			Label:       "Chat ID",
			Type:        notify.FieldTypeString,
			Required:    true,
			Description: "Target chat ID, e.g. 123456 or @channelname.",
		},
	}
}

type config struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// Validate enforces a non-empty bot token and chat ID. Messages never echo the
// bot token, which is secret (SPEC §18.9).
func (p *Provider) Validate(_ context.Context, raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	return validateConfig(cfg)
}

// Send delivers the rendered message via the Bot API sendMessage method.
func (p *Provider) Send(ctx context.Context, raw json.RawMessage, msg notify.Message) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	body, err := json.Marshal(payload{
		ChatID: cfg.ChatID,
		Text:   messageText(msg),
	})
	if err != nil {
		return fmt.Errorf("telegram: marshal payload: %w", err)
	}
	endpoint := strings.TrimRight(p.baseURL, "/") + "/bot" + cfg.BotToken + "/sendMessage"
	if err := providerhttp.PostJSON(ctx, p.client, http.MethodPost, endpoint, body); err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	return nil
}

// payload is the Telegram sendMessage body (SPEC §18.5). The bot token is not a
// payload field — it is part of the request URL — so nothing secret is carried
// in the body.
type payload struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

// messageText folds the title and body into Telegram's single text field,
// joining them with a blank line when both are present and omitting either when
// empty so no stray blank lines appear.
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

func parseConfig(raw json.RawMessage) (config, error) {
	var cfg config
	if len(raw) == 0 {
		return cfg, &notify.FieldError{Field: "bot_token", Message: "must not be empty"}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("telegram: invalid config json: %w", err)
	}
	return cfg, nil
}

func validateConfig(cfg config) error {
	if cfg.BotToken == "" {
		return &notify.FieldError{Field: "bot_token", Message: "must not be empty"}
	}
	if cfg.ChatID == "" {
		return &notify.FieldError{Field: "chat_id", Message: "must not be empty"}
	}
	return nil
}
