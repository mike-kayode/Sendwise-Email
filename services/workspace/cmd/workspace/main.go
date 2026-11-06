package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/outbox"
	"github.com/khayode/email-platform/internal/session"
	"github.com/khayode/email-platform/services/workspace"
	"github.com/nats-io/nats.go/jetstream"
)

func main() {
	cfg, err := workspace.LoadConfig()
	logger := kit.NewLogger(cfg.Env, "workspace")
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.AutoMigrate {
		if err := kit.Migrate(ctx, cfg.DatabaseURL, cfg.MigrationsDir); err != nil {
			logger.Error("run migrations", "error", err)
			os.Exit(1)
		}
	}

	store, err := workspace.NewStore(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect workspace database", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	pooled, ok := store.(interface{ Pool() *pgxpool.Pool })
	if !ok {
		logger.Error("store does not expose a database pool")
		os.Exit(1)
	}

	bus, err := kit.NATSConnect(cfg.NATSURL, "workspace-service")
	if err != nil {
		logger.Warn("nats unavailable; workspace events will remain in the outbox and identity events will not be consumed", "error", err)
	} else {
		defer bus.Close()
		if err := bus.EnsureBusinessEvents(ctx); err != nil {
			logger.Error("ensure business events stream", "error", err)
		} else {
			// Outbox relay for workspace-owned events.
			go outbox.NewStore(pooled.Pool()).Run(ctx, bus, func(err error) {
				logger.Error("dispatch transactional outbox", "error", err)
			})

			// Provision organizations for newly registered users.
			_, consumeErr := bus.Consume(ctx, "BUSINESS_EVENTS", "workspace-service", "identity.user_registered.v1",
				func(consumeCtx context.Context, msg jetstream.Msg) {
					if err := workspace.ProvisionFromEvent(consumeCtx, store, msg.Data(), logger); err != nil {
						logger.Error("provision organization", "error", err, "subject", msg.Subject())
						msg.Nak()
						return
					}
					msg.Ack()
				})
			if consumeErr != nil {
				logger.Error("start identity.user_registered consumer", "error", consumeErr)
			}
		}
	}

	tokens := kit.NewManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL).
		WithSessionStore(session.NewHTTPStore(cfg.IdentityURL, cfg.ServiceToken))
	handler := workspace.NewHandler(store, tokens, cfg, logger)

	logger.Info("workspace service starting", "address", cfg.HTTPAddr)
	server := kit.NewServer(cfg.HTTPAddr, handler.Routes(), logger)
	if err := server.Start(); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
