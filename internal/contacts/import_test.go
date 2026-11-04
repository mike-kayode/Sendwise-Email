package contacts

import (
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
