package transactional

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-sasl"
	smtpserver "github.com/emersion/go-smtp"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/config"
)

type SMTPSubmissionServer struct {
	cfg   config.Config
	pool  *pgxpool.Pool
	store *Store
}

func NewSMTPSubmissionServer(cfg config.Config, pool *pgxpool.Pool) *SMTPSubmissionServer {
	return &SMTPSubmissionServer{cfg: cfg, pool: pool, store: NewStore(pool)}
}

func (s *SMTPSubmissionServer) Run(ctx context.Context) error {
	if !s.cfg.TransactionalSMTPEnabled {
		return nil
	}
	certificate, err := tls.LoadX509KeyPair(s.cfg.TransactionalSMTPTLSCert, s.cfg.TransactionalSMTPTLSKey)
	if err != nil {
		return fmt.Errorf("load transactional SMTP TLS certificate: %w", err)
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}
	startTLSListener, err := net.Listen("tcp", s.cfg.TransactionalSMTPStartTLSAddr)
	if err != nil {
		return fmt.Errorf("listen for transactional SMTP STARTTLS: %w", err)
	}
	implicitTLSListener, err := net.Listen("tcp", s.cfg.TransactionalSMTPImplicitAddr)
	if err != nil {
		_ = startTLSListener.Close()
		return fmt.Errorf("listen for transactional SMTP implicit TLS: %w", err)
	}
	return s.serve(ctx, startTLSListener, implicitTLSListener, tlsConfig)
}

func (s *SMTPSubmissionServer) serve(
	ctx context.Context,
	startTLSListener, implicitTLSListener net.Listener,
	tlsConfig *tls.Config,
) error {
	backend := &smtpSubmissionBackend{cfg: s.cfg, pool: s.pool, store: s.store}
	startTLS := newSMTPProtocolServer(backend, tlsConfig, s.cfg.TransactionalSMTPHostname, s.cfg.TransactionalMaxRecipients, s.cfg.TransactionalMaxMessageBytes)
	implicitTLS := newSMTPProtocolServer(backend, nil, s.cfg.TransactionalSMTPHostname, s.cfg.TransactionalMaxRecipients, s.cfg.TransactionalMaxMessageBytes)
	implicitTLSListener = tls.NewListener(implicitTLSListener, tlsConfig)

	errorsCh := make(chan error, 2)
	go func() { errorsCh <- startTLS.Serve(startTLSListener) }()
	go func() { errorsCh <- implicitTLS.Serve(implicitTLSListener) }()

	select {
	case <-ctx.Done():
		return shutdownSMTPServers(startTLS, implicitTLS)
	case err := <-errorsCh:
		shutdownErr := shutdownSMTPServers(startTLS, implicitTLS)
		if ctx.Err() != nil {
			return shutdownErr
		}
		if err == nil {
			err = errors.New("listener exited unexpectedly")
		}
		if errors.Is(err, net.ErrClosed) {
			err = errors.New("listener closed unexpectedly")
		}
		return errors.Join(fmt.Errorf("transactional SMTP listener stopped: %w", err), shutdownErr)
	}
}

func (s *SMTPSubmissionServer) ServeListeners(
	ctx context.Context,
	startTLSListener, implicitTLSListener net.Listener,
	tlsConfig *tls.Config,
) error {
	return s.serve(ctx, startTLSListener, implicitTLSListener, tlsConfig)
}

func shutdownSMTPServers(startTLS, implicitTLS *smtpserver.Server) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err1 := startTLS.Shutdown(shutdownCtx)
	err2 := implicitTLS.Shutdown(shutdownCtx)
	if err1 != nil {
		err1 = fmt.Errorf("shutdown transactional SMTP STARTTLS listener: %w", err1)
	}
	if err2 != nil {
		err2 = fmt.Errorf("shutdown transactional SMTP implicit TLS listener: %w", err2)
	}
	return errors.Join(err1, err2)
}

func newSMTPProtocolServer(backend smtpserver.Backend, tlsConfig *tls.Config, hostname string, maxRecipients int, maxMessageBytes int64) *smtpserver.Server {
	server := smtpserver.NewServer(backend)
	server.Domain = hostname
	server.MaxRecipients = maxRecipients
	server.MaxMessageBytes = maxMessageBytes
	server.ReadTimeout = 2 * time.Minute
	server.WriteTimeout = 2 * time.Minute
	server.EnableSMTPUTF8 = false
	server.EnableDSN = true
	server.TLSConfig = tlsConfig
	return server
}

type smtpSubmissionBackend struct {
	cfg   config.Config
	pool  *pgxpool.Pool
	store *Store
}

func (b *smtpSubmissionBackend) NewSession(conn *smtpserver.Conn) (smtpserver.Session, error) {
	return &smtpSubmissionSession{backend: b, conn: conn}, nil
}

type smtpSubmissionSession struct {
	backend      *smtpSubmissionBackend
	conn         *smtpserver.Conn
	credential   SMTPCredential
	envelopeFrom string
	recipients   []string
}

func (s *smtpSubmissionSession) AuthMechanisms() []string {
	return []string{sasl.Plain}
}

func (s *smtpSubmissionSession) Auth(mechanism string) (sasl.Server, error) {
	if !s.backend.cfg.TransactionalEnabled || !s.backend.cfg.TransactionalSMTPEnabled {
		return nil, smtpError(421, "4.3.2", "Transactional SMTP submission is disabled")
	}
	if mechanism != sasl.Plain {
		return nil, errors.New("unsupported authentication mechanism")
	}
	if _, ok := s.conn.TLSConnectionState(); !ok {
		return nil, smtpError(538, "5.7.11", "Encryption is required for SMTP authentication")
	}
	return sasl.NewPlainServer(func(identity, username, password string) error {
		if identity != "" && identity != username {
			return errors.New("invalid SMTP credentials")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		credential, err := AuthenticateSMTP(ctx, s.backend.pool, username, password)
		if err != nil {
			if errors.Is(err, ErrInvalidSMTPCredentials) {
				return errors.New("invalid SMTP credentials")
			}
			return smtpError(451, "4.3.0", "SMTP authentication is temporarily unavailable")
		}
		s.credential = credential
		return nil
	}), nil
}

func (s *smtpSubmissionSession) Mail(from string, _ *smtpserver.MailOptions) error {
	if s.credential.ID == "" {
		return smtpError(530, "5.7.0", "Authenticate before submitting mail")
	}
	address, err := ValidateEmail(from)
	if err != nil {
		return smtpError(553, "5.1.7", "The envelope sender is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.validateSender(ctx, address); err != nil {
		if errors.Is(err, errSMTPSenderNotAllowed) {
			return smtpError(553, "5.7.1", "The sender domain is not enabled for transactional sending")
		}
		return smtpError(451, "4.3.0", "Sender authorization is temporarily unavailable")
	}
	s.envelopeFrom = address
	s.recipients = nil
	return nil
}

func (s *smtpSubmissionSession) Rcpt(to string, _ *smtpserver.RcptOptions) error {
	if s.credential.ID == "" || s.envelopeFrom == "" {
		return smtpError(530, "5.7.0", "Authenticate and specify a sender before adding recipients")
	}
	address, err := ValidateEmail(to)
	if err != nil {
		return smtpError(553, "5.1.3", "The recipient address is invalid")
	}
	if len(s.recipients) >= s.backend.cfg.TransactionalMaxRecipients {
		return smtpError(452, "4.5.3", "The recipient limit has been reached")
	}
	for _, existing := range s.recipients {
		if strings.EqualFold(existing, address) {
			return smtpError(550, "5.1.1", "The recipient was already specified")
		}
	}
	s.recipients = append(s.recipients, address)
	return nil
}

func (s *smtpSubmissionSession) Data(reader io.Reader) error {
	if s.credential.ID == "" || s.envelopeFrom == "" || len(s.recipients) == 0 {
		return smtpError(503, "5.5.1", "A valid authenticated SMTP envelope is required")
	}
	input, err := parseSMTPMessage(reader, s.backend.cfg.TransactionalMaxMessageBytes, s.envelopeFrom, s.recipients)
	if err != nil {
		return smtpError(554, "5.6.0", "The message format or size is invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := s.credential.ReserveRequest(ctx, s.backend.pool); err != nil {
		return smtpError(451, "4.7.0", "The credential rate limit was exceeded or the credential was revoked")
	}
	_, err = s.backend.store.Enqueue(ctx, EnqueueOptions{
		OrganizationID: s.credential.OrganizationID,
		EnforceLimits:  true,
	}, input)
	if err != nil {
		switch {
		case errors.Is(err, ErrWorkspaceDisabled):
			return smtpError(550, "5.7.1", "Transactional sending is disabled for this workspace")
		case errors.Is(err, ErrWorkspacePaused):
			return smtpError(451, "4.7.1", "Transactional sending is paused for this workspace")
		case errors.Is(err, ErrDailyLimit), errors.Is(err, ErrMonthlyLimit):
			return smtpError(452, "4.5.3", "The workspace sending quota has been reached")
		default:
			return smtpError(554, "5.6.0", "The message could not be accepted")
		}
	}
	return nil
}

func (s *smtpSubmissionSession) Reset() {
	s.envelopeFrom = ""
	s.recipients = nil
}

func (s *smtpSubmissionSession) Logout() error { return nil }

func (s *smtpSubmissionSession) validateSender(ctx context.Context, address string) error {
	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return errors.New("sender address has no domain")
	}
	var allowed bool
	err := s.backend.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM transactional_settings ts
			JOIN organizations o ON o.id = ts.organization_id
			JOIN sending_domains sd ON sd.organization_id = ts.organization_id
			WHERE ts.organization_id = $1 AND ts.enabled AND ts.status = 'active'
			  AND o.status = 'active'
			  AND lower(sd.domain) = $2 AND sd.transactional_enabled
			  AND sd.status = 'verified' AND sd.spf_status = 'verified'
			  AND sd.dkim_status = 'verified' AND NOT sd.owner_conflict
			  AND sd.admin_disabled_at IS NULL
		)
	`, s.credential.OrganizationID, strings.ToLower(address[at+1:])).Scan(&allowed)
	if err != nil {
		return fmt.Errorf("check transactional sender domain: %w", err)
	}
	if !allowed {
		return errSMTPSenderNotAllowed
	}
	return nil
}

var errSMTPSenderNotAllowed = errors.New("sender domain is not enabled")

func parseSMTPMessage(reader io.Reader, maxBytes int64, envelopeFrom string, envelopeRecipients []string) (MessageInput, error) {
	var input MessageInput
	raw, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return input, fmt.Errorf("read SMTP message: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return input, errors.New("SMTP message exceeds the configured size limit")
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return input, fmt.Errorf("parse SMTP message: %w", err)
	}
	from, err := mail.ParseAddress(message.Header.Get("From"))
	if err != nil || !strings.EqualFold(from.Address, envelopeFrom) {
		return input, errors.New("message From must match the SMTP envelope sender")
	}
	input.From = envelopeFrom
	input.Subject, err = new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if err != nil {
		return input, fmt.Errorf("decode SMTP subject: %w", err)
	}
	if strings.TrimSpace(input.Subject) == "" {
		return input, errors.New("SMTP subject is required")
	}
	if replyTo := message.Header.Get("Reply-To"); replyTo != "" {
		address, err := mail.ParseAddress(replyTo)
		if err != nil {
			return input, fmt.Errorf("parse SMTP Reply-To: %w", err)
		}
		input.ReplyTo = address.Address
	}
	to, err := parseAddressHeader(message.Header.Get("To"))
	if err != nil {
		return input, err
	}
	cc, err := parseAddressHeader(message.Header.Get("Cc"))
	if err != nil {
		return input, err
	}
	envelopeSet := make(map[string]bool, len(envelopeRecipients))
	for _, recipient := range envelopeRecipients {
		envelopeSet[strings.ToLower(recipient)] = true
	}
	visible := make(map[string]bool, len(to)+len(cc))
	for _, recipient := range to {
		if !envelopeSet[strings.ToLower(recipient)] {
			return input, errors.New("To header includes a non-envelope recipient")
		}
		visible[strings.ToLower(recipient)] = true
		input.To = append(input.To, recipient)
	}
	for _, recipient := range cc {
		if !envelopeSet[strings.ToLower(recipient)] {
			return input, errors.New("Cc header includes a non-envelope recipient")
		}
		visible[strings.ToLower(recipient)] = true
		input.CC = append(input.CC, recipient)
	}
	if len(input.To)+len(input.CC) == 0 {
		input.To = append(input.To, envelopeRecipients[0])
		visible[strings.ToLower(envelopeRecipients[0])] = true
	}
	for _, recipient := range envelopeRecipients {
		if !visible[strings.ToLower(recipient)] {
			input.BCC = append(input.BCC, recipient)
		}
	}
	input.Headers = make(map[string]string)
	for name, values := range message.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-") && len(values) > 0 {
			input.Headers[textproto.CanonicalMIMEHeaderKey(name)] = values[0]
		}
	}
	if err := parseMIMEPart(textproto.MIMEHeader(message.Header), message.Body, &input); err != nil {
		return input, err
	}
	if len(input.Attachments) == 0 {
		input.Attachments = nil
	}
	if len(input.Headers) == 0 {
		input.Headers = nil
	}
	return input, nil
}

func parseAddressHeader(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	addresses, err := mail.ParseAddressList(raw)
	if err != nil {
		return nil, fmt.Errorf("parse SMTP recipient header: %w", err)
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		normalized, err := ValidateEmail(address.Address)
		if err != nil {
			return nil, err
		}
		result = append(result, normalized)
	}
	return result, nil
}

func parseMIMEPart(header textproto.MIMEHeader, body io.Reader, input *MessageInput) error {
	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("parse MIME content type: %w", err)
	}
	disposition, dispositionParams, err := mime.ParseMediaType(header.Get("Content-Disposition"))
	if err != nil && header.Get("Content-Disposition") != "" {
		return fmt.Errorf("parse MIME content disposition: %w", err)
	}
	if mediaType == "multipart/alternative" || mediaType == "multipart/mixed" || mediaType == "multipart/related" {
		boundary := params["boundary"]
		if boundary == "" {
			return errors.New("multipart content has no boundary")
		}
		reader := multipart.NewReader(body, boundary)
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return fmt.Errorf("read MIME part: %w", err)
			}
			if err := parseMIMEPart(part.Header, part, input); err != nil {
				_ = part.Close()
				return err
			}
			_ = part.Close()
		}
		return nil
	}
	decoded, err := decodeMIMEBody(body, header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return err
	}
	if disposition == "attachment" || dispositionParams["filename"] != "" || !strings.HasPrefix(mediaType, "text/") {
		filename := dispositionParams["filename"]
		if filename == "" {
			filename = "attachment"
		}
		input.Attachments = append(input.Attachments, Attachment{
			Filename: filename, ContentType: contentType, Content: base64.StdEncoding.EncodeToString(decoded),
		})
		return nil
	}
	switch mediaType {
	case "text/plain":
		input.Text = string(decoded)
	case "text/html":
		input.HTML = string(decoded)
	}
	return nil
}

func decodeMIMEBody(body io.Reader, encoding string) ([]byte, error) {
	var reader io.Reader = body
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary":
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		reader = quotedprintable.NewReader(body)
	default:
		return nil, fmt.Errorf("unsupported content-transfer-encoding %q", encoding)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("decode MIME body: %w", err)
	}
	return decoded, nil
}

func smtpError(code int, enhancedCode, message string) *smtpserver.SMTPError {
	var enhanced smtpserver.EnhancedCode
	parts := strings.Split(enhancedCode, ".")
	if len(parts) == len(enhanced) {
		for i, part := range parts {
			parsed, err := strconv.Atoi(part)
			if err != nil {
				enhanced = smtpserver.EnhancedCode{}
				break
			}
			enhanced[i] = parsed
		}
	}
	return &smtpserver.SMTPError{Code: code, EnhancedCode: enhanced, Message: message}
}
