package delivery

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"sort"
	"strings"
	"time"
)

type SMTPTransport struct {
	Addr        string
	Host        string
	Username    string
	Password    string
	TLSInsecure bool
	DialTimeout time.Duration
}

func NewSMTPTransport(addr, host, username, password string, tlsInsecure bool) *SMTPTransport {
	return &SMTPTransport{
		Addr:        addr,
		Host:        host,
		Username:    username,
		Password:    password,
		TLSInsecure: tlsInsecure,
		DialTimeout: 15 * time.Second,
	}
}

func (t *SMTPTransport) Send(ctx context.Context, msg Message) (*Result, error) {
	messageData, err := marshalMessage(msg)
	if err != nil {
		return nil, fmt.Errorf("prepare smtp message: %w", err)
	}

	dialer := &net.Dialer{Timeout: t.DialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", t.Addr)
	if err != nil {
		return nil, fmt.Errorf("dial smtp: %w", err)
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, t.Host)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("create smtp client: %w", err)
	}
	defer client.Close()

	hasSTARTTLS, _ := client.Extension("STARTTLS")
	if err := ensureSTARTTLS(hasSTARTTLS, t.TLSInsecure); err != nil {
		return nil, err
	}
	if hasSTARTTLS {
		if err := client.StartTLS(&tls.Config{
			ServerName:         t.Host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: t.TLSInsecure,
		}); err != nil {
			return nil, fmt.Errorf("starttls: %w", err)
		}
	}

	if t.Username != "" {
		auth := smtp.PlainAuth("", t.Username, t.Password, t.Host)
		if err := client.Auth(auth); err != nil {
			if isPermanentSMTPError(err) {
				return nil, &PermanentError{Err: &ProviderError{Temporary: true, Message: fmt.Sprintf("smtp auth: %v", err)}}
			}
			return nil, fmt.Errorf("smtp auth: %w", err)
		}
	}

	envelopeSender := msg.EnvelopeFrom
	if envelopeSender == "" {
		envelopeSender = msg.From
	}
	envelopeFrom, err := parseEnvelopeAddress(envelopeSender)
	if err != nil {
		return nil, classifySMTP(fmt.Errorf("smtp mail from: invalid sender address: %w", err))
	}
	if err := client.Mail(envelopeFrom); err != nil {
		return nil, classifySMTP(fmt.Errorf("smtp mail from: %w", err))
	}
	envelopeTo, err := parseEnvelopeAddress(msg.To)
	if err != nil {
		return nil, classifySMTP(fmt.Errorf("smtp rcpt to: invalid recipient address: %w", err))
	}
	if err := client.Rcpt(envelopeTo); err != nil {
		return nil, classifySMTP(fmt.Errorf("smtp rcpt to: %w", err))
	}

	writer, err := client.Data()
	if err != nil {
		return nil, classifySMTP(fmt.Errorf("smtp data: %w", err))
	}

	if _, err := io.Copy(writer, bytes.NewReader(messageData)); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("write message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, classifySMTP(fmt.Errorf("finish message: %w", err))
	}
	if err := client.Quit(); err != nil {
		return nil, classifySMTP(fmt.Errorf("smtp quit: %w", err))
	}

	messageID := formatMessageID(msg.ID)
	return &Result{MessageID: messageID, Accepted: true}, nil
}

func marshalMessage(msg Message) ([]byte, error) {
	var raw bytes.Buffer
	if err := writeMessage(&raw, msg); err != nil {
		return nil, err
	}
	if msg.DKIM == nil {
		return raw.Bytes(), nil
	}
	return SignMessage(raw.Bytes(), msg.DKIM)
}

func ensureSTARTTLS(advertised, allowInsecure bool) error {
	if !advertised && !allowInsecure {
		return errors.New("smtp server does not support STARTTLS")
	}
	return nil
}

func writeMessage(writer interface{ Write([]byte) (int, error) }, msg Message) error {
	headers := make(map[string]string, len(msg.Headers)+8)
	for k, v := range msg.Headers {
		if isReservedHeader(k) {
			continue
		}
		headers[k] = sanitizeHeader(v)
	}
	headers["From"] = sanitizeHeader(msg.From)
	headers["To"] = sanitizeHeader(msg.To)
	headers["Subject"] = mime.QEncoding.Encode("utf-8", sanitizeHeader(msg.Subject))
	headers["Date"] = time.Now().Format(time.RFC1123Z)
	headers["Message-ID"] = formatMessageID(msg.ID)
	headers["MIME-Version"] = "1.0"
	headers["Auto-Submitted"] = "auto-generated"

	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := fmt.Fprintf(writer, "%s: %s\r\n", k, headers[k]); err != nil {
			return err
		}
	}

	mw := multipart.NewWriter(writer)
	if _, err := fmt.Fprintf(writer, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", mw.Boundary()); err != nil {
		return err
	}

	if err := writePart(mw, "text/plain; charset=utf-8", msg.Text); err != nil {
		return err
	}
	if err := writePart(mw, "text/html; charset=utf-8", msg.HTML); err != nil {
		return err
	}

	return mw.Close()
}

func writePart(mw *multipart.Writer, contentType, body string) error {
	part, err := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {contentType},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	qp := quotedprintable.NewWriter(part)
	if _, err := qp.Write([]byte(body)); err != nil {
		return err
	}
	return qp.Close()
}

func parseEnvelopeAddress(value string) (string, error) {
	address, err := mail.ParseAddress(sanitizeHeader(value))
	if err != nil {
		return "", err
	}
	return address.Address, nil
}

func sanitizeHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", "")
	return strings.TrimSpace(value)
}

func isReservedHeader(key string) bool {
	switch strings.ToLower(key) {
	case "from", "to", "subject", "date", "message-id", "mime-version", "content-type", "content-transfer-encoding", "dkim-signature":
		return true
	default:
		return false
	}
}

func formatMessageID(id string) string {
	id = sanitizeHeader(id)
	if strings.HasPrefix(id, "<") && strings.HasSuffix(id, ">") {
		return id
	}
	if !strings.Contains(id, "@") {
		id += "@email-platform.local"
	}
	return "<" + id + ">"
}

func classifySMTP(err error) error {
	if isPermanentSMTPError(err) {
		return &PermanentError{Err: err}
	}
	return err
}

func isPermanentSMTPError(err error) bool {
	var smtpErr *textproto.Error
	if errors.As(err, &smtpErr) {
		return smtpErr.Code >= 500 && smtpErr.Code < 600
	}
	return false
}
