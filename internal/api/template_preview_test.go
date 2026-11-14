package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/khayode/email-platform/internal/config"
)

func TestPreviewTemplateReturnsSendRendererOutput(t *testing.T) {
	server := &Server{cfg: config.Config{AppBaseURL: "https://sendwise.example"}}
	body, err := json.Marshal(map[string]any{
		"design": map[string]any{
			"version": 2, "mode": "html", "html_mode": "original",
			"html": `<!doctype html><html><head><meta name="viewport" content="width=device-width"></head><body><table width="600"><tr><td><img src="https://images.example/logo.png"><script>alert(1)</script></td></tr></table></body></html>`,
		},
	})
	if err != nil {
		t.Fatalf("marshal preview request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/templates/preview", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.previewTemplate(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var result struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	if !strings.Contains(result.HTML, `src="https://images.example/logo.png"`) {
		t.Fatalf("preview lost external image: %s", result.HTML)
	}
	if strings.Contains(result.HTML, "<script") || strings.Contains(result.HTML, "padding:32px") {
		t.Fatalf("preview does not match safe original-document rendering: %s", result.HTML)
	}
	if strings.Count(result.HTML, `data-sendwise-compliance-footer="true"`) != 1 ||
		!strings.Contains(result.HTML, "/unsubscribe-preview") {
		t.Fatalf("preview does not include the same required compliance footer as a send: %s", result.HTML)
	}
}
