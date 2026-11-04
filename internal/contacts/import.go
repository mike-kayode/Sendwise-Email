package contacts

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"strings"
)

var importFieldKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

const (
	MaxCSVImportRows    = 100000
	MaxCSVImportColumns = 200
)

type CSVColumnMapping struct {
	Email        string            `json:"email"`
	FirstName    string            `json:"first_name,omitempty"`
	LastName     string            `json:"last_name,omitempty"`
	CustomFields map[string]string `json:"custom_fields,omitempty"`
}

type CSVContactRow struct {
	Email        string
	FirstName    string
	LastName     string
	CustomFields []byte
}

type CSVReader struct {
	reader         *csv.Reader
	mapping        CSVColumnMapping
	emailIndex     int
	firstNameIndex int
	lastNameIndex  int
	customIndexes  map[string]int
}

func NewCSVReader(source io.Reader, mapping *CSVColumnMapping) (*CSVReader, CSVColumnMapping, error) {
	reader := csv.NewReader(source)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return nil, CSVColumnMapping{}, fmt.Errorf("read CSV header: %w", err)
	}
	if len(header) == 0 || len(header) > MaxCSVImportColumns {
		return nil, CSVColumnMapping{}, fmt.Errorf("CSV must contain between 1 and %d columns", MaxCSVImportColumns)
	}

	indexes := make(map[string]int, len(header))
	for index, column := range header {
		key := normalizeColumn(column)
		if key == "" {
			return nil, CSVColumnMapping{}, fmt.Errorf("CSV header column %d is empty", index+1)
		}
		if _, exists := indexes[key]; exists {
			return nil, CSVColumnMapping{}, fmt.Errorf("duplicate CSV header %q", strings.TrimSpace(column))
		}
		indexes[key] = index
		header[index] = key
	}
	reader.FieldsPerRecord = len(header)

	resolved := CSVColumnMapping{}
	if mapping == nil {
		resolved = defaultCSVMapping(header)
	} else {
		resolved = *mapping
	}

	emailIndex, ok := indexes[normalizeColumn(resolved.Email)]
	if !ok || normalizeColumn(resolved.Email) == "" {
		return nil, CSVColumnMapping{}, fmt.Errorf("mapped email column %q is missing", resolved.Email)
	}
	firstNameIndex, err := optionalColumnIndex(indexes, resolved.FirstName, "first_name")
	if err != nil {
		return nil, CSVColumnMapping{}, err
	}
	lastNameIndex, err := optionalColumnIndex(indexes, resolved.LastName, "last_name")
	if err != nil {
		return nil, CSVColumnMapping{}, err
	}

	customIndexes := make(map[string]int, len(resolved.CustomFields))
	fieldKeys := make(map[string]struct{}, len(resolved.CustomFields))
	for column, fieldKey := range resolved.CustomFields {
		headerKey := normalizeColumn(column)
		columnIndex, exists := indexes[headerKey]
		if !exists || headerKey == "" {
			return nil, CSVColumnMapping{}, fmt.Errorf("mapped custom field column %q is missing", column)
		}
		fieldKey = strings.TrimSpace(fieldKey)
		if !importFieldKey.MatchString(fieldKey) {
			return nil, CSVColumnMapping{}, fmt.Errorf("invalid custom field key %q", fieldKey)
		}
		if _, exists := fieldKeys[fieldKey]; exists {
			return nil, CSVColumnMapping{}, fmt.Errorf("custom field key %q is mapped more than once", fieldKey)
		}
		fieldKeys[fieldKey] = struct{}{}
		customIndexes[fieldKey] = columnIndex
	}

	resolved.Email = header[emailIndex]
	if firstNameIndex >= 0 {
		resolved.FirstName = header[firstNameIndex]
	}
	if lastNameIndex >= 0 {
		resolved.LastName = header[lastNameIndex]
	}
	return &CSVReader{
		reader:         reader,
		mapping:        resolved,
		emailIndex:     emailIndex,
		firstNameIndex: firstNameIndex,
		lastNameIndex:  lastNameIndex,
		customIndexes:  customIndexes,
	}, resolved, nil
}

func (r *CSVReader) Read() (CSVContactRow, error) {
	record, err := r.reader.Read()
	if err != nil {
		return CSVContactRow{}, err
	}
	rawEmail := strings.TrimSpace(cell(record, r.emailIndex))
	address, err := mail.ParseAddress(rawEmail)
	if err != nil || !strings.EqualFold(address.Address, rawEmail) {
		return CSVContactRow{}, fmt.Errorf("invalid email address")
	}

	customFields := make(map[string]string, len(r.customIndexes))
	for key, index := range r.customIndexes {
		if value := strings.TrimSpace(cell(record, index)); value != "" {
			customFields[key] = value
		}
	}
	customJSON, err := json.Marshal(customFields)
	if err != nil {
		return CSVContactRow{}, fmt.Errorf("encode custom fields: %w", err)
	}
	return CSVContactRow{
		Email:        strings.ToLower(address.Address),
		FirstName:    strings.TrimSpace(cell(record, r.firstNameIndex)),
		LastName:     strings.TrimSpace(cell(record, r.lastNameIndex)),
		CustomFields: customJSON,
	}, nil
}

func defaultCSVMapping(headers []string) CSVColumnMapping {
	mapping := CSVColumnMapping{CustomFields: make(map[string]string)}
	for _, header := range headers {
		switch header {
		case "email":
			mapping.Email = header
		case "first_name":
			mapping.FirstName = header
		case "last_name":
			mapping.LastName = header
		case "status", "source":
			continue
		default:
			if importFieldKey.MatchString(header) {
				mapping.CustomFields[header] = header
			}
		}
	}
	return mapping
}

func optionalColumnIndex(indexes map[string]int, column, label string) (int, error) {
	if strings.TrimSpace(column) == "" {
		return -1, nil
	}
	index, ok := indexes[normalizeColumn(column)]
	if !ok {
		return -1, fmt.Errorf("mapped %s column %q is missing", label, column)
	}
	return index, nil
}

func normalizeColumn(column string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(column, "\ufeff")))
}

func cell(record []string, index int) string {
	if index < 0 || index >= len(record) {
		return ""
	}
	return record[index]
}
