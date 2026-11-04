package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContactConsentInputValidated(t *testing.T) {
	input := &contactConsentInput{
		Source:   "website-form",
		Purpose:  "Marketing",
		Evidence: json.RawMessage(`{"form_id":"signup","checkbox":true}`),
	}

	source, metadata, err := input.validated()
	if err != nil {
		t.Fatalf("validated() error = %v", err)
	}
	if source != "website-form" {
		t.Fatalf("source = %q, want website-form", source)
	}
	var decoded map[string]any
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		t.Fatalf("metadata is invalid JSON: %v", err)
	}
	if decoded["purpose"] != "marketing" {
		t.Fatalf("purpose = %v, want marketing", decoded["purpose"])
	}
}

func TestContactConsentInputRejectsInvalidEvidence(t *testing.T) {
	tests := []struct {
		name  string
		input *contactConsentInput
	}{
		{name: "missing consent", input: nil},
		{name: "missing source", input: &contactConsentInput{Purpose: "marketing", Evidence: json.RawMessage(`{"form":"x"}`)}},
		{name: "wrong purpose", input: &contactConsentInput{Source: "form", Purpose: "transactional", Evidence: json.RawMessage(`{"form":"x"}`)}},
		{name: "invalid JSON", input: &contactConsentInput{Source: "form", Purpose: "marketing", Evidence: json.RawMessage(`not-json`)}},
		{name: "not an object", input: &contactConsentInput{Source: "form", Purpose: "marketing", Evidence: json.RawMessage(`[]`)}},
		{name: "empty object", input: &contactConsentInput{Source: "form", Purpose: "marketing", Evidence: json.RawMessage(`{}`)}},
		{name: "oversized evidence", input: &contactConsentInput{Source: "form", Purpose: "marketing", Evidence: json.RawMessage(`{"proof":"` + strings.Repeat("x", 16<<10) + `"}`)}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := test.input.validated(); err == nil {
				t.Fatal("validated() accepted invalid consent")
			}
		})
	}
}
