package delivery

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

var placeholderPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)
var importedHTMLPolicy = newImportedHTMLPolicy()

func newImportedHTMLPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements(
		"a", "b", "blockquote", "br", "caption", "center", "col", "colgroup",
		"div", "em", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "li",
		"ol", "p", "span", "strong", "table", "tbody", "td", "th", "thead",
		"tr", "u", "ul",
	)
	policy.AllowAttrs(
		"align", "border", "cellpadding", "cellspacing", "colspan", "height",
		"role", "rowspan", "style", "valign", "width",
	).Globally()
	policy.AllowStyles(
		"background-color", "border", "border-collapse", "border-radius", "color",
		"display", "font-family", "font-size", "font-weight", "height", "line-height",
		"margin", "max-width", "padding", "text-align", "text-decoration", "vertical-align",
		"width",
	).Globally()
	policy.AllowAttrs("href", "title").OnElements("a")
	policy.AllowURLSchemes("http", "https", "mailto")
	policy.RequireParseableURLs(true)
	return policy
}

type Design struct {
	Version     int     `json:"version"`
	Mode        string  `json:"mode"`
	TextContent string  `json:"text_content"`
	Theme       Theme   `json:"theme"`
	Blocks      []Block `json:"blocks"`
}

type Theme struct {
	Background        string `json:"background"`
	ContentBackground string `json:"content_background"`
	Text              string `json:"text"`
	Heading           string `json:"heading"`
	Accent            string `json:"accent"`
	Button            string `json:"button"`
	Link              string `json:"link"`
}

type Block struct {
	Type      string    `json:"type"`
	Content   string    `json:"content"`
	URL       string    `json:"url"`
	Src       string    `json:"src"`
	Alt       string    `json:"alt"`
	Align     string    `json:"align"`
	BgColor   string    `json:"bg_color"`
	TextColor string    `json:"text_color"`
	Size      string    `json:"size"`
	Columns   [][]Block `json:"columns"`
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
	theme := defaultTheme(design.Theme)

	var body strings.Builder
	var text strings.Builder

	for _, block := range design.Blocks {
		htmlBlock, textBlock := renderBlock(block, data, theme)
		body.WriteString(htmlBlock)
		if textBlock != "" {
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(textBlock)
		}
	}

	if design.Mode == "plain" {
		plainText := design.TextContent
		if strings.TrimSpace(plainText) == "" {
			plainText = text.String()
		}
		if strings.TrimSpace(plainText) == "" {
			return "", "", fmt.Errorf("plain-text template content is required")
		}
		plainText = personalize(plainText, data)
		textDocument := fmt.Sprintf(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:%s;padding:24px 12px;"><tr><td align="center"><table role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%%;max-width:600px;background-color:%s;"><tr><td style="padding:32px;font-family:Arial,sans-serif;font-size:16px;line-height:1.6;color:%s;white-space:pre-wrap;">%s</td></tr></table></td></tr></table>`,
			theme.Background, theme.ContentBackground, theme.Text, html.EscapeString(plainText))
		return documentWithTheme(textDocument, theme), plainText, nil
	}

	if strings.TrimSpace(design.TextContent) != "" {
		text.Reset()
		text.WriteString(personalize(design.TextContent, data))
	}
	if strings.TrimSpace(text.String()) == "" {
		text.WriteString(textFromHTML(body.String()))
	}
	if strings.TrimSpace(text.String()) == "" {
		text.WriteString("This email contains HTML content. Please use an email client that displays HTML.")
	}
	return documentWithTheme(body.String(), theme), text.String(), nil
}

func documentWithTheme(body string, theme Theme) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title></title>
<style>@media screen and (max-width:620px){.email-shell{width:100%%!important}.email-content{padding:20px!important}}</style>
</head>
<body style="margin:0;padding:0;background-color:%s;">
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:%s;padding:24px 12px;">
<tr><td align="center">
<table class="email-shell" role="presentation" width="600" cellpadding="0" cellspacing="0" border="0" style="width:100%%;max-width:600px;background-color:%s;">
<tr><td class="email-content" style="padding:32px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:16px;line-height:1.6;color:%s;">
%s
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`, theme.Background, theme.Background, theme.ContentBackground, theme.Text, body)
}

func renderBlock(block Block, data map[string]string, theme Theme) (string, string) {
	content := personalize(block.Content, data)
	safe := html.EscapeString(content)
	align := safeAlign(block.Align)
	textColor := safeColor(block.TextColor, theme.Text)
	backgroundColor := safeColor(block.BgColor, "transparent")

	switch block.Type {
	case "header", "logo":
		if src, ok := safeImageURL(personalize(block.Src, data), data); block.Src != "" && ok {
			image := fmt.Sprintf(`<img src="%s" alt="%s" width="180" style="display:block;width:100%%;max-width:180px;height:auto;border:0;margin:0 auto 12px;">`,
				html.EscapeString(src), html.EscapeString(personalize(block.Alt, data)))
			return `<div style="text-align:center;">` + image + `</div>`, content
		}
		if safe == "" {
			return "", ""
		}
		return fmt.Sprintf(`<p style="margin:0 0 20px;text-align:%s;font-weight:700;color:%s;">%s</p>`, align, textColor, safe), content

	case "heading":
		if strings.TrimSpace(safe) == "" {
			return "", ""
		}
		return fmt.Sprintf(`<h1 style="margin:0 0 16px;font-size:28px;line-height:1.3;font-weight:700;color:%s;text-align:%s;background-color:%s;">%s</h1>`, safeColor(block.TextColor, theme.Heading), align, backgroundColor, safe),
			content

	case "paragraph", "text":
		if strings.TrimSpace(safe) == "" {
			return "", ""
		}
		paragraph := strings.ReplaceAll(safe, "\n", "<br>")
		return fmt.Sprintf(`<p style="margin:0 0 16px;font-size:16px;line-height:1.7;color:%s;text-align:%s;background-color:%s;">%s</p>`, textColor, align, backgroundColor, paragraph),
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
		buttonColor := safeColor(block.BgColor, theme.Button)
		return fmt.Sprintf(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:24px 0;"><tr><td align="%s">
<!--[if mso]><v:roundrect xmlns:v="urn:schemas-microsoft-com:vml" xmlns:w="urn:schemas-microsoft-com:office:word" href="%s" style="height:48px;v-text-anchor:middle;width:220px;" arcsize="12%%" strokecolor="%s" fillcolor="%s"><w:anchorlock/><center style="color:#ffffff;font-family:Arial,sans-serif;font-size:16px;font-weight:bold;">%s</center></v:roundrect><![endif]-->
<!--[if !mso]><!--><a href="%s" style="display:inline-block;padding:14px 24px;border-radius:6px;background-color:%s;color:#ffffff;font-size:16px;font-weight:600;text-decoration:none;">%s</a><!--<![endif]-->
</td></tr></table>`,
			align, html.EscapeString(target), buttonColor, buttonColor, label, html.EscapeString(target), buttonColor, label), fmt.Sprintf("%s: %s", content, target)

	case "image":
		src, ok := safeImageURL(personalize(block.Src, data), data)
		if !ok {
			return "", ""
		}
		alt := html.EscapeString(personalize(block.Alt, data))
		return fmt.Sprintf(`<img src="%s" alt="%s" width="536" style="display:block;width:100%%;max-width:536px;height:auto;border:0;border-radius:8px;margin:0 0 16px;">`,
			html.EscapeString(src), alt), ""

	case "divider":
		return `<hr style="border:0;border-top:1px solid #e2e8f0;margin:24px 0;">`, "---"

	case "spacer":
		height := parseSpacer(block.Size)
		return fmt.Sprintf(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0"><tr><td height="%d" style="height:%dpx;line-height:%dpx;font-size:1px;">&nbsp;</td></tr></table>`, height, height, height), ""

	case "columns-2", "columns-3":
		if len(block.Columns) == 0 {
			return "", ""
		}
		var columnsHTML, columnsText strings.Builder
		columnsHTML.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0"><tr>`)
		width := 100 / len(block.Columns)
		for index, column := range block.Columns {
			if index > 0 {
				columnsText.WriteString("\n\n")
			}
			columnsHTML.WriteString(fmt.Sprintf(`<td valign="top" width="%d%%" style="padding:8px;">`, width))
			for _, child := range column {
				childHTML, childText := renderBlock(child, data, theme)
				columnsHTML.WriteString(childHTML)
				if childText != "" {
					if columnsText.Len() > 0 && index == 0 {
						columnsText.WriteString("\n")
					}
					columnsText.WriteString(childText)
				}
			}
			columnsHTML.WriteString(`</td>`)
		}
		columnsHTML.WriteString(`</tr></table>`)
		return columnsHTML.String(), columnsText.String()

	case "quote", "testimonial":
		if strings.TrimSpace(safe) == "" {
			return "", ""
		}
		return fmt.Sprintf(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;background-color:%s;"><tr><td style="padding:18px;border-left:4px solid %s;color:%s;font-style:italic;">%s</td></tr></table>`,
			backgroundColor, theme.Accent, textColor, safe), content

	case "social":
		target := personalize(block.URL, data)
		if !isSafeURL(target) || safe == "" {
			return "", ""
		}
		return fmt.Sprintf(`<a href="%s" style="margin-right:12px;color:%s;text-decoration:underline;">%s</a>`,
			html.EscapeString(target), theme.Link, safe), fmt.Sprintf("%s: %s", content, target)

	case "footer":
		return fmt.Sprintf(`<p style="margin:24px 0 0;font-size:13px;line-height:1.6;color:#64748b;text-align:%s;">%s</p>`, align, safe),
			content

	case "custom_html":
		sanitized := importedHTMLPolicy.Sanitize(personalize(block.Content, data))
		return sanitized, textFromHTML(sanitized)

	default:
		if strings.TrimSpace(safe) == "" {
			return "", ""
		}
		return fmt.Sprintf(`<p style="margin:0 0 16px;font-size:16px;line-height:1.7;color:#334155;">%s</p>`, safe), content
	}
}

func defaultTheme(theme Theme) Theme {
	theme.Background = safeColor(theme.Background, "#f1f5f9")
	theme.ContentBackground = safeColor(theme.ContentBackground, "#ffffff")
	theme.Text = safeColor(theme.Text, "#334155")
	theme.Heading = safeColor(theme.Heading, "#0f172a")
	theme.Accent = safeColor(theme.Accent, "#4f46e5")
	theme.Button = safeColor(theme.Button, theme.Accent)
	theme.Link = safeColor(theme.Link, "#4f46e5")
	return theme
}

func safeColor(value, fallback string) string {
	value = strings.TrimSpace(value)
	if len(value) == 7 && value[0] == '#' {
		for _, character := range value[1:] {
			if (character < '0' || character > '9') &&
				(character < 'a' || character > 'f') &&
				(character < 'A' || character > 'F') {
				return fallback
			}
		}
		return value
	}
	if value == "transparent" {
		return value
	}
	return fallback
}

func textFromHTML(value string) string {
	value = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|h[1-6]|tr|li)>`).ReplaceAllString(value, "\n")
	value = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(value, "")
	value = html.UnescapeString(value)
	lines := strings.Split(value, "\n")
	trimmed := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			trimmed = append(trimmed, line)
		}
	}
	return strings.Join(trimmed, "\n")
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

func safeImageURL(value string, data map[string]string) (string, bool) {
	value = strings.TrimSpace(value)
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(data["app_base_url"]), "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "", false
	}
	if (strings.HasPrefix(value, "/template-assets/") ||
		strings.HasPrefix(value, "/api/v1/public/assets/")) && !strings.Contains(value, "..") {
		return base.Scheme + "://" + base.Host + value, true
	}
	return "", false
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
