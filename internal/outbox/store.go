package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/shared/contracts/events"
)

const (
	maxBatchSize = 25
	maxErrorSize = 1000
)

type Store struct {
	pool *pgxpool.Pool
}

type Message struct {
	ID         string
	Subject    string
	Payload    []byte
	LeaseToken string
	Attempts   int
}

type Publisher interface {
	PublishWithID(ctx context.Context, subject, messageID string, data []byte) error
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func Enqueue[T any](ctx context.Context, tx pgx.Tx, subject string, event events.Envelope[T]) error {
	eventID, payload, err := encode(subject, event)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO event_outbox (event_id, subject, payload)
		VALUES ($1, $2, $3)
	`, eventID, subject, payload); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

func encode[T any](subject string, event events.Envelope[T]) (uuid.UUID, []byte, error) {
	if err := event.Validate(); err != nil {
		return uuid.Nil, nil, fmt.Errorf("validate outbox event: %w", err)
	}
	expectedSubject := fmt.Sprintf("%s.v%d", event.Type, event.Version)
	if strings.TrimSpace(subject) == "" || subject != expectedSubject {
		return uuid.Nil, nil, fmt.Errorf("outbox subject must match event contract %q", expectedSubject)
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("marshal outbox event: %w", err)
	}
	eventID, err := uuid.Parse(event.ID)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("outbox event id must be a UUID: %w", err)
	}
	return eventID, payload, nil
}

func (s *Store) Claim(ctx context.Context, limit int, lease time.Duration) ([]Message, error) {
	if limit < 1 || limit > maxBatchSize {
		return nil, fmt.Errorf("outbox claim limit must be between 1 and %d", maxBatchSize)
	}
	if lease <= 0 {
		return nil, errors.New("outbox lease must be positive")
	}

	leaseToken := uuid.New()
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT event_id
			FROM event_outbox
			WHERE published_at IS NULL
			  AND next_attempt_at <= now()
			  AND (lease_expires_at IS NULL OR lease_expires_at <= now())
			ORDER BY created_at, event_id
			  LIMIT $1
			  FOR UPDATE SKIP LOCKED
		)
		UPDATE event_outbox AS outbox
		SET lease_token = $2,
		    lease_expires_at = now() + make_interval(secs => $3::double precision / 1000),
		    attempts = outbox.attempts + 1
		FROM candidates
		WHERE outbox.event_id = candidates.event_id
		RETURNING outbox.event_id, outbox.subject, outbox.payload, outbox.attempts
	`, limit, leaseToken, lease.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("claim outbox events: %w", err)
	}
	defer rows.Close()

	messages := make([]Message, 0, limit)
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.ID, &message.Subject, &message.Payload, &message.Attempts); err != nil {
			return nil, fmt.Errorf("scan claimed outbox event: %w", err)
		}
		message.LeaseToken = leaseToken.String()
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read claimed outbox events: %w", err)
	}
	return messages, nil
}

func (s *Store) MarkPublished(ctx context.Context, message Message) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE event_outbox
		SET published_at = now(), lease_token = NULL, lease_expires_at = NULL, last_error = NULL
		WHERE event_id = $1 AND lease_token = $2 AND published_at IS NULL
	`, message.ID, message.LeaseToken)
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("outbox event lease was lost before acknowledgement")
	}
	return nil
}

func (s *Store) Retry(ctx context.Context, message Message, publishErr error) error {
	if publishErr == nil {
		return errors.New("outbox retry requires a publish error")
	}
	errorText := publishErr.Error()
	errorText = strings.ToValidUTF8(errorText, "\uFFFD")
	errorText = strings.ReplaceAll(errorText, "\x00", "")
	if len(errorText) > maxErrorSize {
		errorText = errorText[:maxErrorSize]
		for !utf8.ValidString(errorText) {
			errorText = errorText[:len(errorText)-1]
		}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE event_outbox
		SET lease_token = NULL,
		    lease_expires_at = NULL,
		    next_attempt_at = now() + make_interval(secs => $3::double precision),
		    last_error = $4
		WHERE event_id = $1 AND lease_token = $2 AND published_at IS NULL
	`, message.ID, message.LeaseToken, RetryDelay(message.Attempts).Seconds(), errorText)
	if err != nil {
		return fmt.Errorf("reschedule outbox event: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("outbox event lease was lost before retry")
	}
	return nil
}

func (s *Store) DispatchOnce(ctx context.Context, publisher Publisher) (int, error) {
	messages, err := s.Claim(ctx, maxBatchSize, 2*time.Minute)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, message := range messages {
		publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := publisher.PublishWithID(publishCtx, message.Subject, message.ID, message.Payload)
		cancel()
		if err != nil {
			if retryErr := s.Retry(ctx, message, err); retryErr != nil {
				return published, errors.Join(fmt.Errorf("publish outbox event %s: %w", message.ID, err), retryErr)
			}
			continue
		}
		if err := s.MarkPublished(ctx, message); err != nil {
			return published, fmt.Errorf("acknowledge published event %s: %w", message.ID, err)
		}
		published++
	}
	return published, nil
}

func (s *Store) Run(ctx context.Context, publisher Publisher, reportError func(error)) {
	for {
		if ctx.Err() != nil {
			return
		}
		delay := time.Second
		if _, err := s.DispatchOnce(ctx, publisher); err != nil {
			if reportError != nil {
				reportError(err)
			}
			delay = 5 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func MarkProcessed(ctx context.Context, tx pgx.Tx, consumer string, eventID string) (bool, error) {
	if strings.TrimSpace(consumer) == "" {
		return false, errors.New("inbox consumer name is required")
	}
	parsedID, err := uuid.Parse(eventID)
	if err != nil {
		return false, fmt.Errorf("inbox event id must be a UUID: %w", err)
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO event_inbox (consumer_name, event_id, processed_at)
		VALUES ($1, $2, now())
		ON CONFLICT (consumer_name, event_id) DO NOTHING
	`, consumer, parsedID)
	if err != nil {
		return false, fmt.Errorf("record inbox event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func RetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 12 {
		attempt = 12
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}
