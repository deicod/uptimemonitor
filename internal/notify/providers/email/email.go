// Package email implements the SMTP notification provider (SPEC §18.5). It
// delivers a plain-text message to an SMTP server, optionally upgrading the
// connection with STARTTLS and authenticating with PLAIN. The SMTP password is
// secret (SPEC §18.9): it travels only in the AUTH exchange and is never placed
// in a returned error or the message body.
package email

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/deicod/uptimemonitor/internal/notify"
)

// defaultPort is the conventional SMTP submission port with STARTTLS.
const defaultPort = 587

// Provider is the SMTP notification provider.
type Provider struct {
	// tlsConfig, when non-nil, is used for the STARTTLS handshake instead of a
	// config derived from the target host. It exists as a test seam so the
	// suite can trust an in-process server's self-signed certificate.
	tlsConfig *tls.Config
}

var _ notify.Provider = (*Provider)(nil)

// New returns an SMTP provider ready to send.
func New() *Provider { return &Provider{} }

// Kind implements notify.Provider.
func (p *Provider) Kind() string { return "email" }

// DisplayName implements notify.Provider.
func (p *Provider) DisplayName() string { return "Email (SMTP)" }

// Fields describes the SMTP config form (SPEC §18.5). The password is the only
// secret; STARTTLS defaults to on for the submission port.
func (p *Provider) Fields() []notify.Field {
	return []notify.Field{
		{
			Name:        "host",
			Label:       "SMTP Host",
			Type:        notify.FieldTypeString,
			Required:    true,
			Description: "SMTP server hostname, e.g. smtp.example.com.",
		},
		{
			Name:        "port",
			Label:       "Port",
			Type:        notify.FieldTypeNumber,
			Required:    true,
			Default:     strconv.Itoa(defaultPort),
			Description: "SMTP server port, e.g. 587 for submission.",
		},
		{
			Name:        "username",
			Label:       "Username",
			Type:        notify.FieldTypeString,
			Description: "Username for SMTP authentication; leave blank for none.",
		},
		{
			Name:        "password",
			Label:       "Password",
			Type:        notify.FieldTypeSecretString,
			Secret:      true,
			Description: "Password for SMTP authentication.",
		},
		{
			Name:        "from",
			Label:       "From Address",
			Type:        notify.FieldTypeString,
			Required:    true,
			Description: "Envelope and header From address.",
		},
		{
			Name:        "to",
			Label:       "To Address",
			Type:        notify.FieldTypeString,
			Required:    true,
			Description: "Recipient address.",
		},
		{
			Name:        "starttls",
			Label:       "Use STARTTLS",
			Type:        notify.FieldTypeBool,
			Default:     "true",
			Description: "Upgrade the connection with STARTTLS before sending.",
		},
	}
}

type config struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"`
	StartTLS bool   `json:"starttls"`
}

// Validate enforces the required envelope fields and a usable port. Credentials
// are optional (an open relay or IP-allowlisted server needs none).
func (p *Provider) Validate(_ context.Context, raw json.RawMessage) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	return validateConfig(cfg)
}

// Send delivers the rendered message over SMTP.
func (p *Provider) Send(ctx context.Context, raw json.RawMessage, msg notify.Message) error {
	cfg, err := parseConfig(raw)
	if err != nil {
		return err
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: connect: %w", err)
	}
	// The conn is handed to the smtp.Client, which closes it via Quit/Close.
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("email: %w", err)
	}
	defer func() { _ = client.Close() }()

	if cfg.StartTLS {
		tlsCfg := p.tlsConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}

	if cfg.Username != "" || cfg.Password != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			// smtp surfaces the server's reply, which does not echo the
			// password; the password itself is never in this error.
			return fmt.Errorf("email: auth: %w", err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("email: mail from: %w", err)
	}
	if err := client.Rcpt(cfg.To); err != nil {
		return fmt.Errorf("email: rcpt to: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("email: data: %w", err)
	}
	if _, err := wc.Write(buildMessage(cfg, msg)); err != nil {
		return fmt.Errorf("email: write body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("email: close body: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("email: quit: %w", err)
	}
	return nil
}

// buildMessage renders an RFC 5322 plain-text message. Title becomes the
// Subject and Body the message text. Header values are stripped of CR/LF
// (sanitizeHeader) so a value such as a monitor name folded into the Subject
// cannot inject additional headers (CWE-93); the provider does not rely on
// upstream validation to guarantee single-line header content. The body is
// written below the header separator and is dot-stuffed by net/smtp's DATA
// writer, so it cannot forge headers or terminate DATA early.
func buildMessage(cfg config, msg notify.Message) []byte {
	when := msg.Time
	if when.IsZero() {
		when = time.Now()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", sanitizeHeader(cfg.From))
	fmt.Fprintf(&b, "To: %s\r\n", sanitizeHeader(cfg.To))
	fmt.Fprintf(&b, "Subject: %s\r\n", sanitizeHeader(msg.Title))
	fmt.Fprintf(&b, "Date: %s\r\n", when.Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	return []byte(b.String())
}

// sanitizeHeader removes CR and LF from a header value. These characters have
// no legitimate place in the single-line headers built here, and removing them
// prevents header injection regardless of how the value was produced.
func sanitizeHeader(v string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}

func parseConfig(raw json.RawMessage) (config, error) {
	var cfg config
	if len(raw) == 0 {
		return cfg, &notify.FieldError{Field: "host", Message: "must not be empty"}
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("email: invalid config json: %w", err)
	}
	return cfg, nil
}

func validateConfig(cfg config) error {
	if cfg.Host == "" {
		return &notify.FieldError{Field: "host", Message: "must not be empty"}
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return &notify.FieldError{Field: "port", Message: "must be between 1 and 65535"}
	}
	if cfg.From == "" {
		return &notify.FieldError{Field: "from", Message: "must not be empty"}
	}
	if cfg.To == "" {
		return &notify.FieldError{Field: "to", Message: "must not be empty"}
	}
	return nil
}
