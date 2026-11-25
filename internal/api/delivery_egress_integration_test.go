package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/egressprobe"
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
	w := worker.New(config.Config{DeliveryEgressGateEnabled: true, DeliveryEgressCheckInterval: 5 * time.Minute}, integrationPool, nil, nil, nil, nil, logger)
	server := newIntegrationServer(t)
	token := integrationToken(t, orgID, "owner")

	upsert := func(status string, checkedAt time.Time) {
		t.Helper()
		_, err := integrationPool.Exec(ctx, `
			INSERT INTO delivery_egress_health
				(id, status, checked_at, last_ok_at, last_blocked_at, detail, checks, consecutive_failures, unconfirmed_failure, updated_at)
			VALUES (1::smallint, $1::text, $2::timestamptz,
			        CASE WHEN $1::text = 'ok' THEN $2::timestamptz ELSE NULL END,
			        CASE WHEN $1::text = 'blocked' THEN $2::timestamptz ELSE NULL END,
			        $3::text, $4::jsonb, $5::integer, false, $2::timestamptz)
			ON CONFLICT (id) DO UPDATE SET
				status = EXCLUDED.status,
				checked_at = EXCLUDED.checked_at,
				last_ok_at = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.checked_at ELSE delivery_egress_health.last_ok_at END,
				last_blocked_at = CASE WHEN EXCLUDED.status = 'blocked' THEN EXCLUDED.checked_at ELSE delivery_egress_health.last_blocked_at END,
				detail = EXCLUDED.detail,
				checks = EXCLUDED.checks,
				consecutive_failures = EXCLUDED.consecutive_failures,
				unconfirmed_failure = EXCLUDED.unconfirmed_failure,
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

	disabled := worker.New(config.Config{DeliveryEgressGateEnabled: false, DeliveryEgressCheckInterval: 5 * time.Minute}, integrationPool, nil, nil, nil, nil, logger)
	upsert("blocked", time.Now().UTC().Add(3*time.Second))
	if blocked, reason := disabled.DeliveryEgressGateBlocked(ctx); blocked {
		t.Fatalf("disabled gate blocked=%v reason=%q; want unblocked when gate disabled", blocked, reason)
	}
	resp = getAPI()
	if resp.Status != "blocked" {
		t.Fatalf("api egress status=%q; want blocked status visible when gate disabled", resp.Status)
	}
}

func TestDeliveryEgressHealthStaleReadingIsFailOpen(t *testing.T) {
	if integrationPool == nil {
		t.Skip("TEST_DATABASE_URL is required for delivery egress integration")
	}
	ctx := context.Background()
	orgID := uuid.NewString()
	if _, err := integrationPool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug)
		VALUES ($1::uuid, 'Egress stale', $2::text)
	`, orgID, "egress-stale-"+orgID); err != nil {
		t.Fatalf("create organization: %v", err)
	}

	logger := observability.NewLogger("test", "worker")
	interval := time.Minute
	w := worker.New(config.Config{DeliveryEgressGateEnabled: true, DeliveryEgressCheckInterval: interval}, integrationPool, nil, nil, nil, nil, logger)
	server := newIntegrationServer(t)
	token := integrationToken(t, orgID, "owner")

	_, err := integrationPool.Exec(ctx, `
		INSERT INTO delivery_egress_health
			(id, status, checked_at, last_blocked_at, detail, checks, consecutive_failures, unconfirmed_failure, updated_at)
		VALUES (1::smallint, 'blocked', $1::timestamptz, $1::timestamptz, 'old blocked', '[]'::jsonb, 2, false, $1::timestamptz)
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status, checked_at = EXCLUDED.checked_at, last_blocked_at = EXCLUDED.last_blocked_at,
			detail = EXCLUDED.detail, checks = EXCLUDED.checks, consecutive_failures = EXCLUDED.consecutive_failures,
			unconfirmed_failure = EXCLUDED.unconfirmed_failure, updated_at = EXCLUDED.updated_at
	`, time.Now().UTC().Add(-20*time.Minute))
	if err != nil {
		t.Fatalf("insert stale blocked row: %v", err)
	}

	if blocked, reason := w.DeliveryEgressGateBlocked(ctx); blocked {
		t.Fatalf("stale worker gate blocked=%v reason=%q; want fail-open", blocked, reason)
	}
	w.RefreshDeliveryEgressHealthMetricsForTest(ctx)
	if got := observability.DeliveryEgressBlockedValueForTest(); got != 0 {
		t.Fatalf("stale blocked gauge=%v; want 0", got)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/delivery/egress-health", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/delivery/egress-health status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp egressHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "unknown" {
		t.Fatalf("stale api status=%q; want unknown", resp.Status)
	}
	if resp.Detail == "" || resp.Detail == "old blocked" {
		t.Fatalf("stale api detail=%q; want probe not reporting detail", resp.Detail)
	}
}

func TestDeliveryEgressProbeRestartSequence(t *testing.T) {
	if integrationPool == nil {
		t.Skip("TEST_DATABASE_URL is required for delivery egress probe integration")
	}
	ctx := context.Background()
	logger := observability.NewLogger("test", "egress-probe")
	cfg := config.Config{
		DeliveryEgressCheckInterval: 5 * time.Minute,
		DeliveryEgressCheckTimeout:  50 * time.Millisecond,
		DeliveryEgressCheckHosts:    []string{"mx1.example:25", "mx2.example:25"},
	}

	reset := func() {
		t.Helper()
		_, err := integrationPool.Exec(ctx, `DELETE FROM delivery_egress_health WHERE id = 1::smallint`)
		if err != nil {
			t.Fatalf("reset egress health: %v", err)
		}
	}
	read := func() (status string, failures int, unconfirmed bool) {
		t.Helper()
		err := integrationPool.QueryRow(ctx, `
			SELECT status, consecutive_failures, COALESCE(unconfirmed_failure, false)
			FROM delivery_egress_health WHERE id = 1::smallint
		`).Scan(&status, &failures, &unconfirmed)
		if err != nil {
			t.Fatalf("read egress health: %v", err)
		}
		return
	}

	failDial := func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: network, Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 25}, Err: context.DeadlineExceeded}
	}
	okDial := func(network, address string, timeout time.Duration) (net.Conn, error) {
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}

	reset()
	// First failed cycle on a fresh process: unconfirmed, fail-open, exit non-zero.
	c1 := egressprobe.NewChecker(integrationPool, cfg, logger)
	c1.SetDialFn(failDial)
	if !c1.RunCheck(ctx) {
		t.Fatalf("first failed cycle should request exit/restart")
	}
	status, failures, unconfirmed := read()
	if status != "unknown" || failures != 0 || !unconfirmed {
		t.Fatalf("after first failure got status=%q failures=%d unconfirmed=%v; want unknown,0,true", status, failures, unconfirmed)
	}

	// Seen again after fresh start: counts toward blocked.
	c2 := egressprobe.NewChecker(integrationPool, cfg, logger)
	c2.SetDialFn(failDial)
	if !c2.RunCheck(ctx) {
		t.Fatalf("second failed cycle should request exit/restart")
	}
	status, failures, unconfirmed = read()
	if status != "unknown" || failures != 1 || !unconfirmed {
		t.Fatalf("after second failure got status=%q failures=%d unconfirmed=%v; want unknown,1,true", status, failures, unconfirmed)
	}

	c3 := egressprobe.NewChecker(integrationPool, cfg, logger)
	c3.SetDialFn(failDial)
	if !c3.RunCheck(ctx) {
		t.Fatalf("third failed cycle should request exit/restart")
	}
	status, failures, unconfirmed = read()
	if status != "blocked" || failures != 2 || !unconfirmed {
		t.Fatalf("after third failure got status=%q failures=%d unconfirmed=%v; want blocked,2,true", status, failures, unconfirmed)
	}

	// Success after restart clears state.
	c4 := egressprobe.NewChecker(integrationPool, cfg, logger)
	c4.SetDialFn(okDial)
	if c4.RunCheck(ctx) {
		t.Fatalf("successful cycle should not request exit")
	}
	status, failures, unconfirmed = read()
	if status != "ok" || failures != 0 || unconfirmed {
		t.Fatalf("after recovery got status=%q failures=%d unconfirmed=%v; want ok,0,false", status, failures, unconfirmed)
	}
}
