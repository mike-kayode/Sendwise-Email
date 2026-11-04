package kit

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Bus is a thin wrapper around a NATS JetStream connection.
type Bus struct {
	conn *nats.Conn
	js   jetstream.JetStream
}

// NATSConnect establishes a NATS connection with JetStream context.
func NATSConnect(url, name string) (*Bus, error) {
	nc, err := nats.Connect(
		url,
		nats.Name(name),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.Timeout(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("create jetstream context: %w", err)
	}

	return &Bus{conn: nc, js: js}, nil
}

// Publish publishes data to a JetStream subject.
func (b *Bus) Publish(ctx context.Context, subject string, data []byte) error {
	if _, err := b.js.Publish(ctx, subject, data); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}

// PublishWithID publishes with a deduplication message ID.
func (b *Bus) PublishWithID(ctx context.Context, subject, messageID string, data []byte) error {
	if messageID == "" {
		return fmt.Errorf("publish %s: message id is required", subject)
	}
	if _, err := b.js.Publish(ctx, subject, data, jetstream.WithMsgID(messageID)); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}

// EnsureStream creates or updates a JetStream stream.
func (b *Bus) EnsureStream(ctx context.Context, name string, subjects []string) (jetstream.Stream, error) {
	stream, err := b.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       name,
		Subjects:   subjects,
		Retention:  jetstream.WorkQueuePolicy,
		Storage:    jetstream.FileStorage,
		MaxAge:     24 * time.Hour,
		Duplicates: 2 * time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure stream %s: %w", name, err)
	}
	return stream, nil
}

// BusinessEventSubjects are the shared platform event subjects. Every service
// that publishes business events must keep this list in sync with the
// documented event catalog.
var BusinessEventSubjects = []string{
	"identity.>", "workspace.>", "audience.>", "consent.>", "campaign.>",
	"delivery.smtp_submitted.>", "delivery.deferred.>", "delivery.bounced.>",
	"delivery.complained.>", "tracking.>", "automation.>", "billing.>",
	"notification.>", "form.>", "webhook.>",
}

// EnsureBusinessEvents creates or updates the shared business-events stream.
// It is idempotent so any service can call it at startup.
func (b *Bus) EnsureBusinessEvents(ctx context.Context) error {
	_, err := b.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       "BUSINESS_EVENTS",
		Subjects:   BusinessEventSubjects,
		Retention:  jetstream.LimitsPolicy,
		Storage:    jetstream.FileStorage,
		MaxAge:     7 * 24 * time.Hour,
		Duplicates: 10 * time.Minute,
	})
	if err != nil {
		return fmt.Errorf("ensure business events stream: %w", err)
	}
	return nil
}

// Consume creates (or reuses) a durable consumer and starts processing.
// An empty filterSubject consumes every subject on the stream.
func (b *Bus) Consume(ctx context.Context, streamName, durable, filterSubject string, handler func(context.Context, jetstream.Msg)) (jetstream.ConsumeContext, error) {
	stream, err := b.js.Stream(ctx, streamName)
	if err != nil {
		return nil, fmt.Errorf("get stream %s: %w", streamName, err)
	}

	cfg := jetstream.ConsumerConfig{
		Durable:       durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       2 * time.Minute,
		MaxDeliver:    10,
		MaxAckPending: 256,
	}
	if filterSubject != "" {
		cfg.FilterSubject = filterSubject
	}

	consumer, err := stream.CreateOrUpdateConsumer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create consumer %s: %w", durable, err)
	}

	return consumer.Consume(func(msg jetstream.Msg) {
		handler(ctx, msg)
	})
}

// Close drains and closes the NATS connection.
func (b *Bus) Close() {
	if b.conn != nil {
		b.conn.Drain()
		b.conn.Close()
	}
}

// Conn returns the underlying NATS connection for advanced use.
func (b *Bus) Conn() *nats.Conn {
	return b.conn
}

// JS returns the underlying JetStream context for advanced use.
func (b *Bus) JS() jetstream.JetStream {
	return b.js
}
