package delivery

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
		return nil, infrastructureError(fmt.Errorf("dial smtp: %w", err))
	}

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client, err := smtp.NewClient(conn, t.Host)
	if err != nil {
		_ = conn.Close()
		return nil, infrastructureError(fmt.Errorf("create smtp client: %w", err))
	}
	defer client.Close()

	hasSTARTTLS, _ := client.Extension("STARTTLS")
	if err := ensureSTARTTLS(hasSTARTTLS, t.TLSInsecure); err != nil {
		return nil, infrastructureError(err)
	}
	if hasSTARTTLS {
		if err := client.StartTLS(&tls.Config{
			ServerName:         t.Host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: t.TLSInsecure,
		}); err != nil {
			return nil, infrastructureError(fmt.Errorf("starttls: %w", err))
		}
	}

	if msg.RequestDSN {
		if supported, _ := client.Extension("DSN"); !supported {
			return nil, &PermanentError{Err: errors.New("smtp server does not support requested delivery status notifications")}
		}
	}

	if t.Username != "" {
		auth := smtp.PlainAuth("", t.Username, t.Password, t.Host)
		if err := client.Auth(auth); err != nil {
			if isPermanentSMTPError(err) {
				return nil, &PermanentError{Err: &ProviderError{Temporary: true, Message: fmt.Sprintf("smtp auth: %v", err)}}
			}
			return nil, infrastructureError(fmt.Errorf("smtp auth: %w", err))
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
		return nil, infrastructureError(classifySMTP(fmt.Errorf("smtp mail from: %w", err)))
	}
	envelopeTo, err := parseEnvelopeAddress(msg.To)
	if err != nil {
		return nil, classifySMTP(fmt.Errorf("smtp rcpt to: invalid recipient address: %w", err))
	}
	if err := sendSMTPRecipient(client, envelopeTo, msg.RequestDSN); err != nil {
		return nil, classifySMTP(fmt.Errorf("smtp rcpt to: %w", err))
	}

	writer, err := client.Data()
	if err != nil {
		return nil, infrastructureError(classifySMTP(fmt.Errorf("smtp data: %w", err)))
	}

	if _, err := io.Copy(writer, bytes.NewReader(messageData)); err != nil {
		_ = writer.Close()
		return nil, infrastructureError(fmt.Errorf("write message: %w", err))
	}
	if err := writer.Close(); err != nil {
		return nil, infrastructureError(classifySMTP(fmt.Errorf("finish message: %w", err)))
	}
	_ = client.Quit()

	messageID := formatMessageID(msg.ID)
	return &Result{MessageID: messageID, Accepted: true}, nil
}

func sendSMTPRecipient(client *smtp.Client, recipient string, requestDSN bool) error {
	if !requestDSN {
		return client.Rcpt(recipient)
	}

	id, err := client.Text.Cmd("RCPT TO:<%s> NOTIFY=SUCCESS,FAILURE,DELAY", recipient)
	if err != nil {
		return err
	}
	client.Text.StartResponse(id)
	defer client.Text.EndResponse(id)
	_, _, err = client.Text.ReadResponse(250)
	return err
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
	if len(msg.Attachments) == 0 {
		mw := multipart.NewWriter(writer)
		headers["Content-Type"] = fmt.Sprintf("multipart/alternative; boundary=%q", mw.Boundary())
		if err := writeHeaders(writer, headers); err != nil {
			return err
		}
		if err := writeAlternative(mw, msg); err != nil {
			return err
		}
		return mw.Close()
	}

	mixed := multipart.NewWriter(writer)
	headers["Content-Type"] = fmt.Sprintf("multipart/mixed; boundary=%q", mixed.Boundary())
	if err := writeHeaders(writer, headers); err != nil {
		return err
	}
	var alternativeBody bytes.Buffer
	alternative := multipart.NewWriter(&alternativeBody)
	if err := writeAlternative(alternative, msg); err != nil {
		return err
	}
	if err := alternative.Close(); err != nil {
		return fmt.Errorf("close message alternative: %w", err)
	}
	alternativePart, err := mixed.CreatePart(textproto.MIMEHeader{
		"Content-Type": {fmt.Sprintf("multipart/alternative; boundary=%q", alternative.Boundary())},
	})
	if err != nil {
		return fmt.Errorf("create message body part: %w", err)
	}
	if _, err := alternativePart.Write(alternativeBody.Bytes()); err != nil {
		return fmt.Errorf("write message alternative: %w", err)
	}

	for _, attachment := range msg.Attachments {
		contentType := strings.TrimSpace(attachment.ContentType)
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		if _, _, err := mime.ParseMediaType(contentType); err != nil {
			return fmt.Errorf("invalid attachment content type: %w", err)
		}
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": attachment.Filename})
		part, err := mixed.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {contentType},
			"Content-Disposition":       {disposition},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return fmt.Errorf("create attachment part: %w", err)
		}
		encoded := base64.StdEncoding.EncodeToString(attachment.Content)
		for len(encoded) > 76 {
			if _, err := io.WriteString(part, encoded[:76]+"\r\n"); err != nil {
				return fmt.Errorf("write attachment: %w", err)
			}
			encoded = encoded[76:]
		}
		if _, err := io.WriteString(part, encoded+"\r\n"); err != nil {
			return fmt.Errorf("write attachment: %w", err)
		}
	}
	return mixed.Close()
}

func writeHeaders(writer interface{ Write([]byte) (int, error) }, headers map[string]string) error {

	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := fmt.Fprintf(writer, "%s: %s\r\n", k, headers[k]); err != nil {
			return fmt.Errorf("write message header %q: %w", k, err)
		}
	}
	_, err := io.WriteString(writer, "\r\n")
	return err
}

func writeAlternative(mw *multipart.Writer, msg Message) error {
	if err := writePart(mw, "text/plain; charset=utf-8", msg.Text); err != nil {
		return err
	}
	if err := writePart(mw, "text/html; charset=utf-8", msg.HTML); err != nil {
		return err
	}
	return nil
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
