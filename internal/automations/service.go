package automations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/delivery"
)

var ErrNotFound = errors.New("automation not found")

// Automation mirrors the legacy schema in migrations/0001_init.sql.
type Automation struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"organization_id"`
	CreatedBy      string          `json:"created_by,omitempty"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Status         string          `json:"status"`
	Trigger        json.RawMessage `json:"trigger"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Steps          []Step          `json:"steps,omitempty"`
}

type Step struct {
	ID        string          `json:"id"`
	Position  int             `json:"position"`
	StepType  string          `json:"step_type"`
	Config    json.RawMessage `json:"config"`
	CreatedAt time.Time       `json:"created_at"`
}

type Execution struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id"`
	AutomationID   string     `json:"automation_id"`
	ContactID      string     `json:"contact_id"`
	Status         string     `json:"status"`
	CurrentStep    int        `json:"current_step"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type Service struct {
	pool              *pgxpool.Pool
	logger            *slog.Logger
	smtpAddr          string
	smtpHost          string
	smtpUser          string
	smtpPass          string
	fromEmail         string
	appBaseURL        string
	dkimEncryptionKey string
}

func NewService(pool *pgxpool.Pool, logger *slog.Logger, cfg config.Config) *Service {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{
		pool:              pool,
		logger:            logger,
		smtpAddr:          cfg.SMTPAddr,
		smtpHost:          cfg.SMTPHost,
		smtpUser:          cfg.SMTPUsername,
		smtpPass:          cfg.SMTPPassword,
		fromEmail:         strings.TrimSpace(cfg.SMTPFrom),
		appBaseURL:        strings.TrimSpace(cfg.AppBaseURL),
		dkimEncryptionKey: cfg.DKIMEncryptionKey,
	}
}

func (s *Service) List(ctx context.Context, orgID string) ([]Automation, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, organization_id, created_by, name, description, status, trigger, created_at, updated_at
		FROM automations
		WHERE organization_id = $1
		ORDER BY updated_at DESC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Automation, 0)
	for rows.Next() {
		var a Automation
		var trigger []byte
		if err := rows.Scan(&a.ID, &a.OrganizationID, &a.CreatedBy, &a.Name, &a.Description, &a.Status, &trigger, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Trigger = json.RawMessage(trigger)
		items = append(items, a)
	}
	return items, rows.Err()
}

func (s *Service) Get(ctx context.Context, orgID, automationID string) (*Automation, error) {
	if s == nil || s.pool == nil {
		return nil, ErrNotFound
	}
	var a Automation
	var trigger []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, organization_id, created_by, name, description, status, trigger, created_at, updated_at
		FROM automations
		WHERE organization_id = $1 AND id = $2
	`, orgID, automationID).Scan(&a.ID, &a.OrganizationID, &a.CreatedBy, &a.Name, &a.Description, &a.Status, &trigger, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.Trigger = json.RawMessage(trigger)
	a.Steps, err = s.listSteps(ctx, orgID, automationID)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Service) Create(ctx context.Context, orgID, createdBy, name, description, status string, trigger json.RawMessage, steps []Step) (*Automation, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("automation service is not configured")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("automation name is required")
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "draft"
	}
	if status != "draft" && status != "active" && status != "paused" && status != "archived" {
		return nil, fmt.Errorf("invalid automation status: %s", status)
	}
	trigger = json.RawMessage(strings.TrimSpace(string(trigger)))
	if len(trigger) == 0 {
		trigger = json.RawMessage(`{"type":"contact_added_to_list"}`)
	}
	if !json.Valid(trigger) {
		return nil, errors.New("automation trigger must be valid JSON")
	}

	var automation Automation
	err := s.pool.QueryRow(ctx, `
		INSERT INTO automations (organization_id, created_by, name, description, status, trigger)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
		RETURNING id, organization_id, created_by, name, description, status, trigger, created_at, updated_at
	`, orgID, createdBy, name, description, status, []byte(trigger)).Scan(
		&automation.ID, &automation.OrganizationID, &automation.CreatedBy, &automation.Name, &automation.Description, &automation.Status, &trigger, &automation.CreatedAt, &automation.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	automation.Trigger = json.RawMessage(trigger)
	for _, step := range steps {
		if step.StepType == "" {
			continue
		}
		step.Config = json.RawMessage(strings.TrimSpace(string(step.Config)))
		if len(step.Config) == 0 {
			step.Config = json.RawMessage(`{}`)
		}
		if !json.Valid(step.Config) {
			return nil, fmt.Errorf("step %d has invalid config JSON", step.Position)
		}
		_, err = s.pool.Exec(ctx, `
			INSERT INTO automation_steps (organization_id, automation_id, position, step_type, config)
			VALUES ($1, $2, $3, $4, $5::jsonb)
		`, orgID, automation.ID, step.Position, step.StepType, []byte(step.Config))
		if err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, orgID, automation.ID)
}

func (s *Service) Update(ctx context.Context, orgID, automationID string, name, description, status string, trigger json.RawMessage) (*Automation, error) {
	if s == nil || s.pool == nil {
		return nil, ErrNotFound
	}
	if strings.TrimSpace(name) == "" {
		existing, err := s.Get(ctx, orgID, automationID)
		if err != nil {
			return nil, errors.New("automation name is required")
		}
		name = existing.Name
		description = existing.Description
	}
	trigger = json.RawMessage(strings.TrimSpace(string(trigger)))
	if len(trigger) > 0 && !json.Valid(trigger) {
		return nil, errors.New("automation trigger must be valid JSON")
	}
	var updateTrigger any
	if len(trigger) == 0 {
		updateTrigger = nil
	} else {
		updateTrigger = []byte(trigger)
	}
	var updated Automation
	err := s.pool.QueryRow(ctx, `
		UPDATE automations
		SET name = $3,
		    description = $4,
		    status = COALESCE(NULLIF($5, ''), status),
		    trigger = COALESCE($6::jsonb, trigger),
		    updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING id, organization_id, created_by, name, description, status, trigger, created_at, updated_at
	`, orgID, automationID, name, description, status, updateTrigger).Scan(
		&updated.ID, &updated.OrganizationID, &updated.CreatedBy, &updated.Name, &updated.Description, &updated.Status, &trigger, &updated.CreatedAt, &updated.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	updated.Trigger = json.RawMessage(trigger)
	return &updated, nil
}

func (s *Service) Delete(ctx context.Context, orgID, automationID string) error {
	if s == nil || s.pool == nil {
		return ErrNotFound
	}
	result, err := s.pool.Exec(ctx, `DELETE FROM automations WHERE organization_id = $1 AND id = $2`, orgID, automationID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) UpsertExecution(ctx context.Context, orgID, automationID, contactID string) error {
	if s == nil || s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO automation_executions (organization_id, automation_id, contact_id, status, current_step, next_run_at, updated_at)
		SELECT $1, $2, $3, 'waiting', 0, now(), now()
		WHERE NOT EXISTS (
			SELECT 1 FROM automation_executions
			WHERE organization_id = $1 AND automation_id = $2 AND contact_id = $3
		)
	`, orgID, automationID, contactID)
	return err
}

func (s *Service) ProcessDue(ctx context.Context, orgID string, limit int) (int, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.organization_id, e.automation_id, e.contact_id, e.status, e.current_step, e.next_run_at
		FROM automation_executions e
		JOIN automations a ON a.id = e.automation_id AND a.organization_id = e.organization_id
		WHERE e.organization_id = $1
		  AND a.status = 'active'
		  AND e.status IN ('running', 'waiting')
		  AND (e.next_run_at IS NULL OR e.next_run_at <= now())
		ORDER BY e.next_run_at NULLS FIRST
		LIMIT $2
	`, orgID, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var execution Execution
		if err := rows.Scan(&execution.ID, &execution.OrganizationID, &execution.AutomationID, &execution.ContactID, &execution.Status, &execution.CurrentStep, &execution.NextRunAt); err != nil {
			return count, err
		}
		if _, err := s.pool.Exec(ctx, `UPDATE automation_executions SET status = 'running', updated_at = now() WHERE id = $1`, execution.ID); err != nil {
			return count, err
		}
		if err := s.advanceExecution(ctx, &execution); err != nil {
			s.logger.Warn("advance automation execution", "execution_id", execution.ID, "error", err)
			continue
		}
		count++
	}
	return count, rows.Err()
}

func (s *Service) advanceExecution(ctx context.Context, execution *Execution) error {
	if s == nil || s.pool == nil {
		return nil
	}
	steps, err := s.listSteps(ctx, execution.OrganizationID, execution.AutomationID)
	if err != nil {
		return err
	}
	if len(steps) == 0 {
		_, err = s.pool.Exec(ctx, `UPDATE automation_executions SET status = 'completed', completed_at = now(), updated_at = now() WHERE id = $1`, execution.ID)
		return err
	}
	if execution.CurrentStep >= len(steps) {
		_, err = s.pool.Exec(ctx, `UPDATE automation_executions SET status = 'completed', completed_at = now(), updated_at = now() WHERE id = $1`, execution.ID)
		return err
	}
	step := steps[execution.CurrentStep]
	cfg := map[string]any{}
	if len(step.Config) > 0 {
		if err := json.Unmarshal(step.Config, &cfg); err != nil {
			return err
		}
	}

	switch step.StepType {
	case "send_email":
		if err := s.sendEmailStep(ctx, execution.OrganizationID, execution.ID, execution.ContactID, cfg); err != nil {
			_, updErr := s.pool.Exec(ctx, `UPDATE automation_executions SET status = 'failed', last_error = $2, updated_at = now(), completed_at = now() WHERE id = $1`, execution.ID, err.Error())
			if updErr != nil {
				return updErr
			}
			return err
		}
		execution.CurrentStep++
		if execution.CurrentStep >= len(steps) {
			_, err = s.pool.Exec(ctx, `UPDATE automation_executions SET status = 'completed', current_step = $2, completed_at = now(), updated_at = now() WHERE id = $1`, execution.ID, execution.CurrentStep)
			return err
		}
		_, err = s.pool.Exec(ctx, `UPDATE automation_executions SET status = 'waiting', current_step = $2, next_run_at = NULL, updated_at = now() WHERE id = $1`, execution.ID, execution.CurrentStep)
		return err
	case "wait":
		duration := waitDuration(cfg)
		nextRun := time.Now().Add(duration)
		_, err = s.pool.Exec(ctx, `UPDATE automation_executions SET status = 'waiting', current_step = $2, next_run_at = $3, updated_at = now() WHERE id = $1`, execution.ID, execution.CurrentStep+1, nextRun)
		return err
	case "condition":
		match, err := s.evaluateCondition(ctx, execution.OrganizationID, execution.ContactID, cfg)
		if err != nil {
			return err
		}
		if match {
			execution.CurrentStep++
			_, err = s.pool.Exec(ctx, `UPDATE automation_executions SET status = 'waiting', current_step = $2, next_run_at = NULL, updated_at = now() WHERE id = $1`, execution.ID, execution.CurrentStep)
			return err
		}
		_, err = s.pool.Exec(ctx, `UPDATE automation_executions SET status = 'completed', current_step = $2, completed_at = now(), updated_at = now() WHERE id = $1`, execution.ID, len(steps))
		return err
	default:
		execution.CurrentStep++
		_, err = s.pool.Exec(ctx, `UPDATE automation_executions SET status = 'waiting', current_step = $2, next_run_at = NULL, updated_at = now() WHERE id = $1`, execution.ID, execution.CurrentStep)
		return err
	}
}

func (s *Service) evaluateCondition(ctx context.Context, orgID, contactID string, cfg map[string]any) (bool, error) {
	if s == nil || s.pool == nil {
		return false, nil
	}
	field, _ := cfg["field"].(string)
	tagID, _ := cfg["tag_id"].(string)
	if field == "tag_present" && tagID != "" {
		var count int
		err := s.pool.QueryRow(ctx, `
			SELECT count(*) FROM contact_tags WHERE organization_id = $1 AND contact_id = $2 AND tag_id = $3
		`, orgID, contactID, tagID).Scan(&count)
		return count > 0, err
	}
	return true, nil
}

func (s *Service) listSteps(ctx context.Context, orgID, automationID string) ([]Step, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, position, step_type, config, created_at
		FROM automation_steps
		WHERE organization_id = $1 AND automation_id = $2
		ORDER BY position ASC
	`, orgID, automationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	steps := make([]Step, 0)
	for rows.Next() {
		var step Step
		var config []byte
		if err := rows.Scan(&step.ID, &step.Position, &step.StepType, &config, &step.CreatedAt); err != nil {
			return nil, err
		}
		step.Config = json.RawMessage(config)
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

func (s *Service) sendEmailStep(ctx context.Context, orgID, executionID, contactID string, cfg map[string]any) error {
	if s == nil || s.pool == nil {
		return nil
	}
	var email, firstName, lastName string
	err := s.pool.QueryRow(ctx, `
		SELECT email, COALESCE(first_name, ''), COALESCE(last_name, '')
		FROM contacts
		WHERE id = $1 AND organization_id = $2
	`, contactID, orgID).Scan(&email, &firstName, &lastName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var templateID string
	if v, ok := cfg["template_id"].(string); ok && v != "" {
		templateID = v
	} else if v, ok := cfg["templateId"].(string); ok && v != "" {
		templateID = v
	}
	var subject, htmlBody, textBody string
	if templateID != "" {
		err = s.pool.QueryRow(ctx, `
			SELECT name, COALESCE(html, ''), COALESCE(text_content, '')
			FROM templates
			WHERE id = $1 AND organization_id = $2
		`, templateID, orgID).Scan(&subject, &htmlBody, &textBody)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	if subject == "" {
		subject = "Your update"
	}
	if htmlBody == "" {
		htmlBody = "<p>Hello " + firstName + ",</p><p>Here is the latest update.</p>"
	}
	if textBody == "" {
		textBody = "Hello " + firstName + ",\nHere is the latest update."
	}
	fromAddress := s.fromEmail
	if v, ok := cfg["from_email"].(string); ok && v != "" {
		fromAddress = v
	}
	renderMap := map[string]string{
		"{{first_name}}":      firstName,
		"{{last_name}}":       lastName,
		"{{email}}":           email,
		"{{unsubscribe_url}}": s.appBaseURL + "/unsubscribe?email=" + email,
	}
	subject = renderTemplate(subject, renderMap)
	htmlBody = renderTemplate(htmlBody, renderMap)
	textBody = renderTemplate(textBody, renderMap)
	dkimConfig, err := delivery.LoadDKIMConfig(ctx, s.pool, orgID, fromAddress, s.dkimEncryptionKey)
	if err != nil {
		return fmt.Errorf("load dkim signing key: %w", err)
	}
	transport := delivery.NewSMTPTransport(s.smtpAddr, s.smtpHost, s.smtpUser, s.smtpPass, true)
	_, err = transport.Send(ctx, delivery.Message{
		ID:      fmt.Sprintf("automation-%s-%s", executionID, contactID),
		From:    fromAddress,
		To:      email,
		Subject: subject,
		HTML:    htmlBody,
		Text:    textBody,
		Headers: map[string]string{"List-Unsubscribe": "<" + s.appBaseURL + "/unsubscribe?email=" + email + ">"},
		DKIM:    dkimConfig,
	})
	return err
}

func renderTemplate(in string, mapping map[string]string) string {
	out := in
	for key, val := range mapping {
		out = strings.ReplaceAll(out, key, val)
	}
	return out
}

func waitDuration(cfg map[string]any) time.Duration {
	if cfg == nil {
		return 0
	}
	if v, ok := cfg["minutes"].(float64); ok {
		return time.Duration(v) * time.Minute
	}
	if v, ok := cfg["seconds"].(float64); ok {
		return time.Duration(v) * time.Second
	}
	if v, ok := cfg["hours"].(float64); ok {
		return time.Duration(v) * time.Hour
	}
	if v, ok := cfg["days"].(float64); ok {
		return time.Duration(v) * 24 * time.Hour
	}
	if v, ok := cfg["delay_minutes"].(float64); ok {
		return time.Duration(v) * time.Minute
	}
	return 0
}
