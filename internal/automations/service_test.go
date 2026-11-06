package automations

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/db"
)

func TestServiceProcessDueSendsEmailForActiveAutomation(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, databaseURL, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	orgID := uuid.NewString()
	userID := uuid.NewString()
	contactID := uuid.NewString()
	templateID := uuid.NewString()
	slug := strings.ReplaceAll(orgID, "-", "")
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, name, slug) VALUES ($1, 'Automation Test Org', $2)
	`, orgID, slug); err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, password_hash, name, status) VALUES ($1, $2, 'unused', 'Automation Owner', 'active')
	`, userID, userID+"@automation-test.invalid"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'owner')
	`, orgID, userID); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO contacts (id, organization_id, email, first_name, last_name, status)
		VALUES ($1, $2, 'ada@example.com', 'Ada', 'Lovelace', 'subscribed')
	`, contactID, orgID); err != nil {
		t.Fatalf("create contact: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO templates (id, organization_id, created_by, name, html, text_content)
		VALUES ($1, $2, $3, 'Automation Template', '<p>Hello {{first_name}}!</p><p>Unsubscribe: {{unsubscribe_url}}</p>', 'Hello {{first_name}}!')
	`, templateID, orgID, userID); err != nil {
		t.Fatalf("create template: %v", err)
	}

	cfg := config.Config{
		AppBaseURL: "http://localhost:3000",
		SMTPAddr:   "127.0.0.1:1025",
		SMTPHost:   "mailpit",
		SMTPFrom:   "hello@example.com",
	}
	service := NewService(pool, nil, cfg)
	automation, err := service.Create(ctx, orgID, userID, "Welcome automation", "Sends a welcome email to a list member.", "active", json.RawMessage(`{"type":"contact_added_to_list"}`), []Step{{
		Position: 0,
		StepType: "send_email",
		Config:   json.RawMessage(`{"template_id":"` + templateID + `"}`),
	}})
	if err != nil {
		t.Fatalf("create automation: %v", err)
	}
	if err := service.UpsertExecution(ctx, orgID, automation.ID, contactID); err != nil {
		t.Fatalf("queue automation execution: %v", err)
	}

	count, err := service.ProcessDue(ctx, orgID, 10)
	if err != nil {
		t.Fatalf("process due automations: %v", err)
	}
	if count != 1 {
		t.Fatalf("process due count = %d, want 1", count)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM automation_executions WHERE organization_id = $1 AND automation_id = $2 AND contact_id = $3`, orgID, automation.ID, contactID).Scan(&status); err != nil {
		t.Fatalf("fetch execution status: %v", err)
	}
	if status != "completed" {
		t.Fatalf("execution status = %q, want %q", status, "completed")
	}

	waitForMailpitMessage(t, func(msg mailpitMessage) bool {
		return strings.Contains(msg.Subject, "Ada") || strings.Contains(msg.Snippet, "unsubscribe")
	})
}

func TestServiceProcessDueSkipsPausedAutomations(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, databaseURL, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	orgID := uuid.NewString()
	userID := uuid.NewString()
	contactID := uuid.NewString()
	slug := strings.ReplaceAll(orgID, "-", "")
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id, name, slug) VALUES ($1, 'Paused Automation Org', $2)`, orgID, slug); err != nil {
		t.Fatalf("create organization: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, name, status) VALUES ($1, $2, 'unused', 'Paused Owner', 'active')`, userID, userID+"@paused.invalid"); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organization_members (organization_id, user_id, role) VALUES ($1, $2, 'owner')`, orgID, userID); err != nil {
		t.Fatalf("create membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO contacts (id, organization_id, email, first_name, last_name, status) VALUES ($1, $2, 'paused@example.com', 'Paused', 'Contact', 'subscribed')`, contactID, orgID); err != nil {
		t.Fatalf("create contact: %v", err)
	}
	service := NewService(pool, nil, config.Config{AppBaseURL: "http://localhost:3000", SMTPAddr: "127.0.0.1:1025", SMTPHost: "mailpit", SMTPFrom: "hello@example.com"})
	automation, err := service.Create(ctx, orgID, userID, "Paused automation", "Should not run when paused.", "paused", json.RawMessage(`{"type":"contact_added_to_list"}`), nil)
	if err != nil {
		t.Fatalf("create paused automation: %v", err)
	}
	if err := service.UpsertExecution(ctx, orgID, automation.ID, contactID); err != nil {
		t.Fatalf("queue automation execution: %v", err)
	}
	count, err := service.ProcessDue(ctx, orgID, 10)
	if err != nil {
		t.Fatalf("process due paused automation: %v", err)
	}
	if count != 0 {
		t.Fatalf("paused automation count = %d, want 0", count)
	}
}

type mailpitMessage struct {
	Subject string `json:"Subject"`
	Snippet string `json:"Snippet"`
}

func waitForMailpitMessage(t *testing.T, match func(mailpitMessage) bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:8025/api/v1/messages")
		if err == nil && resp.StatusCode == http.StatusOK {
			var payload struct {
				Messages []mailpitMessage `json:"messages"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
				for _, message := range payload.Messages {
					if match(message) {
						resp.Body.Close()
						return
					}
				}
			}
			resp.Body.Close()
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("Mailpit did not receive the expected automation email")
}
