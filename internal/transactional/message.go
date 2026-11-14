package transactional

import (
	"errors"
	"fmt"
	htmlstd "html"
	"math/rand"
	"net/mail"
	"net/textproto"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/khayode/email-platform/internal/delivery"
)

var (
	ErrWorkspaceDisabled = errors.New("transactional delivery is disabled for this workspace")
	ErrWorkspacePaused   = errors.New("transactional delivery is paused for this workspace")
	ErrDailyLimit        = errors.New("transactional daily recipient limit exceeded")
	ErrMonthlyLimit      = errors.New("transactional monthly recipient limit exceeded")
)

type Status string

const (
	StatusQueued     Status = "queued"
	StatusSending    Status = "sending"
	StatusDelivered  Status = "delivered"
	StatusDeferred   Status = "deferred"
	StatusBounced    Status = "bounced"
	StatusFailed     Status = "failed"
	StatusComplained Status = "complained"
	StatusSuppressed Status = "suppressed"
	StatusRejected   Status = "rejected"
)

type Recipient struct {
	Email string `json:"email"`
	Type  string `json:"type"`
}

type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
}

type MessageInput struct {
	From        string            `json:"from"`
	To          []string          `json:"to"`
	CC          []string          `json:"cc"`
	BCC         []string          `json:"bcc"`
	TemplateID  string            `json:"template_id,omitempty"`
	Variables   map[string]string `json:"variables,omitempty"`
	ReplyTo     string            `json:"reply_to"`
	Subject     string            `json:"subject"`
	HTML        string            `json:"html"`
	Text        string            `json:"text"`
	Headers     map[string]string `json:"headers"`
	Attachments []Attachment      `json:"attachments"`
	Tags        []string          `json:"tags"`
	Metadata    map[string]any    `json:"metadata"`
}

type Template struct {
	Subject string
	HTML    string
	Text    string
}

type MessageReceipt struct {
	ID        string `json:"id"`
	MessageID string `json:"message_id"`
	Status    Status `json:"status"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

var (
	templateVariablePattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}`)
	enhancedCodePattern     = regexp.MustCompile(`\b([245]\.\d{1,3}\.\d{1,3})\b`)
	headerNamePattern       = regexp.MustCompile(`^X-[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)
)

func ValidateEmail(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	address, err := mail.ParseAddress(raw)
	if err != nil || !strings.EqualFold(address.Address, raw) {
		return "", errors.New("recipient must be a valid email address without a display name")
	}
	return strings.ToLower(address.Address), nil
}

func ValidateMessage(input MessageInput, maxRecipients int, maxBytes int64) ([]Recipient, error) {
	from, err := ValidateEmail(input.From)
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	if strings.ContainsAny(input.Subject, "\r\n") || strings.TrimSpace(input.Subject) == "" {
		return nil, errors.New("subject is required and must not contain line breaks")
	}
	if input.ReplyTo != "" {
		if _, err := ValidateEmail(input.ReplyTo); err != nil {
			return nil, fmt.Errorf("reply_to: %w", err)
		}
	}
	if len(input.To)+len(input.CC)+len(input.BCC) == 0 {
		return nil, errors.New("at least one recipient is required")
	}
	if maxRecipients < 1 || len(input.To)+len(input.CC)+len(input.BCC) > maxRecipients {
		return nil, fmt.Errorf("message exceeds the maximum of %d recipients", maxRecipients)
	}
	if input.HTML == "" && input.Text == "" {
		return nil, errors.New("html or text content is required")
	}
	if input.Text == "" {
		input.Text = PlainTextFromHTML(input.HTML)
	}
	if input.Text == "" {
		return nil, errors.New("a plain-text alternative is required")
	}

	recipients := make([]Recipient, 0, len(input.To)+len(input.CC)+len(input.BCC))
	seen := make(map[string]struct{}, cap(recipients))
	for _, group := range []struct {
		name   string
		emails []string
	}{{"to", input.To}, {"cc", input.CC}, {"bcc", input.BCC}} {
		for _, raw := range group.emails {
			email, err := ValidateEmail(raw)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", group.name, err)
			}
			if email == from {
				return nil, errors.New("sender address cannot also be a recipient")
			}
			if _, exists := seen[email]; exists {
				return nil, fmt.Errorf("recipient %q appears more than once", email)
			}
			seen[email] = struct{}{}
			recipients = append(recipients, Recipient{Email: email, Type: group.name})
		}
	}

	if err := ValidateHeaders(input.Headers); err != nil {
		return nil, err
	}
	for _, attachment := range input.Attachments {
		if attachment.Filename == "" || strings.ContainsAny(attachment.Filename, "\r\n/\\") ||
			!utf8.ValidString(attachment.Filename) {
			return nil, errors.New("attachment filename is invalid")
		}
		if attachment.ContentType == "" || strings.ContainsAny(attachment.ContentType, "\r\n") {
			return nil, errors.New("attachment content_type is invalid")
		}
	}
	if maxBytes > 0 && MessageSize(input) > maxBytes {
		return nil, fmt.Errorf("message exceeds the maximum size of %d bytes", maxBytes)
	}
	return recipients, nil
}

func ValidateHeaders(headers map[string]string) error {
	for name, value := range headers {
		if !headerNamePattern.MatchString(name) || strings.HasPrefix(strings.ToLower(name), "x-sendwise-") {
			return fmt.Errorf("custom header %q is not allowed", name)
		}
		if strings.ContainsAny(value, "\r\n") || len(value) > 998 || !utf8.ValidString(value) {
			return fmt.Errorf("custom header %q has an invalid value", name)
		}
	}
	return nil
}

func MessageSize(input MessageInput) int64 {
	size := int64(len(input.From) + len(input.ReplyTo) + len(input.Subject) + len(input.HTML) + len(input.Text))
	for name, value := range input.Headers {
		size += int64(len(name) + len(value))
	}
	for _, list := range [][]string{input.To, input.CC, input.BCC, input.Tags} {
		for _, value := range list {
			size += int64(len(value))
		}
	}
	for _, attachment := range input.Attachments {
		size += int64(len(attachment.Filename) + len(attachment.ContentType) + len(attachment.Content))
	}
	for key, value := range input.Metadata {
		size += int64(len(key) + len(fmt.Sprint(value)))
	}
	return size
}

func RenderTemplate(source Template, variables map[string]string) (Template, error) {
	subject, err := substitute(source.Subject, variables, false)
	if err != nil {
		return Template{}, fmt.Errorf("render subject: %w", err)
	}
	if strings.ContainsAny(subject, "\r\n") {
		return Template{}, errors.New("rendered subject contains a line break")
	}
	html, err := substitute(source.HTML, variables, true)
	if err != nil {
		return Template{}, fmt.Errorf("render html: %w", err)
	}
	text, err := substitute(source.Text, variables, false)
	if err != nil {
		return Template{}, fmt.Errorf("render text: %w", err)
	}
	return Template{Subject: subject, HTML: html, Text: text}, nil
}

func TemplateVariableNames(source Template) ([]string, error) {
	variables := make(map[string]string)
	for _, content := range []string{source.Subject, source.HTML, source.Text} {
		for _, match := range templateVariablePattern.FindAllStringSubmatch(content, -1) {
			variables[match[1]] = ""
		}
	}
	if _, err := RenderTemplate(source, variables); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func substitute(source string, variables map[string]string, escapeHTML bool) (string, error) {
	rendered := templateVariablePattern.ReplaceAllStringFunc(source, func(token string) string {
		match := templateVariablePattern.FindStringSubmatch(token)
		value, ok := variables[match[1]]
		if !ok {
			return token
		}
		if escapeHTML {
			return htmlstd.EscapeString(value)
		}
		return value
	})
	for _, match := range templateVariablePattern.FindAllStringSubmatch(source, -1) {
		if _, ok := variables[match[1]]; !ok {
			return "", fmt.Errorf("variable %q is not provided", match[1])
		}
	}
	if strings.Contains(rendered, "{{") || strings.Contains(rendered, "}}") {
		return "", errors.New("template contains an unsupported variable expression")
	}
	return rendered, nil
}

func CanTransition(from, to Status) bool {
	if from == to {
		return true
	}
	switch from {
	case StatusQueued:
		return to == StatusSending || to == StatusSuppressed || to == StatusRejected || to == StatusFailed
	case StatusSending:
		return to == StatusDelivered || to == StatusDeferred || to == StatusBounced ||
			to == StatusFailed || to == StatusComplained || to == StatusSuppressed || to == StatusRejected
	case StatusDeferred:
		return to == StatusQueued || to == StatusSending || to == StatusFailed ||
			to == StatusBounced || to == StatusComplained || to == StatusSuppressed || to == StatusRejected
	default:
		return false
	}
}

func RetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	const base = time.Second
	const maxDelay = time.Hour
	if attempt >= 12 {
		return maxDelay
	}
	delay := base * time.Duration(1<<uint(attempt-1))
	if delay > maxDelay {
		delay = maxDelay
	}
	jitter := 0.75 + 0.5*randomUnit()
	return time.Duration(float64(delay) * jitter)
}

func randomUnit() float64 {
	return rand.Float64()
}

type Failure struct {
	SMTPCode     int
	EnhancedCode string
	Temporary    bool
}

func ClassifyFailure(err error) Failure {
	failure := Failure{Temporary: true}
	if err == nil {
		return failure
	}
	var response *textproto.Error
	if errors.As(err, &response) {
		failure.SMTPCode = response.Code
		failure.Temporary = response.Code >= 400 && response.Code < 500
	}
	if match := enhancedCodePattern.FindStringSubmatch(err.Error()); len(match) == 2 {
		failure.EnhancedCode = match[1]
		if strings.HasPrefix(match[1], "4.") {
			failure.Temporary = true
		} else if strings.HasPrefix(match[1], "5.") {
			failure.Temporary = false
		}
	}
	if delivery.IsPermanent(err) {
		failure.Temporary = false
	}
	return failure
}
