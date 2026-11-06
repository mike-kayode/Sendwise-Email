package testutil

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/db"
)

// NewPostgres creates an isolated schema in TEST_DATABASE_URL, runs all real
// migrations there, and drops only that schema when the test ends.
func NewPostgres(t *testing.T) *pgxpool.Pool {
	return NewPostgresWithMigrations(t, "migrations")
}

// NewPostgresWithMigrations creates an isolated schema and applies the named
// repository migration directory.
func NewPostgresWithMigrations(t *testing.T, migrationPath string) *pgxpool.Pool {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	baseConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	admin, err := pgx.ConnectConfig(ctx, baseConfig)
	if err != nil {
		t.Fatalf("connect TEST_DATABASE_URL: %v", err)
	}

	schema := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create test schema: %v", err)
	}

	testURL, err := withSearchPath(databaseURL, schema+",public")
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = admin.Close(ctx)
		t.Fatalf("configure test schema: %v", err)
	}
	migrationsDir := migrationPath
	if !filepath.IsAbs(migrationsDir) {
		migrationsDir = filepath.Join(filepath.Dir(migrationsDirectory(t)), migrationPath)
	}
	if err := db.Migrate(ctx, testURL, migrationsDir); err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = admin.Close(ctx)
		t.Fatalf("apply migrations in test schema: %v", err)
	}

	poolConfig, err := pgxpool.ParseConfig(testURL)
	if err != nil {
		t.Fatalf("parse test pool config: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("connect test pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		if err := admin.Close(cleanupCtx); err != nil {
			t.Errorf("close test database connection: %v", err)
		}
	})
	return pool
}

func withSearchPath(databaseURL, searchPath string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	query := parsed.Query()
	query.Set("search_path", searchPath)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func migrationsDirectory(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		candidate := filepath.Join(current, "migrations")
		if _, err := os.Stat(filepath.Join(candidate, "0001_init.sql")); err == nil {
			return candidate
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("could not find repository migrations directory")
		}
		current = parent
	}
}
