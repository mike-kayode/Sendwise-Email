package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/khayode/email-platform/services/identity"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if os.Getenv("APP_ENV") != "development" {
		return errors.New("development seed refused: APP_ENV must be development")
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	email := strings.ToLower(strings.TrimSpace(os.Getenv("DEV_SEED_EMAIL")))
	password := os.Getenv("DEV_SEED_PASSWORD")
	name := strings.TrimSpace(os.Getenv("DEV_SEED_NAME"))
	organization := strings.TrimSpace(os.Getenv("DEV_SEED_ORGANIZATION"))
	if databaseURL == "" || email == "" || password == "" || name == "" || organization == "" {
		return errors.New("set DATABASE_URL, DEV_SEED_EMAIL, DEV_SEED_PASSWORD, DEV_SEED_NAME, and DEV_SEED_ORGANIZATION")
	}
	if len(password) < 10 || len(password) > 200 {
		return errors.New("DEV_SEED_PASSWORD must be between 10 and 200 characters")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store, err := identity.NewStore(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect identity database: %w", err)
	}
	defer store.Close()

	passwordHash, err := identity.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash development seed password: %w", err)
	}
	user, err := store.CreateUserWithRegistration(ctx, email, passwordHash, name, organization, "development-seed")
	if err != nil {
		return fmt.Errorf("create development seed account: %w", err)
	}
	if err := store.MarkEmailVerified(ctx, user.ID, user.Email, "development-seed"); err != nil {
		return fmt.Errorf("verify development seed account: %w", err)
	}

	fmt.Println("Created verified development seed account; workspace provisioning is queued.")
	return nil
}
