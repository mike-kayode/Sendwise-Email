package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
	"github.com/khayode/email-platform/internal/egressprobe"
	"github.com/khayode/email-platform/internal/observability"
)

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

	checker := egressprobe.NewChecker(pool, cfg, logger)
	checker.RunCheck(ctx)
	ticker := time.NewTicker(cfg.DeliveryEgressCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("delivery egress probe stopped")
			return
		case <-ticker.C:
			checker.RunCheck(ctx)
		}
	}
}
