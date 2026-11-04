package main

import (
	"context"
	"errors"
	"os"

	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/services/gateway"
)

func main() {
	cfg, err := gateway.LoadConfig()
	logger := kit.NewLogger(cfg.Env, "gateway")
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	gateway, err := gateway.New(cfg, logger)
	if err != nil {
		logger.Error("build gateway", "error", err)
		os.Exit(1)
	}

	logger.Info("gateway starting",
		"address", cfg.HTTPAddr,
		"identity", cfg.IdentityURL,
		"workspace", cfg.WorkspaceURL,
		"legacy_api", cfg.LegacyAPIURL,
	)
	server := kit.NewServer(cfg.HTTPAddr, gateway, logger)
	if err := server.Start(); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}
