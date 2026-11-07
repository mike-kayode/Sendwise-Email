package delivery

import "testing"

func TestNormalizeSenderAddress(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "lowercases address", input: "NewsLetter@Example.Test", want: "newsletter@example.test"},
		{name: "allows quoted RFC local-part", input: `"offers+weekly"@example.test`, want: "offers+weekly@example.test"},
		{name: "rejects display name", input: "News <news@example.test>", wantErr: true},
		{name: "blocks postmaster", input: "postmaster@example.test", wantErr: true},
		{name: "blocks abuse", input: "abuse@example.test", wantErr: true},
		{name: "blocks hostmaster", input: "hostmaster@example.test", wantErr: true},
		{name: "rejects local-part longer than 64 bytes", input: string(make([]byte, 65)) + "@example.test", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeSenderAddress(test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("NormalizeSenderAddress() error = %v, wantErr %t", err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("NormalizeSenderAddress() = %q, want %q", got, test.want)
			}
		})
	}
}
