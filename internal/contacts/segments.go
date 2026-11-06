package contacts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	maxSegmentGroups     = 20
	maxSegmentConditions = 50
)

var customFieldKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

type SegmentRules struct {
	Groups []SegmentGroup `json:"groups"`
}

type SegmentGroup struct {
	Match      string             `json:"match"`
	Conditions []SegmentCondition `json:"conditions"`
}

type SegmentCondition struct {
	Field    string          `json:"field"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value,omitempty"`
}

// CompileSegmentRules converts the shared rules schema to a parameterized SQL
// predicate. The caller must apply its organization_id scope to the contacts.
func CompileSegmentRules(raw []byte, firstArg int) (string, []any, error) {
	if firstArg < 1 {
		return "", nil, fmt.Errorf("first SQL argument must be positive")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var rules SegmentRules
	if err := decoder.Decode(&rules); err != nil {
		return "", nil, fmt.Errorf("decode segment rules: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", nil, fmt.Errorf("segment rules must contain a single JSON value")
	}
	if len(rules.Groups) == 0 || len(rules.Groups) > maxSegmentGroups {
		return "", nil, fmt.Errorf("segment must contain 1 to %d groups", maxSegmentGroups)
	}

	args := make([]any, 0)
	groups := make([]string, 0, len(rules.Groups))
	conditionCount := 0
	for _, group := range rules.Groups {
		match := strings.ToUpper(strings.TrimSpace(group.Match))
		if match != "AND" && match != "OR" {
			return "", nil, fmt.Errorf("group match must be AND or OR")
		}
		if len(group.Conditions) == 0 {
			return "", nil, fmt.Errorf("each segment group must contain at least one condition")
		}
		conditionCount += len(group.Conditions)
		if conditionCount > maxSegmentConditions {
			return "", nil, fmt.Errorf("segment may contain at most %d conditions", maxSegmentConditions)
		}

		joiner := " AND "
		if match == "OR" {
			joiner = " OR "
		}
		clauses := make([]string, 0, len(group.Conditions))
		for _, condition := range group.Conditions {
			clause, values, err := compileCondition(condition, firstArg+len(args))
			if err != nil {
				return "", nil, err
			}
			clauses = append(clauses, clause)
			args = append(args, values...)
		}
		groups = append(groups, "("+strings.Join(clauses, joiner)+")")
	}
	return "(" + strings.Join(groups, " AND ") + ")", args, nil
}

func compileCondition(condition SegmentCondition, firstArg int) (string, []any, error) {
	field := strings.TrimSpace(condition.Field)
	operator := strings.ToLower(strings.TrimSpace(condition.Operator))

	switch field {
	case "tag", "list":
		if operator != "equals" && operator != "not_equals" {
			return "", nil, fmt.Errorf("%s only supports equals or not_equals", field)
		}
		value, err := segmentStringValue(condition.Value, operator)
		if err != nil {
			return "", nil, err
		}
		table, membersTable, idColumn, nameColumn := "tags", "contact_tags", "tag_id", "name"
		if field == "list" {
			table, membersTable, idColumn, nameColumn = "contact_lists", "contact_list_members", "list_id", "name"
		}
		exists := fmt.Sprintf(
			"EXISTS (SELECT 1 FROM %s audience_rule JOIN %s audience_members ON audience_members.%s = audience_rule.id WHERE audience_members.contact_id = c.id AND audience_members.organization_id = c.organization_id AND audience_rule.organization_id = c.organization_id AND lower(audience_rule.%s) = lower($%d))",
			table, membersTable, idColumn, nameColumn, firstArg,
		)
		if operator == "not_equals" {
			return "NOT " + exists, []any{value}, nil
		}
		return exists, []any{value}, nil
	}

	fieldSQL := ""
	fieldKind := "text"
	switch field {
	case "email":
		fieldSQL = "c.email"
	case "status":
		fieldSQL = "c.status"
	case "created_at":
		fieldSQL = "c.created_at"
		fieldKind = "date"
	default:
		if !strings.HasPrefix(field, "custom_fields.") {
			return "", nil, fmt.Errorf("unsupported segment field %q", field)
		}
		key := strings.TrimPrefix(field, "custom_fields.")
		if !customFieldKey.MatchString(key) {
			return "", nil, fmt.Errorf("invalid custom field key %q", key)
		}
		fieldSQL = fmt.Sprintf("NULLIF(c.custom_fields ->> $%d::text, '')", firstArg)
		firstArg++
		clause, valueArgs, err := compileSegmentOperator(fieldSQL, fieldKind, operator, condition.Value, firstArg)
		if err != nil {
			return "", nil, err
		}
		return clause, append([]any{key}, valueArgs...), nil
	}
	return compileSegmentOperator(fieldSQL, fieldKind, operator, condition.Value, firstArg)
}

func compileSegmentOperator(fieldSQL, fieldKind, operator string, rawValue json.RawMessage, arg int) (string, []any, error) {
	if operator == "is_empty" || operator == "is_not_empty" {
		if fieldKind == "date" || len(rawValue) != 0 {
			return "", nil, fmt.Errorf("%s does not support %q", fieldKind, operator)
		}
		if operator == "is_empty" {
			return "(" + fieldSQL + " IS NULL OR " + fieldSQL + " = '')", nil, nil
		}
		return "(" + fieldSQL + " IS NOT NULL AND " + fieldSQL + " <> '')", nil, nil
	}

	value, err := segmentStringValue(rawValue, operator)
	if err != nil {
		return "", nil, err
	}
	if fieldKind == "date" {
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return "", nil, fmt.Errorf("date value must use RFC3339: %w", err)
		}
		switch operator {
		case "before", "date_before":
			return fmt.Sprintf("%s < $%d::timestamptz", fieldSQL, arg), []any{value}, nil
		case "after", "date_after":
			return fmt.Sprintf("%s > $%d::timestamptz", fieldSQL, arg), []any{value}, nil
		default:
			return "", nil, fmt.Errorf("created_at only supports before or after")
		}
	}
	if operator == "before" || operator == "after" || operator == "date_before" || operator == "date_after" {
		return "", nil, fmt.Errorf("operator %q is only supported for created_at", operator)
	}

	parameter := fmt.Sprintf("$%d", arg)
	switch operator {
	case "equals":
		return fmt.Sprintf("lower(%s) = lower(%s)", fieldSQL, parameter), []any{value}, nil
	case "not_equals":
		return fmt.Sprintf("lower(%s) <> lower(%s)", fieldSQL, parameter), []any{value}, nil
	case "contains":
		return fmt.Sprintf("position(lower(%s) in lower(%s)) > 0", parameter, fieldSQL), []any{value}, nil
	case "does_not_contain":
		return fmt.Sprintf("position(lower(%s) in lower(%s)) = 0", parameter, fieldSQL), []any{value}, nil
	default:
		return "", nil, fmt.Errorf("unsupported segment operator %q", operator)
	}
}

func segmentStringValue(raw json.RawMessage, operator string) (string, error) {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("operator %q requires a non-empty string value", operator)
	}
	if len(value) > 1024 {
		return "", fmt.Errorf("segment value exceeds 1024 characters")
	}
	return value, nil
}
