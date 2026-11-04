package outbox

import (
	"context"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/testutil"
	"github.com/khayode/email-platform/shared/contracts/events"
)

func TestOutboxClaimPublishAndInboxIdempotency(t *testing.T) {
	pool := testutil.NewPostgres(t)
	ctx := context.Background()
	store := NewStore(pool)
	envelope := events.Envelope[events.UserRegistered]{
		ID:            "11111111-1111-4111-8111-111111111111",
		Type:          "identity.user_registered",
		Version:       1,
		OccurredAt:    time.Now().UTC(),
		Producer:      "identity",
		Aggregate:     events.Aggregate{Type: "user", ID: "22222222-2222-4222-8222-222222222222", Version: 1},
		CorrelationID: "33333333-3333-4333-8333-333333333333",
		Data: events.UserRegistered{
			UserID:           "22222222-2222-4222-8222-222222222222",
			Email:            "ada@example.com",
			Name:             "Ada",
			OrganizationName: "Ada's workspace",
		},
	}

	rolledBack, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rollback transaction: %v", err)
	}
	if err := Enqueue(ctx, rolledBack, "identity.user_registered.v1", envelope); err != nil {
		t.Fatalf("enqueue in rollback transaction: %v", err)
	}
	if err := rolledBack.Rollback(ctx); err != nil {
		t.Fatalf("rollback outbox transaction: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM event_outbox WHERE event_id = $1`, envelope.ID).Scan(&count); err != nil {
		t.Fatalf("count rolled back outbox event: %v", err)
	}
	if count != 0 {
		t.Fatalf("rolled-back transaction left %d outbox events", count)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin outbox transaction: %v", err)
	}
	if err := Enqueue(ctx, tx, "identity.user_registered.v1", envelope); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("enqueue outbox event: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit outbox event: %v", err)
	}

	claimed, err := store.Claim(ctx, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim outbox event: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != envelope.ID {
		t.Fatalf("claimed events = %+v, want only %s", claimed, envelope.ID)
	}
	claimedAgain, err := store.Claim(ctx, 10, time.Minute)
	if err != nil {
		t.Fatalf("claim leased event again: %v", err)
	}
	if len(claimedAgain) != 0 {
		t.Fatalf("claimed leased event twice: %+v", claimedAgain)
	}
	if err := store.MarkPublished(ctx, claimed[0]); err != nil {
		t.Fatalf("mark published: %v", err)
	}

	inboxTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin inbox transaction: %v", err)
	}
	first, err := MarkProcessed(ctx, inboxTx, "test-consumer", envelope.ID)
	if err != nil {
		_ = inboxTx.Rollback(ctx)
		t.Fatalf("record inbox event: %v", err)
	}
	duplicate, err := MarkProcessed(ctx, inboxTx, "test-consumer", envelope.ID)
	if err != nil {
		_ = inboxTx.Rollback(ctx)
		t.Fatalf("record duplicate inbox event: %v", err)
	}
	if !first || duplicate {
		_ = inboxTx.Rollback(ctx)
		t.Fatalf("inbox results first=%v duplicate=%v, want true/false", first, duplicate)
	}
	if err := inboxTx.Commit(ctx); err != nil {
		t.Fatalf("commit inbox transaction: %v", err)
	}
}
