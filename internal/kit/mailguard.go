package kit

import (
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// developmentSMTPHosts are SMTP relays that exist only in the local and CI
// stacks. A secure environment that delivers mail to one of them has the relay
// accept the message and throw it away, so the service must refuse to start
// instead of silently dropping production mail.
var developmentSMTPHosts = map[string]struct{}{
	"mailpit":   {},
	"localhost": {},
	"127.0.0.1": {},
	"::1":       {},
	"0.0.0.0":   {},
}

// normalizeSMTPHost lowercases a host or host:port value and strips the port,
// IPv6 brackets, and any trailing dot.
func normalizeSMTPHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	return strings.TrimSuffix(value, ".")
}

// DevelopmentSMTPHost reports the development-only relay named by smtpAddr
// (host:port) or smtpHost (bare host), if either points at one.
func DevelopmentSMTPHost(smtpAddr, smtpHost string) (string, bool) {
	for _, candidate := range []string{smtpAddr, smtpHost} {
		host := normalizeSMTPHost(candidate)
		if host == "" {
			continue
		}
		if _, ok := developmentSMTPHosts[host]; ok {
			return host, true
		}
	}
	return "", false
}

// ValidateProductionMail fails fast when a secure environment (staging or
// production) is configured to deliver mail to a development relay or to the
// log mailer. Campaign delivery always uses SMTP regardless of MAIL_PROVIDER,
// so a local SMTP target is rejected even when the platform transactional
// fallback is configured. Services call this from their configuration loaders
// so a bad deployment exits at startup instead of at send time.
func ValidateProductionMail(env, provider, smtpAddr, smtpHost string) error {
	if env != "staging" && env != "production" {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "log":
		return fmt.Errorf("MAIL_PROVIDER=log is not allowed in APP_ENV=%s: it prints messages instead of delivering them", env)
	case "", "auto", "smtp":
	default:
		return fmt.Errorf("MAIL_PROVIDER must be auto, smtp, or log, got %q", provider)
	}
	if host, ok := DevelopmentSMTPHost(smtpAddr, smtpHost); ok {
		return fmt.Errorf("SMTP target %q is a development-only relay and must not be used in APP_ENV=%s; set SMTP_ADDR and SMTP_HOST to a real relay", host, env)
	}
	return nil
}

func IsPublicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	blockedRanges := []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
	for _, prefix := range blockedRanges {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
