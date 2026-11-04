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

const maxSegmentConditions = 50

var customFieldKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

type SegmentRules struct {
	Match      string             `json:"match"`
	Conditions []SegmentCondition `json:"conditions"`
}

type SegmentCondition struct {
	Field    string          `json:"field"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value,omitempty"`
}

// CompileSegmentRules converts validated rules to a SQL predicate. firstArg is
// the first PostgreSQL placeholder available to this predicate.
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

	rules.Match = strings.ToLower(strings.TrimSpace(rules.Match))
	if rules.Match != "all" && rules.Match != "any" {
		return "", nil, fmt.Errorf("segment match must be all or any")
	}
	if len(rules.Conditions) == 0 || len(rules.Conditions) > maxSegmentConditions {
		return "", nil, fmt.Errorf("segment must contain 1 to %d conditions", maxSegmentConditions)
	}

	joiner := " AND "
	if rules.Match == "any" {
		joiner = " OR "
	}
	args := make([]any, 0, len(rules.Conditions)*2)
	clauses := make([]string, 0, len(rules.Conditions))
	for _, condition := range rules.Conditions {
		clause, values, err := compileCondition(condition, firstArg+len(args))
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, clause)
		args = append(args, values...)
	}
	return "(" + strings.Join(clauses, joiner) + ")", args, nil
}

func compileCondition(condition SegmentCondition, firstArg int) (string, []any, error) {
	field := strings.TrimSpace(condition.Field)
	operator := strings.ToLower(strings.TrimSpace(condition.Operator))

	fieldSQL := ""
	fieldKind := "text"
	switch field {
	case "email":
		fieldSQL = "c.email"
	case "first_name":
		fieldSQL = "COALESCE(c.first_name, '')"
	case "last_name":
		fieldSQL = "COALESCE(c.last_name, '')"
	case "status":
		fieldSQL = "c.status"
	case "source":
		fieldSQL = "COALESCE(c.source, '')"
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
		fieldArgs := []any{key}
		clause, valueArgs, err := compileOperator(fieldSQL, fieldKind, operator, condition.Value, firstArg)
		if err != nil {
			return "", nil, err
		}
		return clause, append(fieldArgs, valueArgs...), nil
	}

	return compileOperator(fieldSQL, fieldKind, operator, condition.Value, firstArg)
}

func compileOperator(fieldSQL, fieldKind, operator string, rawValue json.RawMessage, arg int) (string, []any, error) {
	if operator == "is_empty" {
		if fieldKind == "date" {
			return "", nil, fmt.Errorf("empty checks are not supported for created_at")
		}
		if len(rawValue) != 0 {
			return "", nil, fmt.Errorf("is_empty does not accept a value")
		}
		return "(" + fieldSQL + " IS NULL OR " + fieldSQL + " = '')", nil, nil
	}
	if operator == "is_not_empty" {
		if fieldKind == "date" {
			return "", nil, fmt.Errorf("empty checks are not supported for created_at")
		}
		if len(rawValue) != 0 {
			return "", nil, fmt.Errorf("is_not_empty does not accept a value")
		}
		return "(" + fieldSQL + " IS NOT NULL AND " + fieldSQL + " <> '')", nil, nil
	}

	var value string
	if len(rawValue) == 0 || json.Unmarshal(rawValue, &value) != nil || strings.TrimSpace(value) == "" {
		return "", nil, fmt.Errorf("operator %q requires a non-empty string value", operator)
	}
	if len(value) > 1024 {
		return "", nil, fmt.Errorf("segment value exceeds 1024 characters")
	}

	if strings.HasPrefix(operator, "date_") {
		if fieldKind != "date" {
			return "", nil, fmt.Errorf("operator %q is only supported for created_at", operator)
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return "", nil, fmt.Errorf("date value must use RFC3339: %w", err)
		}
		switch operator {
		case "date_before":
			return fmt.Sprintf("%s < $%d::timestamptz", fieldSQL, arg), []any{value}, nil
		case "date_after":
			return fmt.Sprintf("%s > $%d::timestamptz", fieldSQL, arg), []any{value}, nil
		default:
			return "", nil, fmt.Errorf("unsupported segment operator %q", operator)
		}
	}

	if fieldKind == "date" {
		return "", nil, fmt.Errorf("created_at requires a date_before or date_after operator")
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
	case "greater_than":
		return fmt.Sprintf("%s > %s", fieldSQL, parameter), []any{value}, nil
	case "less_than":
		return fmt.Sprintf("%s < %s", fieldSQL, parameter), []any{value}, nil
	default:
		return "", nil, fmt.Errorf("unsupported segment operator %q", operator)
	}
}
