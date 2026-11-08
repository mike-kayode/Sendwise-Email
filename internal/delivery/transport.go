package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"
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

// ProviderError describes a delivery-provider failure with enough detail for
// the router to decide whether to fail over or stop.
type ProviderError struct {
	StatusCode int
	Temporary  bool
	RetryAfter time.Duration
	Message    string
}

func (e *ProviderError) Error() string {
	if e.StatusCode != 0 && e.Message != "" {
		return fmt.Sprintf("provider returned HTTP %d: %s", e.StatusCode, e.Message)
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("provider returned HTTP %d", e.StatusCode)
	}
	return e.Message
}

func IsPermanent(err error) bool {
	var permanent *PermanentError
	return errors.As(err, &permanent)
}
