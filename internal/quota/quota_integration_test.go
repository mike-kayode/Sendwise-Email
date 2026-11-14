package quota

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/testutil"
)

func TestConcurrentReservationsNeverExceedDomainLimit(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	if err := SeedPoolDefaults(ctx, pool, 1000, 100, 1000, 10000, 100, 100); err != nil {
		t.Fatalf("seed platform quota defaults: %v", err)
	}
	domainID := createQuotaTestDomain(t, pool, "quota-concurrency")
	if _, err := pool.Exec(ctx, `
		INSERT INTO domain_quotas (domain_id, stream, daily_limit, monthly_limit, per_second_limit)
		VALUES ($1, 'marketing', 10, 20, 100)
	`, domainID); err != nil {
		t.Fatalf("set domain quota: %v", err)
	}

	const attempts = 64
	var wg sync.WaitGroup
	results := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sourceID := uuid.NewString()
			tx, err := pool.Begin(ctx)
			if err != nil {
				results <- err
				return
			}
			_, err = Reserve(ctx, tx, domainID, StreamMarketing, SourceCampaignDelivery, sourceID, 1, 100)
			if err != nil {
				_ = tx.Rollback(ctx)
				results <- err
				return
			}
			results <- tx.Commit(ctx)
		}()
	}
	wg.Wait()
	close(results)

	accepted, exceeded := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		default:
			var limitErr *ExceededError
			if errors.As(err, &limitErr) {
				exceeded++
				continue
			}
			t.Fatalf("unexpected reservation error: %v", err)
		}
	}
	if accepted != 10 || exceeded != attempts-10 {
		t.Fatalf("accepted=%d exceeded=%d; want 10 and %d", accepted, exceeded, attempts-10)
	}

	var used int64
	if err := pool.QueryRow(ctx, `
		SELECT sent_count FROM domain_usage
		WHERE domain_id = $1 AND stream = 'marketing' AND period = 'day'
		  AND period_start = (now() AT TIME ZONE 'UTC')::date
	`, domainID).Scan(&used); err != nil {
		t.Fatalf("load final daily usage: %v", err)
	}
	if used != 10 {
		t.Fatalf("daily usage=%d; want exactly 10", used)
	}
}

func TestQuotaInheritanceUnlimitedAndUTCWindowRollover(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	if err := SeedPoolDefaults(ctx, pool, 100, 50, 5, 20, 2, 10); err != nil {
		t.Fatalf("seed platform quota defaults: %v", err)
	}
	domainID := createQuotaTestDomain(t, pool, "quota-inheritance")

	snapshot, err := GetSnapshot(ctx, pool, domainID, StreamTransactional, 10)
	if err != nil {
		t.Fatalf("load inherited quota snapshot: %v", err)
	}
	if snapshot.DailyLimit == nil || *snapshot.DailyLimit != 5 ||
		snapshot.MonthlyLimit == nil || *snapshot.MonthlyLimit != 20 ||
		snapshot.PerSecondLimit != 2 {
		t.Fatalf("snapshot did not inherit platform defaults: %+v", snapshot)
	}

	now := time.Now().UTC()
	yesterday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	lastMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	if _, err := pool.Exec(ctx, `
		INSERT INTO domain_usage (domain_id, stream, period, period_start, sent_count)
		VALUES ($1, 'transactional', 'day', $2, 99),
		       ($1, 'transactional', 'month', $3, 99)
	`, domainID, yesterday, lastMonth); err != nil {
		t.Fatalf("insert expired quota buckets: %v", err)
	}
	snapshot, err = GetSnapshot(ctx, pool, domainID, StreamTransactional, 10)
	if err != nil {
		t.Fatalf("load quota snapshot after window rollover: %v", err)
	}
	if snapshot.DailyUsed != 0 || snapshot.MonthlyUsed != 0 {
		t.Fatalf("expired quota usage crossed UTC window boundary: daily=%d monthly=%d", snapshot.DailyUsed, snapshot.MonthlyUsed)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO domain_quotas
			(domain_id, stream, unlimited, per_second_limit)
		VALUES ($1, 'transactional', true, 2)
	`, domainID); err != nil {
		t.Fatalf("enable unlimited domain quota: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin unlimited reservations: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := Reserve(ctx, tx, domainID, StreamTransactional, SourceTransactionalRecipient, uuid.NewString(), 1, 10); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("unlimited reservation %d: %v", i, err)
		}
	}
	_, err = Reserve(ctx, tx, domainID, StreamTransactional, SourceTransactionalRecipient, uuid.NewString(), 1, 10)
	var limitErr *ExceededError
	if !errors.As(err, &limitErr) || limitErr.Window != WindowPerSecond {
		_ = tx.Rollback(ctx)
		t.Fatalf("unlimited reservation error = %v; want per-second hard limit", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit accepted unlimited reservations: %v", err)
	}
}

func createQuotaTestDomain(t *testing.T, pool *pgxpool.Pool, slug string) string {
	t.Helper()
	ctx := context.Background()
	var organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ($1, $2)
		RETURNING id::text
	`, slug, fmt.Sprintf("%s-%s", slug, uuid.NewString())).Scan(&organizationID); err != nil {
		t.Fatalf("create quota test organization: %v", err)
	}
	var domainID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO sending_domains (organization_id, domain, status)
		VALUES ($1, $2, 'verified')
		RETURNING id::text
	`, organizationID, slug+".example.test").Scan(&domainID); err != nil {
		t.Fatalf("create quota test domain: %v", err)
	}
	return domainID
}
