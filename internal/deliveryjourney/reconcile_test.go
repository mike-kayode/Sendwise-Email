package deliveryjourney

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/delivery"
)

func TestDecodeQueueRecipientFixtureAndStringStatus(t *testing.T) {
	const fixture = `{"createdAt":"2026-11-24T11:41:16Z","nextRetry":"2026-11-24T17:26:16Z","returnPath":"<>","recipients":{"bounces+v1TOKEN@bounces.example.test":{"retryCount":5,"retryDue":"2026-11-24T17:26:16Z","queueName":"dsn","status":{"errorType":"connectionError","errorMessage":"Unparseable SMTP reply","errorCommand":null,"responseHostname":"mail.example.test","responseCode":null,"responseEnhanced":null,"responseMessage":null,"@type":"TemporaryFailure"}}},"flags":{"dsn":true},"id":"jqb2p3hwfaqa"}`
	var raw rawQueueMessage
	if err := json.Unmarshal([]byte(fixture), &raw); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	msg := parseQueueMessage(raw)
	if !msg.Flags.DSN || msg.ReturnPath != "<>" || msg.ID != "jqb2p3hwfaqa" {
		t.Fatalf("unexpected message envelope: %+v", msg)
	}
	state, ok := msg.Recipients["bounces+v1TOKEN@bounces.example.test"]
	if !ok {
		t.Fatalf("fixture recipient missing: %+v", msg.Recipients)
	}
	if state.Status != "deferred_by_recipient" {
		t.Fatalf("status=%q want deferred_by_recipient", state.Status)
	}
	if state.RemoteHost != "mail.example.test" {
		t.Fatalf("remote host=%q", state.RemoteHost)
	}
	if state.Reason == "" || !strings.Contains(state.Reason, "Unparseable SMTP reply") {
		t.Fatalf("reason=%q want error message fallback", state.Reason)
	}
	if state.Attempt == nil || *state.Attempt != 6 {
		t.Fatalf("attempt=%v want 6", state.Attempt)
	}
	if state.NextRetryAt == nil || state.NextRetryAt.Format(time.RFC3339) != "2026-11-24T17:26:16Z" {
		t.Fatalf("nextRetry=%v", state.NextRetryAt)
	}

	// Old string status form must still decode.
	code := 250
	old := parseQueueRecipient(rawQueueRecipient{Status: json.RawMessage(`"completed"`), RetryCount: 0}, "")
	old.SMTPCode = &code
	old.Reason = "ok"
	old.RemoteHost = "mx"
	// parseQueueRecipient from string gives status "completed"; stage mapping should accept it.
	oldParsed := parseQueueRecipient(rawQueueRecipient{Status: json.RawMessage(`"completed"`)}, "")
	if oldParsed.Status != "completed" {
		t.Fatalf("old status=%q want completed", oldParsed.Status)
	}
	if oldParsed.Attempt == nil || *oldParsed.Attempt != 1 {
		t.Fatalf("old attempt=%v want 1", oldParsed.Attempt)
	}
	stage := queueJourneyStage(QueueRecipient{Status: "completed", SMTPCode: &code, Reason: "ok", RemoteHost: "mx"})
	if stage != "accepted_by_recipient_server" {
		t.Fatalf("stage=%q", stage)
	}
}

func TestDecodeQueueRecipientUnknownTypeDoesNotFail(t *testing.T) {
	raw := json.RawMessage(`{"@type":"SomeFutureState","errorMessage":"mystery","responseHostname":"mx.example"}`)
	status, rawType, remoteHost, _, _, reason := parseRecipientStatus(raw)
	if status != "accepted_final_state_unknown" {
		t.Fatalf("status=%q want accepted_final_state_unknown", status)
	}
	if !strings.Contains(rawType, "SomeFutureState") {
		t.Fatalf("rawType=%q", rawType)
	}
	if remoteHost != "mx.example" || !strings.Contains(reason, "mystery") {
		t.Fatalf("remoteHost=%q reason=%q", remoteHost, reason)
	}
	parsed := parseQueueRecipient(rawQueueRecipient{Status: raw, RetryCount: 1}, "")
	stage := queueJourneyStage(parsed)
	if stage != "accepted_final_state_unknown" {
		t.Fatalf("stage=%q reason=%q", stage, parsed.Reason)
	}
	if !strings.Contains(parsed.Reason, "SomeFutureState") {
		t.Fatalf("reason should include raw @type: %q", parsed.Reason)
	}
}

func TestDecodeQueueRecipientUnknownShapeDoesNotFail(t *testing.T) {
	raw := json.RawMessage(`{"@type":123,"nested":{"bad":true}}`)
	status, _, _, _, _, _ := parseRecipientStatus(raw)
	if status != "accepted_final_state_unknown" {
		t.Fatalf("status=%q", status)
	}
}

type fakeQueueReader struct {
	snapshot QueueSnapshot
	err      error
}

func (f fakeQueueReader) Snapshot(context.Context) (QueueSnapshot, error) {
	if f.err != nil {
		return QueueSnapshot{}, f.err
	}
	return f.snapshot, nil
}

func TestResolveCandidateLegacyBounceDomainMatch(t *testing.T) {
	orgID, campaignID, contactID := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333"
	email := "user@example.test"
	token, err := delivery.NewVERPToken("journey-test-token", delivery.VERPClaims{WorkspaceID: orgID, CampaignID: campaignID, Recipient: email})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	legacyRP, err := delivery.FormatVERPReturnPath("bounces.pipsleague.com", token)
	if err != nil {
		t.Fatalf("format legacy return path: %v", err)
	}
	r := NewReconcilerWithReader(nil, fakeQueueReader{}, "bounces.new.test", WithLegacyBounceDomains("bounces.pipsleague.com"))
	snap := QueueSnapshot{Complete: true, Messages: []QueueMessage{{
		ReturnPath: legacyRP,
		Recipients: map[string]QueueRecipient{email: {Status: "completed", SMTPCode: intPtr(250), RemoteHost: "mx"}},
	}}}
	index := indexSnapshot(snap)
	cand := candidate{organizationID: orgID, campaignID: campaignID, contactID: contactID, jobID: "44444444-4444-4444-4444-444444444444", email: email, token: token}
	ev, found, resolveErr := r.resolveCandidate(context.Background(), cand, index, snap.Complete)
	if resolveErr != nil || !found {
		t.Fatalf("found=%v err=%v want legacy domain match", found, resolveErr)
	}
	if ev == nil || ev.Type != "accepted_by_recipient_server" {
		t.Fatalf("event=%+v", ev)
	}
}

func TestResolveCandidateIgnoresDSNMessages(t *testing.T) {
	snap := QueueSnapshot{Complete: true, DSNInQueue: 1, Messages: []QueueMessage{{
		ReturnPath: "bounces+tok123@bounces.example.test",
		Flags:      QueueFlags{DSN: true},
		Recipients: map[string]QueueRecipient{"user@example.test": {Status: "completed"}},
	}}}
	index := indexSnapshot(snap)
	_, ok := index.lookup("bounces+tok123@bounces.example.test", "user@example.test")
	if ok {
		t.Fatal("DSN message should be ignored in snapshot index")
	}
}

func intPtr(v int) *int { return &v }
