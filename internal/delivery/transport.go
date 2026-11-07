package delivery

import (
	"context"
	"errors"
)

type Message struct {
	ID          string
	WorkspaceID string
	CampaignID  string
	From        string
	To          string
	Subject     string
	HTML        string
	Text        string
	Headers     map[string]string
	DKIM        *DKIMConfig
}

type Result struct {
	MessageID string
	Accepted  bool
	Error     string
}

type Provider interface {
	Send(ctx context.Context, message Message) (*Result, error)
}

type Transport = Provider

type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string {
	return e.Err.Error()
}

func (e *PermanentError) Unwrap() error {
	return e.Err
}

func IsPermanent(err error) bool {
	var permanent *PermanentError
	return errors.As(err, &permanent)
}
