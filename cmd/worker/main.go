package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/campaigns"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/messaging"
	"github.com/khayode/email-platform/internal/observability"
	"github.com/khayode/email-platform/internal/projections"
	"github.com/khayode/email-platform/internal/worker"
)

func main() {
	cfg, err := config.Load()
	logger := observability.NewLogger(cfg.Env, "worker")
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

	bus, err := messaging.Connect(cfg.NATSURL, "email-platform-worker")
	if err != nil {
		logger.Error("connect nats", "error", err)
		os.Exit(1)
	}
	defer bus.Close()

	metricsServer := &http.Server{
		Addr:              cfg.WorkerMetricsAddr,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", observability.MetricsHandler())
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		})
		metricsServer.Handler = mux
		logger.Info("worker metrics listening", "address", cfg.WorkerMetricsAddr)
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server", "error", err)
			stop()
		}
	}()

	tokens := auth.NewManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL)
	var transport delivery.Provider = delivery.NewSMTPTransport(
		cfg.SMTPAddr,
		cfg.SMTPHost,
		cfg.SMTPUsername,
		cfg.SMTPPassword,
		cfg.Env == "development" || cfg.Env == "test",
	)
	transport = delivery.NewCampaignRouter(pool, cfg.DKIMEncryptionKey, cfg.ResendRequestsPerSecond, transport, logger)
	campaignService := campaigns.NewService(pool, bus, logger, cfg.MaxDeliveryAttempts, cfg.RequireVerifiedDomain)

	// Mirror identity/workspace events into the legacy schema so foreign keys
	// keep resolving while the monolith and the new services coexist.
	projector := projections.New(projections.NewStore(pool), logger)
	if err := projector.StartConsumers(ctx, bus); err != nil {
		logger.Error("start projection consumers; identity/workspace events will not reach the legacy schema", "error", err)
	}

	deliveryWorker := worker.New(cfg, pool, bus, transport, tokens, campaignService, logger)

	runErr := deliveryWorker.Run(ctx)
	if runErr != nil {
		logger.Error("worker stopped", "error", runErr)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown metrics server", "error", err)
	}

	if runErr != nil {
		os.Exit(1)
	}
}
