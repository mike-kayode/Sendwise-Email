package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

type contactConsentInput struct {
	Source   string          `json:"source"`
	Purpose  string          `json:"purpose"`
	Evidence json.RawMessage `json:"evidence"`
}

func (input *contactConsentInput) validated() (string, []byte, error) {
	if input == nil {
		return "", nil, fmt.Errorf("consent evidence is required to subscribe a contact")
	}
	source := strings.TrimSpace(input.Source)
	if source == "" || len(source) > 128 {
		return "", nil, fmt.Errorf("consent source must contain 1 to 128 characters")
	}
	purpose := strings.TrimSpace(input.Purpose)
	if !strings.EqualFold(purpose, "marketing") {
		return "", nil, fmt.Errorf("consent purpose must be marketing")
	}
	if len(input.Evidence) == 0 || len(input.Evidence) > 16<<10 {
		return "", nil, fmt.Errorf("consent evidence must be a non-empty JSON object no larger than 16 KiB")
	}
	var evidence map[string]json.RawMessage
	if err := json.Unmarshal(input.Evidence, &evidence); err != nil || len(evidence) == 0 {
		return "", nil, fmt.Errorf("consent evidence must be a non-empty JSON object")
	}
	metadata, err := json.Marshal(map[string]any{
		"purpose":  "marketing",
		"evidence": evidence,
	})
	if err != nil {
		return "", nil, fmt.Errorf("encode consent evidence: %w", err)
	}
	return source, metadata, nil
}

func recordSubscribeConsent(ctx context.Context, tx pgx.Tx, r *http.Request, organizationID, contactID, source string, metadata []byte) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO consent_events
			(organization_id, contact_id, event_type, source, ip_address, user_agent, metadata)
		VALUES ($1, $2, 'subscribe', $3, $4, $5, $6::jsonb)
	`, organizationID, contactID, source, clientIP(r), r.UserAgent(), metadata)
	if err != nil {
		return fmt.Errorf("record subscribe consent: %w", err)
	}
	return nil
}
