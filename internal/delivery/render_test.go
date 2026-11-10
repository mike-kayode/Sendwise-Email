package delivery

import (
	"encoding/json"
	"strings"
	"testing"
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
	for _, forbidden := range []string{"<script", "alert(1)", "<img"} {
		if strings.Contains(htmlBody, forbidden) || strings.Contains(textBody, forbidden) {
			t.Fatalf("unsafe imported markup %q was retained: HTML=%q text=%q", forbidden, htmlBody, textBody)
		}
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
	for _, forbidden := range []string{"tracker.example", "<script", "alert(1)"} {
		if strings.Contains(htmlBody, forbidden) || strings.Contains(textBody, forbidden) {
			t.Fatalf("unsafe EmailBuilder markup %q was retained: HTML=%q text=%q", forbidden, htmlBody, textBody)
		}
	}
	for _, expected := range []string{"Hi Ada", "Welcome to Sendwise"} {
		if !strings.Contains(textBody, expected) {
			t.Fatalf("plain-text version is missing %q: %q", expected, textBody)
		}
	}
}

func TestSafeImageURLRequiresSendwiseHost(t *testing.T) {
	data := map[string]string{"app_base_url": "https://sendwise.example"}
	if got, ok := safeImageURL("/template-assets/neutral-banner.svg", data); !ok || got != "https://sendwise.example/template-assets/neutral-banner.svg" {
		t.Fatalf("safeImageURL() = %q, %v", got, ok)
	}
	assetPath := "/api/v1/public/assets/6416c701-bb37-43ce-a0ff-dba04ec9e215"
	if got, ok := safeImageURL(assetPath, data); !ok || got != "https://sendwise.example"+assetPath {
		t.Fatalf("safeImageURL() did not accept owned asset: %q, %v", got, ok)
	}
	if _, ok := safeImageURL("https://sendwise.example/uploads/external.png", data); ok {
		t.Fatal("safeImageURL() accepted a hotlinked image from the app host")
	}
	if _, ok := safeImageURL("https://third-party.example/image.png", data); ok {
		t.Fatal("safeImageURL() accepted a third-party image host")
	}
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
