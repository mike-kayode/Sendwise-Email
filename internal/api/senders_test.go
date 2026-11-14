package api

import "testing"

func TestSenderProfileCanBeCompleteWithoutPostalAddress(t *testing.T) {
	profile := senderProfile{
		CompanyName:  "Sendwise",
		Website:      "https://example.test",
		SupportEmail: "support@example.test",
	}
	if !senderProfileComplete(profile) {
		t.Fatal("sender profile with company, website, and support email should be complete without a postal address")
	}
}
