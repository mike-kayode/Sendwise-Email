package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/khayode/email-platform/internal/backfill"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/internal/observability"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("backfill", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "inspect email clashes and planned legacy user-reference updates without writing")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}

	identityURL := strings.TrimSpace(os.Getenv("IDENTITY_DATABASE_URL"))
	workspaceURL := strings.TrimSpace(os.Getenv("WORKSPACE_DATABASE_URL"))
	legacyURL := strings.TrimSpace(os.Getenv("LEGACY_DATABASE_URL"))
	if identityURL == "" || workspaceURL == "" || legacyURL == "" {
		return errors.New("set IDENTITY_DATABASE_URL, WORKSPACE_DATABASE_URL, and LEGACY_DATABASE_URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	logger := observability.NewLogger("development", "backfill")
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

	if *dryRun {
		return backfill.DryRun(ctx, identity, workspace, legacy, os.Stdout)
	}

	summary, err := backfill.Run(ctx, identity, workspace, legacy, logger)
	if err != nil {
		return err
	}
	fmt.Printf("Backfill complete: users=%d organizations=%d memberships=%d skipped_users=%d skipped_memberships=%d\n",
		summary.Users, summary.Organizations, summary.Memberships,
		summary.SkippedUsers, summary.SkippedMemberships)
	return nil
}
