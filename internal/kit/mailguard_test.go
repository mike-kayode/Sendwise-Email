package kit

import (
	"strings"
	"testing"
)

func TestValidateProductionMail(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		provider string
		addr     string
		host     string
		wantErr  string
	}{
		{
			name: "production smtp relay is mailpit", env: "production", provider: "smtp",
			addr: "mailpit:1025", host: "mailpit",
			wantErr: "development-only relay",
		},
		{
			name: "production smtp host is localhost", env: "production", provider: "smtp",
			addr: "localhost:587", host: "localhost",
			wantErr: "development-only relay",
		},
		{
			name: "production smtp address is loopback ip", env: "production", provider: "smtp",
			addr: "127.0.0.1:1025", host: "",
			wantErr: "development-only relay",
		},
		{
			name: "production provider is log", env: "production", provider: "log",
			addr: "smtp.example.test:587", host: "smtp.example.test",
			wantErr: "MAIL_PROVIDER=log is not allowed",
		},
		{
			name: "production provider is log without smtp settings", env: "production", provider: "log",
			wantErr: "MAIL_PROVIDER=log is not allowed",
		},
		{
			name: "production rejects unknown provider", env: "production", provider: "carrier-pigeon",
			wantErr: "MAIL_PROVIDER must be",
		},
		{
			name: "staging rejects log provider", env: "staging", provider: "log",
			wantErr: "MAIL_PROVIDER=log is not allowed",
		},
		{
			name: "staging rejects mailpit", env: "staging", provider: "smtp",
			addr: "mailpit:1025", host: "mailpit",
			wantErr: "development-only relay",
		},
		{
			name: "production accepts a real relay", env: "production", provider: "smtp",
			addr: "smtp.example.test:587", host: "smtp.example.test",
		},
		{
			name: "production accepts auto with a real relay", env: "production", provider: "auto",
			addr: "smtp.example.test:465", host: "smtp.example.test",
		},
		{
			name: "production accepts resend with no smtp target", env: "production", provider: "resend",
		},
		{
			// The same configuration is correct in the local and CI stacks.
			name: "development allows mailpit", env: "development", provider: "smtp",
			addr: "mailpit:1025", host: "mailpit",
		},
		{
			name: "test allows the log mailer", env: "test", provider: "log",
			addr: "localhost:1025", host: "localhost",
		},
		{
			name: "empty environment is not validated", env: "", provider: "smtp",
			addr: "mailpit:1025", host: "mailpit",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateProductionMail(test.env, test.provider, test.addr, test.host)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateProductionMail() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateProductionMail() error = nil, want %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateProductionMail() error = %q, want it to contain %q", err, test.wantErr)
			}
		})
	}
}

func TestDevelopmentSMTPHost(t *testing.T) {
	tests := []struct {
		addr, host string
		wantHost   string
		wantOK     bool
	}{
		{addr: "mailpit:1025", host: "mailpit", wantHost: "mailpit", wantOK: true},
		{addr: "Mailpit:1025", host: "MAILPIT", wantHost: "mailpit", wantOK: true},
		{addr: "localhost:1025", host: "mailpit", wantHost: "localhost", wantOK: true},
		{addr: "[::1]:1025", host: "", wantHost: "::1", wantOK: true},
		{addr: "stalwart:587", host: "stalwart"},
		{addr: "", host: ""},
	}
	for _, test := range tests {
		gotHost, gotOK := DevelopmentSMTPHost(test.addr, test.host)
		if gotHost != test.wantHost || gotOK != test.wantOK {
			t.Errorf("DevelopmentSMTPHost(%q, %q) = (%q, %t), want (%q, %t)",
				test.addr, test.host, gotHost, gotOK, test.wantHost, test.wantOK)
		}
	}
}
