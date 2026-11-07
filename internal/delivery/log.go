package delivery

import (
	"context"
	"errors"
	"log/slog"
)

type LogProvider struct {
	Logger *slog.Logger
}

func (p LogProvider) Send(_ context.Context, message Message) (*Result, error) {
	if p.Logger != nil {
		p.Logger.Warn("email was not delivered by log provider", "message_id", message.ID, "workspace_id", message.WorkspaceID, "campaign_id", message.CampaignID)
	}
	return nil, errors.New("log provider records messages but does not deliver email")
}
