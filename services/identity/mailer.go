package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

// MailMessage is a transactional message sent by the Identity service.
// Transactional mail is intentionally separate from marketing delivery.
type MailMessage struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// Mailer delivers verification and password-reset messages.
type Mailer interface {
	Send(ctx context.Context, message MailMessage) error
	// Provider reports the configured backend name for logs and diagnostics.
	Provider() string
}

// NewMailer selects a mail backend from configuration. The "auto" mode prefers
// Resend when an API key is present, then SMTP, and finally falls back to a
// development logger that prints links instead of sending mail.
func NewMailer(cfg Config, logger *slog.Logger) Mailer {
	provider := cfg.MailProvider
	if provider == "auto" || provider == "" {
		switch {
		case cfg.ResendAPIKey != "":
			provider = "resend"
		case cfg.SMTPAddr != "" && cfg.SMTPFrom != "":
			provider = "smtp"
		default:
			provider = "log"
		}
	}
	switch provider {
	case "resend":
		if cfg.ResendAPIKey == "" {
			logger.Warn("MAIL_PROVIDER=resend but RESEND_API_KEY is empty; falling back to log mailer")
			return NewLogMailer(logger)
		}
		return &resendMailer{apiKey: cfg.ResendAPIKey, fromEmail: cfg.ResendFromEmail, fromName: cfg.ResendFromName, client: &http.Client{Timeout: 10 * time.Second}}
	case "smtp":
		return &smtpMailer{addr: cfg.SMTPAddr, host: cfg.SMTPHost, username: cfg.SMTPUsername, password: cfg.SMTPPassword, from: cfg.SMTPFrom}
	default:
		logger.Warn("mail provider is log: messages are printed, not delivered")
		return NewLogMailer(logger)
	}
}

// LogMailer prints messages; used in development and tests.
type LogMailer struct {
	logger *slog.Logger
	sent   []MailMessage
}

// NewLogMailer creates a development mailer that records messages.
func NewLogMailer(logger *slog.Logger) *LogMailer {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogMailer{logger: logger}
}

func (m *LogMailer) Provider() string { return "log" }

func (m *LogMailer) Send(_ context.Context, message MailMessage) error {
	m.sent = append(m.sent, message)
	// The log mailer exists for development and tests: it must surface the
	// verification/reset link so the journey can be completed without a live
	// provider. Never configure MAIL_PROVIDER=log in production.
	m.logger.Info("dev mail delivery",
		"provider", "log",
		"to", message.To,
		"subject", message.Subject,
		"link", firstLink(message.Text),
	)
	return nil
}

// firstLink extracts the first http(s) link from a plain-text message body.
func firstLink(text string) string {
	const http = "http"
	i := strings.Index(text, http)
	if i < 0 {
		return ""
	}
	rest := text[i:]
	end := len(rest)
	for j, r := range rest {
		if r == ' ' || r == '\n' || r == '\r' || r == '"' || r == '\'' {
			end = j
			break
		}
	}
	return rest[:end]
}

// Sent returns a copy of recorded messages (test helper).
func (m *LogMailer) Sent() []MailMessage {
	out := make([]MailMessage, len(m.sent))
	copy(out, m.sent)
	return out
}

type resendMailer struct {
	apiKey    string
	fromEmail string
	fromName  string
	client    *http.Client
}

func (m *resendMailer) Provider() string { return "resend" }

func (m *resendMailer) Send(ctx context.Context, message MailMessage) error {
	from := m.fromEmail
	if from == "" {
		return fmt.Errorf("resend from address is not configured")
	}
	if m.fromName != "" {
		from = fmt.Sprintf("%s <%s>", m.fromName, from)
	}
	payload := map[string]any{
		"from":    from,
		"to":      []string{message.To},
		"subject": message.Subject,
		"html":    message.HTML,
	}
	if message.Text != "" {
		payload["text"] = message.Text
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal resend request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build resend request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("send resend request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("resend returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}

type smtpMailer struct {
	addr     string
	host     string
	username string
	password string
	from     string
}

func (m *smtpMailer) Provider() string { return "smtp" }

func (m *smtpMailer) Send(ctx context.Context, message MailMessage) error {
	if m.from == "" {
		return fmt.Errorf("smtp from address is not configured")
	}

	dial := &net.Dialer{}
	conn, err := dial.DialContext(ctx, "tcp", m.addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer client.Close()

	// Upgrade to TLS if the server supports STARTTLS (required for port 587 submission).
	if ok, _ := client.Extension("STARTTLS"); ok {
		config := &tls.Config{
			ServerName: m.host,
		}
		if config.ServerName == "" {
			config.ServerName = strings.SplitN(m.addr, ":", 2)[0]
		}
		if err := client.StartTLS(config); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}

	var auth smtp.Auth
	if m.username != "" {
		host := m.host
		if host == "" {
			host = strings.SplitN(m.addr, ":", 2)[0]
		}
		auth = smtp.PlainAuth("", m.username, m.password, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(m.from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(message.To); err != nil {
		return fmt.Errorf("smtp rcpt to: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}

	headers := []string{
		"From: " + m.from,
		"To: " + message.To,
		"Subject: " + message.Subject,
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
	}
	body := strings.Join(headers, "\r\n") + "\r\n\r\n" + message.HTML

	if _, err := fmt.Fprint(w, body); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}

	return client.Quit()
}
