package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/observability"
	"github.com/khayode/email-platform/internal/worker"
)

func TestDeliveryEgressHealthPersistReadAndGateFlip(t *testing.T) {
	if integrationPool == nil {
		t.Skip("TEST_DATABASE_URL is required for delivery egress integration")
	}
	ctx := context.Background()
	orgID := uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1::uuid, 'Egress integration', $2::text)
	`, orgID, "egress-"+orgID); err != nil {
		t.Fatalf("create organization: %v", err)
	}

	logger := observability.NewLogger("test", "worker")
	w := worker.New(config.Config{DeliveryEgressGateEnabled: true}, integrationPool, nil, nil, nil, nil, logger)
	server := newIntegrationServer(t)
	token := integrationToken(t, orgID, "owner")

	upsert := func(status string, checkedAt time.Time) {
		t.Helper()
		_, err := integrationPool.Exec(ctx, `
			INSERT INTO delivery_egress_health
				(id, status, checked_at, last_ok_at, last_blocked_at, detail, checks, consecutive_failures, updated_at)
			VALUES (1::smallint, $1::text, $2::timestamptz,
			        CASE WHEN $1::text = 'ok' THEN $2::timestamptz ELSE NULL END,
			        CASE WHEN $1::text = 'blocked' THEN $2::timestamptz ELSE NULL END,
			        $3::text, $4::jsonb, $5::integer, $2::timestamptz)
			ON CONFLICT (id) DO UPDATE SET
				status = EXCLUDED.status,
				checked_at = EXCLUDED.checked_at,
				last_ok_at = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.checked_at ELSE delivery_egress_health.last_ok_at END,
				last_blocked_at = CASE WHEN EXCLUDED.status = 'blocked' THEN EXCLUDED.checked_at ELSE delivery_egress_health.last_blocked_at END,
				detail = EXCLUDED.detail,
				checks = EXCLUDED.checks,
				consecutive_failures = EXCLUDED.consecutive_failures,
				updated_at = EXCLUDED.updated_at
		`, status, checkedAt.UTC(), "detail-"+status, `[{"host":"mx.example:25","ok":true}]`, 0)
		if err != nil {
			t.Fatalf("upsert egress status %s: %v", status, err)
		}
	}

	getAPI := func() egressHealthResponse {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/delivery/egress-health", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/delivery/egress-health status=%d body=%s", rec.Code, rec.Body.String())
		}
		var resp egressHealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode egress response: %v", err)
		}
		return resp
	}

	upsert("ok", time.Now().UTC())
	if blocked, reason := w.DeliveryEgressGateBlocked(ctx); blocked {
		t.Fatalf("worker gate blocked=%v reason=%q; want unblocked for ok", blocked, reason)
	}
	resp := getAPI()
	if resp.Status != "ok" {
		t.Fatalf("api egress status=%q; want ok", resp.Status)
	}

	upsert("blocked", time.Now().UTC().Add(time.Second))
	if blocked, _ := w.DeliveryEgressGateBlocked(ctx); !blocked {
		t.Fatalf("worker gate blocked=%v; want blocked", blocked)
	}
	resp = getAPI()
	if resp.Status != "blocked" {
		t.Fatalf("api egress status=%q; want blocked", resp.Status)
	}

	upsert("ok", time.Now().UTC().Add(2*time.Second))
	if blocked, reason := w.DeliveryEgressGateBlocked(ctx); blocked {
		t.Fatalf("worker gate blocked=%v reason=%q; want unblocked after recovery", blocked, reason)
	}
	resp = getAPI()
	if resp.Status != "ok" {
		t.Fatalf("api egress status=%q; want ok after recovery", resp.Status)
	}

	disabled := worker.New(config.Config{DeliveryEgressGateEnabled: false}, integrationPool, nil, nil, nil, nil, logger)
	upsert("blocked", time.Now().UTC().Add(3*time.Second))
	if blocked, reason := disabled.DeliveryEgressGateBlocked(ctx); blocked {
		t.Fatalf("disabled gate blocked=%v reason=%q; want unblocked when gate disabled", blocked, reason)
	}
	resp = getAPI()
	if resp.Status != "blocked" {
		t.Fatalf("api egress status=%q; want blocked status visible when gate disabled", resp.Status)
	}
}
