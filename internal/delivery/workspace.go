package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRoutingSettings struct {
	Pool *pgxpool.Pool
}

func (s PostgresRoutingSettings) ForWorkspace(ctx context.Context, workspaceID, fromDomain string) (WorkspaceRouting, error) {
	if s.Pool == nil {
		return WorkspaceRouting{}, errors.New("workspace routing database is not configured")
	}
	settings := WorkspaceRouting{Provider: ProviderStalwart}
	err := s.Pool.QueryRow(ctx, `
		SELECT provider, COALESCE(fallback_provider, '')
		FROM workspace_delivery_settings
		WHERE workspace_id = $1
	`, workspaceID).Scan(&settings.Provider, &settings.FallbackProvider)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return WorkspaceRouting{}, fmt.Errorf("load workspace provider setting: %w", err)
	}
	if fromDomain == "" {
		return settings, nil
	}
	err = s.Pool.QueryRow(ctx, `
		SELECT stalwart_authenticated, resend_authenticated
		FROM sending_domains
		WHERE organization_id = $1 AND lower(domain) = lower($2)
	`, workspaceID, fromDomain).Scan(&settings.StalwartDomainAuth, &settings.ResendDomainAuth)
	if errors.Is(err, pgx.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return WorkspaceRouting{}, fmt.Errorf("load sending domain provider authentication: %w", err)
	}
	return settings, nil
}

type WorkspaceResendProvider struct {
	Pool           *pgxpool.Pool
	EncryptionKey  string
	RequestsPerSec int
	BaseURL        string
	BatchWait      time.Duration
	mu             sync.Mutex
	queues         map[string]*workspaceResendQueue
}

func (p *WorkspaceResendProvider) Send(ctx context.Context, message Message) (*Result, error) {
	queue := p.queue(message.WorkspaceID)
	item := workspaceResendItem{message: message, result: make(chan workspaceResendResult, 1)}
	select {
	case queue.items <- item:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case result := <-item.result:
		return result.result, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type workspaceResendItem struct {
	message Message
	result  chan workspaceResendResult
}

type workspaceResendResult struct {
	result *Result
	err    error
}

type workspaceResendQueue struct {
	items chan workspaceResendItem
}

func (p *WorkspaceResendProvider) queue(workspaceID string) *workspaceResendQueue {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.queues == nil {
		p.queues = make(map[string]*workspaceResendQueue)
	}
	queue := p.queues[workspaceID]
	if queue == nil {
		queue = &workspaceResendQueue{items: make(chan workspaceResendItem, resendBatchSize*10)}
		p.queues[workspaceID] = queue
		go p.runQueue(workspaceID, queue)
	}
	return queue
}

func (p *WorkspaceResendProvider) runQueue(workspaceID string, queue *workspaceResendQueue) {
	for first := range queue.items {
		batch := []workspaceResendItem{first}
		batchWait := p.BatchWait
		if batchWait <= 0 {
			batchWait = 5 * time.Millisecond
		}
		timer := time.NewTimer(batchWait)
	collect:
		for len(batch) < resendBatchSize {
			select {
			case item := <-queue.items:
				batch = append(batch, item)
			case <-timer.C:
				break collect
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		p.sendBatch(workspaceID, batch)
	}
}

func (p *WorkspaceResendProvider) sendBatch(workspaceID string, batch []workspaceResendItem) {
	if p.Pool == nil {
		completeWorkspaceBatch(batch, nil, errors.New("workspace Resend provider database is not configured"))
		return
	}
	if workspaceID == "" {
		completeWorkspaceBatch(batch, nil, errors.New("workspace id is required for Resend campaign delivery"))
		return
	}
	var encrypted []byte
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := p.Pool.QueryRow(ctx, `
		SELECT resend_api_key_enc
		FROM workspace_delivery_settings
		WHERE workspace_id = $1
	`, workspaceID).Scan(&encrypted)
	if errors.Is(err, pgx.ErrNoRows) || len(encrypted) == 0 {
		completeWorkspaceBatch(batch, nil, errors.New("Resend is selected but the workspace API key is not configured"))
		return
	}
	if err != nil {
		completeWorkspaceBatch(batch, nil, fmt.Errorf("load workspace Resend API key: %w", err))
		return
	}
	apiKey, err := DecryptSecret(p.EncryptionKey, encrypted)
	if err != nil {
		completeWorkspaceBatch(batch, nil, fmt.Errorf("decrypt workspace Resend API key: %w", err))
		return
	}
	transport := ResendTransport{APIKey: apiKey, BaseURL: p.BaseURL, RequestsRate: p.RequestsPerSec, MaxRetries: 5}
	messages := make([]Message, len(batch))
	for i, item := range batch {
		messages[i] = item.message
	}
	results, err := transport.SendBatch(ctx, messages, batchIdempotencyKey(messages))
	completeWorkspaceBatch(batch, results, err)
}

func completeWorkspaceBatch(batch []workspaceResendItem, results []BatchResult, err error) {
	if err != nil {
		for _, item := range batch {
			item.result <- workspaceResendResult{err: err}
		}
		return
	}
	if len(results) != len(batch) {
		err = fmt.Errorf("resend returned %d results for %d workspace messages", len(results), len(batch))
		for _, item := range batch {
			item.result <- workspaceResendResult{err: err}
		}
		return
	}
	for i, result := range results {
		if !result.Accepted {
			batch[i].result <- workspaceResendResult{err: errors.New("resend did not accept the email")}
			continue
		}
		batch[i].result <- workspaceResendResult{result: &Result{MessageID: result.MessageID, Accepted: true}}
	}
}

type CampaignRouter struct {
	Router *Router
}

func (p CampaignRouter) Send(ctx context.Context, message Message) (*Result, error) {
	fromDomain := senderDomain(message.From)
	return p.Router.Send(ctx, CampaignDelivery, message.WorkspaceID, fromDomain, message)
}

func NewCampaignRouter(pool *pgxpool.Pool, encryptionKey string, requestsPerSecond int, stalwart Provider, logger *slog.Logger) Provider {
	router := &Router{
		Resolver: RoutingResolver{Settings: PostgresRoutingSettings{Pool: pool}},
		Providers: map[string]Provider{
			ProviderStalwart: stalwart,
			ProviderResend: &WorkspaceResendProvider{
				Pool: pool, EncryptionKey: encryptionKey, RequestsPerSec: requestsPerSecond,
			},
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
