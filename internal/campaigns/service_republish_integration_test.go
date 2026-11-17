package campaigns

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/khayode/email-platform/internal/testutil"
)

type republishRecorder struct {
	mu         sync.Mutex
	publishIDs map[string]int
}

func (p *republishRecorder) Publish(context.Context, string, []byte) error {
	return fmt.Errorf("publish without a stable message ID")
}

func (p *republishRecorder) PublishWithID(_ context.Context, _, messageID string, _ []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishIDs[messageID]++
	return nil
}

func (p *republishRecorder) count() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	total, duplicates := 0, 0
	for _, calls := range p.publishIDs {
		total += calls
		if calls > 1 {
			duplicates += calls - 1
		}
	}
	return total, duplicates
}

func TestRepublishPendingClaimsTenThousandJobsAcrossWorkersAndTicks(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	var organizationID, campaignID, pausedCampaignID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ('Deferred resume test', 'deferred-resume-' || gen_random_uuid()::text)
		RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("create resume test workspace: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (organization_id, name, status)
		VALUES ($1, 'Deferred campaign', 'sending')
		RETURNING id::text
	`, organizationID).Scan(&campaignID); err != nil {
		t.Fatalf("create sending campaign: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO campaigns (organization_id, name, status)
		VALUES ($1, 'Paused campaign', 'paused')
		RETURNING id::text
	`, organizationID).Scan(&pausedCampaignID); err != nil {
		t.Fatalf("create paused campaign: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status, next_attempt_at)
		SELECT $1, $2, gen_random_uuid(), 'resume-' || i::text || '@example.test',
		       'deferred', now() - interval '1 minute'
		FROM generate_series(1, 10000) AS i
	`, organizationID, campaignID); err != nil {
		t.Fatalf("create 10000 deferred jobs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status, next_attempt_at)
		SELECT $1, $2, gen_random_uuid(), 'paused-' || i::text || '@example.test',
		       'deferred', now() - interval '1 minute'
		FROM generate_series(1, 5) AS i
	`, organizationID, pausedCampaignID); err != nil {
		t.Fatalf("create paused campaign jobs: %v", err)
	}

	publisher := &republishRecorder{publishIDs: make(map[string]int)}
	firstWorker := NewService(pool, publisher, slog.Default(), 5, false)
	secondWorker := NewService(pool, publisher, slog.Default(), 5, false)
	runTick := func() int {
		t.Helper()
		type result struct {
			count int
			err   error
		}
		results := make(chan result, 2)
		for _, service := range []*Service{firstWorker, secondWorker} {
			go func(service *Service) {
				count, err := service.RepublishPending(ctx, 1000)
				results <- result{count: count, err: err}
			}(service)
		}
		total := 0
		for i := 0; i < 2; i++ {
			res := <-results
			if res.err != nil {
				t.Fatalf("RepublishPending() error = %v", res.err)
			}
			if res.count > 1000 {
				t.Fatalf("one scheduler tick published %d jobs; limit is 1000", res.count)
			}
			total += res.count
		}
		return total
	}

	resumed := runTick()
	if resumed == 0 {
		t.Fatal("concurrent workers resumed no jobs from the due backlog")
	}
	ticks := 1
	for resumed < 10000 && ticks < 12 {
		count, err := firstWorker.RepublishPending(ctx, 1000)
		if err != nil {
			t.Fatalf("resume backlog on tick %d: %v", ticks+1, err)
		}
		if count > 1000 {
			t.Fatalf("tick %d published %d jobs; limit is 1000", ticks+1, count)
		}
		if count == 0 {
			t.Fatalf("backlog stopped after %d ticks with only %d jobs resumed", ticks, resumed)
		}
		resumed += count
		ticks++
	}
	if resumed != 10000 {
		t.Fatalf("resumed %d jobs after %d ticks, want all 10000", resumed, ticks)
	}
	if ticks < 2 {
		t.Fatalf("resumed all 10000 jobs in %d tick; expected batches across ticks", ticks)
	}
	if resumed := runTick(); resumed != 0 {
		t.Fatalf("extra tick resumed %d jobs after backlog drained, want 0", resumed)
	}

	var publishedJobs, pausedJobs int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM delivery_jobs
		WHERE campaign_id = $1 AND status = 'queued'
		  AND nats_publish_claimed_at IS NULL AND nats_published_at IS NOT NULL
	`, campaignID).Scan(&publishedJobs); err != nil {
		t.Fatalf("count resumed campaign jobs: %v", err)
	}
	if publishedJobs != 10000 {
		t.Fatalf("resumed and marked %d sending-campaign jobs; want 10000", publishedJobs)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM delivery_jobs
		WHERE campaign_id = $1 AND status = 'deferred'
	`, pausedCampaignID).Scan(&pausedJobs); err != nil {
		t.Fatalf("count paused campaign jobs: %v", err)
	}
	if pausedJobs != 5 {
		t.Fatalf("paused campaign has %d deferred jobs; want all 5 left untouched", pausedJobs)
	}
	published, duplicates := publisher.count()
	if published != 10000 || duplicates != 0 {
		t.Fatalf("publisher recorded %d publishes and %d duplicates; want 10000 and 0", published, duplicates)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO delivery_jobs (organization_id, campaign_id, contact_id, message_id, status)
		VALUES ($1, $2, gen_random_uuid(), 'startup-recovery@example.test', 'queued')
	`, organizationID, campaignID); err != nil {
		t.Fatalf("create queued startup-recovery job: %v", err)
	}
	if count, err := firstWorker.RepublishPending(ctx, 500); err != nil || count != 1 {
		t.Fatalf("startup RepublishPending() = %d, %v; want one queued recovery", count, err)
	}
	if count, err := secondWorker.RepublishPending(ctx, 500); err != nil || count != 0 {
		t.Fatalf("second RepublishPending() = %d, %v; want no duplicate queued publish", count, err)
	}
	published, duplicates = publisher.count()
	if published != 10001 || duplicates != 0 {
		t.Fatalf("publisher after queued recovery recorded %d publishes and %d duplicates; want 10001 and 0", published, duplicates)
	}
}
