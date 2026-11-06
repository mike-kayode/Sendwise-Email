package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/backfill"
	"github.com/khayode/email-platform/internal/kit"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	identityURL := strings.TrimSpace(os.Getenv("IDENTITY_DATABASE_URL"))
	workspaceURL := strings.TrimSpace(os.Getenv("WORKSPACE_DATABASE_URL"))
	legacyURL := strings.TrimSpace(os.Getenv("LEGACY_DATABASE_URL"))
	if identityURL == "" || workspaceURL == "" || legacyURL == "" {
		return errors.New("set IDENTITY_DATABASE_URL, WORKSPACE_DATABASE_URL, and LEGACY_DATABASE_URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	identity, err := kit.Connect(ctx, identityURL)
	if err != nil {
		return fmt.Errorf("connect identity database: %w", err)
	}
	defer identity.Close()
	workspace, err := kit.Connect(ctx, workspaceURL)
	if err != nil {
		return fmt.Errorf("connect workspace database: %w", err)
	}
	defer workspace.Close()
	legacy, err := kit.Connect(ctx, legacyURL)
	if err != nil {
		return fmt.Errorf("connect legacy database: %w", err)
	}
	defer legacy.Close()

	summary, err := backfill.Run(ctx, identity, workspace, legacy)
	if err != nil {
		return err
	}
	fmt.Printf("Backfill complete: users=%d organizations=%d memberships=%d\n",
		summary.Users, summary.Organizations, summary.Memberships)
	return nil
}
