package delivery

import (
	"regexp"
	"strings"
)

// BounceType classifies a delivery failure.
type BounceType string

const (
	BounceHard   BounceType = "hard"
	BounceSoft   BounceType = "soft"
	BounceBlocked BounceType = "blocked"
)

// enhancedStatusCode matches RFC 3463 / RFC 1894 enhanced status codes
// of the form X.Y.Z where X is the class (2 success, 4 transient, 5 permanent).
var enhancedStatusCode = regexp.MustCompile(`\b([245])\.(\d{1,3})\.(\d{1,3})\b`)

// ClassifyBounce inspects an SMTP error and optional SMTP reply code to
// determine the bounce type and whether the failure is permanent (should not
// be retried).
func ClassifyBounce(err error, smtpCode string) (BounceType, bool) {
	if err == nil {
		return BounceSoft, false
	}

	// Permanent SMTP reply codes (5xx) are hard bounces.
	if smtpCode != "" && len(smtpCode) >= 1 {
		switch smtpCode[0] {
		case '5':
			return BounceHard, true
		case '4':
			return BounceSoft, false
		}
	}

	msg := strings.ToLower(err.Error())

	// Hard bounce indicators (permanent mailbox/user failures).
	hardPatterns := []string{
		"user unknown", "mailbox unavailable", "no such user",
		"does not exist", "invalid mailbox", "account does not exist",
		"550", "551", "552", "553", "554",
	}
	for _, p := range hardPatterns {
		if strings.Contains(msg, p) {
			return BounceHard, true
		}
	}

	// Blocked indicators (reputation/blocklist/policy — permanent for this send).
	blockedPatterns := []string{
		"blocked", "blacklist", "denied", "spam", "reputation",
		"policy", "rate limit", "throttl",
	}
	for _, p := range blockedPatterns {
		if strings.Contains(msg, p) {
			return BounceBlocked, true
		}
	}

	// Enhanced status codes take precedence for precision.
	if matches := enhancedStatusCode.FindStringSubmatch(msg); len(matches) == 4 {
		switch matches[1] {
		case "5":
			return BounceHard, true
		case "4":
			return BounceSoft, false
		}
	}

	// Default: soft bounce (temporary, retryable).
	return BounceSoft, false
}

// ExtractSMTPCode pulls a leading 3-digit SMTP reply code from an error message.
func ExtractSMTPCode(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) >= 3 {
		// SMTP codes typically appear at the start of a line: "550 User unknown"
		if msg[0] >= '0' && msg[0] <= '9' && msg[1] >= '0' && msg[1] <= '9' && msg[2] >= '0' && msg[2] <= '9' {
			return msg[:3]
		}
	}
	return ""
}
