package main

import (
	"context"
	"os"

	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
	"github.com/khayode/email-platform/internal/observability"
)

func main() {
	logger := observability.NewLogger("development", "migrate")

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	if err := db.Migrate(context.Background(), cfg.DatabaseURL, cfg.MigrationsDir); err != nil {
		logger.Error("run migrations", "error", err)
		os.Exit(1)
	}

	logger.Info("migrations applied", "directory", cfg.MigrationsDir)
}
