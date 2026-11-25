package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/khayode/email-platform/internal/httpx"
)

type egressHealthResponse struct {
	Status        string          `json:"status"`
	CheckedAt     *time.Time      `json:"checked_at,omitempty"`
	LastOKAt      *time.Time      `json:"last_ok_at,omitempty"`
	LastBlockedAt *time.Time      `json:"last_blocked_at,omitempty"`
	Detail        string          `json:"detail,omitempty"`
	Checks        json.RawMessage `json:"checks,omitempty"`
}

func (s *Server) getDeliveryEgressHealth(w http.ResponseWriter, r *http.Request) {
	resp, err := s.loadDeliveryEgressHealth(r.Context())
	if err != nil {
		s.logger.Error("load delivery egress health", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load delivery egress health.")
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}

func (s *Server) loadDeliveryEgressHealth(ctx context.Context) (*egressHealthResponse, error) {
	var resp egressHealthResponse
	var checks []byte
	err := s.pool.QueryRow(ctx, `
		SELECT status, checked_at, last_ok_at, last_blocked_at, COALESCE(detail, ''), COALESCE(checks, '[]'::jsonb)
		FROM delivery_egress_health
		WHERE id = 1::smallint
	`).Scan(&resp.Status, &resp.CheckedAt, &resp.LastOKAt, &resp.LastBlockedAt, &resp.Detail, &checks)
	if err != nil {
		if err == pgx.ErrNoRows {
			return &egressHealthResponse{Status: "unknown", Detail: "No egress health check has run yet.", Checks: json.RawMessage("[]")}, nil
		}
		return nil, err
	}
	if len(checks) == 0 {
		checks = []byte("[]")
	}
	resp.Checks = json.RawMessage(checks)
	interval := s.cfg.DeliveryEgressCheckInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	staleAfter := 3 * interval
	if resp.CheckedAt == nil || time.Since(*resp.CheckedAt) > staleAfter {
		resp.Status = "unknown"
		resp.Detail = "Egress probe not reporting; last reading is stale. Sends are not paused by this gate."
	}
	return &resp, nil
}
