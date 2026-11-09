package delivery

import (
	"strings"
	"testing"
)

const feedbackTestSecret = "test-verp-secret-with-enough-bytes"

func TestVERPReturnPathRoundTripAndTampering(t *testing.T) {
	want := VERPClaims{
		WorkspaceID: "workspace-1",
		CampaignID:  "campaign-2",
		Recipient:   "person@example.test",
	}
	token, err := NewVERPToken(feedbackTestSecret, want)
	if err != nil {
		t.Fatal(err)
	}
	if len(token)+len("bounces+") > 64 {
		t.Fatalf("VERP local part exceeds RFC limit: %d characters", len(token)+len("bounces+"))
	}
	address, err := FormatVERPReturnPath("bounces.example.test", token)
	if err != nil {
		t.Fatal(err)
	}
	gotToken, err := ParseVERPTokenAddress("bounces.example.test", address)
	if err != nil {
		t.Fatal(err)
	}
	if gotToken != token || !VerifyVERPToken(feedbackTestSecret, gotToken, want) {
		t.Fatalf("VERP token did not verify for expected claims")
	}

	local, domain, _ := strings.Cut(address, "@")
	tampered := local[:len(local)-1] + "A@" + domain
	tamperedToken, err := ParseVERPTokenAddress("bounces.example.test", tampered)
	if err != nil || VerifyVERPToken(feedbackTestSecret, tamperedToken, want) {
		t.Fatal("tampered VERP token was accepted")
	}
	if _, err := ParseVERPTokenAddress("other.example.test", address); err == nil {
		t.Fatal("VERP token for another bounce domain was accepted")
	}
}

func TestParseDSNHardBounce(t *testing.T) {
	address := testVERPAddress(t)
	raw := "From: mailer-daemon@example.test\r\n" +
		"To: " + address + "\r\n" +
		"Content-Type: multipart/report; report-type=delivery-status; boundary=report\r\n\r\n" +
		"--report\r\nContent-Type: text/plain\r\n\r\nDelivery failed.\r\n" +
		"--report\r\nContent-Type: message/delivery-status\r\n\r\n" +
		"Final-Recipient: rfc822; person@example.test\r\nAction: failed\r\n" +
		"Status: 5.1.1\r\nDiagnostic-Code: smtp; 550 user unknown\r\n" +
		"--report--\r\n"

	got, err := ParseFeedbackMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != FeedbackHardBounce || got.EnhancedCode != "5.1.1" || got.Recipient != "person@example.test" {
		t.Fatalf("parsed hard bounce = %#v", got)
	}
	if got.Diagnostic != "smtp; 550 user unknown" {
		t.Fatalf("diagnostic = %q", got.Diagnostic)
	}
}

func TestParseDSNSoftBounce(t *testing.T) {
	raw := "From: mailer-daemon@example.test\r\n" +
		"To: " + testVERPAddress(t) + "\r\n" +
		"Content-Type: multipart/report; report-type=delivery-status; boundary=report\r\n\r\n" +
		"--report\r\nContent-Type: message/delivery-status\r\n\r\n" +
		"Final-Recipient: rfc822; person@example.test\r\nAction: delayed\r\n" +
		"Status: 4.2.0\r\nDiagnostic-Code: smtp; 450 mailbox busy\r\n" +
		"--report--\r\n"

	got, err := ParseFeedbackMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != FeedbackSoftBounce || got.EnhancedCode != "4.2.0" {
		t.Fatalf("parsed soft bounce = %#v", got)
	}
}

func TestParseARFComplaint(t *testing.T) {
	raw := "From: fbl@example.test\r\n" +
		"To: " + testVERPAddress(t) + "\r\n" +
		"Content-Type: multipart/report; report-type=feedback-report; boundary=report\r\n\r\n" +
		"--report\r\nContent-Type: text/plain\r\n\r\nComplaint report.\r\n" +
		"--report\r\nContent-Type: message/feedback-report\r\n\r\n" +
		"Feedback-Type: abuse\r\nUser-Agent: Example FBL\r\n" +
		"Original-Rcpt-To: person@example.test\r\n" +
		"--report--\r\n"

	got, err := ParseFeedbackMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != FeedbackComplaint || got.FeedbackClass != "abuse" || got.Recipient != "person@example.test" {
		t.Fatalf("parsed ARF complaint = %#v", got)
	}
}

func testVERPAddress(t *testing.T) string {
	t.Helper()
	address, err := NewVERPReturnPath(feedbackTestSecret, "bounces.example.test", VERPClaims{
		WorkspaceID: "workspace-1",
		CampaignID:  "campaign-2",
		Recipient:   "person@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return address
}
