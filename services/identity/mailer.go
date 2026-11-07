package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"net/textproto"
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
		return &smtpMailer{
			addr: cfg.SMTPAddr, host: cfg.SMTPHost, username: cfg.SMTPUsername,
			password: cfg.SMTPPassword, from: cfg.SMTPFrom, tlsMode: cfg.SMTPTLSMode,
		}
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
	addr      string
	host      string
	username  string
	password  string
	from      string
	tlsMode   string
	tlsConfig *tls.Config
}

func (m *smtpMailer) Provider() string { return "smtp" }

type smtpDeliveryError struct {
	phase    string
	code     int
	response string
}

func (e *smtpDeliveryError) Error() string {
	return fmt.Sprintf("SMTP %s failed with code %d: %s", e.phase, e.code, e.response)
}

type smtpMechanismAuth struct {
	mechanism string
	username  string
	password  string
	step      int
}

func (a *smtpMechanismAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	if a.mechanism == "PLAIN" {
		return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
	}
	return "LOGIN", nil, nil
}

func (a *smtpMechanismAuth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if a.mechanism != "LOGIN" {
		return nil, fmt.Errorf("unexpected SMTP authentication challenge")
	}
	a.step++
	switch a.step {
	case 1:
		return []byte(a.username), nil
	case 2:
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("too many SMTP authentication challenges")
	}
}

func smtpCommandFailure(phase string, err error) error {
	if err == nil {
		return nil
	}
	var response *textproto.Error
	if errors.As(err, &response) {
		return &smtpDeliveryError{phase: phase, code: response.Code, response: response.Msg}
	}
	return fmt.Errorf("smtp %s: %w", phase, err)
}

func (m *smtpMailer) connect(ctx context.Context) (*smtp.Client, func(), error) {
	host := m.host
	if host == "" {
		host, _, _ = net.SplitHostPort(m.addr)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	mode := m.tlsMode
	if mode == "" {
		mode = "none"
	}

	conn, err := dialer.DialContext(ctx, "tcp", m.addr)
	if err != nil {
		return nil, nil, fmt.Errorf("smtp dial: %w", err)
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	cleanup := func() {
		stopCancel()
		_ = conn.Close()
	}
	if mode == "tls" {
		tlsConn := tls.Client(conn, m.tlsConfiguration(host))
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("smtp TLS handshake: %w", err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("smtp greeting: %w", err)
	}

	if mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			_ = client.Close()
			cleanup()
			return nil, nil, fmt.Errorf("smtp STARTTLS is required but the server does not advertise it")
		}
		if err := client.StartTLS(m.tlsConfiguration(host)); err != nil {
			_ = client.Close()
			cleanup()
			return nil, nil, smtpCommandFailure("STARTTLS", err)
		}
	} else if mode != "none" && mode != "tls" {
		_ = client.Close()
		cleanup()
		return nil, nil, fmt.Errorf("unsupported SMTP_TLS_MODE %q", mode)
	}
	return client, func() {
		_ = client.Close()
		cleanup()
	}, nil
}

func (m *smtpMailer) tlsConfiguration(host string) *tls.Config {
	config := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if m.tlsConfig != nil {
		config = m.tlsConfig.Clone()
		if config.ServerName == "" {
			config.ServerName = host
		}
		if config.MinVersion == 0 {
			config.MinVersion = tls.VersionTLS12
		}
	}
	return config
}

func (m *smtpMailer) authenticate(client *smtp.Client) error {
	if m.username == "" {
		return nil
	}
	mode := m.tlsMode
	if mode == "" {
		mode = "none"
	}
	host := m.host
	if host == "" {
		host, _, _ = net.SplitHostPort(m.addr)
	}
	if mode == "none" && !isLoopbackSMTPHost(host) {
		return fmt.Errorf("SMTP authentication requires TLS when connecting to a non-loopback host")
	}
	ok, mechanisms := client.Extension("AUTH")
	if !ok {
		return fmt.Errorf("SMTP server does not advertise AUTH")
	}
	supported := make(map[string]bool)
	for _, mechanism := range strings.Fields(mechanisms) {
		supported[strings.ToUpper(mechanism)] = true
	}
	for _, mechanism := range []string{"PLAIN", "LOGIN"} {
		if supported[mechanism] {
			auth := &smtpMechanismAuth{
				mechanism: mechanism,
				username:  m.username,
				password:  m.password,
			}
			if err := client.Auth(auth); err != nil {
				return redactSMTPSecret(smtpCommandFailure("AUTH "+mechanism, err), m.username, m.password)
			}
			return nil
		}
	}
	return fmt.Errorf("SMTP server advertises no supported AUTH mechanism (PLAIN or LOGIN)")
}

func redactSMTPSecret(err error, username, password string) error {
	if err == nil || password == "" {
		return err
	}
	message := err.Error()
	for _, secret := range []string{
		password,
		base64.StdEncoding.EncodeToString([]byte(password)),
		base64.RawStdEncoding.EncodeToString([]byte(password)),
		base64.StdEncoding.EncodeToString([]byte("\x00" + username + "\x00" + password)),
	} {
		message = strings.ReplaceAll(message, secret, "[redacted]")
	}
	return errors.New(message)
}

func isLoopbackSMTPHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Probe performs the same connection setup and authentication as delivery but
// does not issue MAIL, RCPT, or DATA commands.
func (m *smtpMailer) Probe(ctx context.Context) error {
	client, closeClient, err := m.connect(ctx)
	if err != nil {
		return err
	}
	defer closeClient()
	if err := m.authenticate(client); err != nil {
		return err
	}
	return client.Quit()
}

func (m *smtpMailer) Send(ctx context.Context, message MailMessage) error {
	if m.from == "" {
		return fmt.Errorf("smtp from address is not configured")
	}

	client, closeClient, err := m.connect(ctx)
	if err != nil {
		return err
	}
	defer closeClient()
	if err := m.authenticate(client); err != nil {
		return err
	}

	if err := client.Mail(m.from); err != nil {
		return smtpCommandFailure("MAIL FROM", err)
	}
	if err := client.Rcpt(message.To); err != nil {
		return smtpCommandFailure("RCPT TO", err)
	}

	w, err := client.Data()
	if err != nil {
		return smtpCommandFailure("DATA", err)
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
		return smtpCommandFailure("DATA", err)
	}

	return smtpCommandFailure("QUIT", client.Quit())
}
