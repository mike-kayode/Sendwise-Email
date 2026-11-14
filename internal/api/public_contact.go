package api

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/httpx"
)

const maxContactRequestBytes = 16 << 10

var contactTopics = map[string]struct{}{
	"Plans and pricing":           {},
	"Migrating my sending":        {},
	"Domain verification help":    {},
	"Transactional email and API": {},
	"Something else":              {},
}

type publicContactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Topic   string `json:"topic"`
	Message string `json:"message"`
	Website string `json:"website"`
}

func (s *Server) submitPublicContact(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxContactRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request publicContactRequest
	if err := decoder.Decode(&request); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Enter a valid contact form submission.")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		httpx.Error(w, http.StatusBadRequest, "invalid_request", "Enter a valid contact form submission.")
		return
	}
	if strings.TrimSpace(request.Website) != "" {
		httpx.JSON(w, http.StatusAccepted, map[string]string{"message": "Thank you for contacting Sendwise."})
		return
	}

	request.Name = strings.TrimSpace(request.Name)
	request.Email = strings.ToLower(strings.TrimSpace(request.Email))
	request.Topic = strings.TrimSpace(request.Topic)
	request.Message = strings.TrimSpace(request.Message)
	if err := validatePublicContact(request); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_contact", err.Error())
		return
	}

	var id string
	err := s.pool.QueryRow(r.Context(), `
		INSERT INTO contact_messages (name, email, topic, message, notification_status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, request.Name, request.Email, request.Topic, request.Message, contactNotificationStatus(s.cfg.ContactNotifyEmail)).Scan(&id)
	if err != nil {
		s.logger.Error("store public contact message", "error", err)
		httpx.Error(w, http.StatusInternalServerError, "contact_unavailable", "We could not store your message. Please try again.")
		return
	}

	if s.cfg.ContactNotifyEmail != "" {
		if err := s.contactNotifier(r.Context(), request.Name, request.Email, request.Topic, request.Message); err != nil {
			s.logger.Error("send contact notification", "error", err)
			if _, updateErr := s.pool.Exec(r.Context(), `
				UPDATE contact_messages SET notification_status = 'failed' WHERE id = $1
			`, id); updateErr != nil {
				s.logger.Error("record contact notification failure", "error", updateErr)
			}
		} else if _, err := s.pool.Exec(r.Context(), `
			UPDATE contact_messages SET notification_status = 'sent' WHERE id = $1
		`, id); err != nil {
			s.logger.Error("record contact notification success", "error", err)
		}
	}

	httpx.JSON(w, http.StatusAccepted, map[string]string{"message": "Thank you for contacting Sendwise."})
}

func contactNotificationStatus(to string) string {
	if strings.TrimSpace(to) == "" {
		return "disabled"
	}
	return "pending"
}

func validatePublicContact(request publicContactRequest) error {
	if n := utf8.RuneCountInString(request.Name); n < 2 || n > 120 {
		return fmt.Errorf("Name must be between 2 and 120 characters.")
	}
	if len(request.Email) > 254 {
		return fmt.Errorf("Enter a valid email address.")
	}
	address, err := mail.ParseAddress(request.Email)
	if err != nil || !strings.EqualFold(address.Address, request.Email) {
		return fmt.Errorf("Enter a valid email address.")
	}
	if _, ok := contactTopics[request.Topic]; !ok {
		return fmt.Errorf("Choose a valid topic.")
	}
	if n := utf8.RuneCountInString(request.Message); n < 20 || n > 4000 {
		return fmt.Errorf("Message must be between 20 and 4000 characters.")
	}
	return nil
}

func sendContactNotification(ctx context.Context, cfg config.Config, name, email, topic, message string) error {
	body := fmt.Sprintf(
		"<h1>New Sendwise contact message</h1><p><strong>Name:</strong> %s</p><p><strong>Email:</strong> %s</p><p><strong>Topic:</strong> %s</p><p><strong>Message:</strong></p><p>%s</p>",
		html.EscapeString(name),
		html.EscapeString(email),
		html.EscapeString(topic),
		strings.ReplaceAll(html.EscapeString(message), "\n", "<br>"),
	)
	return sendPlatformSMTP(ctx, cfg, cfg.ContactNotifyEmail, "Sendwise contact: "+topic, body)
}
