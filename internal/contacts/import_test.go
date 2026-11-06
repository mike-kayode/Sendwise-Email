package contacts

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestCSVReaderUsesDefaultMappingAndIgnoresStatus(t *testing.T) {
	reader, mapping, err := NewCSVReader(strings.NewReader("email,first_name,last_name,company,status\nUSER@example.com,Ada,Lovelace,Analytical Engines,subscribed\n"), nil)
	if err != nil {
		t.Fatalf("NewCSVReader() error = %v", err)
	}
	if mapping.Email != "email" || mapping.FirstName != "first_name" || mapping.LastName != "last_name" || mapping.CustomFields["company"] != "company" {
		t.Fatalf("unexpected default mapping: %#v", mapping)
	}

	row, err := reader.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if row.Email != "user@example.com" || row.FirstName != "Ada" || row.LastName != "Lovelace" {
		t.Fatalf("unexpected mapped contact row: %#v", row)
	}
	if strings.Contains(string(row.CustomFields), "subscribed") {
		t.Fatalf("CSV status must not enter custom fields: %s", row.CustomFields)
	}
	if _, err := reader.Read(); err != io.EOF {
		t.Fatalf("Read() end error = %v, want EOF", err)
	}
}

func TestCSVReaderUsesExplicitMapping(t *testing.T) {
	mapping := &CSVColumnMapping{
		Email:     "Email Address",
		FirstName: "Given Name",
		CustomFields: map[string]string{
			"Company Name": "company",
		},
	}
	reader, _, err := NewCSVReader(strings.NewReader("Email Address,Given Name,Company Name\nada@example.com,Ada,Engines Inc\n"), mapping)
	if err != nil {
		t.Fatalf("NewCSVReader() error = %v", err)
	}
	row, err := reader.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if row.Email != "ada@example.com" || row.FirstName != "Ada" || string(row.CustomFields) != `{"company":"Engines Inc"}` {
		t.Fatalf("unexpected explicitly mapped row: %#v", row)
	}
}

func TestNewCSVReaderRejectsInvalidMappingsAndHeaders(t *testing.T) {
	tests := []struct {
		name    string
		csv     string
		mapping *CSVColumnMapping
	}{
		{name: "missing email column", csv: "name\nAda\n"},
		{name: "duplicate normalized headers", csv: "email, Email \nada@example.com,a@example.com\n"},
		{name: "mapped column missing", csv: "email,name\nada@example.com,Ada\n", mapping: &CSVColumnMapping{Email: "email", FirstName: "given_name"}},
		{name: "unsafe custom key", csv: "email,company\nada@example.com,Engines Inc\n", mapping: &CSVColumnMapping{Email: "email", CustomFields: map[string]string{"company": "company -> email"}}},
		{name: "duplicate custom target key", csv: "email,company,organization\nada@example.com,Engines,Engines Inc\n", mapping: &CSVColumnMapping{Email: "email", CustomFields: map[string]string{"company": "org", "organization": "org"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := NewCSVReader(strings.NewReader(test.csv), test.mapping); err == nil {
				t.Fatal("NewCSVReader() accepted an invalid mapping/header")
			}
		})
	}
}

func TestCSVReaderRejectsInvalidAddressAndColumnCount(t *testing.T) {
	reader, _, err := NewCSVReader(strings.NewReader("email,name\nnot-an-email,Ada\nada@example.com\n"), nil)
	if err != nil {
		t.Fatalf("NewCSVReader() error = %v", err)
	}
	if _, err := reader.Read(); err == nil {
		t.Fatal("Read() accepted an invalid email")
	}
	if _, err := reader.Read(); err == nil {
		t.Fatal("Read() accepted a row with an invalid number of columns")
	}
}

func TestCSVReaderSupportsBOMSemicolonQuotedMultilineAndHeaderAliases(t *testing.T) {
	source := "\ufeffEmail Address;First Name;Company Name\nada@example.com;Ada;\"Engines;\r\nInc\"\n"
	reader, mapping, err := NewCSVReader(strings.NewReader(source), nil)
	if err != nil {
		t.Fatalf("NewCSVReader() error = %v", err)
	}
	if mapping.Email != "email_address" || mapping.FirstName != "first_name" {
		t.Fatalf("unexpected inferred mapping: %#v", mapping)
	}
	row, err := reader.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	var fields map[string]string
	if err := json.Unmarshal(row.CustomFields, &fields); err != nil {
		t.Fatalf("decode custom fields: %v", err)
	}
	if row.Email != "ada@example.com" || row.FirstName != "Ada" || fields["company_name"] != "Engines;\nInc" {
		t.Fatalf("unexpected parsed row: %#v, fields=%#v", row, fields)
	}
}

func TestCSVReaderSupportsWindows1252(t *testing.T) {
	source := []byte("email,first_name,company\njose@example.com,Jos\xe9,Caf\xe9\n")
	reader, _, err := NewCSVReader(strings.NewReader(string(source)), nil)
	if err != nil {
		t.Fatalf("NewCSVReader() error = %v", err)
	}
	row, err := reader.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if row.FirstName != "José" || string(row.CustomFields) != `{"company":"Café"}` {
		t.Fatalf("unexpected decoded row: %#v", row)
	}
}

func TestCSVReaderKeepsFirstRowWhenHeaderIsAbsent(t *testing.T) {
	reader, mapping, err := NewCSVReader(strings.NewReader("ada@example.com\nlinus@example.com\n"), nil)
	if err != nil {
		t.Fatalf("NewCSVReader() error = %v", err)
	}
	if mapping.Email != "column_1" || mapping.HasHeader == nil || *mapping.HasHeader {
		t.Fatalf("unexpected headerless mapping: %#v", mapping)
	}
	for _, email := range []string{"ada@example.com", "linus@example.com"} {
		row, err := reader.Read()
		if err != nil {
			t.Fatalf("Read() error = %v", err)
		}
		if row.Email != email {
			t.Fatalf("email = %q, want %q", row.Email, email)
		}
	}
	if _, err := reader.Read(); err != io.EOF {
		t.Fatalf("Read() end error = %v, want EOF", err)
	}
}

func TestCSVReaderHonorsExplicitHeaderlessMapping(t *testing.T) {
	hasHeader := false
	mapping := &CSVColumnMapping{
		Email:     "Column 2",
		FirstName: "Column 1",
		HasHeader: &hasHeader,
	}
	reader, _, err := NewCSVReader(strings.NewReader("Ada,EMAIL@EXAMPLE.COM\n"), mapping)
	if err != nil {
		t.Fatalf("NewCSVReader() error = %v", err)
	}
	row, err := reader.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if row.Email != "email@example.com" {
		t.Fatalf("email = %q, want normalized email", row.Email)
	}
	if row.FirstName != "Ada" {
		t.Fatalf("first name = %q, want Ada", row.FirstName)
	}
}

// TestImportCSVReaderPassesDuplicatesAndRejectsInvalidRows documents the
// parser's role in the import pipeline: duplicate rows are passed through
// (de-duplication happens in the database on conflict), invalid email rows
// return an error so the worker can count them as invalid with a row number.
func TestImportCSVReaderPassesDuplicatesAndRejectsInvalidRows(t *testing.T) {
	source := "email,first_name\nada@example.test,Ada\nada@example.test,Ada\nnot-an-email,Broken\n"
	reader, mapping, err := NewCSVReader(strings.NewReader(source), nil)
	if err != nil {
		t.Fatalf("NewCSVReader() error = %v", err)
	}
	if mapping.HasHeader == nil || !*mapping.HasHeader {
		t.Fatal("header row was not detected")
	}

	first, err := reader.Read()
	if err != nil {
		t.Fatalf("first Read() error = %v", err)
	}
	if first.Email != "ada@example.test" {
		t.Fatalf("first row email = %q", first.Email)
	}

	duplicate, err := reader.Read()
	if err != nil {
		t.Fatalf("duplicate row Read() error = %v, duplicates must pass through to the DB", err)
	}
	if duplicate.Email != first.Email {
		t.Fatalf("duplicate row email = %q, want %q", duplicate.Email, first.Email)
	}

	if _, err := reader.Read(); err == nil || !strings.Contains(err.Error(), "invalid email address") {
		t.Fatalf("invalid row Read() error = %v, want invalid email address", err)
	}
	if _, err := reader.Read(); err != io.EOF {
		t.Fatalf("final Read() error = %v, want EOF", err)
	}
}
