package contacts

import (
	"strings"
	"testing"
)

func TestCompileSegmentRulesUsesParameters(t *testing.T) {
	rules := []byte(`{"groups":[{"match":"AND","conditions":[{"field":"status","operator":"equals","value":"subscribed"},{"field":"custom_fields.country","operator":"contains","value":"nigeria"}]}]}`)
	query, args, err := CompileSegmentRules(rules, 4)
	if err != nil {
		t.Fatalf("CompileSegmentRules() error = %v", err)
	}
	if !strings.Contains(query, "lower(c.status) = lower($4)") ||
		!strings.Contains(query, "c.custom_fields ->> $5::text") ||
		!strings.Contains(query, "position(lower($6)") {
		t.Fatalf("compiled query is unexpected: %s", query)
	}
	if len(args) != 3 || args[0] != "subscribed" || args[1] != "country" || args[2] != "nigeria" {
		t.Fatalf("compiled args = %#v", args)
	}
	if strings.Contains(query, "nigeria") || strings.Contains(query, "subscribed") {
		t.Fatal("user values must not be interpolated into SQL")
	}
}

func TestCompileSegmentRulesSupportsGroupsAndReferences(t *testing.T) {
	rules := []byte(`{"groups":[{"match":"OR","conditions":[{"field":"created_at","operator":"after","value":"2026-01-01T00:00:00Z"},{"field":"email","operator":"does_not_contain","value":"example.invalid"}]},{"match":"AND","conditions":[{"field":"tag","operator":"equals","value":"VIP"},{"field":"list","operator":"equals","value":"Newsletter"}]}]}`)
	query, args, err := CompileSegmentRules(rules, 2)
	if err != nil {
		t.Fatalf("CompileSegmentRules() error = %v", err)
	}
	for _, fragment := range []string{
		" OR ",
		" AND ",
		"c.created_at > $2::timestamptz",
		"FROM tags audience_rule JOIN contact_tags",
		"FROM contact_lists audience_rule JOIN contact_list_members",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("compiled query missing %q: %s", fragment, query)
		}
	}
	if len(args) != 4 || args[2] != "VIP" || args[3] != "Newsletter" {
		t.Fatalf("compiled args = %#v", args)
	}
}

func TestCompileSegmentRulesRejectsMalformedRules(t *testing.T) {
	tests := []struct {
		name  string
		rules string
	}{
		{name: "unknown field", rules: `{"groups":[{"match":"AND","conditions":[{"field":"email; DROP TABLE contacts","operator":"equals","value":"x"}]}]}`},
		{name: "unknown operator", rules: `{"groups":[{"match":"AND","conditions":[{"field":"email","operator":"raw_sql","value":"x"}]}]}`},
		{name: "unknown group match", rules: `{"groups":[{"match":"XOR","conditions":[{"field":"email","operator":"equals","value":"x"}]}]}`},
		{name: "empty groups", rules: `{"groups":[]}`},
		{name: "unknown property", rules: `{"groups":[{"match":"AND","sql":"true","conditions":[{"field":"email","operator":"equals","value":"x"}]}]}`},
		{name: "trailing garbage", rules: `{"groups":[{"match":"AND","conditions":[{"field":"email","operator":"equals","value":"x"}]}]} garbage`},
		{name: "bad date", rules: `{"groups":[{"match":"AND","conditions":[{"field":"created_at","operator":"after","value":"yesterday"}]}]}`},
		{name: "timestamp empty check", rules: `{"groups":[{"match":"AND","conditions":[{"field":"created_at","operator":"is_empty"}]}]}`},
		{name: "unsafe custom key", rules: `{"groups":[{"match":"AND","conditions":[{"field":"custom_fields.foo -> email","operator":"equals","value":"x"}]}]}`},
		{name: "tag operator", rules: `{"groups":[{"match":"AND","conditions":[{"field":"tag","operator":"contains","value":"vip"}]}]}`},
		{name: "empty condition group", rules: `{"groups":[{"match":"AND","conditions":[]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := CompileSegmentRules([]byte(test.rules), 1); err == nil {
				t.Fatal("CompileSegmentRules() accepted unsafe rules")
			}
		})
	}
}
