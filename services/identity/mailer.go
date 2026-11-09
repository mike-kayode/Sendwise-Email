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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/observability"
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

// NewMailer selects the platform transactional transport. Production Identity
// mail uses the dedicated platform relay configuration, never the campaign
// SMTP settings; a configured Resend API is its fallback. Legacy SMTP settings
// remain available to local development only.
func NewMailer(cfg Config, logger *slog.Logger) Mailer {
	if logger == nil {
		logger = slog.Default()
	}
	provider := cfg.MailProvider
	if provider == "auto" || provider == "" {
		switch {
		case cfg.PlatformSMTPAddr != "" && cfg.PlatformFromEmail != "":
			provider = "smtp"
		case cfg.PlatformResendAPIKey != "" && cfg.PlatformFromEmail != "":
			provider = "resend"
		case cfg.Env != "production" && cfg.SMTPAddr != "" && cfg.SMTPFrom != "":
			provider = "smtp"
		default:
			provider = "log"
		}
	}
	switch provider {
	case "smtp":
		addr, host, username, password, tlsMode := cfg.PlatformSMTPAddr, cfg.PlatformSMTPHost,
			cfg.PlatformSMTPUsername, cfg.PlatformSMTPPassword, cfg.PlatformSMTPTLSMode
		from := cfg.PlatformFromEmail
		if cfg.Env != "production" && addr == "" {
			addr, host, username, password, tlsMode = cfg.SMTPAddr, cfg.SMTPHost,
				cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPTLSMode
			if from == "" {
				from = cfg.SMTPFrom
			}
		}
		primary := &smtpMailer{
			addr: addr, host: host, username: username,
			password: password, from: from, tlsMode: tlsMode,
		}
		if cfg.PlatformResendAPIKey != "" && cfg.PlatformFromEmail != "" {
			return &platformMailer{
				primary: primary,
				fallback: &resendMailer{
					apiKey: cfg.PlatformResendAPIKey, fromEmail: cfg.PlatformFromEmail,
					client: &http.Client{Timeout: 10 * time.Second},
				},
				breaker: newMailerCircuitBreaker(5, time.Minute, 2*time.Minute),
				logger:  logger,
			}
		}
		return primary
	case "resend":
		return &resendMailer{
			apiKey:    cfg.PlatformResendAPIKey,
			fromEmail: cfg.PlatformFromEmail,
			client:    &http.Client{Timeout: 10 * time.Second},
		}
	default:
		logger.Warn("mail provider is log: messages are printed, not delivered")
		return NewLogMailer(logger)
	}
}

type platformMailer struct {
	primary  Mailer
	fallback Mailer
	breaker  *mailerCircuitBreaker
	logger   *slog.Logger
}

func (m *platformMailer) Provider() string { return "smtp+fallback" }

func (m *platformMailer) Probe(ctx context.Context) error {
	probe, ok := m.primary.(interface{ Probe(context.Context) error })
	if !ok {
		return errors.New("primary SMTP provider does not support readiness probes")
	}
	return probe.Probe(ctx)
}

func (m *platformMailer) Send(ctx context.Context, message MailMessage) error {
	if !m.breaker.Allow() {
		m.logFallback(message, "circuit_open", errors.New("primary SMTP circuit is open"))
		return m.fallback.Send(ctx, message)
	}
	if err := m.primary.Send(ctx, message); err == nil {
		m.breaker.Success()
		return nil
	} else {
		m.breaker.Failure()
		if !shouldFailoverMail(err) {
			return err
		}
		m.logFallback(message, "provider_error", err)
		return m.fallback.Send(ctx, message)
	}
}

func (m *platformMailer) logFallback(message MailMessage, reason string, err error) {
	if m.logger != nil {
		m.logger.Warn("platform email provider fallback",
			"from", m.primary.Provider(), "to", m.fallback.Provider(), "reason", reason,
			"recipient_domain", mailRecipientDomain(message.To), "error", err,
		)
	}
	observability.ProviderFallbacks.WithLabelValues("platform", m.primary.Provider(), m.fallback.Provider(), reason).Inc()
}

func shouldFailoverMail(err error) bool {
	var smtpErr *smtpDeliveryError
	if errors.As(err, &smtpErr) {
		return smtpErr.code >= 400
	}
	var resendErr *resendDeliveryError
	if errors.As(err, &resendErr) {
		return resendErr.temporary
	}
	return true
}

type mailerCircuitBreaker struct {
	mu        sync.Mutex
	failures  []time.Time
	openUntil time.Time
	threshold int
	window    time.Duration
	openFor   time.Duration
	now       func() time.Time
}

func newMailerCircuitBreaker(threshold int, window, openFor time.Duration) *mailerCircuitBreaker {
	return &mailerCircuitBreaker{threshold: threshold, window: window, openFor: openFor, now: time.Now}
}

func (b *mailerCircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if now.Before(b.openUntil) {
		return false
	}
	if !b.openUntil.IsZero() {
		b.openUntil = time.Time{}
		b.failures = nil
	}
	return true
}

func (b *mailerCircuitBreaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	cutoff := now.Add(-b.window)
	firstValid := 0
	for firstValid < len(b.failures) && b.failures[firstValid].Before(cutoff) {
		firstValid++
	}
	b.failures = append(b.failures[firstValid:], now)
	if len(b.failures) >= b.threshold {
		b.openUntil = now.Add(b.openFor)
	}
}

func (b *mailerCircuitBreaker) Success() {
	b.mu.Lock()
	b.failures = nil
	b.openUntil = time.Time{}
	b.mu.Unlock()
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
	baseURL   string
	client    *http.Client
}

type resendDeliveryError struct {
	status    int
	temporary bool
	message   string
}

func (e *resendDeliveryError) Error() string {
	return fmt.Sprintf("Resend returned HTTP %d: %s", e.status, e.message)
}

func (m *resendMailer) Provider() string { return "resend" }

func (m *resendMailer) Send(ctx context.Context, message MailMessage) error {
	from := m.fromEmail
	if from == "" {
		return fmt.Errorf("resend from address is not configured")
	}
	payload := map[string]any{
		"from":    from,
		"to":      []string{message.To},
		"subject": message.Subject,
		"html":    message.HTML,
		"tags": []map[string]string{
			{"name": "workspace_id", "value": "platform"},
			{"name": "campaign_id", "value": "platform"},
			{"name": "message_id", "value": uuid.NewString()},
		},
	}
	if message.Text != "" {
		payload["text"] = message.Text
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal resend request: %w", err)
	}
	endpoint := m.baseURL
	if endpoint == "" {
		endpoint = "https://api.resend.com"
	}
	endpoint = strings.TrimRight(endpoint, "/") + "/emails"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
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
		return &resendDeliveryError{
			status: resp.StatusCode,
			temporary: resp.StatusCode == http.StatusRequestTimeout ||
				resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500,
			message: strings.TrimSpace(string(bodyBytes)),
		}
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
