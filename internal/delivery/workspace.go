package delivery

import (
	"context"
	"log/slog"
	"net/mail"
	"strings"
)

type CampaignRouter struct {
	Router *Router
}

func (p CampaignRouter) Send(ctx context.Context, message Message) (*Result, error) {
	fromDomain := senderDomain(message.From)
	return p.Router.Send(ctx, CampaignDelivery, message.WorkspaceID, fromDomain, message)
}

// NewCampaignRouter builds the campaign send path. Campaign delivery is
// Stalwart-only: no other provider is registered, so a campaign message can
// never leave through the platform transactional fallback.
func NewCampaignRouter(stalwart Provider, logger *slog.Logger) Provider {
	router := &Router{
		Resolver: RoutingResolver{},
		Providers: map[string]Provider{
			ProviderStalwart: stalwart,
		},
		Logger: logger,
	}
	return CampaignRouter{Router: router}
}

func senderDomain(from string) string {
	address, err := mail.ParseAddress(from)
	if err != nil {
		return ""
	}
	at := strings.LastIndexByte(address.Address, '@')
	if at < 0 {
		return ""
	}
	return strings.ToLower(address.Address[at+1:])
}
