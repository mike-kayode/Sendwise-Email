package api

import (
	"context"
	"fmt"
	"time"

	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/delivery"
)

// platformFromAddress picks the envelope/header sender for platform
// transactional mail (verification, password reset, form confirmations).
// PLATFORM_FROM_EMAIL is preferred; SMTP_FROM is the legacy fallback.
func platformFromAddress(cfg config.Config) string {
	if cfg.PlatformFromEmail != "" {
		return cfg.PlatformFromEmail
	}
	return cfg.SMTPFrom
}

// sendPlatformSMTP sends one platform transactional message over the platform
// SMTP relay (Stalwart in every environment that matters). It is deliberately
// separate from campaign delivery: campaign code never calls into it, and it
// never calls a campaign provider.
func sendPlatformSMTP(ctx context.Context, cfg config.Config, to, subject, html string) error {
	from := platformFromAddress(cfg)
	if from == "" {
		return fmt.Errorf("platform from address is not configured")
	}
	transport := delivery.NewSMTPTransport(
		cfg.SMTPAddr,
		cfg.SMTPHost,
		cfg.SMTPUsername,
		cfg.SMTPPassword,
		cfg.Env == "development" || cfg.Env == "test",
	)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := transport.Send(ctx, delivery.Message{
		From:    from,
		To:      to,
		Subject: subject,
		HTML:    html,
		Text:    stripHTMLForText(html),
	})
	return err
}

// stripHTMLForText reduces a simple transactional HTML body to a plain-text
// part: it drops tags and collapses the whitespace they leave behind.
func stripHTMLForText(html string) string {
	out := make([]byte, 0, len(html))
	inTag := false
	for i := 0; i < len(html); i++ {
		c := html[i]
		switch {
		case c == '<':
			inTag = true
		case c == '>':
			inTag = false
		case !inTag:
			out = append(out, c)
		}
	}
	return string(out)
}
