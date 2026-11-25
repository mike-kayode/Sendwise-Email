package egressprobe

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/config"
)

type HostCheck struct {
	Host      string `json:"host"`
	OK        bool   `json:"ok"`
	Detail    string `json:"detail,omitempty"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
}

type Checker struct {
	pool       *pgxpool.Pool
	cfg        config.Config
	logger     *slog.Logger
	freshStart bool
	dial       func(network, address string, timeout time.Duration) (net.Conn, error)
}

func NewChecker(pool *pgxpool.Pool, cfg config.Config, logger *slog.Logger) *Checker {
	return &Checker{pool: pool, cfg: cfg, logger: logger, freshStart: true, dial: net.DialTimeout}
}

func (c *Checker) SetDialFn(fn func(network, address string, timeout time.Duration) (net.Conn, error)) {
	if fn != nil {
		c.dial = fn
	}
}

// RunCheck probes configured MX hosts and persists egress health. It returns
// true when the cycle failed and the caller should exit non-zero so Docker
// restarts the probe into Stalwart's current network namespace.
func (c *Checker) RunCheck(ctx context.Context) bool {
	hosts := c.cfg.DeliveryEgressCheckHosts
	if len(hosts) == 0 {
		hosts = []string{"gmail-smtp-in.l.google.com:25", "outlook-com.olc.protection.outlook.com:25", "mta5.am0.yahoodns.net:25"}
	}
	timeout := c.cfg.DeliveryEgressCheckTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	checks := make([]HostCheck, 0, len(hosts))
	anyOK := false
	allFailed := true
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		start := time.Now()
		conn, err := c.dial("tcp", host, timeout)
		latency := time.Since(start).Milliseconds()
		check := HostCheck{Host: host, LatencyMS: latency}
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
		checks = append(checks, HostCheck{Host: "no-hosts-configured", OK: false, Detail: "no egress check hosts configured"})
	}

	cycleFailed := allFailed && !anyOK
	now := time.Now().UTC()
	checksJSON, err := json.Marshal(checks)
	if err != nil {
		c.logger.Warn("marshal egress checks", "error", err)
		checksJSON = []byte("[]")
	}

	var prevStatus string
	var prevFailures int
	var prevUnconfirmed bool
	err = c.pool.QueryRow(ctx, `
		SELECT status, consecutive_failures, COALESCE(unconfirmed_failure, false)
		FROM delivery_egress_health
		WHERE id = 1::smallint
	`).Scan(&prevStatus, &prevFailures, &prevUnconfirmed)
	if err != nil {
		c.logger.Warn("read previous egress health", "error", err)
		prevStatus = "unknown"
		prevFailures = 0
		prevUnconfirmed = false
	}

	status := prevStatus
	consecutiveFailures := prevFailures
	unconfirmedFailure := prevUnconfirmed
	detail := "outbound SMTP port 25 reachable from Stalwart network path"
	switch {
	case !cycleFailed:
		status = "ok"
		consecutiveFailures = 0
		unconfirmedFailure = false
		detail = "outbound SMTP port 25 reachable from Stalwart network path"
	case c.freshStart && !prevUnconfirmed:
		status = "unknown"
		unconfirmedFailure = true
		detail = "outbound SMTP port 25 check failed; probe restarting to confirm Stalwart network namespace"
	default:
		consecutiveFailures = prevFailures + 1
		unconfirmedFailure = true
		if consecutiveFailures >= 2 {
			status = "blocked"
			detail = "outbound SMTP port 25 unreachable on all configured public MX hosts"
		} else {
			if prevStatus == "blocked" {
				status = "blocked"
			} else {
				status = "unknown"
			}
			detail = "outbound SMTP port 25 check failed; waiting for hysteresis confirmation"
		}
	}

	_, err = c.pool.Exec(ctx, `
		INSERT INTO delivery_egress_health
			(id, status, checked_at, last_ok_at, last_blocked_at, detail, checks, updated_at, consecutive_failures, unconfirmed_failure)
		VALUES (1::smallint, $1::text, $2::timestamptz,
		        CASE WHEN $1::text = 'ok' THEN $2::timestamptz ELSE NULL::timestamptz END,
		        CASE WHEN $1::text = 'blocked' THEN $2::timestamptz ELSE NULL::timestamptz END,
		        $3::text, $4::jsonb, $2::timestamptz, $5::integer, $6::boolean)
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status,
			checked_at = EXCLUDED.checked_at,
			last_ok_at = CASE WHEN EXCLUDED.status = 'ok' THEN EXCLUDED.checked_at ELSE delivery_egress_health.last_ok_at END,
			last_blocked_at = CASE WHEN EXCLUDED.status = 'blocked' THEN EXCLUDED.checked_at ELSE delivery_egress_health.last_blocked_at END,
			detail = EXCLUDED.detail,
			checks = EXCLUDED.checks,
			updated_at = EXCLUDED.updated_at,
			consecutive_failures = EXCLUDED.consecutive_failures,
			unconfirmed_failure = EXCLUDED.unconfirmed_failure
	`, status, now, detail, string(checksJSON), consecutiveFailures, unconfirmedFailure)
	if err != nil {
		c.logger.Warn("persist delivery egress health", "error", err)
		return cycleFailed
	}

	c.logger.Info("delivery egress probe check",
		"status", status,
		"cycle_failed", cycleFailed,
		"consecutive_failures", consecutiveFailures,
		"unconfirmed_failure", unconfirmedFailure,
		"fresh_start", c.freshStart,
		"detail", detail,
	)
	if !cycleFailed {
		c.freshStart = false
	}
	return cycleFailed
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
