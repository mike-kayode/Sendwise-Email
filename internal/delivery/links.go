package delivery

import (
	"encoding/base64"
	"html"
	"net/url"
	"regexp"
	"strings"
)

var hrefPattern = regexp.MustCompile(`href="(https?://[^"]+)"`)

// RewriteLinks replaces outbound anchors with signed tracking redirects.
// The original target is embedded in the signed token, never taken from an
// unsigned query parameter.
func RewriteLinks(document, trackingBaseURL string, sign func(target string) (string, error)) string {
	base := strings.TrimRight(trackingBaseURL, "/")

	return hrefPattern.ReplaceAllStringFunc(document, func(match string) string {
		parts := hrefPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}

		target := html.UnescapeString(parts[1])
		parsed, err := url.Parse(target)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return match
		}

		token, err := sign(target)
		if err != nil {
			return match
		}

		return `href="` + base + `/t/click/` + token + `"`
	})
}

func TinyPixel() []byte {
	const gif = "R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7"
	raw, err := base64.StdEncoding.DecodeString(gif)
	if err != nil {
		return nil
	}
	return raw
}
