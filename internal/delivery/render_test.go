package delivery

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

func TestRenderDesignEmpty(t *testing.T) {
	html, text, err := NewRenderer().RenderDesign(nil, nil)
	if err != nil {
		t.Fatalf("RenderDesign() error = %v", err)
	}
	if !strings.Contains(html, "<html") {
		t.Fatalf("RenderDesign() html missing <html> tag: %q", html)
	}
	if !strings.Contains(html, "<body") {
		t.Fatalf("RenderDesign() html missing <body> tag: %q", html)
	}
	if text == "" {
		t.Fatal("RenderDesign() text should not be empty")
	}
}

func TestRenderDesignWithBlocks(t *testing.T) {
	design := map[string]any{
		"version": 1,
		"blocks": []map[string]any{
			{"type": "heading", "content": "Hello {{first_name}}"},
			{"type": "paragraph", "content": "Welcome to our newsletter."},
			{"type": "button", "content": "Read more", "url": "https://example.com"},
			{"type": "divider"},
			{"type": "spacer", "size": "20"},
		},
	}
	raw, err := json.Marshal(design)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	html, text, err := NewRenderer().RenderDesign(json.RawMessage(raw), map[string]string{
		"first_name": "John",
	})
	if err != nil {
		t.Fatalf("RenderDesign() error = %v", err)
	}
	if !strings.Contains(html, "Hello John") {
		t.Fatalf("RenderDesign() html missing personalized content: %q", html)
	}
	if !strings.Contains(html, "https://example.com") {
		t.Fatalf("RenderDesign() html missing button URL: %q", html)
	}
	if !strings.Contains(html, "<hr") {
		t.Fatalf("RenderDesign() html missing divider: %q", html)
	}
	if !strings.Contains(text, "Read more: https://example.com") {
		t.Fatalf("RenderDesign() text missing button: %q", text)
	}
	if !strings.Contains(html, "v:roundrect") || !strings.Contains(html, "@media screen") {
		t.Fatalf("RenderDesign() is missing Outlook button or mobile styles: %q", html)
	}
}

func TestRenderDesignPlainTextAndTheme(t *testing.T) {
	raw := json.RawMessage(`{
		"version":1,"mode":"plain","text_content":"Hello {{first_name}}\nRead the update.",
		"theme":{"background":"#010203","content_background":"#ffffff","text":"#123456"}
	}`)
	htmlBody, textBody, err := NewRenderer().RenderDesign(raw, map[string]string{"first_name": "Ada"})
	if err != nil {
		t.Fatalf("RenderDesign() error = %v", err)
	}
	if textBody != "Hello Ada\nRead the update." {
		t.Fatalf("plain text = %q", textBody)
	}
	if !strings.Contains(htmlBody, "background-color:#010203") ||
		!strings.Contains(htmlBody, "Hello Ada") {
		t.Fatalf("plain text HTML did not preserve the theme or personalized text: %q", htmlBody)
	}
}

func TestRenderDesignGeneratesTextFromHTMLBlocks(t *testing.T) {
	raw := json.RawMessage(`{
		"version":1,
		"blocks":[
			{"type":"heading","content":"Hello"},
			{"type":"columns-2","columns":[
				[{"type":"paragraph","content":"First column"}],
				[{"type":"paragraph","content":"Second column"}]
			]}
		]
	}`)
	_, textBody, err := NewRenderer().RenderDesign(raw, nil)
	if err != nil {
		t.Fatalf("RenderDesign() error = %v", err)
	}
	for _, expected := range []string{"Hello", "First column", "Second column"} {
		if !strings.Contains(textBody, expected) {
			t.Fatalf("generated text is missing %q: %q", expected, textBody)
		}
	}
}

func TestRenderDesignSanitizesImportedHTML(t *testing.T) {
	raw := json.RawMessage(`{"version":1,"blocks":[{"type":"custom_html","content":"<table style=\"color:#123456\"><tr><td>Safe text<script>alert(1)</script><img src=\"https://tracker.example/pixel\"></td></tr></table>"}]}`)
	htmlBody, textBody, err := NewRenderer().RenderDesign(raw, nil)
	if err != nil {
		t.Fatalf("RenderDesign() error = %v", err)
	}
	if !strings.Contains(htmlBody, "<table") || !strings.Contains(htmlBody, "color: #123456") {
		t.Fatalf("imported table markup or inline style was lost: %q", htmlBody)
	}
	for _, forbidden := range []string{"<script", "alert(1)"} {
		if strings.Contains(htmlBody, forbidden) || strings.Contains(textBody, forbidden) {
			t.Fatalf("unsafe imported markup %q was retained: HTML=%q text=%q", forbidden, htmlBody, textBody)
		}
	}
	if !strings.Contains(htmlBody, `src="https://tracker.example/pixel"`) {
		t.Fatalf("external HTTPS image was lost from imported markup: %q", htmlBody)
	}
	invalidSource := json.RawMessage(`{"version":2,"html":"<img src=\"data:image/png;base64,AAAA\">","mode":"html","html_mode":"original"}`)
	invalidHTML, _, err := NewRenderer().RenderDesign(invalidSource, nil)
	if err != nil {
		t.Fatalf("RenderDesign() invalid image error = %v", err)
	}
	if !strings.Contains(invalidHTML, "Image unavailable because its source is not a supported HTTP or HTTPS URL.") {
		t.Fatalf("unsupported image source was removed without visible replacement: %q", invalidHTML)
	}
}

func TestRenderEmailBuilderDesignSanitizesAndPersonalizesHTML(t *testing.T) {
	raw := json.RawMessage(`{
		"version":2,
		"editor":"grapesjs",
		"editor_project":{"pages":[{"component":"saved project data"}]},
		"html":"<table><tr><td><h1>Hi {{first_name}}</h1><p style=\"color:#123456\">Welcome to {{organization}}</p><img src=\"/api/v1/public/assets/6416c701-bb37-43ce-a0ff-dba04ec9e215\" alt=\"Logo\"><img src=\"https://tracker.example/pixel\"><script>alert(1)</script></td></tr></table>"
	}`)
	htmlBody, textBody, err := NewRenderer().RenderDesign(raw, map[string]string{
		"first_name":   "Ada",
		"organization": "Sendwise",
		"app_base_url": "https://sendwise.example",
	})
	if err != nil {
		t.Fatalf("RenderDesign() error = %v", err)
	}
	for _, expected := range []string{
		"Hi Ada",
		"Welcome to Sendwise",
		"https://sendwise.example/api/v1/public/assets/6416c701-bb37-43ce-a0ff-dba04ec9e215",
		"color: #123456",
	} {
		if !strings.Contains(htmlBody, expected) {
			t.Fatalf("EmailBuilder HTML is missing %q: %q", expected, htmlBody)
		}
	}
	for _, forbidden := range []string{"<script", "alert(1)"} {
		if strings.Contains(htmlBody, forbidden) || strings.Contains(textBody, forbidden) {
			t.Fatalf("unsafe EmailBuilder markup %q was retained: HTML=%q text=%q", forbidden, htmlBody, textBody)
		}
	}
	if !strings.Contains(htmlBody, `src="https://tracker.example/pixel"`) {
		t.Fatalf("external HTTPS image was lost from EmailBuilder HTML: %q", htmlBody)
	}
	for _, expected := range []string{"Hi Ada", "Welcome to Sendwise"} {
		if !strings.Contains(textBody, expected) {
			t.Fatalf("plain-text version is missing %q: %q", expected, textBody)
		}
	}
}

func TestSafeImageURLPreservesHTTPSImages(t *testing.T) {
	data := map[string]string{"app_base_url": "https://sendwise.example"}
	if got, ok := safeImageURL("/template-assets/neutral-banner.svg", data); !ok || got != "https://sendwise.example/template-assets/neutral-banner.svg" {
		t.Fatalf("safeImageURL() = %q, %v", got, ok)
	}
	assetPath := "/api/v1/public/assets/6416c701-bb37-43ce-a0ff-dba04ec9e215"
	if got, ok := safeImageURL(assetPath, data); !ok || got != "https://sendwise.example"+assetPath {
		t.Fatalf("safeImageURL() did not accept owned asset: %q, %v", got, ok)
	}
	if got, ok := safeImageURL("https://sendwise.example/uploads/external.png", data); !ok || got != "https://sendwise.example/uploads/external.png" {
		t.Fatalf("safeImageURL() rejected HTTPS image: %q, %v", got, ok)
	}
	if got, ok := safeImageURL("https://third-party.example/image.png", data); !ok || got != "https://third-party.example/image.png" {
		t.Fatalf("safeImageURL() rejected external HTTPS image: %q, %v", got, ok)
	}
}

func TestRenderImportedFullDocumentPreservesLayoutAndStyles(t *testing.T) {
	source, err := os.ReadFile("testdata/nairatrader-get-funded.html")
	if err != nil {
		t.Fatalf("read NairaTrader HTML fixture: %v", err)
	}
	importedHTML := strings.NewReplacer(
		"https://res.cloudinary.com/nairatrader/image/upload/v1/brand/logo.png",
		"https://sendwise.example/api/v1/public/assets/nairatrader-logo",
		"https://res.cloudinary.com/nairatrader/image/upload/v1/brand/mark.png",
		"https://sendwise.example/api/v1/public/assets/nairatrader-mark",
	).Replace(string(source))
	for _, htmlMode := range []string{"original", "visual"} {
		raw, err := json.Marshal(map[string]any{
			"version": 2, "mode": "html", "html_mode": htmlMode, "html": importedHTML,
			"compliance_footer": map[string]string{
				"alignment": "center", "background_color": "#f8fafc",
				"text_color": "#64748b", "button_color": "#4f46e5", "button_text": "Unsubscribe",
			},
		})
		if err != nil {
			t.Fatalf("marshal design: %v", err)
		}
		htmlBody, _, err := NewRenderer().RenderDesign(raw, map[string]string{
			"app_base_url":        "https://sendwise.example",
			"unsubscribe_url":     "https://sendwise.example/unsubscribe/test",
			"sender_company":      "NairaTrader",
			"sender_social_links": `[{"label":"Instagram","url":"https://instagram.com/nairatrader"}]`,
		})
		if err != nil {
			t.Fatalf("RenderDesign() error = %v", err)
		}
		for _, expected := range []string{
			`src="https://sendwise.example/api/v1/public/assets/nairatrader-logo"`,
			`src="https://sendwise.example/api/v1/public/assets/nairatrader-mark"`,
			`class="price-row"`, `bgcolor="#f7f7f7"`, "25,000 account", "$149 fee", "100,000 account",
			"letter-spacing", "white-space", "@media", "mso-table-lspace", "Instagram", "LinkedIn",
			`target="_blank"`, `rel="noopener"`, "<!--[if mso]>",
		} {
			if !strings.Contains(htmlBody, expected) {
				t.Errorf("%s render is missing %q", htmlMode, expected)
			}
		}
		if strings.Contains(htmlBody, `class="email-content"`) || strings.Contains(htmlBody, "padding:32px") {
			t.Fatalf("%s render wrapped the full-document 600px template in the Sendwise padded shell", htmlMode)
		}
		if got := strings.Count(htmlBody, `width="600"`); got != 1 {
			t.Fatalf("%s render has %d 600px layout widths, want exactly once", htmlMode, got)
		}
		if strings.Count(htmlBody, `data-sendwise-compliance-footer="true"`) != 1 {
			t.Fatalf("%s render should contain exactly one compliance footer: %s", htmlMode, htmlBody)
		}
		footerIndex := strings.Index(htmlBody, `data-sendwise-compliance-footer="true"`)
		if footerIndex < strings.Index(htmlBody, "LinkedIn") || footerIndex > strings.Index(htmlBody, "</body>") {
			t.Fatalf("%s compliance footer is not at the bottom after template content: %s", htmlMode, htmlBody)
		}
		if !hasPricingRowWithThreeCells(htmlBody) {
			t.Fatalf("%s render lost the size | fee | button table row structure: %s", htmlMode, htmlBody)
		}
	}
}

func TestRenderPreservesGrapesJSExpandedInlineStyles(t *testing.T) {
	raw := json.RawMessage(`{
		"version":2,
		"mode":"html",
		"html_mode":"original",
		"html":"<!doctype html><html><head><style>@media only screen and (max-width: 600px) { .hero-h1 { font-size: 32px !important; line-height: 40px !important; } }</style></head><body><table class=\"wrapper\" style=\"box-sizing:border-box;margin-top:0px;margin-right:auto;margin-bottom:0px;margin-left:auto;width:100%;max-width:600px;border-top-left-radius:12px;border-top-right-radius:12px;border-image-outset:0;border-image-repeat:stretch;border-image-slice:100%;border-image-source:none;border-image-width:1;overflow-x:hidden\"><tr><td class=\"hero\" style=\"padding-top:40px;padding-right:30px;padding-bottom:30px;padding-left:30px;text-size-adjust:100%\"><h1 class=\"hero-h1\" style=\"margin-top:0px;margin-right:0px;margin-bottom:16px;margin-left:0px;font-size:42px\">Hello</h1><a style=\"display:inline-block;padding-top:12px;padding-right:20px;padding-bottom:12px;padding-left:20px;background-color:#c98a00;color:#ffffff;text-decoration-line:none\">Continue</a></td></tr></table></body></html>"
	}`)
	rendered, _, err := NewRenderer().RenderDesign(raw, nil)
	if err != nil {
		t.Fatalf("RenderDesign() error = %v", err)
	}
	for _, expected := range []string{
		"margin-top: 0px", "margin-bottom: 16px", "padding-top: 40px",
		"padding-left: 30px", "border-top-left-radius: 12px", "box-sizing: border-box",
		"border-image-outset: 0", "border-image-repeat: stretch", "border-image-slice: 100%",
		"border-image-source: none", "border-image-width: 1", "text-size-adjust: 100%",
		"overflow-x: hidden", "text-decoration-line: none",
		"@media only screen and (max-width: 600px)", "font-size: 32px !important",
		"background-color: #c98a00", "color: #ffffff",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("render lost GrapesJS email style %q: %s", expected, rendered)
		}
	}
}

func hasPricingRowWithThreeCells(source string) bool {
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
		if node.Type == xhtml.ElementNode && node.Data == "tr" {
			cells := make([]string, 0, 3)
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == xhtml.ElementNode && child.Data == "td" {
					cells = append(cells, strings.TrimSpace(textFromNode(child)))
				}
			}
			if len(cells) == 3 && strings.Contains(cells[0], "25,000 account") &&
				strings.Contains(cells[1], "$149 fee") && strings.Contains(cells[2], "Get funded") {
				found = true
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	return found
}

func TestPersonalize(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		data     map[string]string
		expected string
	}{
		{name: "known key", value: "Hi {{first_name}}", data: map[string]string{"first_name": "John"}, expected: "Hi John"},
		{name: "unknown key", value: "Hi {{missing}}", data: map[string]string{"first_name": "John"}, expected: "Hi "},
		{name: "no placeholder", value: "Hello world", data: map[string]string{}, expected: "Hello world"},
		{name: "custom key", value: "{{custom.company}}", data: map[string]string{"custom.company": "Acme"}, expected: "Acme"},
		{name: "empty data", value: "{{first_name}}", data: map[string]string{}, expected: ""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := personalize(test.value, test.data); got != test.expected {
				t.Fatalf("personalize(%q, %v) = %q, want %q", test.value, test.data, got, test.expected)
			}
		})
	}
}

func TestIsSafeURL(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		{name: "https URL", value: "https://example.com", expected: true},
		{name: "http URL", value: "http://example.com/path", expected: true},
		{name: "ftp URL", value: "ftp://example.com", expected: false},
		{name: "javascript URL", value: "javascript:alert(1)", expected: false},
		{name: "empty host", value: "https://", expected: false},
		{name: "empty string", value: "", expected: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isSafeURL(test.value); got != test.expected {
				t.Fatalf("isSafeURL(%q) = %v, want %v", test.value, got, test.expected)
			}
		})
	}
}

func TestParseSpacer(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected int
	}{
		{name: "valid", value: "24", expected: 24},
		{name: "invalid", value: "abc", expected: 24},
		{name: "zero", value: "0", expected: 24},
		{name: "negative", value: "-5", expected: 24},
		{name: "too large", value: "1000", expected: 320},
		{name: "empty", value: "", expected: 24},
		{name: "boundary min", value: "1", expected: 1},
		{name: "boundary max", value: "320", expected: 320},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseSpacer(test.value); got != test.expected {
				t.Fatalf("parseSpacer(%q) = %d, want %d", test.value, got, test.expected)
			}
		})
	}
}

func TestSafeAlign(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{name: "center", value: "center", expected: "center"},
		{name: "right", value: "right", expected: "right"},
		{name: "left", value: "left", expected: "left"},
		{name: "empty", value: "", expected: "left"},
		{name: "invalid", value: "justify", expected: "left"},
		{name: "uppercase center", value: "CENTER", expected: "center"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := safeAlign(test.value); got != test.expected {
				t.Fatalf("safeAlign(%q) = %q, want %q", test.value, got, test.expected)
			}
		})
	}
}

func TestRenderDesignInvalidJSON(t *testing.T) {
	_, _, err := NewRenderer().RenderDesign(json.RawMessage(`{invalid json`), nil)
	if err == nil {
		t.Fatal("RenderDesign() should return error for invalid JSON")
	}
}
