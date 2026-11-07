package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/outbox"
	"github.com/khayode/email-platform/services/identity"
)

func main() {
	cfg, err := identity.LoadConfig()
	logger := kit.NewLogger(cfg.Env, "identity")
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

	if cfg.AutoMigrate {
		if err := kit.Migrate(ctx, cfg.DatabaseURL, cfg.MigrationsDir); err != nil {
			logger.Error("run migrations", "error", err)
			os.Exit(1)
		}
	}

	store, err := identity.NewStore(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect identity database", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// Transactional outbox relay: publish business events to JetStream.
	var publisher outbox.Publisher
	bus, err := kit.NATSConnect(cfg.NATSURL, "identity-service")
	if err != nil {
		logger.Warn("nats unavailable; registered events will remain in the outbox", "error", err)
	} else if err := bus.EnsureBusinessEvents(ctx); err != nil {
		logger.Error("ensure business events stream; events will remain in the outbox", "error", err)
		bus.Close()
	} else {
		publisher = bus
		defer bus.Close()
		pooled, ok := store.(interface{ Pool() *pgxpool.Pool })
		if !ok {
			logger.Error("store does not expose a database pool for the outbox relay")
			os.Exit(1)
		}
		go outbox.NewStore(pooled.Pool()).Run(ctx, publisher, func(err error) {
			logger.Error("dispatch transactional outbox", "error", err)
		})
	}

	tokens := kit.NewManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL)
	workspace := identity.NewHTTPWorkspaceClient(cfg.WorkspaceURL, cfg.ServiceToken)
	mailer := identity.NewMailer(cfg, logger)
	handler := identity.NewHandler(store, tokens, workspace, mailer, cfg, logger)
	go handler.RunMailRetries(ctx)

	logger.Info("identity service starting",
		"address", cfg.HTTPAddr,
		"mail_provider", mailer.Provider(),
		"workspace_url", cfg.WorkspaceURL,
	)
	server := kit.NewServer(cfg.HTTPAddr, handler.Routes(), logger)
	if err := server.Start(); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
