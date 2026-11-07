package delivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const resendBatchSize = 100

type ProviderError struct {
	StatusCode int
	Temporary  bool
	RetryAfter time.Duration
	Message    string
}

func (e *ProviderError) Error() string {
	if e.StatusCode != 0 && e.Message != "" {
		return fmt.Sprintf("resend API returned HTTP %d: %s", e.StatusCode, e.Message)
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("resend API returned HTTP %d", e.StatusCode)
	}
	return e.Message
}

type ResendTransport struct {
	APIKey       string
	BaseURL      string
	RequestsRate int
	Client       *http.Client
	MaxRetries   int
}

type ResendDNSRecord struct {
	Record   string `json:"record,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Status   string `json:"status"`
	Priority int    `json:"priority,omitempty"`
	TTL      string `json:"ttl,omitempty"`
}

type ResendDomain struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Status  string            `json:"status"`
	Records []ResendDNSRecord `json:"records"`
}

type resendEmail struct {
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	HTML    string            `json:"html,omitempty"`
	Text    string            `json:"text,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Tags    []resendTag       `json:"tags,omitempty"`
}

type resendTag struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type resendResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

type BatchResult struct {
	MessageID string
	Accepted  bool
}

func (t *ResendTransport) Send(ctx context.Context, message Message) (*Result, error) {
	results, err := t.SendBatch(ctx, []Message{message}, message.ID)
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, errors.New("resend returned an unexpected number of batch results")
	}
	if !results[0].Accepted {
		return nil, errors.New("resend did not accept the email")
	}
	return &Result{MessageID: results[0].MessageID, Accepted: results[0].Accepted}, nil
}

func (t *ResendTransport) ListDomains(ctx context.Context, apiKey string) ([]ResendDomain, error) {
	var response struct {
		Data []ResendDomain `json:"data"`
	}
	if err := t.requestJSON(ctx, http.MethodGet, "/domains", apiKey, nil, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

func (t *ResendTransport) CreateDomain(ctx context.Context, apiKey, domain string) (*ResendDomain, error) {
	var response ResendDomain
	if err := t.requestJSON(ctx, http.MethodPost, "/domains", apiKey, map[string]string{"name": domain}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (t *ResendTransport) GetDomain(ctx context.Context, apiKey, domainID string) (*ResendDomain, error) {
	var response ResendDomain
	if err := t.requestJSON(ctx, http.MethodGet, "/domains/"+url.PathEscape(domainID), apiKey, nil, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (t *ResendTransport) VerifyDomain(ctx context.Context, apiKey, domainID string) (*ResendDomain, error) {
	var response ResendDomain
	if err := t.requestJSON(ctx, http.MethodPost, "/domains/"+url.PathEscape(domainID)+"/verify", apiKey, struct{}{}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (t *ResendTransport) requestJSON(ctx context.Context, method, path, apiKey string, body, output any) error {
	if err := WaitResendRequest(ctx, apiKey, t.RequestsRate, false); err != nil {
		return fmt.Errorf("wait for resend request limit: %w", err)
	}
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode resend domains request: %w", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, t.endpoint(path), requestBody)
	if err != nil {
		return fmt.Errorf("create resend domains request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send resend domains request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read resend domains response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return parseResendError(response.StatusCode, response.Header, responseBody)
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("decode resend domains response: %w", err)
	}
	return nil
}

func (t *ResendTransport) SendBatch(ctx context.Context, messages []Message, idempotencyKey string) ([]BatchResult, error) {
	if len(messages) == 0 {
		return []BatchResult{}, nil
	}
	if len(messages) > resendBatchSize {
		return nil, fmt.Errorf("resend batch contains %d emails; maximum is %d", len(messages), resendBatchSize)
	}
	if strings.TrimSpace(t.APIKey) == "" {
		return nil, errors.New("resend API key is not configured")
	}
	payload := make([]resendEmail, 0, len(messages))
	for _, message := range messages {
		workspaceID := message.WorkspaceID
		if workspaceID == "" {
			workspaceID = "platform"
		}
		campaignID := message.CampaignID
		if campaignID == "" {
			campaignID = "platform"
		}
		tags := []resendTag{
			{Name: "workspace_id", Value: workspaceID},
			{Name: "campaign_id", Value: campaignID},
			{Name: "message_id", Value: message.ID},
		}
		payload = append(payload, resendEmail{
			From: message.From, To: []string{message.To}, Subject: message.Subject,
			HTML: message.HTML, Text: message.Text, Headers: message.Headers, Tags: tags,
		})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode resend batch: %w", err)
	}
	if idempotencyKey == "" {
		idempotencyKey = batchIdempotencyKey(messages)
	}
	maxRetries := t.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	} else if maxRetries == 0 {
		maxRetries = 5
	}
	if maxRetries > 5 {
		maxRetries = 5
	}
	for attempt := 0; ; attempt++ {
		platformPriority := len(messages) > 0 && messages[0].WorkspaceID == ""
		if err := WaitResendRequest(ctx, t.APIKey, t.RequestsRate, platformPriority); err != nil {
			return nil, fmt.Errorf("wait for resend request limit: %w", err)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint("/emails/batch"), bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create resend request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+t.APIKey)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", idempotencyKey)
		client := t.Client
		if client == nil {
			client = http.DefaultClient
		}
		response, err := client.Do(request)
		if err != nil {
			if attempt >= maxRetries {
				return nil, fmt.Errorf("send resend batch: %w", err)
			}
			if err := waitContext(ctx, retryDelay(attempt, 0)); err != nil {
				return nil, err
			}
			continue
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read resend response: %w", readErr)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			var decoded resendResponse
			if err := json.Unmarshal(responseBody, &decoded); err != nil {
				return nil, fmt.Errorf("decode resend response: %w", err)
			}
			if len(decoded.Data) != len(messages) {
				return nil, fmt.Errorf("resend returned %d results for %d emails", len(decoded.Data), len(messages))
			}
			results := make([]BatchResult, len(decoded.Data))
			for i, item := range decoded.Data {
				results[i] = BatchResult{MessageID: item.ID, Accepted: item.ID != ""}
			}
			return results, nil
		}
		providerErr := parseResendError(response.StatusCode, response.Header, responseBody)
		if !providerErr.Temporary || attempt >= maxRetries {
			return nil, providerErr
		}
		if err := waitContext(ctx, retryDelay(attempt, providerErr.RetryAfter)); err != nil {
			return nil, err
		}
	}
}

func (t *ResendTransport) endpoint(path string) string {
	base := strings.TrimRight(strings.TrimSpace(t.BaseURL), "/")
	if base == "" {
		base = "https://api.resend.com"
	}
	return base + path
}

func parseResendError(status int, headers http.Header, body []byte) *ProviderError {
	retryAfter := parseRetryAfter(headers, time.Now())
	var response struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &response)
	temporary := status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
	return &ProviderError{StatusCode: status, Temporary: temporary, RetryAfter: retryAfter, Message: response.Message}
}

func parseRetryAfter(headers http.Header, now time.Time) time.Duration {
	for _, name := range []string{"Retry-After", "ratelimit-reset", "x-ratelimit-reset"} {
		raw := strings.TrimSpace(headers.Get(name))
		if raw == "" {
			continue
		}
		if seconds, err := strconv.ParseFloat(raw, 64); err == nil && seconds >= 0 {
			if seconds > 1e9 {
				reset := time.Unix(int64(seconds), int64((seconds-float64(int64(seconds)))*float64(time.Second)))
				if delay := reset.Sub(now); delay > 0 {
					return delay
				}
				continue
			}
			return time.Duration(seconds * float64(time.Second))
		}
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
		if at, err := http.ParseTime(raw); err == nil {
			if delay := at.Sub(now); delay > 0 {
				return delay
			}
		}
		if at, err := time.Parse(time.RFC3339, raw); err == nil {
			if delay := at.Sub(now); delay > 0 {
				return delay
			}
		}
	}
	return 0
}

func retryDelay(attempt int, serverDelay time.Duration) time.Duration {
	if serverDelay > 0 {
		return serverDelay
	}
	delay := time.Second << min(attempt, 4)
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func batchIdempotencyKey(messages []Message) string {
	hash := sha256.New()
	for _, message := range messages {
		_, _ = io.WriteString(hash, message.ID)
		_, _ = io.WriteString(hash, "\n")
	}
	return hex.EncodeToString(hash.Sum(nil))
}
