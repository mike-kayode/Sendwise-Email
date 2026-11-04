package contacts

import (
	"strings"
	"testing"
)

func TestCompileSegmentRulesUsesParameters(t *testing.T) {
	rules := []byte(`{"match":"all","conditions":[{"field":"status","operator":"equals","value":"subscribed"},{"field":"custom_fields.country","operator":"contains","value":"nigeria"}]}`)
	query, args, err := CompileSegmentRules(rules, 4)
	if err != nil {
		t.Fatalf("CompileSegmentRules() error = %v", err)
	}
	if !strings.Contains(query, "lower(c.status) = lower($4)") || !strings.Contains(query, "c.custom_fields ->> $5::text") || !strings.Contains(query, "position(lower($6)") {
		t.Fatalf("compiled query is unexpected: %s", query)
	}
	if len(args) != 3 || args[0] != "subscribed" || args[1] != "country" || args[2] != "nigeria" {
		t.Fatalf("compiled args = %#v", args)
	}
	if strings.Contains(query, "nigeria") || strings.Contains(query, "subscribed") {
		t.Fatal("user values must not be interpolated into SQL")
	}
}

func TestCompileSegmentRulesSupportsAnyAndDateBounds(t *testing.T) {
	rules := []byte(`{"match":"any","conditions":[{"field":"created_at","operator":"date_after","value":"2026-01-01T00:00:00Z"},{"field":"email","operator":"does_not_contain","value":"example.invalid"}]}`)
	query, args, err := CompileSegmentRules(rules, 2)
	if err != nil {
		t.Fatalf("CompileSegmentRules() error = %v", err)
	}
	if !strings.Contains(query, " OR ") || !strings.Contains(query, "c.created_at > $2::timestamptz") {
		t.Fatalf("compiled query is unexpected: %s", query)
	}
	if len(args) != 2 || args[0] != "2026-01-01T00:00:00Z" || args[1] != "example.invalid" {
		t.Fatalf("compiled args = %#v", args)
	}
}

func TestCompileSegmentRulesRejectsUnsafeRules(t *testing.T) {
	tests := []struct {
		name  string
		rules string
	}{
		{name: "unknown field", rules: `{"match":"all","conditions":[{"field":"email; DROP TABLE contacts","operator":"equals","value":"x"}]}`},
		{name: "unknown operator", rules: `{"match":"all","conditions":[{"field":"email","operator":"raw_sql","value":"x"}]}`},
		{name: "invalid match", rules: `{"match":"xor","conditions":[{"field":"email","operator":"equals","value":"x"}]}`},
		{name: "empty rules", rules: `{"match":"all","conditions":[]}`},
		{name: "unknown property", rules: `{"match":"all","sql":"true","conditions":[{"field":"email","operator":"equals","value":"x"}]}`},
		{name: "trailing garbage", rules: `{"match":"all","conditions":[{"field":"email","operator":"equals","value":"x"}]} garbage`},
		{name: "bad date", rules: `{"match":"all","conditions":[{"field":"created_at","operator":"date_after","value":"yesterday"}]}`},
		{name: "timestamp empty check", rules: `{"match":"all","conditions":[{"field":"created_at","operator":"is_empty"}]}`},
		{name: "unsafe custom key", rules: `{"match":"all","conditions":[{"field":"custom_fields.foo -> email","operator":"equals","value":"x"}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := CompileSegmentRules([]byte(test.rules), 1); err == nil {
				t.Fatal("CompileSegmentRules() accepted unsafe rules")
			}
		})
	}
}
