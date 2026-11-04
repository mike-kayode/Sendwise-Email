package delivery

import (
	"strings"
	"testing"
)

func TestRewriteLinks(t *testing.T) {
	sign := func(target string) (string, error) {
		return "signed-" + target, nil
	}

	doc := `<a href="https://example.com/offer">Click here</a>`
	result := RewriteLinks(doc, "https://track.example.com", sign)

	if !strings.Contains(result, "/t/click/signed-https://example.com/offer") {
		t.Fatalf("RewriteLinks() did not rewrite link: %q", result)
	}
	if strings.Contains(result, `href="https://example.com/offer"`) {
		t.Fatalf("RewriteLinks() left original link: %q", result)
	}
}

func TestRewriteLinksEmptySign(t *testing.T) {
	sign := func(target string) (string, error) {
		return "", nil
	}

	doc := `<a href="https://example.com">Click</a>`
	result := RewriteLinks(doc, "https://track.example.com", sign)

	if !strings.Contains(result, "/t/click/") {
		t.Fatalf("RewriteLinks() should still rewrite with empty token: %q", result)
	}
}

func TestRewriteLinksNoHTTP(t *testing.T) {
	sign := func(target string) (string, error) {
		return "token", nil
	}

	doc := `<a href="ftp://example.com/file">Download</a>`
	result := RewriteLinks(doc, "https://track.example.com", sign)

	if strings.Contains(result, "/t/click/") {
		t.Fatalf("RewriteLinks() should not rewrite non-HTTP links: %q", result)
	}
	if !strings.Contains(doc, `ftp://example.com`) {
		t.Fatalf("RewriteLinks() modified non-HTTP link: %q", result)
	}
}

func TestRewriteLinksMultiple(t *testing.T) {
	sign := func(target string) (string, error) {
		return "tok-" + target, nil
	}

	doc := `<a href="https://a.com">A</a> and <a href="https://b.com">B</a>`
	result := RewriteLinks(doc, "https://track.example.com", sign)

	if strings.Count(result, "/t/click/") != 2 {
		t.Fatalf("RewriteLinks() should rewrite 2 links, got %q", result)
	}
}

func TestTinyPixel(t *testing.T) {
	pixel := TinyPixel()
	if len(pixel) == 0 {
		t.Fatal("TinyPixel() should return non-empty GIF")
	}
	// GIF files start with "GIF"
	if string(pixel[:3]) != "GIF" {
		t.Fatalf("TinyPixel() does not start with GIF signature: %v", pixel[:3])
	}
}

func TestTinyPixelDeterministic(t *testing.T) {
	p1 := TinyPixel()
	p2 := TinyPixel()
	if string(p1) != string(p2) {
		t.Fatal("TinyPixel() should return deterministic output")
	}
}