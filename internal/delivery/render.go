package delivery

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"
)

var placeholderPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)

type Design struct {
	Version int     `json:"version"`
	Blocks  []Block `json:"blocks"`
}

type Block struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	URL       string `json:"url"`
	Src       string `json:"src"`
	Alt       string `json:"alt"`
	Align     string `json:"align"`
	BgColor   string `json:"bg_color"`
	TextColor string `json:"text_color"`
	Size      string `json:"size"`
}

type Renderer struct{}

func NewRenderer() *Renderer {
	return &Renderer{}
}

func (r *Renderer) RenderDesign(raw json.RawMessage, data map[string]string) (string, string, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{"version":1,"blocks":[]}`)
	}

	var design Design
	if err := json.Unmarshal(raw, &design); err != nil {
		return "", "", fmt.Errorf("decode campaign design: %w", err)
	}

	var body strings.Builder
	var text strings.Builder

	for _, block := range design.Blocks {
		htmlBlock, textBlock := renderBlock(block, data)
		body.WriteString(htmlBlock)
		if textBlock != "" {
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(textBlock)
		}
	}

	if strings.TrimSpace(text.String()) == "" {
		text.WriteString("This email contains HTML content. Please use an email client that displays HTML.")
	}

	document := fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title></title>
</head>
<body style="margin:0;padding:0;background-color:#f1f5f9;">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f1f5f9;padding:24px 12px;">
<tr><td align="center">
<table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%%;max-width:600px;background-color:#ffffff;border-radius:12px;">
<tr><td style="padding:32px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:16px;line-height:1.6;color:#0f172a;">
%s
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`, body.String())

	return document, text.String(), nil
}

func renderBlock(block Block, data map[string]string) (string, string) {
	content := personalize(block.Content, data)
	safe := html.EscapeString(content)
	align := safeAlign(block.Align)

	switch block.Type {
	case "heading":
		if strings.TrimSpace(safe) == "" {
			return "", ""
		}
		return fmt.Sprintf(`<h1 style="margin:0 0 16px;font-size:28px;line-height:1.3;font-weight:700;color:#0f172a;text-align:%s;">%s</h1>`, align, safe),
			content

	case "paragraph":
		if strings.TrimSpace(safe) == "" {
			return "", ""
		}
		paragraph := strings.ReplaceAll(safe, "\n", "<br>")
		return fmt.Sprintf(`<p style="margin:0 0 16px;font-size:16px;line-height:1.7;color:#334155;text-align:%s;">%s</p>`, align, paragraph),
			content

	case "button":
		target := personalize(block.URL, data)
		if !isSafeURL(target) {
			return "", ""
		}
		label := safe
		if label == "" {
			label = "Read more"
		}
		return fmt.Sprintf(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0;"><tr><td style="border-radius:8px;background-color:#4f46e5;"><a href="%s" style="display:inline-block;padding:14px 24px;font-size:16px;font-weight:600;color:#ffffff;text-decoration:none;">%s</a></td></tr></table>`,
			html.EscapeString(target), label), fmt.Sprintf("%s: %s", content, target)

	case "image":
		src := personalize(block.Src, data)
		if !isSafeURL(src) {
			return "", ""
		}
		alt := html.EscapeString(personalize(block.Alt, data))
		return fmt.Sprintf(`<img src="%s" alt="%s" width="536" style="display:block;width:100%%;max-width:536px;height:auto;border:0;border-radius:8px;margin:0 0 16px;">`,
			html.EscapeString(src), alt), ""

	case "divider":
		return `<hr style="border:0;border-top:1px solid #e2e8f0;margin:24px 0;">`, "---"

	case "spacer":
		height := parseSpacer(block.Size)
		return fmt.Sprintf(`<div style="height:%dpx;line-height:%dpx;font-size:1px;">&nbsp;</div>`, height, height), ""

	case "footer":
		return fmt.Sprintf(`<p style="margin:24px 0 0;font-size:13px;line-height:1.6;color:#64748b;text-align:%s;">%s</p>`, align, safe),
			content

	default:
		if strings.TrimSpace(safe) == "" {
			return "", ""
		}
		return fmt.Sprintf(`<p style="margin:0 0 16px;font-size:16px;line-height:1.7;color:#334155;">%s</p>`, safe), content
	}
}

func personalize(value string, data map[string]string) string {
	return placeholderPattern.ReplaceAllStringFunc(value, func(match string) string {
		key := strings.TrimSpace(placeholderPattern.FindStringSubmatch(match)[1])
		if replacement, ok := data[key]; ok {
			return replacement
		}
		return ""
	})
}

func isSafeURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func safeAlign(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "center":
		return "center"
	case "right":
		return "right"
	default:
		return "left"
	}
}

func parseSpacer(value string) int {
	var height int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &height); err != nil {
		return 24
	}
	if height < 1 {
		return 24
	}
	if height > 320 {
		return 320
	}
	return height
}
