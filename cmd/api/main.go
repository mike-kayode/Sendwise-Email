package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/khayode/email-platform/internal/api"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/campaigns"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/messaging"
	"github.com/khayode/email-platform/internal/observability"
	"github.com/khayode/email-platform/internal/outbox"
	"github.com/khayode/email-platform/internal/session"
)

func main() {
	cfg, err := config.Load()
	logger := observability.NewLogger(cfg.Env, "api")
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	redisCtx, redisCancel := context.WithTimeout(ctx, 5*time.Second)
	redisClient, err := delivery.ConfigureSharedResendRedis(redisCtx, cfg.RedisURL)
	redisCancel()
	if err != nil {
		logger.Error("connect shared Resend limiter", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if cfg.AutoMigrate {
		if err := db.Migrate(ctx, cfg.DatabaseURL, cfg.MigrationsDir); err != nil {
			logger.Error("run migrations", "error", err)
			os.Exit(1)
		}
	}

	var publisher campaigns.Publisher
	bus, err := messaging.Connect(cfg.NATSURL, "email-platform-api")
	if err != nil {
		logger.Warn("nats unavailable; campaigns will remain queued until a worker republishes them", "error", err)
	} else {
		if err := bus.EnsureEventStream(ctx); err != nil {
			logger.Error("ensure business events stream; events will remain in the outbox", "error", err)
			bus.Close()
			bus = nil
		} else {
			publisher = bus
			defer bus.Close()
			go outbox.NewStore(pool).Run(ctx, bus, func(err error) {
				logger.Error("dispatch transactional outbox", "error", err)
			})
		}
	}
	tokens := auth.NewManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL)
	if cfg.IdentityURL != "" {
		tokens = tokens.WithSessionStore(session.NewHTTPStore(cfg.IdentityURL, cfg.ServiceToken))
	}

	campaignService := campaigns.NewService(pool, publisher, logger, cfg.MaxDeliveryAttempts, cfg.RequireVerifiedDomain)
	apiKeyService := auth.NewAPIKeyService(pool, logger)
	server := api.New(cfg, pool, tokens, campaignService, apiKeyService, bus, logger)

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("api listening", "address", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down api")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		// manager that is configured to consult the session revocation store on each
		// request. This is what makes logout immediately invalidate issued access
		// tokens.
		logger.Error("graceful shutdown", "error", err)
	}
}
