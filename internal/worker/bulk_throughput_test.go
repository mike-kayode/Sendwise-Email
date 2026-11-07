package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/campaigns"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/observability"
)

const bulkRecipientCount = 100000

type localQueuePublisher struct {
	jobs chan campaigns.JobPayload
}

func (p localQueuePublisher) Publish(_ context.Context, _ string, data []byte) error {
	var job campaigns.JobPayload
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}
	p.jobs <- job
	return nil
}

func TestBulkCampaignSMTPThroughput100k(t *testing.T) {
	if os.Getenv("RUN_BULK_THROUGHPUT") != "1" {
		t.Skip("set RUN_BULK_THROUGHPUT=1 to run the 100,000-recipient sink proof")
	}
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for the 100,000-recipient proof")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, databaseURL, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	var organizationID, campaignID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('100k SMTP sink proof', 'bulk-sink-' || gen_random_uuid()::text)
		RETURNING id
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create test organization: %v", err)
	}
	defer func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM organizations WHERE id = $1`, organizationID); err != nil {
			t.Errorf("clean up throughput fixture: %v", err)
		}
	}()
	if _, err := pool.Exec(ctx, `
		INSERT INTO contacts (organization_id, email, status)
		SELECT $1, 'bulk-' || n::text || '@sink.example', 'subscribed'
		FROM generate_series(1, $2) AS n
	`, organizationID, bulkRecipientCount); err != nil {
		t.Fatalf("seed throughput contacts: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (
			organization_id, name, subject, sender_name, sender_email,
			sender_postal_address, content, status
		)
		VALUES (
			$1, '100k SMTP sink campaign', 'Sink throughput proof',
			'Sendwise', 'hello@bulk.example', '1 Example Street, Example City',
			'{"version":1,"blocks":[{"type":"paragraph","content":"Throughput proof"}]}'::jsonb,
			'processing'
		)
		RETURNING id
	`, organizationID).Scan(&campaignID); err != nil {
		t.Fatalf("create throughput campaign: %v", err)
	}

	closeSink, sinkAddress, accepted, err := startDiscardSMTPServer()
	if err != nil {
		t.Fatalf("start SMTP sink: %v", err)
	}
	defer closeSink()
	queue := make(chan campaigns.JobPayload, bulkRecipientCount)
	publisher := localQueuePublisher{jobs: queue}
	logger := observability.NewLogger("test", "bulk-throughput")
	campaignService := campaigns.NewService(pool, publisher, logger, 2, false)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	queued, err := campaignService.Queue(ctx, campaignID, organizationID)
	if err != nil {
		t.Fatalf("queue bulk campaign: %v", err)
	}
	if queued != bulkRecipientCount {
		t.Fatalf("queued jobs = %d, want %d", queued, bulkRecipientCount)
	}
	maxQueueDepth := atomic.Int64{}
	maxQueueDepth.Store(int64(len(queue)))
	var queuedHeap runtime.MemStats
	runtime.ReadMemStats(&queuedHeap)

	cfg := config.Config{
		APIBaseURL:             "http://localhost:8080",
		TrackingBaseURL:        "http://localhost:8080",
		JWTSecret:              strings.Repeat("x", 32),
		JWTIssuer:              "bulk-throughput-test",
		AccessTokenTTL:         15 * time.Minute,
		SendingRate:            100000,
		MaxDeliveryAttempts:    2,
		MaxMessageAge:          48 * time.Hour,
		MaxDailyEmails:         bulkRecipientCount,
		WarmupStartPerDay:      bulkRecipientCount,
		WarmupGrowthFactor:     1.5,
		RecipientDomainDefault: 100000,
		DomainConcurrencyLimit: 100,
		SMTPAddr:               sinkAddress,
		SMTPHost:               "localhost",
		DKIMEncryptionKey:      "bulk-throughput-test-encryption-key",
	}
	worker := New(cfg, pool, nil, delivery.NewSMTPTransport(cfg.SMTPAddr, cfg.SMTPHost, "", "", true),
		auth.NewManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL), campaignService, logger)

	const parallelWorkers = 32
	var completed, failures atomic.Int64
	var peakHeap atomic.Uint64
	peakHeap.Store(queuedHeap.HeapAlloc)
	stopSampling := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				for old := peakHeap.Load(); stats.HeapAlloc > old && !peakHeap.CompareAndSwap(old, stats.HeapAlloc); old = peakHeap.Load() {
				}
				depth := int64(len(queue))
				for old := maxQueueDepth.Load(); depth > old && !maxQueueDepth.CompareAndSwap(old, depth); old = maxQueueDepth.Load() {
				}
			}
		}
	}()

	var workers sync.WaitGroup
	for i := 0; i < parallelWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range queue {
				retryAfter, processErr := worker.process(ctx, job)
				if processErr != nil || retryAfter > 0 {
					failures.Add(1)
				}
				completed.Add(1)
			}
		}()
	}
	close(queue)
	workers.Wait()
	close(stopSampling)
	sampler.Wait()
	elapsed := time.Since(started)

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if got := accepted.Load(); got != bulkRecipientCount {
		t.Fatalf("SMTP sink accepted %d messages, want %d (processed=%d failures=%d)", got, bulkRecipientCount, completed.Load(), failures.Load())
	}
	if got := failures.Load(); got != 0 {
		t.Fatalf("delivery failures = %d, want 0", got)
	}
	var warmupReserved int
	if err := pool.QueryRow(ctx, `
		SELECT reserved_count
		FROM sending_domain_daily_stats
		WHERE organization_id = $1 AND domain = 'bulk.example' AND sending_day = CURRENT_DATE
	`, organizationID).Scan(&warmupReserved); err != nil {
		t.Fatalf("load warm-up usage: %v", err)
	}
	if warmupReserved != bulkRecipientCount {
		t.Fatalf("warm-up reservations = %d, want %d", warmupReserved, bulkRecipientCount)
	}
	rate := float64(accepted.Load()) / elapsed.Seconds()
	fmt.Printf("bulk-sink: recipients=%d elapsed=%s sends_per_second=%.2f peak_heap_bytes=%d heap_after_bytes=%d queue_depth_peak=%d failures=%d warmup_reserved=%d\n",
		bulkRecipientCount, elapsed.Round(time.Millisecond), rate, peakHeap.Load(), after.HeapAlloc,
		maxQueueDepth.Load(), failures.Load(), warmupReserved)
}

func startDiscardSMTPServer() (func(), string, *atomic.Int64, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", nil, err
	}
	var accepted atomic.Int64
	var connections sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			go func() {
				defer connections.Done()
				if serveDiscardSMTP(conn) == nil {
					accepted.Add(1)
				}
			}()
		}
	}()
	closeServer := func() {
		_ = listener.Close()
		<-done
		connections.Wait()
	}
	return closeServer, listener.Addr().String(), &accepted, nil
}

func serveDiscardSMTP(conn net.Conn) error {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	respond := func(value string) error {
		if _, err := writer.WriteString(value + "\r\n"); err != nil {
			return err
		}
		return writer.Flush()
	}
	if err := respond("220 localhost ESMTP sink"); err != nil {
		return err
	}
	for {
		command, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		switch {
		case strings.HasPrefix(strings.ToUpper(command), "EHLO "):
			if err := respond("250-localhost"); err != nil {
				return err
			}
			if err := respond("250 OK"); err != nil {
				return err
			}
		case strings.HasPrefix(strings.ToUpper(command), "HELO "):
			if err := respond("250 localhost"); err != nil {
				return err
			}
		case strings.HasPrefix(strings.ToUpper(command), "MAIL FROM:"),
			strings.HasPrefix(strings.ToUpper(command), "RCPT TO:"):
			if err := respond("250 2.1.0 accepted"); err != nil {
				return err
			}
		case strings.HasPrefix(strings.ToUpper(command), "DATA"):
			if err := respond("354 end with <CRLF>.<CRLF>"); err != nil {
				return err
			}
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				if line == ".\r\n" {
					break
				}
			}
			if err := respond("250 2.0.0 discarded"); err != nil {
				return err
			}
		case strings.HasPrefix(strings.ToUpper(command), "QUIT"):
			return respond("221 2.0.0 bye")
		default:
			return fmt.Errorf("unexpected SMTP command %q", strings.TrimSpace(command))
		}
	}
}
