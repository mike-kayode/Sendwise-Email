package delivery

import (
	"encoding/json"
	"fmt"
	"html"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/aymerick/douceur/css"
	"github.com/aymerick/douceur/parser"
	"github.com/microcosm-cc/bluemonday"
	xhtml "golang.org/x/net/html"
)

var placeholderPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)
var safeCSSSelector = regexp.MustCompile(`^[a-zA-Z0-9_.*#\[\]="':(), >+~|^$-]+$`)
var safeMediaQuery = regexp.MustCompile(`(?i)^(?:(?:only\s+)?(?:all|screen)(?:\s+and\s+)?|\s*)\((?:min|max)-(?:device-)?(?:width|height):\s*[0-9.]+(px|em|rem)\)(?:\s+and\s+\((?:min|max)-(?:device-)?(?:width|height):\s*[0-9.]+(px|em|rem)\))*$`)
var safeMSOProperty = regexp.MustCompile(`^mso-[a-z-]+$`)
var safeMSOValue = regexp.MustCompile(`^[a-zA-Z0-9 .#%(),+-]+$`)
var unsafeCSSValue = regexp.MustCompile(`(?i)url\s*\(|expression\s*\(|javascript:|data:|@import|behavior|binding|[<>\\]`)
var fixedEmailWidth = regexp.MustCompile(`(?i)(?:max-)?width\s*:\s*(?:5[6-9]\d|[6-9]\d\d|[1-9]\d{3,})px`)
var importedHTMLPolicy = newImportedHTMLPolicy()

func newImportedHTMLPolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements(
		"a", "b", "blockquote", "br", "caption", "center", "col", "colgroup",
		"div", "em", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "img", "li",
		"ol", "p", "span", "strong", "table", "tbody", "td", "th", "thead",
		"tr", "u", "ul",
	)
	policy.AllowAttrs(
		"align", "border", "cellpadding", "cellspacing", "colspan", "height",
		"role", "rowspan", "style", "valign", "width",
	).Globally()
	policy.AllowAttrs("bgcolor").Matching(regexp.MustCompile(`(?i)^(#[0-9a-f]{3,8}|[a-z]{1,20})$`)).Globally()
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`^[a-zA-Z0-9 _-]{1,256}$`)).Globally()
	policy.AllowAttrs("target").Matching(regexp.MustCompile(`^_(blank|self|top|parent)$`)).OnElements("a")
	policy.AllowAttrs("rel").OnElements("a")
	policy.AllowStyles(
		"background", "background-color", "background-position", "background-repeat",
		"background-size", "border", "border-bottom", "border-collapse", "border-left",
		"border-radius", "border-right", "border-top", "color", "direction", "display",
		"font-family", "font-size", "font-style", "font-weight", "height", "letter-spacing",
		"line-height", "margin", "max-width", "min-height", "padding", "text-align",
		"text-decoration", "text-transform", "vertical-align", "white-space", "width",
		"word-break", "mso-border-alt", "mso-hide", "mso-line-height-rule",
		"mso-padding-alt", "mso-table-lspace", "mso-table-rspace",
	).Globally()
	policy.AllowAttrs("src", "alt", "title", "width", "height").OnElements("img")
	policy.AllowAttrs("href", "title").OnElements("a")
	policy.AllowURLSchemes("http", "https", "mailto")
	policy.RequireParseableURLs(true)
	return policy
}

type Design struct {
	Version          int                     `json:"version"`
	Mode             string                  `json:"mode"`
	HTMLMode         string                  `json:"html_mode"`
	TextContent      string                  `json:"text_content"`
	HTML             string                  `json:"html"`
	Theme            Theme                   `json:"theme"`
	ComplianceFooter ComplianceFooterSetting `json:"compliance_footer"`
	Blocks           []Block                 `json:"blocks"`
}

type ComplianceFooterSetting struct {
	Alignment       string `json:"alignment"`
	BackgroundColor string `json:"background_color"`
	TextColor       string `json:"text_color"`
	ButtonColor     string `json:"button_color"`
	ButtonText      string `json:"button_text"`
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

	if design.Mode != "plain" && strings.TrimSpace(design.HTML) != "" {
		body, styles := sanitizeImportedDocument(personalize(design.HTML, data), data)
		text := strings.TrimSpace(design.TextContent)
		if text == "" {
			text = textFromHTML(body)
		} else {
			text = personalize(text, data)
		}
		if text == "" {
			text = "This email contains HTML content. Please use an email client that displays HTML."
		}
		noShell := design.HTMLMode == "original" || hasFixedEmailLayout(design.HTML)
		document := documentWithContent(body, styles, theme, noShell)
		document, err := appendDesignComplianceFooter(document, design.ComplianceFooter, data)
		if err != nil {
			return "", "", err
		}
		return document, text, nil
	}

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
		document, err := appendDesignComplianceFooter(documentWithContent(textDocument, "", theme, false), design.ComplianceFooter, data)
		if err != nil {
			return "", "", err
		}
		return document, plainText, nil
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
	document, err := appendDesignComplianceFooter(documentWithContent(body.String(), "", theme, false), design.ComplianceFooter, data)
	if err != nil {
		return "", "", err
	}
	return document, text.String(), nil
}

func appendDesignComplianceFooter(document string, setting ComplianceFooterSetting, data map[string]string) (string, error) {
	unsubscribeURL := strings.TrimSpace(data["unsubscribe_url"])
	if unsubscribeURL == "" {
		return document, nil
	}
	var socialLinks []struct {
		Label string `json:"label"`
		URL   string `json:"url"`
	}
	if raw := strings.TrimSpace(data["sender_social_links"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &socialLinks); err != nil {
			return "", fmt.Errorf("decode sender profile social links: %w", err)
		}
	}

	alignment := safeAlign(setting.Alignment)
	background := safeColor(setting.BackgroundColor, "#f8fafc")
	textColor := safeColor(setting.TextColor, "#64748b")
	buttonColor := safeColor(setting.ButtonColor, "#4f46e5")
	buttonText := strings.TrimSpace(setting.ButtonText)
	if buttonText == "" {
		buttonText = "Unsubscribe"
	}
	var footer strings.Builder
	footer.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" data-sendwise-compliance-footer="true"><tr><td align="`)
	footer.WriteString(alignment)
	footer.WriteString(`" style="padding:24px 12px;background-color:`)
	footer.WriteString(background)
	footer.WriteString(`;font-family:Arial,sans-serif;font-size:12px;line-height:1.6;color:`)
	footer.WriteString(textColor)
	footer.WriteString(`;text-align:`)
	footer.WriteString(alignment)
	footer.WriteString(`;"><p style="margin:0 0 8px;">You are receiving this email because you subscribed to updates from `)
	footer.WriteString(html.EscapeString(strings.TrimSpace(data["sender_company"])))
	footer.WriteString(`.</p>`)
	if address := strings.TrimSpace(data["sender_postal_address"]); address != "" {
		footer.WriteString(`<p style="margin:0 0 8px;">`)
		footer.WriteString(html.EscapeString(address))
		footer.WriteString(`</p>`)
	}
	if text := strings.TrimSpace(data["sender_footer_text"]); text != "" {
		footer.WriteString(`<p style="margin:0 0 8px;">`)
		footer.WriteString(html.EscapeString(text))
		footer.WriteString(`</p>`)
	}
	if website := strings.TrimSpace(data["sender_website"]); website != "" {
		if !isSafeURL(website) {
			return "", fmt.Errorf("sender profile website is not a valid HTTP or HTTPS URL")
		}
		footer.WriteString(`<p style="margin:0 0 8px;"><a href="`)
		footer.WriteString(html.EscapeString(website))
		footer.WriteString(`" style="color:` + textColor + `;">`)
		footer.WriteString(html.EscapeString(website))
		footer.WriteString(`</a></p>`)
	}
	if support := strings.TrimSpace(data["sender_support_email"]); support != "" {
		address, err := mail.ParseAddress(support)
		if err != nil || address.Address == "" {
			return "", fmt.Errorf("sender profile support email is invalid")
		}
		footer.WriteString(`<p style="margin:0 0 8px;">Support: <a href="mailto:`)
		footer.WriteString(url.QueryEscape(address.Address))
		footer.WriteString(`" style="color:` + textColor + `;">`)
		footer.WriteString(html.EscapeString(support))
		footer.WriteString(`</a></p>`)
	}
	for _, link := range socialLinks {
		if !isSafeURL(link.URL) {
			return "", fmt.Errorf("sender profile contains an invalid social link URL")
		}
		label := strings.TrimSpace(link.Label)
		if label == "" {
			parsed, _ := url.Parse(link.URL)
			label = parsed.Host
		}
		footer.WriteString(`<a href="` + html.EscapeString(link.URL) + `" style="margin-right:8px;color:` + textColor + `;">`)
		footer.WriteString(html.EscapeString(label))
		footer.WriteString(`</a>`)
	}
	footer.WriteString(`<p style="margin:12px 0 0;"><a href="`)
	footer.WriteString(html.EscapeString(unsubscribeURL))
	footer.WriteString(`" style="display:inline-block;padding:10px 18px;border-radius:4px;background-color:`)
	footer.WriteString(buttonColor)
	footer.WriteString(`;color:#ffffff;text-decoration:none;font-weight:600;">`)
	footer.WriteString(html.EscapeString(buttonText))
	footer.WriteString(`</a></p></td></tr></table>`)
	return appendFooterAtBodyEnd(document, footer.String()), nil
}

func appendFooterAtBodyEnd(document, footer string) string {
	const marker = `data-sendwise-compliance-footer="true"`
	if strings.Contains(document, marker) {
		return document
	}
	if index := strings.LastIndex(strings.ToLower(document), "</body>"); index >= 0 {
		return document[:index] + footer + document[index:]
	}
	return document + footer
}

func sanitizeEmailHTML(source string, data map[string]string) string {
	body, styles := sanitizeImportedDocument(source, data)
	if styles == "" {
		return body
	}
	return styles + body
}

func sanitizeImportedDocument(source string, data map[string]string) (string, string) {
	source, conditionalComments := extractSafeMSOConditionalComments(source)
	document, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return "", ""
	}
	var bodyNode *xhtml.Node
	var headNode *xhtml.Node
	var findBody func(*xhtml.Node)
	findBody = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && node.Data == "body" {
			bodyNode = node
		}
		if node.Type == xhtml.ElementNode && node.Data == "head" {
			headNode = node
		}
		for child := node.FirstChild; child != nil && bodyNode == nil; child = child.NextSibling {
			findBody(child)
		}
	}
	findBody(document)
	if bodyNode == nil {
		return "", ""
	}
	var styles strings.Builder
	var collectStylesFn func(*xhtml.Node)
	collectStylesFn = func(parent *xhtml.Node) {
		for child := parent.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == xhtml.ElementNode && child.Data == "style" {
				cssText := textFromNode(child)
				if safeCSS := sanitizeStylesheet(cssText); safeCSS != "" {
					styles.WriteString("<style>")
					styles.WriteString(safeCSS)
					styles.WriteString("</style>")
				}
				parent.RemoveChild(child)
			} else {
				collectStylesFn(child)
			}
			child = next
		}
	}
	if headNode != nil {
		collectStylesFn(headNode)
	}
	collectStylesFn(bodyNode)

	var sanitizeImages func(*xhtml.Node)
	sanitizeImages = func(parent *xhtml.Node) {
		for child := parent.FirstChild; child != nil; {
			next := child.NextSibling
			if child.Type == xhtml.ElementNode && child.Data == "img" {
				src := ""
				for _, attribute := range child.Attr {
					if attribute.Key == "src" {
						src = attribute.Val
						break
					}
				}
				if safeURL, ok := safeImageURL(personalize(src, data), data); ok {
					for index := range child.Attr {
						if child.Attr[index].Key == "src" {
							child.Attr[index].Val = safeURL
						}
					}
				} else {
					alt := ""
					for _, attribute := range child.Attr {
						if attribute.Key == "alt" {
							alt = strings.TrimSpace(attribute.Val)
						}
					}
					if alt == "" {
						alt = "Image unavailable because its source is not a supported HTTP or HTTPS URL."
					}
					placeholder := &xhtml.Node{
						Type: xhtml.ElementNode, Data: "span",
						Attr: []xhtml.Attribute{{Key: "class", Val: "sendwise-image-warning"}},
					}
					placeholder.AppendChild(&xhtml.Node{Type: xhtml.TextNode, Data: alt})
					parent.InsertBefore(placeholder, child)
					parent.RemoveChild(child)
				}
			} else {
				sanitizeImages(child)
			}
			child = next
		}
	}
	sanitizeImages(bodyNode)

	msoStyles := preserveMSOInlineStyles(bodyNode)
	var markup strings.Builder
	for child := bodyNode.FirstChild; child != nil; child = child.NextSibling {
		if err := xhtml.Render(&markup, child); err != nil {
			return "", ""
		}
	}
	safeHTML := importedHTMLPolicy.Sanitize(markup.String())
	safeHTML = addNoopenerToBlankTargets(safeHTML)
	safeHTML = restoreMSOInlineStyles(safeHTML, msoStyles)
	for marker, comment := range conditionalComments {
		safeHTML = strings.ReplaceAll(safeHTML, marker, comment)
	}
	return safeHTML, styles.String()
}

func preserveMSOInlineStyles(root *xhtml.Node) map[string]string {
	styles := make(map[string]string)
	index := 0
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode {
			for attributeIndex := range node.Attr {
				if node.Attr[attributeIndex].Key != "style" {
					continue
				}
				var declarations []string
				for _, declaration := range strings.Split(node.Attr[attributeIndex].Val, ";") {
					property, value, ok := strings.Cut(declaration, ":")
					property = strings.ToLower(strings.TrimSpace(property))
					value = strings.TrimSpace(value)
					if ok && safeMSOProperty.MatchString(property) && value != "" &&
						!unsafeCSSValue.MatchString(value) && safeMSOValue.MatchString(value) {
						declarations = append(declarations, property+":"+value)
					}
				}
				if len(declarations) == 0 {
					continue
				}
				marker := fmt.Sprintf("sendwise-mso-preserve-%04d", index)
				index++
				styles[marker] = strings.Join(declarations, ";")
				for classIndex := range node.Attr {
					if node.Attr[classIndex].Key == "class" {
						node.Attr[classIndex].Val = strings.TrimSpace(node.Attr[classIndex].Val + " " + marker)
						marker = ""
						break
					}
				}
				if marker != "" {
					node.Attr = append(node.Attr, xhtml.Attribute{Key: "class", Val: marker})
				}
				break
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(root)
	return styles
}

func restoreMSOInlineStyles(source string, styles map[string]string) string {
	if len(styles) == 0 {
		return source
	}
	document, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return source
	}
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode {
			for index := range node.Attr {
				if node.Attr[index].Key != "class" {
					continue
				}
				classes := strings.Fields(node.Attr[index].Val)
				remaining := make([]string, 0, len(classes))
				for _, className := range classes {
					style, ok := styles[className]
					if !ok {
						remaining = append(remaining, className)
						continue
					}
					for styleIndex := range node.Attr {
						if node.Attr[styleIndex].Key == "style" {
							node.Attr[styleIndex].Val = strings.TrimRight(node.Attr[styleIndex].Val, ";") + ";" + style
							style = ""
							break
						}
					}
					if style != "" {
						node.Attr = append(node.Attr, xhtml.Attribute{Key: "style", Val: style})
					}
				}
				node.Attr[index].Val = strings.Join(remaining, " ")
				if node.Attr[index].Val == "" {
					node.Attr = append(node.Attr[:index], node.Attr[index+1:]...)
				}
				break
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	var output strings.Builder
	var renderBody func(*xhtml.Node)
	renderBody = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && node.Data == "body" {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				_ = xhtml.Render(&output, child)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			renderBody(child)
		}
	}
	renderBody(document)
	if output.Len() == 0 {
		return source
	}
	return output.String()
}

var safeMSOConditionalComment = regexp.MustCompile(`(?is)<!--\s*\[if\s+((?:gte\s+)?mso(?:\s+[0-9]+)?)\]>(.*?)<!\s*\[endif\]\s*-->`)

func extractSafeMSOConditionalComments(source string) (string, map[string]string) {
	comments := make(map[string]string)
	index := 0
	source = safeMSOConditionalComment.ReplaceAllStringFunc(source, func(comment string) string {
		match := safeMSOConditionalComment.FindStringSubmatch(comment)
		if len(match) != 3 {
			return ""
		}
		content := importedHTMLPolicy.Sanitize(match[2])
		if strings.TrimSpace(content) == "" {
			return ""
		}
		marker := fmt.Sprintf("SENDWISEMSOCOMMENT%04d", index)
		index++
		comments[marker] = "<!--[if " + strings.TrimSpace(match[1]) + "]>" + content + "<![endif]-->"
		return marker
	})
	return source, comments
}

func textFromNode(node *xhtml.Node) string {
	if node.Type == xhtml.TextNode {
		return node.Data
	}
	var text strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		text.WriteString(textFromNode(child))
	}
	return text.String()
}

func sanitizeStylesheet(source string) string {
	stylesheet, err := parser.Parse(source)
	if err != nil {
		return ""
	}
	safeRules := sanitizeCSSRules(stylesheet.Rules)
	if len(safeRules) == 0 {
		return ""
	}
	return (&css.Stylesheet{Rules: safeRules}).String()
}

func sanitizeCSSRules(rules []*css.Rule) []*css.Rule {
	var safeRules []*css.Rule
	for _, rule := range rules {
		if rule.Kind == css.QualifiedRule {
			selectors := make([]string, 0, len(rule.Selectors))
			for _, selector := range rule.Selectors {
				selector = strings.TrimSpace(selector)
				if selector != "" && safeCSSSelector.MatchString(selector) {
					selectors = append(selectors, selector)
				}
			}
			declarations := sanitizeCSSDeclarations(rule.Declarations)
			if len(selectors) > 0 && len(declarations) > 0 {
				safeRules = append(safeRules, &css.Rule{
					Kind: css.QualifiedRule, Selectors: selectors, Declarations: declarations,
				})
			}
			continue
		}
		if rule.Kind == css.AtRule && strings.EqualFold(rule.Name, "@media") &&
			safeMediaQuery.MatchString(strings.TrimSpace(rule.Prelude)) {
			children := sanitizeCSSRules(rule.Rules)
			if len(children) > 0 {
				safeRules = append(safeRules, &css.Rule{
					Kind: css.AtRule, Name: "@media", Prelude: strings.TrimSpace(rule.Prelude), Rules: children,
				})
			}
		}
	}
	return safeRules
}

func sanitizeCSSDeclarations(declarations []*css.Declaration) []*css.Declaration {
	allowed := map[string]bool{
		"background": true, "background-color": true, "background-position": true,
		"background-repeat": true, "background-size": true, "border": true,
		"border-bottom": true, "border-collapse": true, "border-left": true,
		"border-radius": true, "border-right": true, "border-top": true,
		"box-sizing": true, "color": true, "direction": true, "display": true,
		"font-family": true, "font-size": true, "font-style": true, "font-weight": true,
		"height": true, "letter-spacing": true, "line-height": true, "margin": true,
		"max-height": true, "max-width": true, "min-height": true, "min-width": true,
		"opacity": true, "overflow": true, "padding": true, "text-align": true,
		"text-decoration": true, "text-transform": true, "vertical-align": true,
		"white-space": true, "width": true, "word-break": true, "word-wrap": true,
	}
	var safe []*css.Declaration
	for _, declaration := range declarations {
		property := strings.ToLower(strings.TrimSpace(declaration.Property))
		if !allowed[property] && !safeMSOProperty.MatchString(property) {
			continue
		}
		value := strings.TrimSpace(declaration.Value)
		if value == "" || unsafeCSSValue.MatchString(value) {
			continue
		}
		safe = append(safe, &css.Declaration{
			Property: property, Value: value, Important: declaration.Important,
		})
	}
	return safe
}

func addNoopenerToBlankTargets(source string) string {
	document, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return source
	}
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && node.Data == "a" {
			targetBlank := false
			for _, attribute := range node.Attr {
				if attribute.Key == "target" && strings.EqualFold(attribute.Val, "_blank") {
					targetBlank = true
				}
			}
			if targetBlank {
				found := false
				for index := range node.Attr {
					if node.Attr[index].Key == "rel" {
						found = true
						if !strings.Contains(strings.ToLower(node.Attr[index].Val), "noopener") {
							node.Attr[index].Val = strings.TrimSpace(node.Attr[index].Val + " noopener")
						}
					}
				}
				if !found {
					node.Attr = append(node.Attr, xhtml.Attribute{Key: "rel", Val: "noopener"})
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	var output strings.Builder
	var findBody func(*xhtml.Node)
	findBody = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && node.Data == "body" {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				_ = xhtml.Render(&output, child)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			findBody(child)
		}
	}
	findBody(document)
	if output.Len() == 0 {
		return source
	}
	return output.String()
}

func hasFixedEmailLayout(source string) bool {
	document, err := xhtml.Parse(strings.NewReader(source))
	if err != nil {
		return false
	}
	found := false
	var visit func(*xhtml.Node)
	visit = func(node *xhtml.Node) {
		if found {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "table" {
			for _, attribute := range node.Attr {
				if attribute.Key == "width" {
					width, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(attribute.Val), "px"))
					if err == nil && width >= 560 {
						found = true
						return
					}
				}
				if attribute.Key == "style" && fixedEmailWidth.MatchString(attribute.Val) {
					found = true
					return
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return found
}

func documentWithContent(body, styles string, theme Theme, noShell bool) string {
	if noShell {
		return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
%s
</head>
<body style="margin:0;padding:0;">
%s
</body>
</html>`, styles, body)
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
%s
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
</html>`, styles, theme.Background, theme.Background, theme.ContentBackground, theme.Text, body)
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
		sanitized := sanitizeEmailHTML(personalize(block.Content, data), data)
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
	parsedValue, err := url.Parse(value)
	if err == nil && (parsedValue.Scheme == "https" || parsedValue.Scheme == "http") &&
		parsedValue.Host != "" && parsedValue.User == nil {
		return value, true
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(data["app_base_url"]), "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return "", false
	}
	if (strings.HasPrefix(value, "/template-assets/") ||
		strings.HasPrefix(value, "/api/v1/public/assets/")) && !strings.Contains(value, "..") && parsedValue != nil && parsedValue.Scheme == "" {
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
