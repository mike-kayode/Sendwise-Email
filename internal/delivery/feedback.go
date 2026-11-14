package delivery

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
)

const maxFeedbackMessageSize = 2 << 20

type VERPClaims struct {
	WorkspaceID string `json:"w"`
	CampaignID  string `json:"c"`
	Recipient   string `json:"r"`
}

type FeedbackType string

const (
	FeedbackHardBounce FeedbackType = "hard_bounce"
	FeedbackSoftBounce FeedbackType = "soft_bounce"
	FeedbackComplaint  FeedbackType = "complaint"
	FeedbackDelivered  FeedbackType = "delivered"
)

type Feedback struct {
	ReturnPath    string
	Recipient     string
	Type          FeedbackType
	EnhancedCode  string
	Action        string
	Diagnostic    string
	FeedbackClass string
}

var verpAddressPattern = regexp.MustCompile(`(?i)\bbounces\+[a-z0-9._-]+@[a-z0-9.-]+\b`)
var verpTokenPattern = regexp.MustCompile(`^v1[a-f0-9]{32}$`)
var bounceDomainPattern = regexp.MustCompile(`^[a-z0-9.-]+$`)

func NewVERPReturnPath(secret, bounceDomain string, claims VERPClaims) (string, error) {
	token, err := NewVERPToken(secret, claims)
	if err != nil {
		return "", err
	}
	return FormatVERPReturnPath(bounceDomain, token)
}

func NewVERPToken(secret string, claims VERPClaims) (string, error) {
	if len(secret) < 16 || claims.WorkspaceID == "" || claims.CampaignID == "" || claims.Recipient == "" {
		return "", errors.New("VERP secret, workspace, campaign, and recipient are required")
	}
	if _, err := mail.ParseAddress(claims.Recipient); err != nil {
		return "", fmt.Errorf("invalid VERP recipient: %w", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode VERP claims: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return "v1" + hex.EncodeToString(mac.Sum(nil)[:16]), nil
}

func VerifyVERPToken(secret, token string, claims VERPClaims) bool {
	expected, err := NewVERPToken(secret, claims)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(expected), []byte(token))
}

func FormatVERPReturnPath(bounceDomain, token string) (string, error) {
	bounceDomain = strings.ToLower(strings.TrimSpace(bounceDomain))
	if bounceDomain == "" || !bounceDomainPattern.MatchString(bounceDomain) || !verpTokenPattern.MatchString(token) {
		return "", errors.New("invalid VERP domain or token")
	}
	return "bounces+" + token + "@" + bounceDomain, nil
}

func ParseVERPTokenAddress(bounceDomain, address string) (string, error) {
	parsed, err := mail.ParseAddress(address)
	if err != nil {
		return "", fmt.Errorf("parse VERP address: %w", err)
	}
	local, domain, ok := strings.Cut(parsed.Address, "@")
	if !ok || !strings.EqualFold(domain, strings.TrimSpace(bounceDomain)) || !strings.HasPrefix(strings.ToLower(local), "bounces+") {
		return "", errors.New("address is not a VERP address for the configured bounce domain")
	}
	token := strings.TrimPrefix(strings.ToLower(local), "bounces+")
	if !verpTokenPattern.MatchString(token) {
		return "", errors.New("malformed VERP token")
	}
	return token, nil
}

func ParseFeedbackMessage(reader io.Reader) (Feedback, error) {
	var feedback Feedback
	raw, err := io.ReadAll(io.LimitReader(reader, maxFeedbackMessageSize+1))
	if err != nil {
		return feedback, fmt.Errorf("read feedback message: %w", err)
	}
	if len(raw) > maxFeedbackMessageSize {
		return feedback, fmt.Errorf("feedback message exceeds %d bytes", maxFeedbackMessageSize)
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return feedback, fmt.Errorf("parse feedback message: %w", err)
	}

	var fields strings.Builder
	for _, key := range []string{"To", "Delivered-To", "X-Original-To", "Return-Path"} {
		for _, value := range message.Header[textproto.CanonicalMIMEHeaderKey(key)] {
			_, _ = fields.WriteString(value)
			_, _ = fields.WriteString("\n")
		}
	}
	if err := appendFeedbackBody(&fields, textproto.MIMEHeader(message.Header), message.Body); err != nil {
		return feedback, fmt.Errorf("read feedback body: %w", err)
	}
	all := strings.NewReplacer(
		"\r\n ", "", "\r\n\t", "",
		"\n ", "", "\n\t", "",
	).Replace(fields.String())
	all, err = new(mime.WordDecoder).DecodeHeader(all)
	if err != nil {
		return feedback, fmt.Errorf("decode MIME-encoded feedback fields: %w", err)
	}

	feedback.ReturnPath = verpAddressPattern.FindString(all)
	if feedback.ReturnPath == "" {
		return feedback, errors.New("feedback message does not contain a VERP return address")
	}

	lower := strings.ToLower(all)
	if strings.Contains(lower, "feedback-type:") {
		feedback.Type = FeedbackComplaint
		feedback.FeedbackClass = fieldValue(all, "Feedback-Type")
	} else {
		feedback.EnhancedCode = enhancedStatusCode.FindString(all)
		feedback.Action = strings.ToLower(fieldValue(all, "Action"))
		if feedback.Action == "delivered" && strings.HasPrefix(feedback.EnhancedCode, "2.") {
			feedback.Type = FeedbackDelivered
		} else if strings.HasPrefix(feedback.EnhancedCode, "5.") {
			feedback.Type = FeedbackHardBounce
		} else if strings.HasPrefix(feedback.EnhancedCode, "4.") || feedback.Action == "delayed" {
			feedback.Type = FeedbackSoftBounce
		} else {
			return feedback, errors.New("feedback report has no supported 2.x.x delivery, 4.x.x delay, 5.x.x failure, or ARF complaint classification")
		}
	}
	feedback.Recipient = firstRecipient(all)
	feedback.Diagnostic = fieldValue(all, "Diagnostic-Code")
	if feedback.Type == FeedbackComplaint && feedback.FeedbackClass == "" {
		return feedback, errors.New("ARF complaint report has no Feedback-Type")
	}
	return feedback, nil
}

func appendFeedbackBody(fields *strings.Builder, header textproto.MIMEHeader, body io.Reader) error {
	if fields.Len() > maxFeedbackMessageSize {
		return fmt.Errorf("decoded feedback body exceeds %d bytes", maxFeedbackMessageSize)
	}
	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("parse feedback MIME content type: %w", err)
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return errors.New("multipart feedback has no boundary")
		}
		reader := multipart.NewReader(body, boundary)
		for {
			part, err := reader.NextPart()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("read feedback MIME part: %w", err)
			}
			err = appendFeedbackBody(fields, part.Header, part)
			_ = part.Close()
			if err != nil {
				return err
			}
		}
	}
	if !strings.HasPrefix(mediaType, "text/") && !strings.HasPrefix(mediaType, "message/") {
		return nil
	}
	var decoded io.Reader = body
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "", "7bit", "8bit", "binary":
	case "base64":
		decoded = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		decoded = quotedprintable.NewReader(body)
	default:
		return fmt.Errorf("unsupported feedback transfer encoding %q", header.Get("Content-Transfer-Encoding"))
	}
	content, err := io.ReadAll(io.LimitReader(decoded, int64(maxFeedbackMessageSize-fields.Len())+1))
	if err != nil {
		return fmt.Errorf("decode feedback MIME body: %w", err)
	}
	separatorSize := 0
	if len(content) > 0 && content[len(content)-1] != '\n' {
		separatorSize = 1
	}
	if fields.Len()+len(content)+separatorSize > maxFeedbackMessageSize {
		return fmt.Errorf("decoded feedback body exceeds %d bytes", maxFeedbackMessageSize)
	}
	_, _ = fields.Write(content)
	if separatorSize > 0 {
		fields.WriteByte('\n')
	}
	return nil
}

func fieldValue(message, wanted string) string {
	for _, line := range strings.Split(message, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), wanted) {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func firstRecipient(message string) string {
	for _, wanted := range []string{"Original-Rcpt-To", "Final-Recipient", "Original-Recipient"} {
		value := fieldValue(message, wanted)
		if value == "" {
			continue
		}
		if _, after, ok := strings.Cut(value, ";"); ok {
			value = strings.TrimSpace(after)
		}
		if parsed, err := mail.ParseAddress(value); err == nil {
			return parsed.Address
		}
		return strings.Trim(value, "<> \t\r")
	}
	return ""
}

func FeedbackMessageKey(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
