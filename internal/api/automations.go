package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	auto "github.com/khayode/email-platform/internal/automations"
	"github.com/khayode/email-platform/internal/httpx"
)

type automationInput struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Status      string                 `json:"status"`
	Trigger     json.RawMessage        `json:"trigger"`
	Steps       []automationStepInput `json:"steps"`
}

type automationStepInput struct {
	Position int             `json:"position"`
	StepType string          `json:"step_type"`
	Config   json.RawMessage `json:"config"`
}

type automationResponse struct {
	ID          string                 `json:"id"`
	OrganizationID string             `json:"organization_id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Status      string                 `json:"status"`
	Trigger     json.RawMessage        `json:"trigger"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
	Steps       []automationStepValue `json:"steps,omitempty"`
}

type automationStepValue struct {
	ID        string          `json:"id"`
	Position  int             `json:"position"`
	StepType  string          `json:"step_type"`
	Config    json.RawMessage `json:"config"`
	CreatedAt time.Time       `json:"created_at"`
}

func (s *Server) listAutomations(w http.ResponseWriter, r *http.Request) {
	items, err := auto.NewService(s.pool, s.logger, s.cfg).List(r.Context(), s.organizationID(r))
	if err != nil {
		s.logger.Error("list automations", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load automations.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) getAutomation(w http.ResponseWriter, r *http.Request) {
	a, err := auto.NewService(s.pool, s.logger, s.cfg).Get(r.Context(), s.organizationID(r), r.PathValue("id"))
	if errors.Is(err, auto.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "automation_not_found", "The automation could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("get automation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the automation.")
		return
	}
	httpx.JSON(w, http.StatusOK, a)
}

func (s *Server) createAutomation(w http.ResponseWriter, r *http.Request) {
	var input automationInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "Automation name is required.")
		return
	}
	if input.Status == "" {
		input.Status = "draft"
	}
	service := auto.NewService(s.pool, s.logger, s.cfg)
	steps := make([]auto.Step, 0, len(input.Steps))
	for _, step := range input.Steps {
		steps = append(steps, auto.Step{
			Position: step.Position,
			StepType: step.StepType,
			Config:   step.Config,
		})
	}
	item, err := service.Create(r.Context(), s.organizationID(r), s.claims(r).Subject, input.Name, input.Description, input.Status, input.Trigger, steps)
	if err != nil {
		s.logger.Error("create automation", "error", err)
		httpx.Error(w, http.StatusBadRequest, "invalid_automation", err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, item)
}

func (s *Server) updateAutomation(w http.ResponseWriter, r *http.Request) {
	var input automationInput
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_name", "Automation name is required.")
		return
	}
	item, err := auto.NewService(s.pool, s.logger, s.cfg).Update(r.Context(), s.organizationID(r), r.PathValue("id"), input.Name, input.Description, input.Status, input.Trigger)
	if errors.Is(err, auto.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "automation_not_found", "The automation could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("update automation", "error", err)
		httpx.Error(w, http.StatusBadRequest, "invalid_automation", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, item)
}

func (s *Server) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	if err := auto.NewService(s.pool, s.logger, s.cfg).Delete(r.Context(), s.organizationID(r), r.PathValue("id")); err != nil {
		if errors.Is(err, auto.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "automation_not_found", "The automation could not be found.")
			return
		}
		s.logger.Error("delete automation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not delete the automation.")
		return
	}
	httpx.NoContent(w)
}

func (s *Server) listAutomationExecutions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.pool.Query(r.Context(), `
		SELECT id, organization_id, automation_id, contact_id, status, current_step, next_run_at, last_error, started_at, completed_at, updated_at
		FROM automation_executions
		WHERE organization_id = $1 AND automation_id = $2
		ORDER BY started_at DESC
	`, s.organizationID(r), r.PathValue("id"))
	if err != nil {
		s.logger.Error("list automation executions", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load automation history.")
		return
	}
	defer rows.Close()

	items := make([]auto.Execution, 0)
	for rows.Next() {
		var item auto.Execution
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.AutomationID, &item.ContactID, &item.Status, &item.CurrentStep, &item.NextRunAt, &item.LastError, &item.StartedAt, &item.CompletedAt, &item.UpdatedAt); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load automation history.")
			return
		}
		items = append(items, item)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) triggerAutomation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ContactID string `json:"contact_id"`
	}
	if err := httpx.Decode(w, r, &input); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_body", "The request body is invalid.")
		return
	}
	if strings.TrimSpace(input.ContactID) == "" {
		httpx.Error(w, http.StatusBadRequest, "missing_contact", "A contact id is required.")
		return
	}
	if err := auto.NewService(s.pool, s.logger, s.cfg).UpsertExecution(r.Context(), s.organizationID(r), r.PathValue("id"), input.ContactID); err != nil {
		s.logger.Error("trigger automation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not start the automation for that contact.")
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) processDueAutomations(w http.ResponseWriter, r *http.Request) {
	count, err := auto.NewService(s.pool, s.logger, s.cfg).ProcessDue(r.Context(), s.organizationID(r), 25)
	if err != nil {
		s.logger.Error("process due automations", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not process pending automation steps.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"processed": count})
}

func (s *Server) activateAutomation(w http.ResponseWriter, r *http.Request) {
	service := auto.NewService(s.pool, s.logger, s.cfg)
	current, err := service.Get(r.Context(), s.organizationID(r), r.PathValue("id"))
	if errors.Is(err, auto.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "automation_not_found", "The automation could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("activate automation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the automation.")
		return
	}
	item, err := service.Update(r.Context(), s.organizationID(r), r.PathValue("id"), current.Name, current.Description, "active", current.Trigger)
	if errors.Is(err, auto.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "automation_not_found", "The automation could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("activate automation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not activate the automation.")
		return
	}
	httpx.JSON(w, http.StatusOK, item)
}

func (s *Server) pauseAutomation(w http.ResponseWriter, r *http.Request) {
	service := auto.NewService(s.pool, s.logger, s.cfg)
	current, err := service.Get(r.Context(), s.organizationID(r), r.PathValue("id"))
	if errors.Is(err, auto.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "automation_not_found", "The automation could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("pause automation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not load the automation.")
		return
	}
	item, err := service.Update(r.Context(), s.organizationID(r), r.PathValue("id"), current.Name, current.Description, "paused", current.Trigger)
	if errors.Is(err, auto.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "automation_not_found", "The automation could not be found.")
		return
	}
	if err != nil {
		s.logger.Error("pause automation", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not pause the automation.")
		return
	}
	httpx.JSON(w, http.StatusOK, item)
}

func (s *Server) setAutomationStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := s.pool.Exec(r.Context(), `UPDATE automations SET status = $3, updated_at = now() WHERE organization_id = $1 AND id = $2`, s.organizationID(r), r.PathValue("id"), strings.TrimSpace(r.PathValue("status"))); err != nil {
		s.logger.Error("set automation status", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "internal_error", "Could not update the automation status.")
		return
	}
	httpx.NoContent(w)
}

var _ = pgx.ErrNoRows
