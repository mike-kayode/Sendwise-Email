package contacts

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
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
	FullName     string            `json:"full_name,omitempty"`
	CustomFields map[string]string `json:"custom_fields,omitempty"`
	HasHeader    *bool             `json:"has_header,omitempty"`
	ImportStatus string            `json:"import_status,omitempty"`
	Consent      *CSVImportConsent `json:"consent,omitempty"`
}

type CSVImportConsent struct {
	Source   string          `json:"source"`
	Evidence json.RawMessage `json:"evidence"`
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
	pendingRecord  []string
	emailIndex     int
	firstNameIndex int
	lastNameIndex  int
	fullNameIndex  int
	customIndexes  map[string]int
}

func NewCSVReader(source io.Reader, mapping *CSVColumnMapping) (*CSVReader, CSVColumnMapping, error) {
	data, err := io.ReadAll(source)
	if err != nil {
		return nil, CSVColumnMapping{}, fmt.Errorf("read CSV data: %w", err)
	}
	data, err = decodeCSV(data)
	if err != nil {
		return nil, CSVColumnMapping{}, fmt.Errorf("decode CSV data: %w", err)
	}

	reader := newCSVDecoder(data)
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return nil, CSVColumnMapping{}, fmt.Errorf("read CSV header: %w", err)
	}
	if len(header) == 0 || len(header) > MaxCSVImportColumns {
		return nil, CSVColumnMapping{}, fmt.Errorf("CSV must contain between 1 and %d columns", MaxCSVImportColumns)
	}

	hasHeader := inferCSVHeader(header)
	if mapping != nil && mapping.HasHeader != nil {
		hasHeader = *mapping.HasHeader
	}
	var pendingRecord []string
	if !hasHeader {
		pendingRecord = header
		header = make([]string, len(pendingRecord))
		for index := range header {
			header[index] = fmt.Sprintf("column_%d", index+1)
		}
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
		if !hasHeader {
			resolved.Email = header[0]
		}
	} else {
		resolved = *mapping
	}
	resolved.HasHeader = &hasHeader

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
	fullNameIndex, err := optionalColumnIndex(indexes, resolved.FullName, "full_name")
	if err != nil {
		return nil, CSVColumnMapping{}, err
	}
	if fullNameIndex >= 0 && (firstNameIndex >= 0 || lastNameIndex >= 0) {
		return nil, CSVColumnMapping{}, fmt.Errorf("map either a full name column or separate first and last name columns")
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
	if fullNameIndex >= 0 {
		resolved.FullName = header[fullNameIndex]
	}
	return &CSVReader{
		reader:         reader,
		mapping:        resolved,
		pendingRecord:  pendingRecord,
		emailIndex:     emailIndex,
		firstNameIndex: firstNameIndex,
		lastNameIndex:  lastNameIndex,
		fullNameIndex:  fullNameIndex,
		customIndexes:  customIndexes,
	}, resolved, nil
}

func (r *CSVReader) Read() (CSVContactRow, error) {
	var record []string
	if r.pendingRecord != nil {
		record = r.pendingRecord
		r.pendingRecord = nil
	} else {
		var err error
		record, err = r.reader.Read()
		if err != nil {
			return CSVContactRow{}, err
		}
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
	firstName := strings.TrimSpace(cell(record, r.firstNameIndex))
	lastName := strings.TrimSpace(cell(record, r.lastNameIndex))
	if r.fullNameIndex >= 0 {
		firstName, lastName = splitFullName(cell(record, r.fullNameIndex))
	}
	return CSVContactRow{
		Email:        strings.ToLower(address.Address),
		FirstName:    firstName,
		LastName:     lastName,
		CustomFields: customJSON,
	}, nil
}

func defaultCSVMapping(headers []string) CSVColumnMapping {
	mapping := CSVColumnMapping{CustomFields: make(map[string]string)}
	for _, header := range headers {
		switch header {
		case "email", "email_address", "e_mail", "mail":
			mapping.Email = header
		case "first_name", "firstname", "given_name", "given":
			mapping.FirstName = header
		case "last_name", "lastname", "family_name", "surname":
			mapping.LastName = header
		case "fullname", "full_name", "name":
			mapping.FullName = header
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

func splitFullName(value string) (string, string) {
	parts := strings.Fields(value)
	if len(parts) < 2 {
		if len(parts) == 1 {
			return parts[0], ""
		}
		return "", ""
	}
	return parts[0], strings.Join(parts[1:], " ")
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
	column = strings.TrimSpace(strings.TrimPrefix(column, "\ufeff"))
	var normalized strings.Builder
	separator := true
	for _, character := range strings.ToLower(column) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			normalized.WriteRune(character)
			separator = false
		} else if !separator {
			normalized.WriteByte('_')
			separator = true
		}
	}
	return strings.Trim(normalized.String(), "_")
}

func cell(record []string, index int) string {
	if index < 0 || index >= len(record) {
		return ""
	}
	return record[index]
}

func decodeCSV(data []byte) ([]byte, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if utf8.Valid(data) {
		return data, nil
	}
	decoded, _, err := transform.Bytes(charmap.Windows1252.NewDecoder(), data)
	return decoded, err
}

func newCSVDecoder(data []byte) *csv.Reader {
	delimiter := ','
	bestColumns := 0
	for _, candidate := range []rune{',', ';'} {
		reader := csv.NewReader(bytes.NewReader(data))
		reader.Comma = candidate
		reader.TrimLeadingSpace = true
		reader.FieldsPerRecord = -1
		record, err := reader.Read()
		if err == nil && len(record) > bestColumns {
			delimiter = candidate
			bestColumns = len(record)
		}
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = delimiter
	return reader
}

func inferCSVHeader(firstRecord []string) bool {
	if len(firstRecord) == 0 {
		return true
	}
	for _, column := range firstRecord {
		switch normalizeColumn(column) {
		case "email", "email_address", "e_mail", "mail":
			return true
		}
	}
	for _, value := range firstRecord {
		value = strings.TrimSpace(value)
		address, err := mail.ParseAddress(value)
		if err == nil && strings.EqualFold(address.Address, value) {
			return false
		}
	}
	return true
}
