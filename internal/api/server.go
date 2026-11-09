package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/khayode/email-platform/internal/auth"
	"github.com/khayode/email-platform/internal/campaigns"
	"github.com/khayode/email-platform/internal/config"
	"github.com/khayode/email-platform/internal/httpx"
	"github.com/khayode/email-platform/internal/observability"
)

type Server struct {
	cfg                    config.Config
	pool                   *pgxpool.Pool
	tokens                 *auth.Manager
	campaigns              *campaigns.Service
	apiKeys                *auth.Service
	delivery               *DeliveryHandler
	dnsResolver            DNSResolver
	stalwartDomainHoster   stalwartDomainHoster
	logger                 *slog.Logger
	formConfirmationSender func(context.Context, string, string, string) error
}

func New(
	cfg config.Config,
	pool *pgxpool.Pool,
	tokens *auth.Manager,
	campaignService *campaigns.Service,
	apiKeyService *auth.Service,
	publisher publisher,
	logger *slog.Logger,
) *Server {
	server := &Server{
		cfg:         cfg,
		pool:        pool,
		tokens:      tokens,
		campaigns:   campaignService,
		apiKeys:     apiKeyService,
		delivery:    NewDeliveryHandler(pool, tokens, logger, nil, publisher, cfg.WebhookSecret, cfg.MaxBounceRate, cfg.MaxComplaintRate),
		dnsResolver: newDNSResolver(cfg.DNSResolverAddr, cfg.DNSResolverTimeout),
		logger:      logger,
	}
	server.stalwartDomainHoster = stalwartDomainProvisionFunc(func(ctx context.Context, domain string) error {
		return ensureStalwartDomain(ctx, cfg, domain)
	})
	server.formConfirmationSender = func(ctx context.Context, to, formName, confirmURL string) error {
		return sendFormConfirmationEmail(ctx, cfg, to, formName, confirmURL)
	}
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.Handle("GET /metrics", observability.MetricsHandler())

	mux.HandleFunc("POST /api/v1/auth/register", s.register)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("GET /api/v1/auth/verify", s.verifyAccount)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", s.forgotPassword)
	mux.HandleFunc("POST /api/v1/auth/reset-password", s.resetPassword)

	mux.HandleFunc("POST /api/v1/public/subscribe", s.publicSubscribe)
	mux.HandleFunc("GET /api/v1/public/unsubscribe", s.unsubscribePage)
	mux.HandleFunc("POST /api/v1/public/unsubscribe", s.unsubscribeAPI)
	mux.HandleFunc("GET /t/open/{token}", s.trackOpen)
	mux.HandleFunc("GET /t/click/{token}", s.trackClick)

	protect := func(handler http.HandlerFunc, roles ...string) http.Handler {
		var next http.Handler = handler
		if len(roles) > 0 {
			next = auth.RequireRoles(roles...)(next)
		}
		// Order matters: tokens middleware runs first (validates the JWT and
		// stores claims), then the FK self-heal sees those claims, then roles.
		next = s.ensureActorMiddleware(next)
		return s.tokens.Middleware(next)
	}
	audienceManagers := []string{auth.RoleOwner, auth.RoleAdmin, auth.RoleManager}
	campaignEditors := []string{auth.RoleOwner, auth.RoleAdmin, auth.RoleManager, auth.RoleAuthor}
	campaignManagers := []string{auth.RoleOwner, auth.RoleAdmin, auth.RoleManager}
	domainManagers := []string{auth.RoleOwner, auth.RoleAdmin}

	mux.Handle("GET /api/v1/auth/me", protect(s.me))

	mux.Handle("GET /api/v1/contacts", protect(s.listContacts))
	mux.Handle("POST /api/v1/contacts", protect(s.createContact, audienceManagers...))
	mux.Handle("POST /api/v1/contacts/import", protect(s.importContacts, audienceManagers...))
	mux.Handle("GET /api/v1/contact-imports/{id}", protect(s.getContactImport))
	mux.Handle("GET /api/v1/contacts/export", protect(s.exportContacts))
	mux.Handle("GET /api/v1/contacts/{id}", protect(s.getContact))
	mux.Handle("PATCH /api/v1/contacts/{id}", protect(s.updateContact, audienceManagers...))
	mux.Handle("DELETE /api/v1/contacts/{id}", protect(s.deleteContact, audienceManagers...))

	mux.Handle("GET /api/v1/lists", protect(s.listAudienceLists))
	mux.Handle("POST /api/v1/lists", protect(s.createAudienceList, audienceManagers...))
	mux.Handle("GET /api/v1/lists/{id}", protect(s.getAudienceList))
	mux.Handle("PATCH /api/v1/lists/{id}", protect(s.updateAudienceList, audienceManagers...))
	mux.Handle("GET /api/v1/lists/{id}/contacts", protect(s.listAudienceListContacts))
	mux.Handle("POST /api/v1/lists/{id}/contacts", protect(s.addAudienceListContacts, audienceManagers...))
	mux.Handle("DELETE /api/v1/lists/{id}/contacts/{contactID}", protect(s.removeAudienceListContact, audienceManagers...))
	mux.Handle("GET /api/v1/lists/{id}/contacts/export", protect(s.exportAudienceListContacts))
	mux.Handle("DELETE /api/v1/lists/{id}", protect(s.deleteAudienceList, audienceManagers...))
	mux.Handle("GET /api/v1/tags", protect(s.listTags))
	mux.Handle("POST /api/v1/tags", protect(s.createTag, audienceManagers...))
	mux.Handle("PATCH /api/v1/tags/{id}", protect(s.updateTag, audienceManagers...))
	mux.Handle("DELETE /api/v1/tags/{id}", protect(s.deleteTag, audienceManagers...))
	mux.Handle("GET /api/v1/contacts/{id}/tags", protect(s.listContactTags))
	mux.Handle("POST /api/v1/contacts/tags", protect(s.bulkAssignContactTag, audienceManagers...))
	mux.Handle("POST /api/v1/contacts/{id}/tags/{tagID}", protect(s.assignContactTag, audienceManagers...))
	mux.Handle("DELETE /api/v1/contacts/{id}/tags/{tagID}", protect(s.unassignContactTag, audienceManagers...))
	mux.Handle("GET /api/v1/segments", protect(s.listSegments))
	mux.Handle("POST /api/v1/segments", protect(s.createSegment, audienceManagers...))
	mux.Handle("POST /api/v1/segments/preview", protect(s.previewSegment))
	mux.Handle("GET /api/v1/segments/{id}/contacts", protect(s.listSegmentContacts))
	// Forms module (protected).
	mux.Handle("GET /api/v1/forms", protect(s.listForms))
	mux.Handle("POST /api/v1/forms", protect(s.createForm, audienceManagers...))
	mux.Handle("GET /api/v1/forms/{id}", protect(s.getForm))
	mux.Handle("PATCH /api/v1/forms/{id}", protect(s.updateForm, audienceManagers...))
	mux.Handle("DELETE /api/v1/forms/{id}", protect(s.deleteForm, audienceManagers...))
	mux.Handle("POST /api/v1/forms/{id}/publish", protect(s.publishForm, audienceManagers...))
	mux.Handle("POST /api/v1/forms/{id}/unpublish", protect(s.unpublishForm, audienceManagers...))
	mux.Handle("GET /api/v1/forms/{id}/submissions", protect(s.listFormSubmissions))
	mux.HandleFunc("GET /api/v1/public/forms/{slug}", s.publicForm)
	mux.HandleFunc("POST /api/v1/public/forms/{slug}/submit", s.submitPublicForm)
	mux.HandleFunc("GET /api/v1/public/forms/{slug}/confirm", s.confirmPublicForm)

	mux.Handle("DELETE /api/v1/segments/{id}", protect(s.deleteSegment, audienceManagers...))
	mux.Handle("GET /api/v1/templates", protect(s.listTemplates))
	mux.Handle("POST /api/v1/templates", protect(s.createTemplate, campaignEditors...))
	mux.Handle("GET /api/v1/templates/{id}", protect(s.getTemplate))
	mux.Handle("PATCH /api/v1/templates/{id}", protect(s.updateTemplate, campaignEditors...))
	mux.Handle("DELETE /api/v1/templates/{id}", protect(s.deleteTemplate, campaignManagers...))

	mux.Handle("GET /api/v1/automations", protect(s.listAutomations))
	mux.Handle("POST /api/v1/automations", protect(s.createAutomation, campaignEditors...))
	mux.Handle("GET /api/v1/automations/{id}", protect(s.getAutomation))
	mux.Handle("PATCH /api/v1/automations/{id}", protect(s.updateAutomation, campaignEditors...))
	mux.Handle("DELETE /api/v1/automations/{id}", protect(s.deleteAutomation, campaignManagers...))
	mux.Handle("GET /api/v1/automations/{id}/executions", protect(s.listAutomationExecutions))
	mux.Handle("POST /api/v1/automations/{id}/trigger", protect(s.triggerAutomation, campaignEditors...))
	mux.Handle("POST /api/v1/automations/{id}/activate", protect(s.activateAutomation, campaignManagers...))
	mux.Handle("POST /api/v1/automations/{id}/pause", protect(s.pauseAutomation, campaignManagers...))
	mux.Handle("POST /api/v1/automations/process-due", protect(s.processDueAutomations, campaignManagers...))

	mux.Handle("GET /api/v1/campaigns", protect(s.listCampaigns))
	mux.Handle("POST /api/v1/campaigns", protect(s.createCampaign, campaignEditors...))
	mux.Handle("GET /api/v1/campaigns/{id}", protect(s.getCampaign))
	mux.Handle("PATCH /api/v1/campaigns/{id}", protect(s.updateCampaign, campaignEditors...))
	mux.Handle("DELETE /api/v1/campaigns/{id}", protect(s.deleteCampaign, campaignManagers...))
	mux.Handle("POST /api/v1/campaigns/{id}/validate", protect(s.validateCampaign, campaignEditors...))
	mux.Handle("POST /api/v1/campaigns/{id}/schedule", protect(s.scheduleCampaign, campaignManagers...))
	mux.Handle("POST /api/v1/campaigns/{id}/send", protect(s.sendCampaign, campaignManagers...))
	mux.Handle("POST /api/v1/campaigns/{id}/pause", protect(s.pauseCampaign, campaignManagers...))
	mux.Handle("POST /api/v1/campaigns/{id}/resume", protect(s.resumeCampaign, campaignManagers...))
	mux.Handle("POST /api/v1/campaigns/{id}/cancel", protect(s.cancelCampaign, campaignManagers...))

	mux.Handle("GET /api/v1/domains", protect(s.listDomains))
	mux.Handle("POST /api/v1/domains", protect(s.createDomain, domainManagers...))
	mux.Handle("POST /api/v1/domains/{id}/verify", protect(s.verifyDomain, domainManagers...))
	mux.Handle("POST /api/v1/domains/{id}/providers/{provider}", protect(s.configureDomainProvider, domainManagers...))
	mux.Handle("POST /api/v1/domains/{id}/providers/{provider}/verify", protect(s.verifyDomainProvider, domainManagers...))
	mux.Handle("PUT /api/v1/domains/{id}/warmup-profile", protect(s.updateDomainWarmupProfile, domainManagers...))
	mux.Handle("GET /api/v1/domains/{id}/dns-records", protect(s.domainDNSRecords))

	// Dead-letter queue and suppression management (protected).
	mux.Handle("GET /api/v1/delivery/dead-letter", protect(s.delivery.listDeadLetter, audienceManagers...))
	mux.Handle("POST /api/v1/delivery/dead-letter/{id}/replay", protect(s.delivery.replayDeadLetter, campaignManagers...))
	mux.Handle("GET /api/v1/suppressions", protect(s.delivery.listSuppressions, audienceManagers...))
	mux.Handle("POST /api/v1/suppressions", protect(s.delivery.createSuppression, audienceManagers...))
	mux.Handle("DELETE /api/v1/suppressions/{email}", protect(s.delivery.deleteSuppression, audienceManagers...))

	// Inbound bounce / feedback-loop webhooks (HMAC-secured, idempotent).
	mux.Handle("POST /api/v1/webhooks/bounce", s.delivery.requireWebhookAuth(s.delivery.bounceWebhook))
	mux.Handle("POST /api/v1/webhooks/complaint", s.delivery.requireWebhookAuth(s.delivery.complaintWebhook))

	mux.Handle("GET /api/v1/analytics/overview", protect(s.analyticsOverview))
	mux.Handle("GET /api/v1/analytics/campaigns/{id}", protect(s.campaignReport))

	mux.Handle("GET /api/v1/api-keys", protect(s.listAPIKeys, auth.RoleOwner, auth.RoleAdmin))
	mux.Handle("POST /api/v1/api-keys", protect(s.createAPIKey, auth.RoleOwner, auth.RoleAdmin))
	mux.Handle("DELETE /api/v1/api-keys/{id}", protect(s.deleteAPIKey, auth.RoleOwner, auth.RoleAdmin))

	return httpx.Chain(
		mux,
		httpx.RequestID,
		httpx.Recover(s.logger),
		httpx.AccessLog(s.logger),
		httpx.CORS(s.cfg.CORSOrigins),
		observability.HTTPMiddleware,
	)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "not_ready", "The database is unavailable.")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) claims(r *http.Request) *auth.Claims {
	claims, _ := auth.FromContext(r.Context())
	return claims
}

func (s *Server) organizationID(r *http.Request) string {
	return s.claims(r).OrganizationID
}

func pagination(r *http.Request) (limit, offset int) {
	limit = 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}

	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			offset = n
		}
	}
	return limit, offset
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

func nullIfEmpty(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

type paginationResponse struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type listResponse[T any] struct {
	Data       []T                `json:"data"`
	Pagination paginationResponse `json:"pagination"`
}
