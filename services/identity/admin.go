package identity

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/khayode/email-platform/internal/delivery"
	"github.com/khayode/email-platform/internal/kit"
)

const (
	PlatformRoleNone         = "none"
	PlatformRoleSupportAdmin = "support_admin"
	PlatformRoleSuperAdmin   = "super_admin"
	adminRefreshSessionTTL   = time.Hour
	adminMFAFreshness        = time.Hour
	adminMFAReauthFreshness  = 5 * time.Minute
)

type AdminPermission string

const (
	AdminViewOverview       AdminPermission = "view_overview"
	AdminReadUsers          AdminPermission = "read_users"
	AdminResendVerification AdminPermission = "resend_verification"
	AdminReadWorkspaces     AdminPermission = "read_workspaces"
	AdminReadDomains        AdminPermission = "read_domains"
	AdminManageDomains      AdminPermission = "manage_domains"
	AdminReadCampaigns      AdminPermission = "read_campaigns"
	AdminManageCampaigns    AdminPermission = "manage_campaigns"
	AdminReadDelivery       AdminPermission = "read_delivery"
	AdminReadAudit          AdminPermission = "read_audit"
	AdminReadSettings       AdminPermission = "read_settings"
	AdminExportAudit        AdminPermission = "export_audit"
	AdminManageWorkspaces   AdminPermission = "manage_workspaces"
	AdminManageSettings     AdminPermission = "manage_settings"
	AdminManagePlatformRole AdminPermission = "manage_platform_role"
	AdminManageSuppressions AdminPermission = "manage_suppressions"
	AdminInspectMessage     AdminPermission = "inspect_message"
	AdminManageCustomers    AdminPermission = "manage_customers"
)

var adminPermissions = map[string]map[AdminPermission]struct{}{
	PlatformRoleSupportAdmin: {
		AdminViewOverview:       {},
		AdminReadUsers:          {},
		AdminResendVerification: {},
		AdminReadWorkspaces:     {},
		AdminReadDomains:        {},
		AdminReadCampaigns:      {},
		AdminReadDelivery:       {},
		AdminReadAudit:          {},
		AdminReadSettings:       {},
	},
	PlatformRoleSuperAdmin: {
		AdminViewOverview:       {},
		AdminReadUsers:          {},
		AdminResendVerification: {},
		AdminReadWorkspaces:     {},
		AdminReadDomains:        {},
		AdminReadCampaigns:      {},
		AdminReadDelivery:       {},
		AdminReadAudit:          {},
		AdminExportAudit:        {},
		AdminManageWorkspaces:   {},
		AdminManageDomains:      {},
		AdminManageCampaigns:    {},
		AdminManageSettings:     {},
		AdminReadSettings:       {},
		AdminManageSuppressions: {},
		AdminManagePlatformRole: {},
		AdminInspectMessage:     {},
		AdminManageCustomers:    {},
	},
}

func CanAdmin(role string, permission AdminPermission) bool {
	_, ok := adminPermissions[role][permission]
	return ok
}

type adminSecurityStore interface {
	GetAdminMFA(context.Context, string) (adminMFAState, error)
	SaveAdminMFASetup(context.Context, string, string, []string) error
	EnableAdminMFA(context.Context, string) error
	ConsumeAdminRecoveryCode(context.Context, string, string) (bool, error)
	MarkAdminSessionMFAVerified(context.Context, string, string, time.Time) error
	AdminMFALocked(context.Context, string) (bool, error)
	RecordAdminMFAFailure(context.Context, string) (bool, error)
	ClearAdminMFAFailures(context.Context, string) error
	WriteAdminAudit(context.Context, adminAuditRecord) error
	ListAdminAudit(context.Context, int, int) ([]adminAuditRecord, error)
}

type adminUserSummary struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	PlatformRole    string     `json:"platform_role"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type adminUserCounts struct {
	Total     int `json:"total"`
	Active    int `json:"active"`
	Pending   int `json:"pending"`
	Suspended int `json:"suspended"`
	Closed    int `json:"closed"`
}

type adminUserStore interface {
	ListAdminUsers(context.Context, string, string, int, int) ([]adminUserSummary, int, error)
	GetAdminUser(context.Context, string) (adminUserSummary, error)
	GetAdminUserCounts(context.Context) (adminUserCounts, error)
}

type adminUserMutationStore interface {
	SetAdminUserStatus(context.Context, string, string, adminAuditRecord) (adminUserSummary, int, error)
	RevokeAdminUserSessions(context.Context, string, adminAuditRecord) (int, error)
	VerifyAdminUserEmail(context.Context, string, adminAuditRecord) (adminUserSummary, error)
	DemoteAdminUser(context.Context, string, adminAuditRecord) (adminUserSummary, int, error)
}

type adminWorkspaceNote struct {
	ID          int64     `json:"id,omitempty"`
	ActorUserID *string   `json:"actor_user_id,omitempty"`
	Note        string    `json:"note"`
	Reason      string    `json:"reason"`
	CreatedAt   time.Time `json:"created_at"`
}

type adminWorkspaceNotesStore interface {
	ListAdminWorkspaceNotes(context.Context, string, int) ([]adminWorkspaceNote, error)
}

type adminAuditSearchStore interface {
	ListAdminAuditFiltered(context.Context, string, string, int, int) ([]adminAuditRecord, int, error)
}

type adminActor struct {
	user    *User
	claims  *kit.Claims
	session *Session
}

type adminActorKey struct{}

func (h *Handler) adminRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/session", h.getAdminSession)
	mux.HandleFunc("POST /admin/mfa/setup", h.setupAdminMFA)
	mux.Handle("POST /admin/mfa/verify", kit.RateLimit(kit.NewRateLimiter(10, 5))(http.HandlerFunc(h.verifyAdminMFA)))
	mux.Handle("GET /admin/permissions", h.requireAdminMFA(AdminReadAudit, http.HandlerFunc(h.getAdminPermissions)))
	mux.Handle("GET /admin/overview", h.requireAdminMFA(AdminViewOverview, http.HandlerFunc(h.getAdminOverview)))
	mux.Handle("GET /admin/audit", h.requireAdminMFA(AdminReadAudit, http.HandlerFunc(h.getAdminAudit)))
	mux.Handle("POST /admin/audit/export", h.requireAdminMFA(AdminExportAudit, http.HandlerFunc(h.exportAdminAudit)))
	mux.Handle("GET /admin/users", h.requireAdminMFA(AdminReadUsers, http.HandlerFunc(h.getAdminUsers)))
	mux.Handle("GET /admin/users/{userID}", h.requireAdminMFA(AdminReadUsers, http.HandlerFunc(h.getAdminUser)))
	mux.Handle("POST /admin/users/{userID}/resend-verification", kit.RateLimit(kit.NewRateLimiter(10, 5))(h.requireAdminMFA(AdminResendVerification, http.HandlerFunc(h.resendAdminUserVerification))))
	mux.Handle("POST /admin/users/{userID}/verify-email", h.requireAdminMFA(AdminManageCustomers, http.HandlerFunc(h.verifyAdminUserEmail)))
	mux.Handle("POST /admin/users/{userID}/password-reset", kit.RateLimit(kit.NewRateLimiter(10, 5))(h.requireAdminMFA(AdminManageCustomers, http.HandlerFunc(h.sendAdminPasswordReset))))
	mux.Handle("PATCH /admin/users/{userID}/platform-role", h.requireAdminMFA(AdminManagePlatformRole, http.HandlerFunc(h.demoteAdminUser)))
	mux.Handle("PATCH /admin/users/{userID}/status", h.requireAdminMFA(AdminManageCustomers, http.HandlerFunc(h.updateAdminUserStatus)))
	mux.Handle("POST /admin/users/{userID}/revoke-sessions", h.requireAdminMFA(AdminManageCustomers, http.HandlerFunc(h.revokeAdminUserSessions)))
	mux.Handle("GET /admin/workspaces", h.requireAdminMFA(AdminReadWorkspaces, http.HandlerFunc(h.getAdminWorkspaces)))
	mux.Handle("GET /admin/workspaces/{workspaceID}", h.requireAdminMFA(AdminReadWorkspaces, http.HandlerFunc(h.getAdminWorkspace)))
	mux.Handle("GET /admin/workspaces/{workspaceID}/status", h.requireAdminMFA(AdminReadWorkspaces, http.HandlerFunc(h.getAdminWorkspaceStatus)))
	mux.Handle("PATCH /admin/workspaces/{workspaceID}/status", h.requireAdminMFA(AdminManageWorkspaces, http.HandlerFunc(h.setAdminWorkspaceStatus)))
	mux.Handle("GET /admin/domains", h.requireAdminMFA(AdminReadDomains, http.HandlerFunc(h.getAdminDomains)))
	mux.Handle("GET /admin/domains/{domainID}", h.requireAdminMFA(AdminReadDomains, http.HandlerFunc(h.getAdminDomain)))
	mux.Handle("GET /admin/domains/{domainID}/quotas", h.requireAdminMFA(AdminReadDomains, http.HandlerFunc(h.getAdminDomainQuotas)))
	mux.Handle("PUT /admin/domains/{domainID}/quotas", h.requireAdminMFA(AdminManageDomains, http.HandlerFunc(h.setAdminDomainQuotas)))
	mux.Handle("GET /admin/domains/{domainID}/warmup", h.requireAdminMFA(AdminReadDomains, http.HandlerFunc(h.getAdminDomainWarmup)))
	mux.Handle("PUT /admin/domains/{domainID}/warmup-override", h.requireAdminMFA(AdminManageDomains, http.HandlerFunc(h.setAdminDomainWarmupOverride)))
	mux.Handle("DELETE /admin/domains/{domainID}/warmup-override", h.requireAdminMFA(AdminManageDomains, http.HandlerFunc(h.clearAdminDomainWarmupOverride)))
	mux.Handle("POST /admin/domains/{domainID}/warmup-graduation", h.requireAdminMFA(AdminManageDomains, http.HandlerFunc(h.graduateAdminDomainWarmup)))
	mux.Handle("DELETE /admin/domains/{domainID}/warmup-graduation", h.requireAdminMFA(AdminManageDomains, http.HandlerFunc(h.revokeAdminDomainWarmupGraduation)))
	mux.Handle("POST /admin/domains/{domainID}/verify", h.requireAdminMFA(AdminManageDomains, http.HandlerFunc(h.recheckAdminDomain)))
	mux.Handle("PATCH /admin/domains/{domainID}/disabled", h.requireAdminMFA(AdminManageDomains, http.HandlerFunc(h.setAdminDomainDisabled)))
	mux.Handle("DELETE /admin/domains/{domainID}", h.requireAdminMFA(AdminManageDomains, http.HandlerFunc(h.deleteAdminDomain)))
	mux.Handle("POST /admin/domains/{domainID}/resolve-conflict", h.requireAdminMFA(AdminManageDomains, http.HandlerFunc(h.resolveAdminDomainConflict)))
	mux.Handle("GET /admin/quota-defaults", h.requireAdminMFA(AdminReadSettings, http.HandlerFunc(h.getAdminQuotaDefaults)))
	mux.Handle("PUT /admin/quota-defaults", h.requireAdminMFA(AdminManageSettings, http.HandlerFunc(h.setAdminQuotaDefaults)))
	mux.Handle("GET /admin/quotas/near-limit", h.requireAdminMFA(AdminViewOverview, http.HandlerFunc(h.getAdminQuotasNearLimit)))
	mux.Handle("GET /admin/campaigns", h.requireAdminMFA(AdminReadCampaigns, http.HandlerFunc(h.getAdminCampaigns)))
	mux.Handle("POST /admin/campaigns/{campaignID}/stop", h.requireAdminMFA(AdminManageCampaigns, http.HandlerFunc(h.stopAdminCampaign)))
	mux.Handle("GET /admin/campaigns/{campaignID}/delivery", h.requireAdminMFA(AdminReadCampaigns, http.HandlerFunc(h.getAdminCampaignDelivery)))
	mux.Handle("POST /admin/campaigns/{campaignID}/resume-deferred", h.requireAdminMFA(AdminManageCampaigns, http.HandlerFunc(h.resumeAdminCampaignDeferredJobs)))
	mux.Handle("POST /admin/campaigns/{campaignID}/replay-dead-letter", h.requireAdminMFA(AdminManageCampaigns, http.HandlerFunc(h.replayAdminCampaignDeadLetters)))
	mux.Handle("GET /admin/transactional-messages", h.requireAdminMFA(AdminReadDelivery, http.HandlerFunc(h.searchAdminTransactionalMessages)))
	mux.Handle("GET /admin/suppressions", h.requireAdminMFA(AdminManageSuppressions, http.HandlerFunc(h.getAdminSuppressions)))
	mux.Handle("POST /admin/suppressions", h.requireAdminMFA(AdminManageSuppressions, http.HandlerFunc(h.createAdminSuppression)))
	mux.Handle("DELETE /admin/suppressions/{id}", h.requireAdminMFA(AdminManageSuppressions, http.HandlerFunc(h.deleteAdminSuppression)))
	mux.Handle("GET /admin/workspaces/{workspaceID}/notes", h.requireAdminMFA(AdminManageWorkspaces, http.HandlerFunc(h.getAdminWorkspaceNotes)))
	mux.Handle("POST /admin/workspaces/{workspaceID}/notes", h.requireAdminMFA(AdminManageWorkspaces, http.HandlerFunc(h.addAdminWorkspaceNote)))
	return h.requirePlatformAdmin(mux)
}

func (h *Handler) requirePlatformAdmin(next http.Handler) http.Handler {
	return h.tokens.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := kit.FromContext(r.Context())
		if !ok {
			kit.Error(w, http.StatusUnauthorized, "unauthorized", "A valid user session is required.")
			return
		}
		if !h.adminIPAllowed(r) {
			kit.Error(w, http.StatusForbidden, "admin_ip_restricted", "This network is not allowed to access the admin console.")
			return
		}
		user, err := h.store.GetUserByID(r.Context(), claims.Subject)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				kit.Error(w, http.StatusForbidden, "admin_forbidden", "Platform administrator access is required.")
				return
			}
			h.logger.Error("load platform role for admin request", "error", err)
			kit.Error(w, http.StatusServiceUnavailable, "admin_authorization_unavailable", "Admin authorization could not be verified.")
			return
		}
		if user.Status != "active" || !isPlatformAdmin(user.PlatformRole) {
			kit.Error(w, http.StatusForbidden, "admin_forbidden", "Platform administrator access is required.")
			return
		}
		session, err := h.store.GetSession(r.Context(), claims.SessionID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				kit.Error(w, http.StatusUnauthorized, "admin_session_revoked", "The admin session is no longer valid.")
				return
			}
			h.logger.Error("load admin session for authorization", "error", err)
			kit.Error(w, http.StatusServiceUnavailable, "admin_session_unavailable", "Admin session could not be verified.")
			return
		}
		if session.UserID != user.ID || session.RevokedAt != nil || !session.ExpiresAt.After(time.Now()) {
			kit.Error(w, http.StatusUnauthorized, "admin_session_revoked", "The admin session is no longer valid.")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminActorKey{}, &adminActor{
			user: user, claims: claims, session: session,
		})))
	}))
}

func (h *Handler) requireAdminMFA(permission AdminPermission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := r.Context().Value(adminActorKey{}).(*adminActor)
		if !ok {
			kit.Error(w, http.StatusForbidden, "admin_forbidden", "Platform administrator access is required.")
			return
		}
		if !CanAdmin(actor.user.PlatformRole, permission) {
			kit.Error(w, http.StatusForbidden, "admin_permission_denied", "Your platform role does not permit this action.")
			return
		}
		store, ok := h.store.(adminSecurityStore)
		if !ok {
			h.logger.Error("identity store does not support admin security operations")
			kit.Error(w, http.StatusServiceUnavailable, "admin_security_unavailable", "Admin security could not be verified.")
			return
		}
		session := actor.session
		if !adminMFAIsFresh(session.AdminMFAVerifiedAt, time.Now()) {
			kit.Error(w, http.StatusForbidden, "admin_mfa_required", "Verify your admin authenticator before continuing.")
			return
		}
		state, err := store.GetAdminMFA(r.Context(), actor.user.ID)
		if err != nil || !state.Enabled {
			if err != nil {
				h.logger.Error("verify admin MFA state", "error", err)
				kit.Error(w, http.StatusServiceUnavailable, "admin_mfa_unavailable", "Admin MFA could not be verified.")
			} else {
				kit.Error(w, http.StatusForbidden, "admin_mfa_required", "Set up admin MFA before continuing.")
			}
			return
		}
		actor.session = session
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) getAdminSession(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	store, ok := h.store.(adminSecurityStore)
	if !ok {
		kit.Error(w, http.StatusServiceUnavailable, "admin_security_unavailable", "Admin security could not be verified.")
		return
	}
	state, err := store.GetAdminMFA(r.Context(), actor.user.ID)
	if err != nil {
		h.logger.Error("load admin MFA state", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_mfa_unavailable", "Admin MFA could not be verified.")
		return
	}
	session, err := h.store.GetSession(r.Context(), actor.claims.SessionID)
	if err != nil {
		h.logger.Error("load admin session state", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_session_unavailable", "Admin session could not be verified.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"user_id":       actor.user.ID,
		"platform_role": actor.user.PlatformRole,
		"mfa_enabled":   state.Enabled,
		"mfa_verified":  adminMFAIsFresh(session.AdminMFAVerifiedAt, time.Now()),
		"expires_at":    session.ExpiresAt,
	})
}

func (h *Handler) setupAdminMFA(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	store, ok := h.store.(adminSecurityStore)
	if !ok {
		kit.Error(w, http.StatusServiceUnavailable, "admin_security_unavailable", "Admin security could not be updated.")
		return
	}
	state, err := store.GetAdminMFA(r.Context(), actor.user.ID)
	if err != nil {
		h.logger.Error("load admin MFA state", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_mfa_unavailable", "Admin MFA could not be verified.")
		return
	}
	if state.Enabled {
		kit.Error(w, http.StatusConflict, "admin_mfa_already_enabled", "Admin MFA is already enabled.")
		return
	}
	secret, err := newTOTPSecret()
	if err != nil {
		h.logger.Error("generate admin TOTP secret", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_mfa_setup_failed", "Admin MFA setup could not be started.")
		return
	}
	recoveryCodes, hashes, err := newRecoveryCodes()
	if err != nil {
		h.logger.Error("generate admin recovery codes", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_mfa_setup_failed", "Admin MFA setup could not be started.")
		return
	}
	encryptedSecret, err := delivery.EncryptSecret(h.cfg.JWTSecret+":admin-mfa:v1", secret)
	if err != nil {
		h.logger.Error("encrypt admin TOTP secret", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_mfa_setup_failed", "Admin MFA setup could not be started.")
		return
	}
	if err := store.SaveAdminMFASetup(r.Context(), actor.user.ID, base64.RawStdEncoding.EncodeToString(encryptedSecret), hashes); err != nil {
		h.logger.Error("save admin MFA setup", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_mfa_setup_failed", "Admin MFA setup could not be started.")
		return
	}
	if err := h.writeAdminAudit(r, actor, "admin_mfa_setup_started", "", "", "MFA enrollment", nil, nil); err != nil {
		h.logger.Error("audit admin MFA setup", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_audit_failed", "Admin MFA setup could not be recorded.")
		return
	}
	issuer := url.QueryEscape("Sendwise")
	account := url.QueryEscape(actor.user.Email)
	kit.JSON(w, http.StatusOK, map[string]any{
		"secret":         secret,
		"otpauth_url":    fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30", issuer, account, secret, issuer),
		"recovery_codes": recoveryCodes,
	})
}

func (h *Handler) verifyAdminMFA(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	var request struct {
		Code string `json:"code"`
	}
	if err := kit.Decode(w, r, &request); err != nil || strings.TrimSpace(request.Code) == "" {
		kit.Error(w, http.StatusBadRequest, "invalid_mfa_code", "Provide a six-digit authenticator code or an unused recovery code.")
		return
	}
	store, ok := h.store.(adminSecurityStore)
	if !ok {
		kit.Error(w, http.StatusServiceUnavailable, "admin_security_unavailable", "Admin MFA could not be verified.")
		return
	}
	locked, err := store.AdminMFALocked(r.Context(), actor.user.ID)
	if err != nil {
		h.logger.Error("check admin MFA lockout", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_mfa_unavailable", "Admin MFA could not be verified.")
		return
	}
	if locked {
		kit.Error(w, http.StatusTooManyRequests, "admin_mfa_locked", "Admin MFA is temporarily locked after repeated failed codes.")
		return
	}
	state, err := store.GetAdminMFA(r.Context(), actor.user.ID)
	if err != nil {
		h.logger.Error("load admin MFA state", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_mfa_unavailable", "Admin MFA could not be verified.")
		return
	}
	if state.Secret == "" {
		kit.Error(w, http.StatusConflict, "admin_mfa_not_setup", "Start MFA setup before verifying a code.")
		return
	}
	encryptedSecret, err := base64.RawStdEncoding.DecodeString(state.Secret)
	if err != nil {
		h.logger.Error("decode encrypted admin TOTP secret", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_mfa_unavailable", "Admin MFA could not be verified.")
		return
	}
	secret, err := delivery.DecryptSecret(h.cfg.JWTSecret+":admin-mfa:v1", encryptedSecret)
	if err != nil {
		h.logger.Error("decrypt admin TOTP secret", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_mfa_unavailable", "Admin MFA could not be verified.")
		return
	}
	code := strings.TrimSpace(request.Code)
	valid := verifyTOTP(secret, code, time.Now())
	usedRecovery := false
	if !valid && state.Enabled {
		consumed, consumeErr := store.ConsumeAdminRecoveryCode(r.Context(), actor.user.ID, hashRecoveryCode(code))
		if consumeErr != nil {
			h.logger.Error("consume admin recovery code", "error", consumeErr)
			kit.Error(w, http.StatusServiceUnavailable, "admin_recovery_unavailable", "The recovery code could not be verified.")
			return
		}
		valid = consumed
		usedRecovery = consumed
	}
	if !valid {
		locked, lockErr := store.RecordAdminMFAFailure(r.Context(), actor.user.ID)
		if lockErr != nil {
			h.logger.Error("record admin MFA failure", "error", lockErr)
			kit.Error(w, http.StatusServiceUnavailable, "admin_mfa_unavailable", "Admin MFA failure could not be recorded.")
			return
		}
		h.audit(r, &actor.user.ID, "admin_mfa_failed", map[string]any{"platform_role": actor.user.PlatformRole})
		if locked {
			kit.Error(w, http.StatusTooManyRequests, "admin_mfa_locked", "Admin MFA is temporarily locked after repeated failed codes.")
			return
		}
		kit.Error(w, http.StatusUnauthorized, "invalid_mfa_code", "The authenticator or recovery code is invalid.")
		return
	}
	if err := store.ClearAdminMFAFailures(r.Context(), actor.user.ID); err != nil {
		h.logger.Error("clear admin MFA failures", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_mfa_unavailable", "Admin MFA could not be verified.")
		return
	}
	if !state.Enabled {
		if err := store.EnableAdminMFA(r.Context(), actor.user.ID); err != nil {
			h.logger.Error("enable admin MFA", "error", err)
			kit.Error(w, http.StatusInternalServerError, "admin_mfa_enable_failed", "Admin MFA could not be enabled.")
			return
		}
	}
	verifiedAt := time.Now()
	if err := store.MarkAdminSessionMFAVerified(r.Context(), actor.claims.SessionID, actor.user.ID, verifiedAt); err != nil {
		h.logger.Error("mark admin session MFA verified", "error", err)
		kit.Error(w, http.StatusUnauthorized, "admin_session_unavailable", "Admin MFA could not be applied to this session.")
		return
	}
	if err := h.writeAdminAudit(r, actor, "admin_mfa_verified", "", "", "MFA authentication", nil, nil); err != nil {
		h.logger.Error("audit admin MFA verification", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_audit_failed", "Admin MFA verification could not be recorded.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"verified": true, "used_recovery_code": usedRecovery})
}

func (h *Handler) getAdminPermissions(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	permissions := make([]string, 0, len(adminPermissions[actor.user.PlatformRole]))
	for permission := range adminPermissions[actor.user.PlatformRole] {
		permissions = append(permissions, string(permission))
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"platform_role": actor.user.PlatformRole,
		"permissions":   permissions,
	})
}

func (h *Handler) getAdminAudit(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	limit := 50
	offset := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			kit.Error(w, http.StatusBadRequest, "invalid_limit", "Limit must be an integer from 1 to 100.")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			kit.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be a non-negative integer.")
			return
		}
		offset = value
	}
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	actorID := strings.TrimSpace(r.URL.Query().Get("actor_user_id"))
	if len(action) > 100 || len(actorID) > 64 {
		kit.Error(w, http.StatusBadRequest, "invalid_filter", "Audit filters are too long.")
		return
	}
	searchStore, ok := h.store.(adminAuditSearchStore)
	if !ok {
		h.logger.Error("identity store does not support filtered admin audit queries")
		kit.Error(w, http.StatusServiceUnavailable, "admin_audit_unavailable", "The audit log could not be loaded.")
		return
	}
	records, total, err := searchStore.ListAdminAuditFiltered(r.Context(), action, actorID, limit, offset)
	if err != nil {
		h.logger.Error("list admin audit", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_audit_failed", "The audit log could not be loaded.")
		return
	}
	if actor.user.PlatformRole == PlatformRoleSupportAdmin {
		for i := range records {
			if records[i].Action == "admin_workspace_note_added" {
				records[i].AfterState = nil
				records[i].Reason = "Restricted workspace note"
			}
		}
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": records, "limit": limit, "offset": offset, "total": total,
		"platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) exportAdminAudit(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	var request struct {
		Reason  string `json:"reason"`
		Action  string `json:"action"`
		ActorID string `json:"actor_user_id"`
		Limit   int    `json:"limit"`
		Offset  int    `json:"offset"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_export_request", "Provide a valid audit export request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	request.Action = strings.TrimSpace(request.Action)
	request.ActorID = strings.TrimSpace(request.ActorID)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "audit_export_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if len(request.Action) > 100 || len(request.ActorID) > 64 {
		kit.Error(w, http.StatusBadRequest, "invalid_filter", "Audit filters are too long.")
		return
	}
	if request.Limit == 0 {
		request.Limit = 1000
	}
	if request.Limit < 1 || request.Limit > 1000 || request.Offset < 0 {
		kit.Error(w, http.StatusBadRequest, "invalid_pagination", "Export limit must be 1 to 1000 and offset must be non-negative.")
		return
	}
	store, ok := h.store.(adminAuditSearchStore)
	if !ok {
		h.logger.Error("identity store does not support admin audit export")
		kit.Error(w, http.StatusServiceUnavailable, "admin_audit_unavailable", "The audit log could not be exported.")
		return
	}
	records, _, err := store.ListAdminAuditFiltered(r.Context(), request.Action, request.ActorID, request.Limit, request.Offset)
	if err != nil {
		h.logger.Error("load admin audit export", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_audit_failed", "The audit log could not be exported.")
		return
	}
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{
		"id", "created_at", "actor_user_id", "actor_role", "action", "target_type",
		"target_id", "workspace_id", "reason", "ip_address", "request_id",
	}); err != nil {
		h.logger.Error("write admin audit export header", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_audit_export_failed", "The audit log could not be exported.")
		return
	}
	for _, record := range records {
		actorID, workspaceID := "", ""
		if record.ActorUserID != nil {
			actorID = *record.ActorUserID
		}
		if record.WorkspaceID != nil {
			workspaceID = *record.WorkspaceID
		}
		if err := writer.Write([]string{
			safeCSVCell(strconv.FormatInt(record.ID, 10)), safeCSVCell(record.CreatedAt.UTC().Format(time.RFC3339Nano)),
			safeCSVCell(actorID), safeCSVCell(record.ActorRole), safeCSVCell(record.Action),
			safeCSVCell(record.TargetType), safeCSVCell(record.TargetID), safeCSVCell(workspaceID),
			safeCSVCell(record.Reason), safeCSVCell(record.IPAddress), safeCSVCell(record.RequestID),
		}); err != nil {
			h.logger.Error("write admin audit export row", "error", err)
			kit.Error(w, http.StatusInternalServerError, "admin_audit_export_failed", "The audit log could not be exported.")
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		h.logger.Error("flush admin audit export", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_audit_export_failed", "The audit log could not be exported.")
		return
	}
	if err := h.writeAdminAudit(r, actor, "admin_audit_exported", "audit_log", "", request.Reason, nil,
		[]byte(fmt.Sprintf(`{"records_exported":%d}`, len(records)))); err != nil {
		h.logger.Error("audit admin audit export", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_audit_failed", "The export could not be audited.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"filename": "sendwise-admin-audit.csv",
		"csv":      output.String(),
		"exported": len(records),
	})
}

func safeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func (h *Handler) getAdminUsers(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if len(search) > 100 || len(status) > 32 {
		kit.Error(w, http.StatusBadRequest, "invalid_filter", "User search and status filters are too long.")
		return
	}
	limit := 50
	offset := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			kit.Error(w, http.StatusBadRequest, "invalid_limit", "Limit must be an integer from 1 to 100.")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			kit.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be a non-negative integer.")
			return
		}
		offset = value
	}
	store, ok := h.store.(adminUserStore)
	if !ok {
		h.logger.Error("identity store does not support admin user queries")
		kit.Error(w, http.StatusServiceUnavailable, "admin_users_unavailable", "Admin user data could not be loaded.")
		return
	}
	users, total, err := store.ListAdminUsers(r.Context(), search, status, limit, offset)
	if err != nil {
		h.logger.Error("list admin users", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_users_failed", "The user list could not be loaded.")
		return
	}
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": users,
		"pagination": map[string]int{
			"total": total, "limit": limit, "offset": offset,
		},
		"platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) getAdminOverview(w http.ResponseWriter, r *http.Request) {
	userStore, ok := h.store.(adminUserStore)
	if !ok {
		h.logger.Error("identity store does not support admin overview queries")
		kit.Error(w, http.StatusServiceUnavailable, "admin_overview_unavailable", "Platform overview data could not be loaded.")
		return
	}
	users, err := userStore.GetAdminUserCounts(r.Context())
	if err != nil {
		h.logger.Error("load admin user counts", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_overview_failed", "Platform overview data could not be loaded.")
		return
	}
	recentUsers, _, err := userStore.ListAdminUsers(r.Context(), "", "", 10, 0)
	if err != nil {
		h.logger.Error("load recent admin signups", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_overview_failed", "Platform overview data could not be loaded.")
		return
	}
	_, workspaceCount, err := h.workspace.AdminOrganizations(r.Context(), "", 1, 0)
	if err != nil {
		h.logger.Error("load admin workspace count", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_overview_unavailable", "Workspace overview data could not be loaded.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_overview_unavailable", "Operational overview data could not be loaded.")
		return
	}
	operationalMetrics, err := h.legacyAPI.AdminOperationalMetrics(r.Context())
	if err != nil {
		h.logger.Error("load API service operational metrics", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_overview_unavailable", "Operational overview data could not be loaded.")
		return
	}
	nearLimitDomains, err := h.legacyAPI.AdminQuotasNearLimit(r.Context())
	if err != nil {
		h.logger.Error("load domains near quota limits", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_overview_unavailable", "Quota overview data could not be loaded.")
		return
	}
	auditStore, ok := h.store.(adminAuditSearchStore)
	if !ok {
		h.logger.Error("identity store does not support admin overview audit query")
		kit.Error(w, http.StatusServiceUnavailable, "admin_overview_unavailable", "Recent admin activity could not be loaded.")
		return
	}
	auditRows, _, err := auditStore.ListAdminAuditFiltered(r.Context(), "", "", 10, 0)
	if err != nil {
		h.logger.Error("load recent admin activity", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_overview_failed", "Recent admin activity could not be loaded.")
		return
	}
	type recentAdminAction struct {
		ID          int64     `json:"id"`
		ActorUserID *string   `json:"actor_user_id,omitempty"`
		ActorRole   string    `json:"actor_role"`
		Action      string    `json:"action"`
		TargetType  string    `json:"target_type,omitempty"`
		TargetID    string    `json:"target_id,omitempty"`
		Reason      string    `json:"reason"`
		CreatedAt   time.Time `json:"created_at"`
	}
	recentActions := make([]recentAdminAction, 0, len(auditRows))
	for _, row := range auditRows {
		recentActions = append(recentActions, recentAdminAction{
			ID: row.ID, ActorUserID: row.ActorUserID, ActorRole: row.ActorRole,
			Action: row.Action, TargetType: row.TargetType, TargetID: row.TargetID,
			Reason: row.Reason, CreatedAt: row.CreatedAt,
		})
	}
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	kit.JSON(w, http.StatusOK, map[string]any{
		"user_counts":          users,
		"workspace_count":      workspaceCount,
		"operational_metrics":  operationalMetrics,
		"near_limit_domains":   nearLimitDomains,
		"recent_signups":       recentUsers,
		"recent_admin_actions": recentActions,
		"platform_role":        actor.user.PlatformRole,
	})
}

func (h *Handler) getAdminUser(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("userID")
	if _, err := uuid.Parse(userID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_user_id", "The user id must be a UUID.")
		return
	}
	store, ok := h.store.(adminUserStore)
	if !ok {
		h.logger.Error("identity store does not support admin user queries")
		kit.Error(w, http.StatusServiceUnavailable, "admin_users_unavailable", "Admin user data could not be loaded.")
		return
	}
	user, err := store.GetAdminUser(r.Context(), userID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_user_not_found", "User not found.")
		return
	}
	if err != nil {
		h.logger.Error("load admin user", "error", err, "user_id", userID)
		kit.Error(w, http.StatusInternalServerError, "admin_users_failed", "The user details could not be loaded.")
		return
	}
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": user, "platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) resendAdminUserVerification(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	userID := r.PathValue("userID")
	if _, err := uuid.Parse(userID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_user_id", "The user id must be a UUID.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid verification resend request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	user, err := h.store.GetUserByID(r.Context(), userID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_user_not_found", "User not found.")
		return
	}
	if err != nil {
		h.logger.Error("load user for admin verification resend", "error", err, "user_id", userID)
		kit.Error(w, http.StatusInternalServerError, "admin_users_failed", "The verification request could not be completed.")
		return
	}
	if user.Status != "pending" || user.EmailVerifiedAt != nil {
		kit.Error(w, http.StatusConflict, "admin_user_already_verified", "Only pending, unverified accounts can receive a verification email.")
		return
	}
	if err := h.writeAdminAudit(r, actor, "admin_user_verification_requested", "user", userID,
		request.Reason, nil, []byte(`{"delivery":"requested"}`)); err != nil {
		h.logger.Error("audit admin verification resend", "error", err, "user_id", userID)
		kit.Error(w, http.StatusInternalServerError, "admin_audit_failed", "The verification request could not be audited.")
		return
	}
	if err := h.sendVerificationEmail(r, user); err != nil {
		h.logger.Error("queue admin verification email",
			"recipient_domain", mailRecipientDomain(user.Email),
			"error", err,
		)
		kit.Error(w, http.StatusServiceUnavailable, "verification_email_unavailable", "The verification email could not be queued.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]string{"delivery_status": "queued"})
}

func (h *Handler) verifyAdminUserEmail(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before verifying an account.")
		return
	}
	userID := r.PathValue("userID")
	if _, err := uuid.Parse(userID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_user_id", "The user id must be a UUID.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid email verification request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	store, ok := h.store.(adminUserMutationStore)
	if !ok {
		h.logger.Error("identity store does not support admin user actions")
		kit.Error(w, http.StatusServiceUnavailable, "admin_users_unavailable", "The email could not be verified.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_user_email_verified", "user", userID, request.Reason)
	user, err := store.VerifyAdminUserEmail(r.Context(), userID, record)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_user_not_found", "User not found.")
		return
	}
	if errors.Is(err, ErrInvalidUserStatus) {
		kit.Error(w, http.StatusConflict, "admin_user_status_conflict", "Only pending, unverified accounts can be manually verified.")
		return
	}
	if err != nil {
		h.logger.Error("verify admin user email", "error", err, "user_id", userID)
		kit.Error(w, http.StatusInternalServerError, "admin_user_update_failed", "The email could not be verified.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"data": user})
}

func (h *Handler) sendAdminPasswordReset(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before sending a password reset link.")
		return
	}
	userID := r.PathValue("userID")
	if _, err := uuid.Parse(userID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_user_id", "The user id must be a UUID.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid password reset request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	user, err := h.store.GetUserByID(r.Context(), userID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_user_not_found", "User not found.")
		return
	}
	if err != nil {
		h.logger.Error("load user for admin password reset", "error", err, "user_id", userID)
		kit.Error(w, http.StatusInternalServerError, "admin_password_reset_failed", "The password reset link could not be sent.")
		return
	}
	if user.Status != "active" || user.EmailVerifiedAt == nil {
		kit.Error(w, http.StatusConflict, "admin_password_reset_unavailable", "Password reset links can only be sent to active, verified users.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_password_reset_requested", "user", userID, request.Reason)
	if err := h.writeAdminAudit(r, actor, record.Action, record.TargetType, record.TargetID, record.Reason, nil, nil); err != nil {
		h.logger.Error("audit admin password reset request", "error", err, "user_id", userID)
		kit.Error(w, http.StatusInternalServerError, "admin_password_reset_failed", "The password reset link could not be sent.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := h.queuePasswordReset(ctx, user); err != nil {
		h.logger.Error("queue admin password reset email", "error", err, "user_id", userID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_password_reset_failed", "The password reset link could not be queued.")
		return
	}
	kit.JSON(w, http.StatusAccepted, map[string]string{
		"message": "A password reset link was queued for the user's verified email address.",
	})
}

func (h *Handler) demoteAdminUser(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before changing a platform role.")
		return
	}
	userID := r.PathValue("userID")
	if _, err := uuid.Parse(userID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_user_id", "The user id must be a UUID.")
		return
	}
	if userID == actor.user.ID {
		kit.Error(w, http.StatusBadRequest, "admin_self_role_change_forbidden", "You cannot remove your own platform-admin role.")
		return
	}
	var request struct {
		PlatformRole string `json:"platform_role"`
		Reason       string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_platform_role", "Provide a valid platform role change.")
		return
	}
	request.PlatformRole = strings.TrimSpace(request.PlatformRole)
	request.Reason = strings.TrimSpace(request.Reason)
	if request.PlatformRole != PlatformRoleNone {
		kit.Error(w, http.StatusBadRequest, "admin_role_promotion_forbidden", "Platform-admin access can only be granted through the operator bootstrap process.")
		return
	}
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	store, ok := h.store.(adminUserMutationStore)
	if !ok {
		h.logger.Error("identity store does not support admin user actions")
		kit.Error(w, http.StatusServiceUnavailable, "admin_users_unavailable", "The platform role could not be changed.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_platform_role_removed", "user", userID, request.Reason)
	user, sessionsRevoked, err := store.DemoteAdminUser(r.Context(), userID, record)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_user_not_found", "User not found.")
		return
	}
	if errors.Is(err, ErrInvalidUserStatus) {
		kit.Error(w, http.StatusConflict, "admin_platform_role_conflict", "This user does not have a platform-admin role.")
		return
	}
	if errors.Is(err, ErrLastSuperAdmin) {
		kit.Error(w, http.StatusConflict, "admin_last_super_admin", "The last active super_admin cannot be demoted.")
		return
	}
	if err != nil {
		h.logger.Error("remove admin platform role", "error", err, "user_id", userID)
		kit.Error(w, http.StatusInternalServerError, "admin_user_update_failed", "The platform role could not be changed.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": user, "sessions_revoked": sessionsRevoked,
	})
}

func (h *Handler) updateAdminUserStatus(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before changing account status.")
		return
	}
	userID := r.PathValue("userID")
	if _, err := uuid.Parse(userID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_user_id", "The user id must be a UUID.")
		return
	}
	var request struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_user_status", "Provide a valid account status change.")
		return
	}
	request.Status = strings.TrimSpace(request.Status)
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Status != "active" && request.Status != "suspended" {
		kit.Error(w, http.StatusBadRequest, "invalid_user_status", "Account status must be active or suspended.")
		return
	}
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if userID == actor.user.ID && request.Status == "suspended" {
		kit.Error(w, http.StatusBadRequest, "admin_self_suspend_forbidden", "You cannot suspend your own account.")
		return
	}
	store, ok := h.store.(adminUserMutationStore)
	if !ok {
		h.logger.Error("identity store does not support admin user actions")
		kit.Error(w, http.StatusServiceUnavailable, "admin_users_unavailable", "The account status could not be changed.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_user_status_changed", "user", userID, request.Reason)
	user, sessionsRevoked, err := store.SetAdminUserStatus(r.Context(), userID, request.Status, record)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_user_not_found", "User not found.")
		return
	}
	if errors.Is(err, ErrInvalidUserStatus) {
		kit.Error(w, http.StatusConflict, "admin_user_status_conflict", "This user's account status cannot be changed through this action.")
		return
	}
	if err != nil {
		h.logger.Error("change admin user status", "error", err, "user_id", userID)
		kit.Error(w, http.StatusInternalServerError, "admin_user_update_failed", "The account status could not be changed.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": user, "sessions_revoked": sessionsRevoked,
	})
}

func (h *Handler) revokeAdminUserSessions(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before revoking sessions.")
		return
	}
	userID := r.PathValue("userID")
	if _, err := uuid.Parse(userID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_user_id", "The user id must be a UUID.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid session revocation request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	store, ok := h.store.(adminUserMutationStore)
	if !ok {
		h.logger.Error("identity store does not support admin user actions")
		kit.Error(w, http.StatusServiceUnavailable, "admin_users_unavailable", "The user's sessions could not be revoked.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_user_sessions_revoked", "user", userID, request.Reason)
	revoked, err := store.RevokeAdminUserSessions(r.Context(), userID, record)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_user_not_found", "User not found.")
		return
	}
	if err != nil {
		h.logger.Error("revoke admin user sessions", "error", err, "user_id", userID)
		kit.Error(w, http.StatusInternalServerError, "admin_session_revoke_failed", "The user's sessions could not be revoked.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]int{"sessions_revoked": revoked})
}

func (h *Handler) getAdminWorkspaces(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) > 100 {
		kit.Error(w, http.StatusBadRequest, "invalid_search", "Search must be 100 characters or fewer.")
		return
	}
	limit := 50
	offset := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			kit.Error(w, http.StatusBadRequest, "invalid_limit", "Limit must be an integer from 1 to 100.")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			kit.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be a non-negative integer.")
			return
		}
		offset = value
	}
	workspaces, total, err := h.workspace.AdminOrganizations(r.Context(), search, limit, offset)
	if err != nil {
		h.logger.Error("list admin workspaces", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspaces_unavailable", "Workspace metadata could not be loaded.")
		return
	}
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": workspaces,
		"pagination": map[string]int{
			"total": total, "limit": limit, "offset": offset,
		},
		"platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) getAdminWorkspace(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if _, err := uuid.Parse(workspaceID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace id must be a UUID.")
		return
	}
	workspace, err := h.workspace.AdminOrganization(r.Context(), workspaceID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_workspace_not_found", "Workspace not found.")
		return
	}
	if err != nil {
		h.logger.Error("load admin workspace", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_unavailable", "Workspace details could not be loaded.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data":          workspace,
		"platform_role": r.Context().Value(adminActorKey{}).(*adminActor).user.PlatformRole,
	})
}

func (h *Handler) getAdminWorkspaceStatus(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if _, err := uuid.Parse(workspaceID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace id must be a UUID.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_unavailable", "Workspace status could not be loaded.")
		return
	}
	status, err := h.legacyAPI.AdminWorkspaceStatus(r.Context(), workspaceID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_workspace_not_found", "Workspace not found.")
		return
	}
	if err != nil {
		h.logger.Error("load admin workspace status", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_unavailable", "Workspace status could not be loaded.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]string{"status": status})
}

func (h *Handler) setAdminWorkspaceStatus(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before changing workspace status.")
		return
	}
	workspaceID := r.PathValue("workspaceID")
	if _, err := uuid.Parse(workspaceID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace id must be a UUID.")
		return
	}
	var request struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid workspace status.")
		return
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status != "active" && request.Status != "suspended" {
		kit.Error(w, http.StatusBadRequest, "invalid_workspace_status", "Workspace status must be active or suspended.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_update_failed", "Workspace status could not be changed.")
		return
	}
	before, err := h.legacyAPI.AdminWorkspaceStatus(r.Context(), workspaceID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_workspace_not_found", "Workspace not found.")
		return
	}
	if err != nil {
		h.logger.Error("load workspace status before admin update", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_update_failed", "Workspace status could not be changed.")
		return
	}
	if before == "closed" {
		kit.Error(w, http.StatusConflict, "admin_workspace_closed", "A closed workspace cannot be changed.")
		return
	}
	if before == request.Status {
		kit.JSON(w, http.StatusOK, map[string]string{"status": before})
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		h.logger.Error("identity store does not support admin audit")
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_update_failed", "Workspace status could not be changed.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_workspace_status_changed", "workspace", workspaceID, request.Reason)
	record.WorkspaceID = &workspaceID
	record.BeforeState, err = json.Marshal(map[string]string{"status": before})
	if err != nil {
		h.logger.Error("encode workspace status audit state", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusInternalServerError, "admin_workspace_update_failed", "Workspace status could not be changed.")
		return
	}
	record.AfterState, err = json.Marshal(map[string]string{"status": request.Status})
	if err != nil {
		h.logger.Error("encode workspace status audit state", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusInternalServerError, "admin_workspace_update_failed", "Workspace status could not be changed.")
		return
	}
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("audit workspace status update", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusInternalServerError, "admin_workspace_update_failed", "Workspace status could not be changed.")
		return
	}
	updated, err := h.legacyAPI.SetAdminWorkspaceStatus(r.Context(), workspaceID, before, request.Status)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_workspace_not_found", "Workspace not found.")
		return
	}
	if errors.Is(err, ErrAdminWorkspaceClosed) {
		kit.Error(w, http.StatusConflict, "admin_workspace_closed", "A closed workspace cannot be changed.")
		return
	}
	if errors.Is(err, ErrAdminWorkspaceConflict) {
		kit.Error(w, http.StatusConflict, "admin_workspace_status_changed", "Workspace status changed; reload it before retrying.")
		return
	}
	if err != nil {
		h.logger.Error("update admin workspace status", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_update_failed", "Workspace status could not be changed.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]string{"status": updated})
}

func (h *Handler) getAdminDomains(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	nearLimit := false
	if raw := strings.TrimSpace(r.URL.Query().Get("near_limit")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			kit.Error(w, http.StatusBadRequest, "invalid_near_limit", "near_limit must be true or false.")
			return
		}
		nearLimit = value
	}
	if len(search) > 100 || len(status) > 32 {
		kit.Error(w, http.StatusBadRequest, "invalid_filter", "Domain filters are too long.")
		return
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			kit.Error(w, http.StatusBadRequest, "invalid_limit", "Limit must be an integer from 1 to 100.")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			kit.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be a non-negative integer.")
			return
		}
		offset = value
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "Sending-domain metadata could not be loaded.")
		return
	}
	domains, total, err := h.legacyAPI.AdminDomains(r.Context(), search, status, nearLimit, limit, offset)
	if err != nil {
		h.logger.Error("load admin sending domains", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "Sending-domain metadata could not be loaded.")
		return
	}
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": domains,
		"pagination": map[string]int{
			"total": total, "limit": limit, "offset": offset,
		},
		"platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) getAdminDomain(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "Sending-domain metadata could not be loaded.")
		return
	}
	domain, err := h.legacyAPI.AdminDomain(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load admin sending domain", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "Sending-domain metadata could not be loaded.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data":          domain,
		"platform_role": r.Context().Value(adminActorKey{}).(*adminActor).user.PlatformRole,
	})
}

func (h *Handler) getAdminDomainQuotas(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID must be a UUID.")
		return
	}
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Domain quotas could not be loaded.")
		return
	}
	data, err := h.legacyAPI.AdminDomainQuotas(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load admin domain quotas", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Domain quotas could not be loaded.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": data, "platform_role": r.Context().Value(adminActorKey{}).(*adminActor).user.PlatformRole,
	})
}

func (h *Handler) setAdminDomainQuotas(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before changing domain quotas.")
		return
	}
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID must be a UUID.")
		return
	}
	var request struct {
		Streams json.RawMessage `json:"streams"`
		Reason  string          `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil || len(request.Streams) == 0 {
		kit.Error(w, http.StatusBadRequest, "invalid_quota_update", "Provide valid quota overrides.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Domain quotas could not be updated.")
		return
	}
	domain, err := h.legacyAPI.AdminDomain(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load domain before quota update", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Domain quotas could not be updated.")
		return
	}
	before, err := h.legacyAPI.AdminDomainQuotas(r.Context(), domainID)
	if err != nil {
		h.logger.Error("load domain quotas before update", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Domain quotas could not be updated.")
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Domain quotas could not be updated.")
		return
	}
	after, err := json.Marshal(map[string]any{"streams": request.Streams})
	if err != nil {
		h.logger.Error("encode domain quota audit state", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_quotas_unavailable", "Domain quotas could not be updated.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_domain_quotas_updated", "domain", domainID, request.Reason)
	record.WorkspaceID = &domain.WorkspaceID
	record.BeforeState = before
	record.AfterState = after
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("audit domain quota update", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_quotas_unavailable", "Domain quotas could not be updated.")
		return
	}
	updated, err := h.legacyAPI.SetAdminDomainQuotas(
		r.Context(), domainID, actor.user.ID, request.Reason, request.Streams,
	)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("update admin domain quotas", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_update_failed", "Domain quotas could not be updated.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": updated, "platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) getAdminDomainWarmup(w http.ResponseWriter, r *http.Request) {
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID must be a UUID.")
		return
	}
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "Domain warm-up data could not be loaded.")
		return
	}
	data, err := h.legacyAPI.AdminDomainWarmup(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load admin domain warm-up state", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "Domain warm-up data could not be loaded.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": data, "platform_role": r.Context().Value(adminActorKey{}).(*adminActor).user.PlatformRole,
	})
}

func (h *Handler) setAdminDomainWarmupOverride(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before changing a warm-up override.")
		return
	}
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID must be a UUID.")
		return
	}
	var request struct {
		DailyCap  int       `json:"daily_cap"`
		ExpiresAt time.Time `json:"expires_at"`
		Reason    string    `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_warmup_override", "Provide a valid warm-up override.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if err := delivery.ValidateWarmupOverride(request.DailyCap, request.ExpiresAt, time.Now()); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_warmup_override", err.Error()+".")
		return
	}
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain warm-up override could not be updated.")
		return
	}
	domain, err := h.legacyAPI.AdminDomain(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load domain before warm-up override", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain warm-up override could not be updated.")
		return
	}
	before, err := h.legacyAPI.AdminDomainWarmup(r.Context(), domainID)
	if err != nil {
		h.logger.Error("load warm-up state before override", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain warm-up override could not be updated.")
		return
	}
	beforeState, err := warmupOverrideAuditState(before)
	if err != nil {
		h.logger.Error("decode warm-up state before override", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain warm-up override could not be updated.")
		return
	}
	after, err := json.Marshal(map[string]any{
		"override": map[string]any{
			"daily_cap": request.DailyCap, "expires_at": request.ExpiresAt.UTC(),
			"reason": request.Reason, "set_by": actor.user.ID,
		},
	})
	if err != nil {
		h.logger.Error("encode warm-up override audit state", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_warmup_unavailable", "The domain warm-up override could not be updated.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_domain_warmup_override_set", "domain", domainID, request.Reason)
	record.WorkspaceID = &domain.WorkspaceID
	record.BeforeState = beforeState
	record.AfterState = after
	if err := h.writeAdminAuditRecord(r, record); err != nil {
		h.logger.Error("audit domain warm-up override", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_warmup_unavailable", "The domain warm-up override could not be updated.")
		return
	}
	data, err := h.legacyAPI.SetAdminDomainWarmupOverride(
		r.Context(), domainID, actor.user.ID, request.DailyCap, request.ExpiresAt.UTC(), request.Reason,
	)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("set domain warm-up override", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_update_failed", "The domain warm-up override could not be updated.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": data, "platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) clearAdminDomainWarmupOverride(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before clearing a warm-up override.")
		return
	}
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID must be a UUID.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_warmup_override", "Provide a valid warm-up override clear request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain warm-up override could not be cleared.")
		return
	}
	domain, err := h.legacyAPI.AdminDomain(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load domain before clearing warm-up override", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain warm-up override could not be cleared.")
		return
	}
	before, err := h.legacyAPI.AdminDomainWarmup(r.Context(), domainID)
	if err != nil {
		h.logger.Error("load warm-up state before clearing override", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain warm-up override could not be cleared.")
		return
	}
	beforeState, err := warmupOverrideAuditState(before)
	if err != nil {
		h.logger.Error("decode warm-up state before clearing override", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain warm-up override could not be cleared.")
		return
	}
	after := json.RawMessage(`{"override":null}`)
	record := h.adminAuditRecord(r, actor, "admin_domain_warmup_override_cleared", "domain", domainID, request.Reason)
	record.WorkspaceID = &domain.WorkspaceID
	record.BeforeState = beforeState
	record.AfterState = after
	if err := h.writeAdminAuditRecord(r, record); err != nil {
		h.logger.Error("audit cleared domain warm-up override", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_warmup_unavailable", "The domain warm-up override could not be cleared.")
		return
	}
	data, err := h.legacyAPI.ClearAdminDomainWarmupOverride(
		r.Context(), domainID, actor.user.ID, request.Reason,
	)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("clear domain warm-up override", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_update_failed", "The domain warm-up override could not be cleared.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": data, "platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) graduateAdminDomainWarmup(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before graduating warm-up.")
		return
	}
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID must be a UUID.")
		return
	}
	var request struct {
		Reason            string `json:"reason"`
		ConfirmIneligible bool   `json:"confirm_ineligible"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_warmup_graduation", "Provide a valid warm-up graduation request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain could not be graduated.")
		return
	}
	domain, err := h.legacyAPI.AdminDomain(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load domain before warm-up graduation", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain could not be graduated.")
		return
	}
	before, err := h.legacyAPI.AdminDomainWarmup(r.Context(), domainID)
	if err != nil {
		h.logger.Error("load warm-up state before graduation", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain could not be graduated.")
		return
	}
	var state struct {
		Graduated             bool `json:"graduated"`
		GraduationEligibility struct {
			Eligible bool `json:"eligible"`
		} `json:"graduation_eligibility"`
	}
	if err := json.Unmarshal(before, &state); err != nil {
		h.logger.Error("decode warm-up state before graduation", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain could not be graduated.")
		return
	}
	if state.Graduated {
		kit.Error(w, http.StatusConflict, "warmup_already_graduated", "The sending domain is already graduated.")
		return
	}
	if !state.GraduationEligibility.Eligible && !request.ConfirmIneligible {
		kit.Error(w, http.StatusConflict, "warmup_graduation_ineligible", "Confirm ineligible graduation explicitly and provide a reason.")
		return
	}
	beforeState, err := warmupGraduationAuditState(before)
	if err != nil {
		h.logger.Error("encode warm-up graduation before state", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain could not be graduated.")
		return
	}
	afterState, err := json.Marshal(map[string]any{
		"graduated": true, "graduated_by": actor.user.ID,
		"graduated_reason": request.Reason, "eligible_at_change": state.GraduationEligibility.Eligible,
	})
	if err != nil {
		h.logger.Error("encode warm-up graduation after state", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_warmup_unavailable", "The domain could not be graduated.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_domain_warmup_graduated", "domain", domainID, request.Reason)
	record.WorkspaceID = &domain.WorkspaceID
	record.BeforeState = beforeState
	record.AfterState = afterState
	if err := h.writeAdminAuditRecord(r, record); err != nil {
		h.logger.Error("audit domain warm-up graduation", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_warmup_unavailable", "The domain could not be graduated.")
		return
	}
	data, err := h.legacyAPI.GraduateAdminDomainWarmup(
		r.Context(), domainID, actor.user.ID, request.Reason, request.ConfirmIneligible,
	)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("graduate domain warm-up", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_update_failed", "The domain could not be graduated.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"data": data, "platform_role": actor.user.PlatformRole})
}

func (h *Handler) revokeAdminDomainWarmupGraduation(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before revoking warm-up graduation.")
		return
	}
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID must be a UUID.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_warmup_graduation", "Provide a valid warm-up graduation revoke request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain graduation could not be revoked.")
		return
	}
	domain, err := h.legacyAPI.AdminDomain(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load domain before warm-up graduation revoke", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain graduation could not be revoked.")
		return
	}
	before, err := h.legacyAPI.AdminDomainWarmup(r.Context(), domainID)
	if err != nil {
		h.logger.Error("load warm-up state before graduation revoke", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain graduation could not be revoked.")
		return
	}
	var state struct {
		Graduated bool `json:"graduated"`
	}
	if err := json.Unmarshal(before, &state); err != nil {
		h.logger.Error("decode warm-up state before graduation revoke", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain graduation could not be revoked.")
		return
	}
	if !state.Graduated {
		kit.Error(w, http.StatusConflict, "warmup_not_graduated", "The sending domain is not graduated.")
		return
	}
	beforeState, err := warmupGraduationAuditState(before)
	if err != nil {
		h.logger.Error("encode warm-up graduation before revoke state", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_unavailable", "The domain graduation could not be revoked.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_domain_warmup_graduation_revoked", "domain", domainID, request.Reason)
	record.WorkspaceID = &domain.WorkspaceID
	record.BeforeState = beforeState
	record.AfterState = json.RawMessage(`{"graduated":false}`)
	if err := h.writeAdminAuditRecord(r, record); err != nil {
		h.logger.Error("audit domain warm-up graduation revoke", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_warmup_unavailable", "The domain graduation could not be revoked.")
		return
	}
	data, err := h.legacyAPI.RevokeAdminDomainWarmupGraduation(r.Context(), domainID, actor.user.ID, request.Reason)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("revoke domain warm-up graduation", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_warmup_update_failed", "The domain graduation could not be revoked.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"data": data, "platform_role": actor.user.PlatformRole})
}

func (h *Handler) getAdminQuotaDefaults(w http.ResponseWriter, r *http.Request) {
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Platform quota defaults could not be loaded.")
		return
	}
	data, err := h.legacyAPI.AdminQuotaDefaults(r.Context())
	if err != nil {
		h.logger.Error("load platform quota defaults", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Platform quota defaults could not be loaded.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": data, "platform_role": r.Context().Value(adminActorKey{}).(*adminActor).user.PlatformRole,
	})
}

func (h *Handler) setAdminQuotaDefaults(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before changing platform quota defaults.")
		return
	}
	var request struct {
		Defaults json.RawMessage `json:"defaults"`
		Reason   string          `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil || len(request.Defaults) == 0 {
		kit.Error(w, http.StatusBadRequest, "invalid_quota_defaults", "Provide valid platform quota defaults.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Platform quota defaults could not be updated.")
		return
	}
	before, err := h.legacyAPI.AdminQuotaDefaults(r.Context())
	if err != nil {
		h.logger.Error("load quota defaults before update", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Platform quota defaults could not be updated.")
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Platform quota defaults could not be updated.")
		return
	}
	after, err := json.Marshal(map[string]any{"defaults": request.Defaults})
	if err != nil {
		h.logger.Error("encode quota defaults audit state", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_quotas_unavailable", "Platform quota defaults could not be updated.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_quota_defaults_updated", "platform_settings", "quota_defaults", request.Reason)
	record.BeforeState = before
	record.AfterState = after
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("audit platform quota defaults update", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_quotas_unavailable", "Platform quota defaults could not be updated.")
		return
	}
	updated, err := h.legacyAPI.SetAdminQuotaDefaults(r.Context(), actor.user.ID, request.Reason, request.Defaults)
	if err != nil {
		h.logger.Error("update platform quota defaults", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_update_failed", "Platform quota defaults could not be updated.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": updated, "platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) getAdminQuotasNearLimit(w http.ResponseWriter, r *http.Request) {
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Quota overview could not be loaded.")
		return
	}
	data, err := h.legacyAPI.AdminQuotasNearLimit(r.Context())
	if err != nil {
		h.logger.Error("load domains near quota limits", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_quotas_unavailable", "Quota overview could not be loaded.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"data": data})
}

func (h *Handler) recheckAdminDomain(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before re-checking DNS.")
		return
	}

	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID is invalid.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid domain re-check request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "Domain DNS could not be re-checked.")
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		h.logger.Error("identity store does not support admin audit")
		kit.Error(w, http.StatusServiceUnavailable, "admin_domain_recheck_failed", "Domain DNS could not be re-checked.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_domain_recheck_requested", "domain", domainID, request.Reason)
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("audit admin domain re-check", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_domain_recheck_failed", "Domain DNS could not be re-checked.")
		return
	}
	domain, err := h.legacyAPI.RecheckAdminDomain(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if errors.Is(err, ErrAdminDomainConflict) {
		kit.Error(w, http.StatusConflict, "admin_domain_conflict", "This domain has an ownership conflict and cannot be re-checked until it is resolved.")
		return
	}
	if err != nil {
		h.logger.Error("re-check admin domain DNS", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_domain_recheck_failed", "Domain DNS could not be re-checked.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data":          domain,
		"platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) setAdminDomainDisabled(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before changing domain status.")
		return
	}
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID must be a UUID.")
		return
	}
	var request struct {
		Disabled *bool  `json:"disabled"`
		Reason   string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid domain status request.")
		return
	}
	if request.Disabled == nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Specify whether the domain should be disabled.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "Domain status could not be changed.")
		return
	}
	current, err := h.legacyAPI.AdminDomain(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load admin domain before status change", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "Domain status could not be changed.")
		return
	}
	isDisabled := current.AdminDisabledAt != nil
	if isDisabled == *request.Disabled {
		kit.Error(w, http.StatusConflict, "domain_status_unchanged", "The sending domain already has the requested status.")
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		h.logger.Error("identity store does not support admin audit")
		kit.Error(w, http.StatusServiceUnavailable, "admin_domain_status_failed", "Domain status could not be changed.")
		return
	}
	action := "admin_domain_enabled"
	if *request.Disabled {
		action = "admin_domain_disabled"
	}
	record := h.adminAuditRecord(r, actor, action, "domain", domainID, request.Reason)
	record.WorkspaceID = &current.WorkspaceID
	record.BeforeState = json.RawMessage(`{"disabled":` + strconv.FormatBool(isDisabled) + `}`)
	record.AfterState = json.RawMessage(`{"disabled":` + strconv.FormatBool(*request.Disabled) + `}`)
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("audit admin domain status change", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_domain_status_failed", "Domain status could not be changed.")
		return
	}
	updated, err := h.legacyAPI.SetAdminDomainDisabled(r.Context(), domainID, *request.Disabled)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("set admin domain disabled state", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_domain_status_failed", "Domain status could not be changed.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": updated, "platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) deleteAdminDomain(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before removing a domain.")
		return
	}
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID must be a UUID.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid domain removal request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "The sending domain could not be removed.")
		return
	}
	domain, err := h.legacyAPI.AdminDomain(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load admin domain before removal", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "The sending domain could not be removed.")
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		h.logger.Error("identity store does not support admin audit")
		kit.Error(w, http.StatusServiceUnavailable, "admin_domain_remove_failed", "The sending domain could not be removed.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_domain_remove_requested", "domain", domainID, request.Reason)
	record.WorkspaceID = &domain.WorkspaceID
	record.BeforeState, err = json.Marshal(map[string]any{
		"domain": domain.Domain, "owner_conflict": domain.OwnerConflict,
		"disabled": domain.AdminDisabledAt != nil,
	})
	if err != nil {
		h.logger.Error("encode admin domain removal audit state", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_domain_remove_failed", "The sending domain could not be removed.")
		return
	}
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("audit admin domain removal", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_domain_remove_failed", "The sending domain could not be removed.")
		return
	}
	if err := h.legacyAPI.DeleteAdminDomain(r.Context(), domainID); errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
	} else if errors.Is(err, ErrAdminDomainConflict) {
		kit.Error(w, http.StatusConflict, "admin_domain_conflict", "Resolve the sending-domain ownership conflict before removing this record.")
		return
	} else if err != nil {
		h.logger.Error("remove admin sending domain", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_domain_remove_failed", "The sending domain could not be removed.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (h *Handler) resolveAdminDomainConflict(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before resolving domain ownership.")
		return
	}
	domainID := r.PathValue("domainID")
	if _, err := uuid.Parse(domainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_domain_id", "The sending domain ID must be a UUID.")
		return
	}
	var request struct {
		RightfulDomainID string `json:"rightful_domain_id"`
		Reason           string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid ownership resolution.")
		return
	}
	if _, err := uuid.Parse(request.RightfulDomainID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_owner_selection", "The rightful domain ID must be a UUID.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "Domain ownership could not be resolved.")
		return
	}
	domain, err := h.legacyAPI.AdminDomain(r.Context(), domainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if err != nil {
		h.logger.Error("load admin domain before ownership resolution", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "Domain ownership could not be resolved.")
		return
	}
	candidates, _, err := h.legacyAPI.AdminDomains(r.Context(), domain.Domain, "", false, 100, 0)
	if err != nil {
		h.logger.Error("load domain ownership candidates", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_domains_unavailable", "Domain ownership could not be resolved.")
		return
	}
	matching := make([]AdminSendingDomain, 0, len(candidates))
	hasConflict := false
	selectedExists := false
	var selected AdminSendingDomain
	for _, candidate := range candidates {
		if !strings.EqualFold(candidate.Domain, domain.Domain) {
			continue
		}
		matching = append(matching, candidate)
		if candidate.OwnerConflict {
			hasConflict = true
		}
		if candidate.ID == request.RightfulDomainID {
			selectedExists = true
			selected = candidate
		}
	}
	if !hasConflict || len(matching) < 2 {
		kit.Error(w, http.StatusConflict, "domain_conflict_not_found", "This domain has no ownership conflict to resolve.")
		return
	}
	if !selectedExists {
		kit.Error(w, http.StatusBadRequest, "invalid_owner_selection", "Select one of the records for this domain as the rightful owner.")
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		h.logger.Error("identity store does not support admin audit")
		kit.Error(w, http.StatusServiceUnavailable, "admin_domain_resolution_failed", "Domain ownership could not be resolved.")
		return
	}
	previousOwners := make([]map[string]string, 0, len(matching))
	for _, candidate := range matching {
		if !candidate.OwnerConflict {
			previousOwners = append(previousOwners, map[string]string{
				"domain_id": candidate.ID, "workspace_id": candidate.WorkspaceID,
			})
		}
	}
	beforeState, err := json.Marshal(map[string]any{
		"domain": domain.Domain, "existing_owners": previousOwners,
		"conflict_record_ids": func() []string {
			ids := make([]string, 0, len(matching))
			for _, candidate := range matching {
				if candidate.OwnerConflict {
					ids = append(ids, candidate.ID)
				}
			}
			return ids
		}(),
	})
	if err != nil {
		h.logger.Error("encode domain ownership audit state", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_domain_resolution_failed", "Domain ownership could not be resolved.")
		return
	}
	afterState, err := json.Marshal(map[string]string{
		"rightful_domain_id": selected.ID, "workspace_id": selected.WorkspaceID,
	})
	if err != nil {
		h.logger.Error("encode resolved domain audit state", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_domain_resolution_failed", "Domain ownership could not be resolved.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_domain_owner_conflict_resolved", "domain", domainID, request.Reason)
	record.WorkspaceID = &selected.WorkspaceID
	record.BeforeState = beforeState
	record.AfterState = afterState
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("audit domain ownership resolution", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusInternalServerError, "admin_domain_resolution_failed", "Domain ownership could not be resolved.")
		return
	}
	updated, err := h.legacyAPI.ResolveAdminDomainConflict(r.Context(), domainID, request.RightfulDomainID)
	if errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_domain_not_found", "Sending domain not found.")
		return
	}
	if errors.Is(err, ErrAdminDomainConflict) {
		kit.Error(w, http.StatusConflict, "domain_conflict_not_found", "This domain has no ownership conflict to resolve.")
		return
	}
	if err != nil {
		h.logger.Error("resolve sending-domain ownership", "error", err, "domain_id", domainID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_domain_resolution_failed", "Domain ownership could not be resolved.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": updated, "platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) getAdminCampaigns(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if len(search) > 100 || len(status) > 32 {
		kit.Error(w, http.StatusBadRequest, "invalid_filter", "Campaign filters are too long.")
		return
	}
	if workspaceID != "" {
		if _, err := uuid.Parse(workspaceID); err != nil {
			kit.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace ID must be a UUID.")
			return
		}
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			kit.Error(w, http.StatusBadRequest, "invalid_limit", "Limit must be an integer from 1 to 100.")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			kit.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be a non-negative integer.")
			return
		}
		offset = value
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaigns_unavailable", "Campaign metadata could not be loaded.")
		return
	}
	campaigns, total, err := h.legacyAPI.AdminCampaigns(r.Context(), search, status, workspaceID, limit, offset)
	if err != nil {
		h.logger.Error("load admin campaigns", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaigns_unavailable", "Campaign metadata could not be loaded.")
		return
	}
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": campaigns,
		"pagination": map[string]int{
			"total": total, "limit": limit, "offset": offset,
		},
		"platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) stopAdminCampaign(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before stopping a campaign.")
		return
	}
	campaignID := r.PathValue("campaignID")
	if _, err := uuid.Parse(campaignID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_campaign_id", "The campaign ID must be a UUID.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid campaign stop request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaigns_unavailable", "The campaign could not be stopped.")
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		h.logger.Error("identity store does not support admin audit")
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaign_stop_failed", "The campaign could not be stopped.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_campaign_stop_requested", "campaign", campaignID, request.Reason)
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("audit admin campaign stop", "error", err, "campaign_id", campaignID)
		kit.Error(w, http.StatusInternalServerError, "admin_campaign_stop_failed", "The campaign could not be stopped.")
		return
	}
	err := h.legacyAPI.StopAdminCampaign(r.Context(), campaignID)
	if errors.Is(err, ErrAdminCampaignNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_campaign_not_found", "Campaign not found.")
		return
	}
	if errors.Is(err, ErrAdminCampaignConflict) {
		kit.Error(w, http.StatusConflict, "admin_campaign_not_stoppable", "The campaign cannot be stopped in its current state.")
		return
	}
	if err != nil {
		h.logger.Error("stop admin campaign", "error", err, "campaign_id", campaignID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaign_stop_failed", "The campaign could not be stopped.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (h *Handler) getAdminCampaignDelivery(w http.ResponseWriter, r *http.Request) {
	campaignID := r.PathValue("campaignID")
	if _, err := uuid.Parse(campaignID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_campaign_id", "The campaign ID must be a UUID.")
		return
	}
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaigns_unavailable", "Campaign delivery diagnostics could not be loaded.")
		return
	}
	data, err := h.legacyAPI.AdminCampaignDelivery(r.Context(), campaignID)
	if errors.Is(err, ErrAdminCampaignNotFound) || errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_campaign_not_found", "Campaign not found.")
		return
	}
	if err != nil {
		h.logger.Error("load admin campaign delivery diagnostics", "error", err, "campaign_id", campaignID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaigns_unavailable", "Campaign delivery diagnostics could not be loaded.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"data": data})
}

func (h *Handler) resumeAdminCampaignDeferredJobs(w http.ResponseWriter, r *http.Request) {
	h.runAdminCampaignDeliveryAction(w, r, "resume_deferred", "resume deferred jobs")
}

func (h *Handler) replayAdminCampaignDeadLetters(w http.ResponseWriter, r *http.Request) {
	h.runAdminCampaignDeliveryAction(w, r, "replay_dead_letter", "replay dead-lettered jobs")
}

func (h *Handler) runAdminCampaignDeliveryAction(w http.ResponseWriter, r *http.Request, action, label string) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before managing campaign delivery jobs.")
		return
	}
	campaignID := r.PathValue("campaignID")
	if _, err := uuid.Parse(campaignID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_campaign_id", "The campaign ID must be a UUID.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid campaign delivery action request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaigns_unavailable", "Campaign delivery jobs could not be updated.")
		return
	}
	before, err := h.legacyAPI.AdminCampaignDelivery(r.Context(), campaignID)
	if errors.Is(err, ErrAdminCampaignNotFound) || errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_campaign_not_found", "Campaign not found.")
		return
	}
	if err != nil {
		h.logger.Error("load campaign delivery state before admin action", "error", err, "campaign_id", campaignID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaigns_unavailable", "Campaign delivery jobs could not be updated.")
		return
	}
	var count int64
	if action == "resume_deferred" {
		count, err = h.legacyAPI.ResumeAdminCampaignDeferredJobs(r.Context(), campaignID)
	} else {
		count, err = h.legacyAPI.ReplayAdminCampaignDeadLetters(r.Context(), campaignID)
	}
	if errors.Is(err, ErrAdminCampaignNotFound) || errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_campaign_not_found", "Campaign not found.")
		return
	}
	if err != nil {
		h.logger.Error("run admin campaign delivery action", "error", err, "campaign_id", campaignID, "action", action)
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaign_action_failed", "Campaign delivery jobs could not be updated.")
		return
	}
	after, err := h.legacyAPI.AdminCampaignDelivery(r.Context(), campaignID)
	if err != nil {
		h.logger.Error("load campaign delivery state after admin action", "error", err, "campaign_id", campaignID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_campaign_action_failed", "The jobs were updated but the resulting delivery state could not be loaded.")
		return
	}
	beforeState, err := json.Marshal(before)
	if err != nil {
		h.logger.Error("encode before campaign delivery audit state", "error", err, "campaign_id", campaignID)
		kit.Error(w, http.StatusInternalServerError, "admin_campaign_action_failed", "Campaign delivery jobs could not be updated.")
		return
	}
	afterState, err := json.Marshal(map[string]any{"diagnostics": after, "affected": count, "action": action})
	if err != nil {
		h.logger.Error("encode after campaign delivery audit state", "error", err, "campaign_id", campaignID)
		kit.Error(w, http.StatusInternalServerError, "admin_campaign_action_failed", "Campaign delivery jobs could not be updated.")
		return
	}
	if err := h.writeAdminAudit(r, actor, "admin_campaign_"+action, "campaign", campaignID, request.Reason, beforeState, afterState); err != nil {
		h.logger.Error("audit campaign delivery action", "error", err, "campaign_id", campaignID, "action", action)
		kit.Error(w, http.StatusInternalServerError, "admin_campaign_action_failed", "The campaign delivery action completed but could not be audited.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"count": count, "action": label})
}

func (h *Handler) searchAdminTransactionalMessages(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	if len(search) < 2 || len(search) > 100 {
		kit.Error(w, http.StatusBadRequest, "invalid_search", "Search must be from 2 to 100 characters.")
		return
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			kit.Error(w, http.StatusBadRequest, "invalid_limit", "Limit must be an integer from 1 to 100.")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			kit.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be a non-negative integer.")
			return
		}
		offset = value
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_transactional_search_unavailable", "Transactional message metadata could not be searched.")
		return
	}
	messages, total, err := h.legacyAPI.SearchAdminTransactionalMessages(r.Context(), search, limit, offset)
	if err != nil {
		h.logger.Error("search admin transactional message metadata", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_transactional_search_unavailable", "Transactional message metadata could not be searched.")
		return
	}
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": messages,
		"pagination": map[string]int{
			"total": total, "limit": limit, "offset": offset,
		},
		"platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) getAdminSuppressions(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if len(search) > 254 || len(workspaceID) > 36 {
		kit.Error(w, http.StatusBadRequest, "invalid_filter", "Suppression filters are too long.")
		return
	}
	if workspaceID != "" {
		if _, err := uuid.Parse(workspaceID); err != nil {
			kit.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace ID must be a UUID.")
			return
		}
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			kit.Error(w, http.StatusBadRequest, "invalid_limit", "Limit must be an integer from 1 to 100.")
			return
		}
		limit = value
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			kit.Error(w, http.StatusBadRequest, "invalid_offset", "Offset must be a non-negative integer.")
			return
		}
		offset = value
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_suppressions_unavailable", "Suppression metadata could not be loaded.")
		return
	}
	items, total, err := h.legacyAPI.AdminSuppressions(r.Context(), search, workspaceID, limit, offset)
	if err != nil {
		h.logger.Error("load admin suppressions", "error", err)
		kit.Error(w, http.StatusServiceUnavailable, "admin_suppressions_unavailable", "Suppression metadata could not be loaded.")
		return
	}
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	kit.JSON(w, http.StatusOK, map[string]any{
		"data": items,
		"pagination": map[string]int{
			"total": total, "limit": limit, "offset": offset,
		},
		"platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) createAdminSuppression(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before adding a suppression.")
		return
	}
	var request struct {
		WorkspaceID string `json:"workspace_id"`
		Email       string `json:"email"`
		Reason      string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid suppression request.")
		return
	}
	request.WorkspaceID = strings.TrimSpace(request.WorkspaceID)
	request.Email = strings.TrimSpace(request.Email)
	request.Reason = strings.TrimSpace(request.Reason)
	if _, err := uuid.Parse(request.WorkspaceID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace ID must be a UUID.")
		return
	}
	if len(request.Email) > 254 || !strings.Contains(request.Email, "@") {
		kit.Error(w, http.StatusBadRequest, "invalid_email", "Provide a valid email address.")
		return
	}
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_suppressions_unavailable", "The suppression could not be added.")
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		h.logger.Error("identity store does not support admin audit")
		kit.Error(w, http.StatusServiceUnavailable, "admin_suppression_failed", "The suppression could not be added.")
		return
	}
	emailDigest := sha256.Sum256([]byte(strings.ToLower(request.Email)))
	targetID := hex.EncodeToString(emailDigest[:])
	record := h.adminAuditRecord(r, actor, "admin_suppression_add_requested", "suppression", targetID, request.Reason)
	record.WorkspaceID = &request.WorkspaceID
	record.AfterState = json.RawMessage(`{"workspace_id":"` + request.WorkspaceID + `","email_sha256":"` + targetID + `"}`)
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("audit admin suppression add", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_suppression_failed", "The suppression could not be added.")
		return
	}
	item, err := h.legacyAPI.CreateAdminSuppression(r.Context(), request.WorkspaceID, request.Email)
	if errors.Is(err, ErrAdminSuppressionConflict) {
		kit.Error(w, http.StatusConflict, "suppression_exists", "This address is already suppressed in that workspace.")
		return
	}
	if err != nil {
		h.logger.Error("create admin suppression", "error", err, "workspace_id", request.WorkspaceID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_suppression_failed", "The suppression could not be added.")
		return
	}
	kit.JSON(w, http.StatusCreated, map[string]any{
		"data": item, "platform_role": actor.user.PlatformRole,
	})
}

func (h *Handler) deleteAdminSuppression(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before removing a suppression.")
		return
	}
	suppressionID := r.PathValue("id")
	if _, err := uuid.Parse(suppressionID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_suppression_id", "The suppression ID must be a UUID.")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid suppression removal request.")
		return
	}
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if h.legacyAPI == nil {
		h.logger.Error("legacy API admin client is unavailable")
		kit.Error(w, http.StatusServiceUnavailable, "admin_suppressions_unavailable", "The suppression could not be removed.")
		return
	}
	suppressions, _, err := h.legacyAPI.AdminSuppressions(r.Context(), suppressionID, "", 1, 0)
	if err != nil {
		h.logger.Error("load admin suppression before removal", "error", err, "suppression_id", suppressionID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_suppressions_unavailable", "The suppression could not be removed.")
		return
	}
	if len(suppressions) == 0 || suppressions[0].ID != suppressionID {
		kit.Error(w, http.StatusNotFound, "suppression_not_found", "Suppression not found.")
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		h.logger.Error("identity store does not support admin audit")
		kit.Error(w, http.StatusServiceUnavailable, "admin_suppression_failed", "The suppression could not be removed.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_suppression_remove_requested", "suppression", suppressionID, request.Reason)
	record.WorkspaceID = &suppressions[0].WorkspaceID
	record.BeforeState = json.RawMessage(`{"workspace_id":"` + suppressions[0].WorkspaceID + `"}`)
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("audit admin suppression removal", "error", err, "suppression_id", suppressionID)
		kit.Error(w, http.StatusInternalServerError, "admin_suppression_failed", "The suppression could not be removed.")
		return
	}
	if err := h.legacyAPI.DeleteAdminSuppression(r.Context(), suppressionID); errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "suppression_not_found", "Suppression not found.")
		return
	} else if err != nil {
		h.logger.Error("delete admin suppression", "error", err, "suppression_id", suppressionID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_suppression_failed", "The suppression could not be removed.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (h *Handler) getAdminWorkspaceNotes(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspaceID")
	if _, err := uuid.Parse(workspaceID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace id must be a UUID.")
		return
	}
	if _, err := h.workspace.AdminOrganization(r.Context(), workspaceID); errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_workspace_not_found", "Workspace not found.")
		return
	} else if err != nil {
		h.logger.Error("verify admin workspace for notes", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_unavailable", "Workspace metadata could not be loaded.")
		return
	}
	store, ok := h.store.(adminWorkspaceNotesStore)
	if !ok {
		h.logger.Error("identity store does not support admin workspace notes")
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_notes_unavailable", "Workspace notes could not be loaded.")
		return
	}
	notes, err := store.ListAdminWorkspaceNotes(r.Context(), workspaceID, 100)
	if err != nil {
		h.logger.Error("list admin workspace notes", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusInternalServerError, "admin_workspace_notes_unavailable", "Workspace notes could not be loaded.")
		return
	}
	kit.JSON(w, http.StatusOK, map[string]any{"data": notes})
}

func (h *Handler) addAdminWorkspaceNote(w http.ResponseWriter, r *http.Request) {
	actor, _ := r.Context().Value(adminActorKey{}).(*adminActor)
	if !adminMFAIsRecent(actor.session.AdminMFAVerifiedAt, time.Now()) {
		kit.Error(w, http.StatusForbidden, "admin_reauth_required", "Re-verify your authenticator before adding a workspace note.")
		return
	}
	workspaceID := r.PathValue("workspaceID")
	if _, err := uuid.Parse(workspaceID); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_workspace_id", "The workspace id must be a UUID.")
		return
	}
	var request struct {
		Note   string `json:"note"`
		Reason string `json:"reason"`
	}
	if err := kit.Decode(w, r, &request); err != nil {
		kit.Error(w, http.StatusBadRequest, "invalid_admin_action", "Provide a valid workspace note.")
		return
	}
	request.Note = strings.TrimSpace(request.Note)
	request.Reason = strings.TrimSpace(request.Reason)
	if request.Note == "" || len(request.Note) > 2000 {
		kit.Error(w, http.StatusBadRequest, "invalid_workspace_note", "A note of 1 to 2000 characters is required.")
		return
	}
	if request.Reason == "" || len(request.Reason) > 500 {
		kit.Error(w, http.StatusBadRequest, "admin_reason_required", "Provide a reason of 1 to 500 characters.")
		return
	}
	if _, err := h.workspace.AdminOrganization(r.Context(), workspaceID); errors.Is(err, ErrNotFound) {
		kit.Error(w, http.StatusNotFound, "admin_workspace_not_found", "Workspace not found.")
		return
	} else if err != nil {
		h.logger.Error("verify admin workspace for note", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_unavailable", "Workspace metadata could not be loaded.")
		return
	}
	auditStore, ok := h.store.(adminSecurityStore)
	if !ok {
		h.logger.Error("identity store does not support admin audit")
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_note_failed", "The workspace note could not be saved.")
		return
	}
	if _, ok := h.store.(adminWorkspaceNotesStore); !ok {
		h.logger.Error("identity store does not support admin workspace notes")
		kit.Error(w, http.StatusServiceUnavailable, "admin_workspace_note_failed", "The workspace note could not be saved.")
		return
	}
	afterState, err := json.Marshal(map[string]string{"note": request.Note})
	if err != nil {
		h.logger.Error("encode admin workspace note", "error", err)
		kit.Error(w, http.StatusInternalServerError, "admin_workspace_note_failed", "The workspace note could not be saved.")
		return
	}
	record := h.adminAuditRecord(r, actor, "admin_workspace_note_added", "workspace", workspaceID, request.Reason)
	record.AfterState = afterState
	if err := auditStore.WriteAdminAudit(r.Context(), record); err != nil {
		h.logger.Error("write admin workspace note", "error", err, "workspace_id", workspaceID)
		kit.Error(w, http.StatusInternalServerError, "admin_workspace_note_failed", "The workspace note could not be saved.")
		return
	}
	kit.JSON(w, http.StatusCreated, map[string]any{"data": adminWorkspaceNote{
		ActorUserID: &actor.user.ID,
		Note:        request.Note,
		Reason:      request.Reason,
		CreatedAt:   time.Now().UTC(),
	}})
}

func warmupOverrideAuditState(data []byte) ([]byte, error) {
	var state struct {
		Override json.RawMessage `json:"override"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode warm-up override audit state: %w", err)
	}
	result, err := json.Marshal(struct {
		Override json.RawMessage `json:"override"`
	}{Override: state.Override})
	if err != nil {
		return nil, fmt.Errorf("encode warm-up override audit state: %w", err)
	}
	return result, nil
}

func warmupGraduationAuditState(data []byte) ([]byte, error) {
	var state struct {
		Graduated             bool            `json:"graduated"`
		GraduatedAt           json.RawMessage `json:"graduated_at"`
		GraduatedBy           json.RawMessage `json:"graduated_by"`
		GraduatedReason       string          `json:"graduated_reason"`
		GraduationEligibility json.RawMessage `json:"graduation_eligibility"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode warm-up graduation audit state: %w", err)
	}
	result, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("encode warm-up graduation audit state: %w", err)
	}
	return result, nil
}

func (h *Handler) writeAdminAudit(r *http.Request, actor *adminActor, action, targetType, targetID, reason string, before, after []byte) error {
	store, ok := h.store.(adminSecurityStore)
	if !ok {
		return fmt.Errorf("identity store does not support admin audit")
	}
	var beforeState, afterState []byte
	if len(before) > 0 {
		beforeState = before
	}
	if len(after) > 0 {
		afterState = after
	}
	record := h.adminAuditRecord(r, actor, action, targetType, targetID, reason)
	record.BeforeState = beforeState
	record.AfterState = afterState
	return store.WriteAdminAudit(r.Context(), record)
}

func (h *Handler) writeAdminAuditRecord(r *http.Request, record adminAuditRecord) error {
	store, ok := h.store.(adminSecurityStore)
	if !ok {
		return fmt.Errorf("identity store does not support admin audit")
	}
	return store.WriteAdminAudit(r.Context(), record)
}

func (h *Handler) adminAuditRecord(r *http.Request, actor *adminActor, action, targetType, targetID, reason string) adminAuditRecord {
	return adminAuditRecord{
		ActorUserID: &actor.user.ID,
		ActorRole:   actor.user.PlatformRole,
		Action:      action,
		TargetType:  targetType,
		TargetID:    targetID,
		Reason:      reason,
		IPAddress:   clientIP(r),
		UserAgent:   r.UserAgent(),
		RequestID:   r.Header.Get("X-Request-ID"),
	}
}

func (h *Handler) adminIPAllowed(r *http.Request) bool {
	allowlist := strings.TrimSpace(h.cfg.AdminIPAllowlist)
	if allowlist == "" {
		return true
	}
	ip := net.ParseIP(clientIP(r))
	if ip == nil {
		return false
	}
	for _, entry := range strings.Split(allowlist, ",") {
		entry = strings.TrimSpace(entry)
		if allowedIP := net.ParseIP(entry); allowedIP != nil && allowedIP.Equal(ip) {
			return true
		}
		_, network, err := net.ParseCIDR(entry)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func validateAdminIPAllowlist(allowlist string) error {
	allowlist = strings.TrimSpace(allowlist)
	if allowlist == "" {
		return nil
	}
	for _, entry := range strings.Split(allowlist, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return fmt.Errorf("ADMIN_IP_ALLOWLIST contains an empty entry")
		}
		if net.ParseIP(entry) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(entry); err != nil {
			return fmt.Errorf("ADMIN_IP_ALLOWLIST entry %q is not an IP address or CIDR range", entry)
		}
	}
	return nil
}

func isPlatformAdmin(role string) bool {
	return role == PlatformRoleSupportAdmin || role == PlatformRoleSuperAdmin
}

func adminMFAIsFresh(verifiedAt *time.Time, now time.Time) bool {
	if verifiedAt == nil {
		return false
	}
	age := now.Sub(*verifiedAt)
	return age >= -30*time.Second && age <= adminMFAFreshness
}

func adminMFAIsRecent(verifiedAt *time.Time, now time.Time) bool {
	if verifiedAt == nil {
		return false
	}
	age := now.Sub(*verifiedAt)
	return age >= -30*time.Second && age <= adminMFAReauthFreshness
}

func newTOTPSecret() (string, error) {
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

func totpCode(secret string, timestamp time.Time) (string, bool) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", false
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(timestamp.Unix()/30))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	value := (binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff) % 1_000_000
	return fmt.Sprintf("%06d", value), true
}

func verifyTOTP(secret, candidate string, timestamp time.Time) bool {
	candidate = strings.TrimSpace(candidate)
	if len(candidate) != 6 {
		return false
	}
	for _, digit := range candidate {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	for delta := -1; delta <= 1; delta++ {
		expected, ok := totpCode(secret, timestamp.Add(time.Duration(delta)*30*time.Second))
		if ok && subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1 {
			return true
		}
	}
	return false
}

func newRecoveryCodes() ([]string, []string, error) {
	const count = 10
	codes := make([]string, 0, count)
	hashes := make([]string, 0, count)
	for range count {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		encoded := strings.ToUpper(hex.EncodeToString(raw))
		code := encoded[:8] + "-" + encoded[8:16] + "-" + encoded[16:24] + "-" + encoded[24:]
		codes = append(codes, code)
		hashes = append(hashes, hashRecoveryCode(code))
	}
	return codes, hashes, nil
}

func hashRecoveryCode(code string) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
