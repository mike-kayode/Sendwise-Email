package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestVerifyStalwartTelemetrySignature(t *testing.T) {
	body := []byte(`{"events":[]}`)
	mac := hmac.New(sha256.New, []byte("obvious-test-secret"))
	_, _ = mac.Write(body)
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	if !verifyStalwartTelemetrySignature("obvious-test-secret", body, signature) {
		t.Fatal("valid signed telemetry body was rejected")
	}
	if verifyStalwartTelemetrySignature("obvious-test-secret", []byte(`{"events":[{}]}`), signature) {
		t.Fatal("signature was accepted for a modified body")
	}
	if verifyStalwartTelemetrySignature("", body, signature) {
		t.Fatal("signature was accepted without a configured secret")
	}
}

func TestMapStalwartJourneyType(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		source string
		data   map[string]json.RawMessage
		want   string
	}{
		{name: "relay attempt", source: "delivery.attempt-start", want: "relaying"},
		{name: "recipient acceptance", source: "delivery.delivered", want: "accepted_by_recipient_server"},
		{name: "temporary recipient failure", source: "delivery.dsn-temp-fail", want: "deferred_by_recipient"},
		{name: "permanent recipient failure", source: "delivery.dsn-perm-fail", want: "bounced"},
		{name: "mail server expiration", source: "delivery.failed",
			data: map[string]json.RawMessage{"expired": json.RawMessage(`true`)}, want: "expired"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := mapStalwartJourneyType(test.source, test.data, now)
			if !ok || got != test.want {
				t.Fatalf("mapStalwartJourneyType() = %q, %v; want %q, true", got, ok, test.want)
			}
		})
	}
}
