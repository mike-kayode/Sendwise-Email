package kit

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect establishes a PostgreSQL connection pool with sensible defaults and a
// startup health check. The caller is responsible for calling pool.Close().
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}

	// Verify connectivity before returning so startup fails fast on misconfiguration.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// PoolStats returns a snapshot of pool health for observability endpoints.
func PoolStats(pool *pgxpool.Pool) map[string]any {
	s := pool.Stat()
	return map[string]any{
		"max_connections":      s.MaxConns,
		"open_connections":     s.TotalConns(),
		"idle_connections":     s.IdleConns(),
		"acquired_connections": s.AcquiredConns(),
	}
}

// WithDefaultQueryTimeout wraps a context with a bounded statement timeout.
func WithDefaultQueryTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

// MustPing verifies the database is reachable, returning an error otherwise.
func MustPing(ctx context.Context, pool *pgxpool.Pool) error {
	cancelCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(cancelCtx); err != nil {
		return fmt.Errorf("database ping: %w", err)
	}
	return nil
}
