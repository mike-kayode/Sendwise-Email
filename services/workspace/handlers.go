package workspace

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/kit"
	"github.com/khayode/email-platform/shared/contracts/events"
)

// Handler serves the Workspace HTTP contract: public tenant APIs for the
// gateway and internal service-to-service lookups for Identity.
type Handler struct {
	store  Store
	tokens *kit.Manager
	cfg    Config
	logger *slog.Logger
}

// NewHandler wires the Workspace HTTP handler.
func NewHandler(store Store, tokens *kit.Manager, cfg Config, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{store: store, tokens: tokens, cfg: cfg, logger: logger}
}

// Routes returns the Workspace HTTP router.
func (h *Handler) Routes() http.Handler {
	r := http.NewServeMux()

	// Service-to-service endpoints (fail closed without a shared secret).
	internal := kit.Chain(http.HandlerFunc(h.internal), kit.ServiceAuth(cfgSecret(h.cfg)))
	r.Handle("GET /internal/users/{userID}/memberships", internal)
	r.Handle("GET /internal/organizations/{organizationID}", internal)

	// Public endpoints reached through the gateway with a user JWT.
	auth := h.tokens.Middleware
	admin := kit.RequireRoles("owner", "admin")

	r.Handle("GET /organizations", auth(http.HandlerFunc(h.listOrganizations)))
	r.Handle("GET /organizations/{organizationID}", auth(http.HandlerFunc(h.getOrganization)))
	r.Handle("PATCH /organizations/{organizationID}", auth(admin(http.HandlerFunc(h.updateOrganization))))
	r.Handle("GET /members", auth(http.HandlerFunc(h.listMembers)))
	r.Handle("GET /invitations", auth(http.HandlerFunc(h.listInvitations)))
	r.Handle("POST /invitations", auth(admin(http.HandlerFunc(h.createInvitation))))
	r.Handle("DELETE /invitations/{invitationID}", auth(admin(http.HandlerFunc(h.revokeInvitation))))
	r.Handle("POST /invitations/accept", auth(http.HandlerFunc(h.acceptInvitation)))

	r.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		kit.JSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "workspace"})
	})
	r.HandleFunc("GET /readyz", h.ready)

	return kit.Chain(r, kit.Recover(h.logger), kit.AccessLog(h.logger), kit.RequestID)
}

func cfgSecret(cfg Config) string {
	return cfg.ServiceToken
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.store.Ping(ctx); err != nil {
		kit.Error(w, http.StatusServiceUnavailable, "not_ready", "Workspace database is not reachable.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// --- payloads ---------------------------------------------------------------

type invitationRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type acceptRequest struct {
	Token string `json:"token"`
}

type updateOrganizationRequest struct {
	Name string `json:"name,omitempty"`
	Slug string `json:"slug,omitempty"`
}

type membershipResponse struct {
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Role           string `json:"role"`
}

type memberResponse struct {
	UserID    string    `json:"user_id"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

var allowedInviteRoles = map[string]bool{
	"admin": true, "manager": true, "author": true, "analyst": true, "viewer": true,
}

// --- internal endpoints ------------------------------------------------------

func (h *Handler) internal(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/internal/users/"):
		userID := r.PathValue("userID")
		if _, err := uuid.Parse(userID); err != nil {
			kit.Error(w, http.StatusBadRequest, "invalid_user_id", "The user id must be a UUID.")
			return
		}
		memberships, err := h.store.MembershipsForUser(r.Context(), userID)
		if err != nil {
			h.logger.Error("list memberships", "error", err, "user_id", userID)
			kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
			return
		}
		resp := make([]membershipResponse, 0, len(memberships))
		for _, m := range memberships {
			resp = append(resp, membershipResponse{
				OrganizationID: m.Organization.ID,
				Name:           m.Organization.Name,
				Slug:           m.Organization.Slug,
				Role:           m.Role,
			})
		}
		kit.JSON(w, http.StatusOK, map[string]any{"memberships": resp})

	case strings.HasPrefix(r.URL.Path, "/internal/organizations/"):
		organizationID := r.PathValue("organizationID")
		if _, err := uuid.Parse(organizationID); err != nil {
			kit.Error(w, http.StatusBadRequest, "invalid_organization_id", "The organization id must be a UUID.")
			return
		}
		org, err := h.store.GetOrganization(r.Context(), organizationID)
		if errors.Is(err, ErrNotFound) {
			kit.Error(w, http.StatusNotFound, "not_found", "Organization not found.")
			return
		}
		if err != nil {
			h.logger.Error("load organization", "error", err)
			kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
			return
		}
		kit.JSON(w, http.StatusOK, org)

	default:
		kit.Error(w, http.StatusNotFound, "not_found", "Not found.")
	}
}

// --- public endpoints --------------------------------------------------------

func (h *Handler) listOrganizations(w http.ResponseWriter, r *http.Request) {
	claims, _ := kit.FromContext(r.Context())
	memberships, err := h.store.MembershipsForUser(r.Context(), claims.Subject)
	if err != nil {
		h.logger.Error("list organizations", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	data := make([]membershipResponse, 0, len(memberships))
	for _, m := range memberships {
		data = append(data, membershipResponse{
			OrganizationID: m.Organization.ID,
			Name:           m.Organization.Name,
			Slug:           m.Organization.Slug,
			Role:           m.Role,
		})
	}
	kit.JSON(w, http.StatusOK, map[string]any{"data": data})
}

func (h *Handler) getOrganization(w http.ResponseWriter, r *http.Request) {
	claims, _ := kit.FromContext(r.Context())
	organizationID := r.PathValue("organizationID")

	membership, err := h.store.Membership(r.Context(), organizationID, claims.Subject)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "not_found", "Organization not found.")
		return
	}
	if err != nil {
		h.logger.Error("load membership", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	org, err := h.store.GetOrganization(r.Context(), organizationID)
	if err != nil {
		h.logger.Error("load organization", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"id":         org.ID,
		"name":       org.Name,
		"slug":       org.Slug,
		"role":       membership.Role,
		"created_at": org.CreatedAt,
	})
}

func (h *Handler) updateOrganization(w http.ResponseWriter, r *http.Request) {
	claims, _ := kit.FromContext(r.Context())
	organizationID := r.PathValue("organizationID")

	if claims.OrganizationID != organizationID {
		kit.Error(w, http.StatusForbidden, "forbidden", "Your token is scoped to a different organization.")
		return
	}

	var req updateOrganizationRequest
	if err := kit.Decode(w, r, &req); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "The request body is not valid JSON.")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.TrimSpace(req.Slug)
	if req.Name == "" && req.Slug == "" {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "Provide a name or slug to update.")
		return
	}
	if req.Name != "" && len(req.Name) > 200 {
		kit.Error(w, http.StatusBadRequest, "invalid_name", "Name must be 200 characters or fewer.")
		return
	}
	if req.Slug != "" && !validSlug(req.Slug) {
		kit.Error(w, http.StatusBadRequest, "invalid_slug", "Slug must be lowercase letters, numbers, and hyphens.")
		return
	}

	org, err := h.store.UpdateOrganization(r.Context(), organizationID, req.Name, req.Slug, r.Header.Get("X-Request-ID"))
	if errors.Is(err, ErrSlugInUse) {
		kit.Error(w, http.StatusConflict, "slug_in_use", "That slug is already taken.")
		return
	}
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "not_found", "Organization not found.")
		return
	}
	if err != nil {
		h.logger.Error("update organization", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	kit.JSON(w, http.StatusOK, org)
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
	claims, _ := kit.FromContext(r.Context())
	if claims.OrganizationID == "" {
		kit.Error(w, http.StatusForbidden, "missing_organization", "Your token is not scoped to an organization.")
		return
	}
	members, err := h.store.ListMembers(r.Context(), claims.OrganizationID)
	if err != nil {
		h.logger.Error("list members", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	data := make([]memberResponse, 0, len(members))
	for _, m := range members {
		data = append(data, memberResponse{UserID: m.UserID, Role: m.Role, CreatedAt: m.CreatedAt})
	}
	kit.JSON(w, http.StatusOK, map[string]any{"data": data})
}

func (h *Handler) listInvitations(w http.ResponseWriter, r *http.Request) {
	claims, _ := kit.FromContext(r.Context())
	if claims.OrganizationID == "" {
		kit.Error(w, http.StatusForbidden, "missing_organization", "Your token is not scoped to an organization.")
		return
	}
	invitations, err := h.store.ListInvitations(r.Context(), claims.OrganizationID)
	if err != nil {
		h.logger.Error("list invitations", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"data": invitations})
}

func (h *Handler) createInvitation(w http.ResponseWriter, r *http.Request) {
	claims, _ := kit.FromContext(r.Context())
	if claims.OrganizationID == "" {
		kit.Error(w, http.StatusForbidden, "missing_organization", "Your token is not scoped to an organization.")
		return
	}

	var req invitationRequest
	if err := kit.Decode(w, r, &req); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "The request body is not valid JSON.")
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !validEmail(req.Email) {
		kit.Error(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
		return
	}
	if req.Role == "" {
		req.Role = "viewer"
	}
	if !allowedInviteRoles[req.Role] {
		kit.Error(w, http.StatusForbidden, "invalid_role", "Choose one of admin, manager, author, analyst, or viewer.")
		return
	}

	token, err := randomToken()
	if err != nil {
		h.logger.Error("generate invitation token", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	invitation, err := h.store.CreateInvitation(r.Context(), InvitationCreate{
		OrganizationID: claims.OrganizationID,
		Email:          req.Email,
		Role:           req.Role,
		InvitedBy:      claims.Subject,
		TokenHash:      HashToken(token),
		ExpiresAt:      time.Now().Add(h.cfg.InvitationTTL),
	})
	if err != nil {
		h.logger.Error("create invitation", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	kit.JSON(w, http.StatusCreated, map[string]any{
		"invitation": invitation,
		"token":      token,
		"accept_url": h.cfg.AppBaseURL + "/auth/accept-invite?token=" + token,
	})
}

func (h *Handler) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	claims, _ := kit.FromContext(r.Context())
	if claims.OrganizationID == "" {
		kit.Error(w, http.StatusForbidden, "missing_organization", "Your token is not scoped to an organization.")
		return
	}
	invitationID := r.PathValue("invitationID")
	if _, err := uuid.Parse(invitationID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_invitation_id", "The invitation id must be a UUID.")
		return
	}
	err := h.store.RevokeInvitation(r.Context(), claims.OrganizationID, invitationID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "not_found", "Invitation not found.")
		return
	}
	if err != nil {
		h.logger.Error("revoke invitation", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}
	kit.NoContent(w)
}

func (h *Handler) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	claims, _ := kit.FromContext(r.Context())
	var req acceptRequest
	if err := kit.Decode(w, r, &req); err != nil || strings.TrimSpace(req.Token) == "" {
		kit.Error(w, http.StatusBadRequest, "invalid_request", "An invitation token is required.")
		return
	}

	membership, err := h.store.AcceptInvitation(r.Context(), HashToken(req.Token), claims.Subject, claims.Email, r.Header.Get("X-Request-ID"))
	switch {
	case errors.Is(err, ErrInvitationEmail):
		kit.Error(w, http.StatusForbidden, "invitation_email_mismatch", "This invitation was issued to a different email address.")
		return
	case errors.Is(err, ErrInvitationInvalid):
		kit.Error(w, http.StatusBadRequest, "invalid_invitation", "This invitation is invalid, expired, or already used.")
		return
	case err != nil:
		h.logger.Error("accept invitation", "error", err)
		kit.Error(w, http.StatusInternalServerError, "internal_error", "An unexpected error occurred.")
		return
	}

	kit.JSON(w, http.StatusOK, map[string]any{
		"organization_id": membership.OrganizationID,
		"role":            membership.Role,
		"message":         "Invitation accepted.",
	})
}

// --- helpers -----------------------------------------------------------------

// HashToken hashes a high-entropy token with SHA-256 for at-rest storage.
func HashToken(token string) string { return kit.HashSecret(token) }

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func validEmail(raw string) bool {
	if raw == "" || len(raw) > 320 {
		return false
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil {
		return false
	}
	return addr.Address == raw
}

func validSlug(slug string) bool {
	if slug == "" || len(slug) > 64 {
		return false
	}
	for _, r := range slug {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return slug[0] != '-' && slug[len(slug)-1] != '-'
}

// ProvisionFromEvent applies an identity.user_registered event through the
// store's idempotent provisioning path. It is the consumer entry point.
func ProvisionFromEvent(ctx context.Context, store Store, data []byte, logger *slog.Logger) error {
	var envelope events.Envelope[events.UserRegistered]
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if envelope.ID == "" || envelope.Data.UserID == "" {
		return errors.New("identity.user_registered envelope is missing required fields")
	}
	org, created, err := store.ProvisionUserOrganization(ctx, envelope.ID, envelope.Data.UserID, envelope.Data.OrganizationName, envelope.CorrelationID)
	if err != nil {
		return err
	}
	if created && org != nil && logger != nil {
		logger.Info("provisioned organization",
			"organization_id", org.ID,
			"slug", org.Slug,
			"user_id", envelope.Data.UserID,
		)
	}
	return nil
}
