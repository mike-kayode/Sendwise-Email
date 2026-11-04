package projections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/khayode/email-platform/internal/messaging"
	"github.com/khayode/email-platform/shared/contracts/events"
	"github.com/nats-io/nats.go/jetstream"
)

// retryDelay is how long a failed projection waits before redelivery. The
// user projection usually lands within a few milliseconds of the organization
// event, so a short delay resolves ordering without hot-looping.
const retryDelay = 5 * time.Second

// Projector mirrors identity and workspace events into the legacy schema so
// the monolith's foreign keys keep resolving during the migration window.
type Projector struct {
	store  Store
	logger *slog.Logger
}

// New creates a Projector over the given store.
func New(store Store, logger *slog.Logger) *Projector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Projector{store: store, logger: logger}
}

// HandleUserRegistered projects identity.user_registered.v1 into legacy users.
func (p *Projector) HandleUserRegistered(ctx context.Context, data []byte) error {
	var envelope events.Envelope[events.UserRegistered]
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode identity.user_registered: %w", err)
	}
	if envelope.ID == "" || envelope.Data.UserID == "" || envelope.Data.Email == "" {
		return errors.New("identity.user_registered envelope is missing required fields")
	}
	projected, err := p.store.ProjectUser(ctx, envelope.ID, UserProjection{
		ID:    envelope.Data.UserID,
		Email: envelope.Data.Email,
		Name:  envelope.Data.Name,
	})
	if err != nil {
		return err
	}
	if projected {
		p.logger.Info("projected legacy user", "user_id", envelope.Data.UserID, "email", envelope.Data.Email)
	}
	return nil
}

// HandleOrganizationCreated projects workspace.organization_created.v1 into
// legacy organizations and the owner membership.
func (p *Projector) HandleOrganizationCreated(ctx context.Context, data []byte) error {
	var envelope events.Envelope[events.OrganizationCreated]
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode workspace.organization_created: %w", err)
	}
	if envelope.ID == "" || envelope.Data.OrganizationID == "" || envelope.Data.OwnerUserID == "" {
		return errors.New("workspace.organization_created envelope is missing required fields")
	}
	err := p.store.ProjectOrganization(ctx, envelope.ID, OrganizationProjection{
		OrganizationID: envelope.Data.OrganizationID,
		Name:           envelope.Data.Name,
		Slug:           envelope.Data.Slug,
		OwnerUserID:    envelope.Data.OwnerUserID,
	})
	if err != nil {
		return err
	}
	p.logger.Info("projected legacy organization",
		"organization_id", envelope.Data.OrganizationID,
		"slug", envelope.Data.Slug,
	)
	return nil
}

// StartConsumers registers the durable projection consumers on the business
// events stream. It fails if the stream cannot be ensured.
func (p *Projector) StartConsumers(ctx context.Context, bus *messaging.Bus) error {
	if err := bus.EnsureEventStream(ctx); err != nil {
		return err
	}

	subscriptions := []struct {
		durable string
		filter  string
		handler func(context.Context, []byte) error
	}{
		{
			durable: "legacy-user-registered",
			filter:  "identity.user_registered.v1",
			handler: p.HandleUserRegistered,
		},
		{
			durable: "legacy-organization-created",
			filter:  "workspace.organization_created.v1",
			handler: p.HandleOrganizationCreated,
		},
	}

	for _, sub := range subscriptions {
		_, err := bus.ConsumeFiltered(ctx, "BUSINESS_EVENTS", sub.durable, sub.filter,
			func(consumeCtx context.Context, msg jetstream.Msg) {
				if err := sub.handler(consumeCtx, msg.Data()); err != nil {
					p.logger.Error("projection failed; will retry",
						"durable", sub.durable,
						"subject", msg.Subject(),
						"error", err,
					)
					msg.NakWithDelay(retryDelay)
					return
				}
				msg.Ack()
			})
		if err != nil {
			return fmt.Errorf("start consumer %s: %w", sub.durable, err)
		}
		p.logger.Info("projection consumer started", "durable", sub.durable, "filter", sub.filter)
	}
	return nil
}
