package messaging

import (
	"context"
	"fmt"
	"time"

	"github.com/khayode/email-platform/internal/kit"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const SubjectDeliveryJobs = "delivery.jobs"

type Bus struct {
	conn *nats.Conn
	js   jetstream.JetStream
}

func Connect(url, name string) (*Bus, error) {
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

func (b *Bus) Publish(ctx context.Context, subject string, data []byte) error {
	if _, err := b.js.Publish(ctx, subject, data); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}

func (b *Bus) PublishWithID(ctx context.Context, subject, messageID string, data []byte) error {
	if messageID == "" {
		return fmt.Errorf("publish %s: message id is required", subject)
	}
	if _, err := b.js.Publish(ctx, subject, data, jetstream.WithMsgID(messageID)); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}

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

func (b *Bus) EnsureEventStream(ctx context.Context) error {
	// Subject list must stay identical across services; the shared list lives
	// in internal/kit so CreateOrUpdateStream never fights over config.
	_, err := b.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       "BUSINESS_EVENTS",
		Subjects:   kit.BusinessEventSubjects,
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

func (b *Bus) Consume(
	ctx context.Context,
	streamName, durable string,
	handler func(context.Context, jetstream.Msg),
) (jetstream.ConsumeContext, error) {
	return b.ConsumeFiltered(ctx, streamName, durable, "", handler)
}

// ConsumeFiltered creates (or reuses) a durable consumer limited to
// filterSubject (empty means every subject on the stream).
func (b *Bus) ConsumeFiltered(
	ctx context.Context,
	streamName, durable, filterSubject string,
	handler func(context.Context, jetstream.Msg),
) (jetstream.ConsumeContext, error) {
	stream, err := b.js.Stream(ctx, streamName)
	if err != nil {
		return nil, fmt.Errorf("get stream %s: %w", streamName, err)
	}

	config := jetstream.ConsumerConfig{
		Durable:       durable,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       2 * time.Minute,
		MaxDeliver:    10,
		MaxAckPending: 256,
	}
	if filterSubject != "" {
		config.FilterSubject = filterSubject
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create consumer %s: %w", durable, err)
	}

	return consumer.Consume(func(msg jetstream.Msg) {
		handler(ctx, msg)
	})
}

func (b *Bus) Close() {
	if b.conn != nil {
		b.conn.Drain()
		b.conn.Close()
	}
}
