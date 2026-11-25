package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
	"github.com/khayode/email-platform/internal/observability"
)

type hostCheck struct {
	Host      string `json:"host"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

func main() {
	cfg, err := config.Load()
	logger := observability.NewLogger(cfg.Env, "egress-probe")
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	logger.Info("delivery egress probe started",
		"interval", cfg.DeliveryEgressCheckInterval.String(),
		"hosts", strings.Join(cfg.DeliveryEgressCheckHosts, ","),
		"gate_enabled", cfg.DeliveryEgressGateEnabled,
	)

	runCheck(ctx, pool, cfg, logger)
	ticker := time.NewTicker(cfg.DeliveryEgressCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("delivery egress probe stopped")
			return
		case <-ticker.C:
			runCheck(ctx, pool, cfg, logger)
		}
	}
}

func runCheck(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) {
	hosts := cfg.DeliveryEgressCheckHosts
	if len(hosts) == 0 {
		hosts = []string{"gmail-smtp-in.l.google.com:25", "outlook-com.olc.protection.outlook.com:25", "mta5.am0.yahoodns.net:25"}
	}
	timeout := cfg.DeliveryEgressCheckTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	checks := make([]hostCheck, 0, len(hosts))
	anyOK := false
	allFailed := true
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		start := time.Now()
		conn, err := net.DialTimeout("tcp", host, timeout)
		latency := time.Since(start).Milliseconds()
		check := hostCheck{Host: host, LatencyMS: latency}
		if err != nil {
			check.OK = false
			check.Detail = truncateErr(err)
		} else {
			_ = conn.Close()
			check.OK = true
			anyOK = true
			allFailed = false
		}
		checks = append(checks, check)
	}
	if len(checks) == 0 {
		allFailed = true
		anyOK = false
		checks = append(checks, hostCheck{Host: "no-hosts-configured", OK: false, Detail: "no egress check hosts configured"})
	}

	cycleFailed := allFailed && !anyOK
	now := time.Now().UTC()
	checksJSON, err := json.Marshal(checks)
	if err != nil {
		logger.Warn("marshal egress checks", "error", err)
		checksJSON = []byte("[]")
	}

	var prevStatus string
	var prevFailures int
	err = pool.QueryRow(ctx, `
		SELECT status, consecutive_failures
		FROM delivery_egress_health
		WHERE id = 1::smallint
	`).Scan(&prevStatus, &prevFailures)
	if err != nil {
		logger.Warn("read previous egress health", "error", err)
		prevStatus = "unknown"
		prevFailures = 0
	}

	status := prevStatus
	consecutiveFailures := prevFailures
	detail := "outbound SMTP port 25 reachable from Stalwart network path"
	switch {
	case !cycleFailed:
		status = "ok"
		consecutiveFailures = 0
		detail = "outbound SMTP port 25 reachable from Stalwart network path"
	case prevStatus == "blocked":
		status = "blocked"
		consecutiveFailures = prevFailures + 1
		detail = "outbound SMTP port 25 unreachable on all configured public MX hosts"
	default:
		consecutiveFailures = prevFailures + 1
		if consecutiveFailures >= 2 {
			status = "blocked"
			detail = "outbound SMTP port 25 unreachable on all configured public MX hosts"
		} else {
			status = "unknown"
			detail = "outbound SMTP port 25 check failed; waiting for hysteresis confirmation"
		}
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO delivery_egress_health
			(id, status, checked_at, last_ok_at, last_blocked_at, detail, checks, updated_at, consecutive_failures)
		VALUES (1::smallint, $1::text, $2::timestamptz,
		        CASE WHEN $1::text = 'ok' THEN $2::timestamptz ELSE NULL::timestamptz END,
		        CASE WHEN $1::text = 'blocked' THEN $2::timestamptz ELSE NULL::timestamptz END,
		        $3::text, $4::jsonb, $2::timestamptz, $5::integer)
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			checked_at = EXCLUDED.checked_at,
			last_ok_at = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.checked_at ELSE delivery_egress_health.last_ok_at END,
			last_blocked_at = CASE WHEN EXCLUDED.status = 'blocked' THEN EXCLUDED.checked_at ELSE delivery_egress_health.last_blocked_at END,
			detail = EXCLUDED.detail,
			checks = EXCLUDED.checks,
			updated_at = EXCLUDED.updated_at,
			consecutive_failures = EXCLUDED.consecutive_failures
	`, status, now, detail, string(checksJSON), consecutiveFailures)
	if err != nil {
		logger.Warn("persist delivery egress health", "error", err)
		return
	}

	logger.Info("delivery egress probe check",
		"status", status,
		"cycle_failed", cycleFailed,
		"consecutive_failures", consecutiveFailures,
		"detail", detail,
	)
}

func truncateErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 240 {
		return msg[:240]
	}
	return msg
}
