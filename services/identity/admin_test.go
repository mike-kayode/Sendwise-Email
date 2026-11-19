package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/delivery"
)

type adminTestStore struct {
	*fakeStore
	mfa       map[string]adminMFAState
	recovery  map[string]map[string]bool
	auditRows []adminAuditRecord
	failures  map[string]int
	lockedTil map[string]time.Time
}

type fakeLegacyAPIAdmin struct {
	domains               []AdminSendingDomain
	campaigns             []AdminCampaign
	transactionalMessages []AdminTransactionalMessage
	suppressions          []AdminSuppression
	metrics               AdminOperationalMetrics
	domain                AdminSendingDomain
	recheck               AdminSendingDomain
	total                 int
	err                   error
	domainErr             error
	recheckErr            error
	disableDomain         AdminSendingDomain
	domainDisabled        bool
	disableDomainErr      error
	deletedDomainID       string
	deleteDomainErr       error
	resolvedDomain        AdminSendingDomain
	resolvedDomainID      string
	rightfulDomainID      string
	resolveDomainErr      error
	workspaceStatus       string
	workspaceStatusErr    error
	search                string
	status                string
	nearLimit             bool
	limit                 int
	offset                int
	recheckedID           string
	campaignSearch        string
	campaignStatus        string
	campaignWorkspaceID   string
	stoppedCampaignID     string
	campaignStopErr       error
	transactionalSearch   string
	suppressionSearch     string
	suppressionWorkspace  string
	createdSuppression    AdminSuppression
	deletedSuppressionID  string
	suppressionErr        error
	domainQuotaData       json.RawMessage
	domainWarmupData      json.RawMessage
	warmupOverrideError   error
	warmupOverrideCap     int
	warmupOverrideExpiry  time.Time
	warmupOverrideReason  string
	warmupOverrideCleared bool
	quotaDefaultsData     json.RawMessage
	nearLimitData         json.RawMessage
	domainQuotaStreams    json.RawMessage
	quotaDefaultsInput    json.RawMessage
	quotaUpdatedBy        string
	quotaUpdateReason     string
}

func (f *fakeLegacyAPIAdmin) AdminDomains(_ context.Context, search, status string, nearLimit bool, limit, offset int) ([]AdminSendingDomain, int, error) {
	f.search, f.status, f.nearLimit, f.limit, f.offset = search, status, nearLimit, limit, offset
	return f.domains, f.total, f.err
}

func (f *fakeLegacyAPIAdmin) AdminDomain(_ context.Context, _ string) (AdminSendingDomain, error) {
	return f.domain, f.domainErr
}

func (f *fakeLegacyAPIAdmin) RecheckAdminDomain(_ context.Context, domainID string) (AdminSendingDomain, error) {
	f.recheckedID = domainID
	return f.recheck, f.recheckErr
}

func (f *fakeLegacyAPIAdmin) SetAdminDomainDisabled(_ context.Context, domainID string, disabled bool) (AdminSendingDomain, error) {
	f.recheckedID = domainID
	f.domainDisabled = disabled
	return f.disableDomain, f.disableDomainErr
}

func (f *fakeLegacyAPIAdmin) DeleteAdminDomain(_ context.Context, domainID string) error {
	f.deletedDomainID = domainID
	return f.deleteDomainErr
}

func (f *fakeLegacyAPIAdmin) ResolveAdminDomainConflict(_ context.Context, domainID, rightfulDomainID string) (AdminSendingDomain, error) {
	f.resolvedDomainID = domainID
	f.rightfulDomainID = rightfulDomainID
	return f.resolvedDomain, f.resolveDomainErr
}

func (f *fakeLegacyAPIAdmin) AdminWorkspaceStatus(context.Context, string) (string, error) {
	return f.workspaceStatus, f.workspaceStatusErr
}

func (f *fakeLegacyAPIAdmin) SetAdminWorkspaceStatus(_ context.Context, _, _ string, status string) (string, error) {
	if f.workspaceStatusErr != nil {
		return "", f.workspaceStatusErr
	}
	f.workspaceStatus = status
	return status, nil
}

func (f *fakeLegacyAPIAdmin) AdminCampaigns(_ context.Context, search, status, workspaceID string, limit, offset int) ([]AdminCampaign, int, error) {
	f.campaignSearch, f.campaignStatus, f.campaignWorkspaceID = search, status, workspaceID
	f.limit, f.offset = limit, offset
	return f.campaigns, f.total, f.err
}

func (f *fakeLegacyAPIAdmin) StopAdminCampaign(_ context.Context, campaignID string) error {
	f.stoppedCampaignID = campaignID
	return f.campaignStopErr
}

func (f *fakeLegacyAPIAdmin) AdminCampaignDelivery(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`{"status_counts":{}}`), f.err
}

func (f *fakeLegacyAPIAdmin) ResumeAdminCampaignDeferredJobs(context.Context, string) (int64, error) {
	return 0, f.err
}

func (f *fakeLegacyAPIAdmin) ReplayAdminCampaignDeadLetters(context.Context, string) (int64, error) {
	return 0, f.err
}

func (f *fakeLegacyAPIAdmin) SearchAdminTransactionalMessages(_ context.Context, search string, limit, offset int) ([]AdminTransactionalMessage, int, error) {
	f.transactionalSearch, f.limit, f.offset = search, limit, offset
	return f.transactionalMessages, f.total, f.err
}

func (f *fakeLegacyAPIAdmin) AdminOperationalMetrics(context.Context) (AdminOperationalMetrics, error) {
	return f.metrics, f.err
}

func (f *fakeLegacyAPIAdmin) AdminDomainQuotas(context.Context, string) (json.RawMessage, error) {
	return f.domainQuotaData, f.domainErr
}

func (f *fakeLegacyAPIAdmin) SetAdminDomainQuotas(
	_ context.Context, _, updatedBy, reason string, streams json.RawMessage,
) (json.RawMessage, error) {
	f.quotaUpdatedBy, f.quotaUpdateReason = updatedBy, reason
	f.domainQuotaStreams = streams
	return f.domainQuotaData, f.domainErr
}

func (f *fakeLegacyAPIAdmin) AdminDomainWarmup(context.Context, string) (json.RawMessage, error) {
	return f.domainWarmupData, f.domainErr
}

func (f *fakeLegacyAPIAdmin) SetAdminDomainWarmupOverride(
	_ context.Context, _, _ string, dailyCap int, expiresAt time.Time, reason string,
) (json.RawMessage, error) {
	f.warmupOverrideCap = dailyCap
	f.warmupOverrideExpiry = expiresAt
	f.warmupOverrideReason = reason
	return f.domainWarmupData, f.warmupOverrideError
}

func (f *fakeLegacyAPIAdmin) ClearAdminDomainWarmupOverride(
	_ context.Context, _, _, _ string,
) (json.RawMessage, error) {
	f.warmupOverrideCleared = true
	return f.domainWarmupData, f.warmupOverrideError
}

func (f *fakeLegacyAPIAdmin) GraduateAdminDomainWarmup(
	_ context.Context, _, _, _ string, _ bool,
) (json.RawMessage, error) {
	return f.domainWarmupData, f.warmupOverrideError
}

func (f *fakeLegacyAPIAdmin) RevokeAdminDomainWarmupGraduation(
	_ context.Context, _, _, _ string,
) (json.RawMessage, error) {
	return f.domainWarmupData, f.warmupOverrideError
}

func (f *fakeLegacyAPIAdmin) AdminQuotaDefaults(context.Context) (json.RawMessage, error) {
	return f.quotaDefaultsData, f.err
}

func (f *fakeLegacyAPIAdmin) SetAdminQuotaDefaults(
	_ context.Context, updatedBy, reason string, defaults json.RawMessage,
) (json.RawMessage, error) {
	f.quotaUpdatedBy, f.quotaUpdateReason = updatedBy, reason
	f.quotaDefaultsInput = defaults
	return f.quotaDefaultsData, f.err
}

func (f *fakeLegacyAPIAdmin) AdminQuotasNearLimit(context.Context) (json.RawMessage, error) {
	return f.nearLimitData, f.err
}

func (f *fakeLegacyAPIAdmin) AdminSuppressions(_ context.Context, search, workspaceID string, limit, offset int) ([]AdminSuppression, int, error) {
	f.suppressionSearch, f.suppressionWorkspace = search, workspaceID
	f.limit, f.offset = limit, offset
	return f.suppressions, f.total, f.suppressionErr
}

func (f *fakeLegacyAPIAdmin) CreateAdminSuppression(_ context.Context, workspaceID, email string) (AdminSuppression, error) {
	if f.suppressionErr != nil {
		return AdminSuppression{}, f.suppressionErr
	}
	item := f.createdSuppression
	item.WorkspaceID = workspaceID
	item.Email = email
	return item, nil
}

func (f *fakeLegacyAPIAdmin) DeleteAdminSuppression(_ context.Context, suppressionID string) error {
	f.deletedSuppressionID = suppressionID
	return f.suppressionErr
}

func newAdminTestStore(store *fakeStore) *adminTestStore {
	return &adminTestStore{
		fakeStore: store,
		mfa:       make(map[string]adminMFAState),
		recovery:  make(map[string]map[string]bool),
		failures:  make(map[string]int),
		lockedTil: make(map[string]time.Time),
	}
}

func (s *adminTestStore) GetAdminMFA(_ context.Context, userID string) (adminMFAState, error) {
	return s.mfa[userID], nil
}

func (s *adminTestStore) SaveAdminMFASetup(_ context.Context, userID, secret string, hashes []string) error {
	if s.mfa[userID].Enabled {
		return errors.New("admin MFA is already enabled")
	}
	s.mfa[userID] = adminMFAState{Secret: secret}
	s.recovery[userID] = make(map[string]bool, len(hashes))
	for _, hash := range hashes {
		s.recovery[userID][hash] = true
	}
	return nil
}

func (s *adminTestStore) EnableAdminMFA(_ context.Context, userID string) error {
	state := s.mfa[userID]
	if state.Secret == "" || state.Enabled {
		return errors.New("admin MFA setup is missing or already enabled")
	}
	state.Enabled = true
	s.mfa[userID] = state
	return nil
}

func (s *adminTestStore) ConsumeAdminRecoveryCode(_ context.Context, userID, codeHash string) (bool, error) {
	if !s.recovery[userID][codeHash] {
		return false, nil
	}
	delete(s.recovery[userID], codeHash)
	return true, nil
}

func (s *adminTestStore) AdminMFALocked(_ context.Context, userID string) (bool, error) {
	return time.Now().Before(s.lockedTil[userID]), nil
}

func (s *adminTestStore) RecordAdminMFAFailure(_ context.Context, userID string) (bool, error) {
	s.failures[userID]++
	if s.failures[userID] >= 5 {
		s.lockedTil[userID] = time.Now().Add(15 * time.Minute)
		return true, nil
	}
	return false, nil
}

func (s *adminTestStore) ClearAdminMFAFailures(_ context.Context, userID string) error {
	delete(s.failures, userID)
	delete(s.lockedTil, userID)
	return nil
}

func (s *adminTestStore) MarkAdminSessionMFAVerified(_ context.Context, sessionID, userID string, verifiedAt time.Time) error {
	session, ok := s.sessions[sessionID]
	if !ok || session.UserID != userID {
		return ErrNotFound
	}
	session.AdminMFAVerifiedAt = &verifiedAt
	return nil
}

func (s *adminTestStore) WriteAdminAudit(_ context.Context, record adminAuditRecord) error {
	s.auditRows = append(s.auditRows, record)
	return nil
}

func (s *adminTestStore) ListAdminAudit(_ context.Context, limit, offset int) ([]adminAuditRecord, error) {
	if offset >= len(s.auditRows) {
		return []adminAuditRecord{}, nil
	}
	end := offset + limit
	if end > len(s.auditRows) {
		end = len(s.auditRows)
	}
	return append([]adminAuditRecord(nil), s.auditRows[offset:end]...), nil
}

func (s *adminTestStore) ListAdminAuditFiltered(_ context.Context, action, actorID string, limit, offset int) ([]adminAuditRecord, int, error) {
	filtered := make([]adminAuditRecord, 0, len(s.auditRows))
	for _, record := range s.auditRows {
		if action != "" && record.Action != action {
			continue
		}
		if actorID != "" && (record.ActorUserID == nil || *record.ActorUserID != actorID) {
			continue
		}
		filtered = append(filtered, record)
	}
	total := len(filtered)
	if offset >= total {
		return []adminAuditRecord{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return append([]adminAuditRecord(nil), filtered[offset:end]...), total, nil
}

func (s *adminTestStore) ListAdminWorkspaceNotes(_ context.Context, workspaceID string, limit int) ([]adminWorkspaceNote, error) {
	notes := make([]adminWorkspaceNote, 0)
	for i := len(s.auditRows) - 1; i >= 0 && len(notes) < limit; i-- {
		record := s.auditRows[i]
		if record.Action != "admin_workspace_note_added" || record.TargetType != "workspace" || record.TargetID != workspaceID {
			continue
		}
		var value struct {
			Note string `json:"note"`
		}
		if err := json.Unmarshal(record.AfterState, &value); err != nil {
			return nil, err
		}
		notes = append(notes, adminWorkspaceNote{
			ID: record.ID, ActorUserID: record.ActorUserID, Note: value.Note,
			Reason: record.Reason, CreatedAt: record.CreatedAt,
		})
	}
	return notes, nil
}

func (s *adminTestStore) ListAdminUsers(_ context.Context, search, status string, limit, offset int) ([]adminUserSummary, int, error) {
	s.fakeStore.mu.Lock()
	defer s.fakeStore.mu.Unlock()
	users := make([]adminUserSummary, 0)
	for _, user := range s.fakeStore.usersByID {
		if status != "" && user.Status != status {
			continue
		}
		query := strings.ToLower(search)
		if query != "" && !strings.Contains(strings.ToLower(user.Email), query) &&
			!strings.Contains(strings.ToLower(user.Name), query) {
			continue
		}
		users = append(users, adminUserSummary{
			ID: user.ID, Email: user.Email, Name: user.Name, Status: user.Status,
			PlatformRole: user.PlatformRole, EmailVerifiedAt: user.EmailVerifiedAt,
			LastLoginAt: user.LastLoginAt, CreatedAt: user.CreatedAt,
		})
	}
	total := len(users)
	if offset >= total {
		return []adminUserSummary{}, total, nil
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return users[offset:end], total, nil
}

func (s *adminTestStore) GetAdminUser(_ context.Context, userID string) (adminUserSummary, error) {
	user, err := s.fakeStore.GetUserByID(context.Background(), userID)
	if err != nil {
		return adminUserSummary{}, err
	}
	return adminUserSummary{
		ID: user.ID, Email: user.Email, Name: user.Name, Status: user.Status,
		PlatformRole: user.PlatformRole, EmailVerifiedAt: user.EmailVerifiedAt,
		LastLoginAt: user.LastLoginAt, CreatedAt: user.CreatedAt,
	}, nil
}

func (s *adminTestStore) GetAdminUserCounts(_ context.Context) (adminUserCounts, error) {
	s.fakeStore.mu.Lock()
	defer s.fakeStore.mu.Unlock()
	var counts adminUserCounts
	for _, user := range s.fakeStore.usersByID {
		counts.Total++
		switch user.Status {
		case "active":
			counts.Active++
		case "pending":
			counts.Pending++
		case "suspended":
			counts.Suspended++
		case "closed":
			counts.Closed++
		}
	}
	return counts, nil
}

func (s *adminTestStore) SetAdminUserStatus(_ context.Context, userID, status string, record adminAuditRecord) (adminUserSummary, int, error) {
	s.fakeStore.mu.Lock()
	user, ok := s.fakeStore.usersByID[userID]
	if !ok {
		s.fakeStore.mu.Unlock()
		return adminUserSummary{}, 0, ErrNotFound
	}
	if (user.Status != "active" && user.Status != "suspended") || user.Status == status {
		s.fakeStore.mu.Unlock()
		return adminUserSummary{}, 0, ErrInvalidUserStatus
	}
	before := user.Status
	user.Status = status
	sessionsRevoked := 0
	if status == "suspended" {
		for _, session := range s.fakeStore.sessions {
			if session.UserID == userID && session.RevokedAt == nil {
				now := time.Now()
				session.RevokedAt = &now
				sessionsRevoked++
			}
		}
	}
	result := adminUserSummary{
		ID: user.ID, Email: user.Email, Name: user.Name, Status: user.Status,
		PlatformRole: user.PlatformRole, EmailVerifiedAt: user.EmailVerifiedAt,
		LastLoginAt: user.LastLoginAt, CreatedAt: user.CreatedAt,
	}
	s.fakeStore.mu.Unlock()
	record.BeforeState = json.RawMessage(`{"status":"` + before + `"}`)
	record.AfterState = json.RawMessage(`{"status":"` + status + `","sessions_revoked":0}`)
	if status == "suspended" {
		record.AfterState = json.RawMessage(`{"status":"suspended","sessions_revoked":` + strconv.Itoa(sessionsRevoked) + `}`)
	}
	s.auditRows = append(s.auditRows, record)
	return result, sessionsRevoked, nil
}

func (s *adminTestStore) VerifyAdminUserEmail(_ context.Context, userID string, record adminAuditRecord) (adminUserSummary, error) {
	s.fakeStore.mu.Lock()
	user, ok := s.fakeStore.usersByID[userID]
	if !ok {
		s.fakeStore.mu.Unlock()
		return adminUserSummary{}, ErrNotFound
	}
	if user.Status != "pending" || user.EmailVerifiedAt != nil {
		s.fakeStore.mu.Unlock()
		return adminUserSummary{}, ErrInvalidUserStatus
	}
	now := time.Now()
	user.Status = "active"
	user.EmailVerifiedAt = &now
	result := adminUserSummary{
		ID: user.ID, Email: user.Email, Name: user.Name, Status: user.Status,
		PlatformRole: user.PlatformRole, EmailVerifiedAt: user.EmailVerifiedAt,
		LastLoginAt: user.LastLoginAt, CreatedAt: user.CreatedAt,
	}
	s.fakeStore.mu.Unlock()
	record.BeforeState = json.RawMessage(`{"status":"pending","email_verified":false}`)
	record.AfterState = json.RawMessage(`{"status":"active","email_verified":true}`)
	s.auditRows = append(s.auditRows, record)
	return result, nil
}

func (s *adminTestStore) DemoteAdminUser(_ context.Context, userID string, record adminAuditRecord) (adminUserSummary, int, error) {
	s.fakeStore.mu.Lock()
	user, ok := s.fakeStore.usersByID[userID]
	if !ok {
		s.fakeStore.mu.Unlock()
		return adminUserSummary{}, 0, ErrNotFound
	}
	if user.PlatformRole == PlatformRoleNone {
		s.fakeStore.mu.Unlock()
		return adminUserSummary{}, 0, ErrInvalidUserStatus
	}
	if user.PlatformRole == PlatformRoleSuperAdmin && user.Status == "active" {
		activeSuperAdmins := 0
		for _, candidate := range s.fakeStore.usersByID {
			if candidate.Status == "active" && candidate.PlatformRole == PlatformRoleSuperAdmin {
				activeSuperAdmins++
			}
		}
		if activeSuperAdmins <= 1 {
			s.fakeStore.mu.Unlock()
			return adminUserSummary{}, 0, ErrLastSuperAdmin
		}
	}
	before := user.PlatformRole
	user.PlatformRole = PlatformRoleNone
	sessionsRevoked := 0
	for _, session := range s.fakeStore.sessions {
		if session.UserID == userID && session.RevokedAt == nil {
			now := time.Now()
			session.RevokedAt = &now
			sessionsRevoked++
		}
	}
	result := adminUserSummary{
		ID: user.ID, Email: user.Email, Name: user.Name, Status: user.Status,
		PlatformRole: user.PlatformRole, EmailVerifiedAt: user.EmailVerifiedAt,
		LastLoginAt: user.LastLoginAt, CreatedAt: user.CreatedAt,
	}
	s.fakeStore.mu.Unlock()
	record.BeforeState = json.RawMessage(`{"platform_role":"` + before + `"}`)
	record.AfterState = json.RawMessage(`{"platform_role":"none","sessions_revoked":` + strconv.Itoa(sessionsRevoked) + `}`)
	s.auditRows = append(s.auditRows, record)
	return result, sessionsRevoked, nil
}

func (s *adminTestStore) RevokeAdminUserSessions(_ context.Context, userID string, record adminAuditRecord) (int, error) {
	s.fakeStore.mu.Lock()
	if _, ok := s.fakeStore.usersByID[userID]; !ok {
		s.fakeStore.mu.Unlock()
		return 0, ErrNotFound
	}
	revoked := 0
	for _, session := range s.fakeStore.sessions {
		if session.UserID == userID && session.RevokedAt == nil {
			now := time.Now()
			session.RevokedAt = &now
			revoked++
		}
	}
	s.fakeStore.mu.Unlock()
	record.BeforeState = json.RawMessage(`{"active_sessions":` + strconv.Itoa(revoked) + `}`)
	record.AfterState = json.RawMessage(`{"sessions_revoked":` + strconv.Itoa(revoked) + `}`)
	s.auditRows = append(s.auditRows, record)
	return revoked, nil
}

func TestAdminPermissionMatrixSeparatesPlatformAndWorkspaceRoles(t *testing.T) {
	if CanAdmin("owner", AdminManagePlatformRole) || CanAdmin("admin", AdminManagePlatformRole) ||
		CanAdmin(PlatformRoleSupportAdmin, AdminManagePlatformRole) ||
		CanAdmin(PlatformRoleSupportAdmin, AdminManageCustomers) {
		t.Fatal("workspace roles and support_admin must not manage platform roles")
	}
	if !CanAdmin(PlatformRoleSupportAdmin, AdminReadAudit) ||
		!CanAdmin(PlatformRoleSupportAdmin, AdminResendVerification) ||
		!CanAdmin(PlatformRoleSuperAdmin, AdminManagePlatformRole) ||
		CanAdmin(PlatformRoleSupportAdmin, AdminManageWorkspaces) ||
		!CanAdmin(PlatformRoleSuperAdmin, AdminManageWorkspaces) ||
		CanAdmin(PlatformRoleSupportAdmin, AdminManageSuppressions) ||
		!CanAdmin(PlatformRoleSuperAdmin, AdminManageSuppressions) ||
		CanAdmin(PlatformRoleSupportAdmin, AdminManageDomains) ||
		!CanAdmin(PlatformRoleSuperAdmin, AdminManageDomains) ||
		CanAdmin(PlatformRoleSuperAdmin, AdminPermission("export_contacts")) {
		t.Fatal("platform admin permissions do not match the deny-by-default matrix")
	}
}

func TestAdminDomainQuotaWriteRequiresSuperAdminFreshMFAReasonAndAudit(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "domain-quota-admin@example.test")
	operatorToken := createAdminTestSession(t, handler, baseStore, operator.ID)
	domainID := "9b2df23a-6c47-4e56-8648-927b1645d408"
	workspaceID := "a83bf6e5-2d12-4ca7-b31f-fcf8bf1277e4"
	before := json.RawMessage(`{"streams":{"marketing":{"daily_limit":100}}}`)
	apiClient := &fakeLegacyAPIAdmin{
		domain:          AdminSendingDomain{ID: domainID, WorkspaceID: workspaceID, Domain: "mail.example.test"},
		domainQuotaData: before,
	}
	handler.legacyAPI = apiClient
	path := "/admin/domains/" + domainID + "/quotas"
	body := `{"streams":[{"stream":"marketing","daily_limit":500},{"stream":"transactional","daily_limit":null}],"reason":"Approved sending increase"}`

	anonymous := adminRequest(t, handler.Routes(), http.MethodPut, path, "", body)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous quota update = %d, want 401: %s", anonymous.Code, anonymous.Body.String())
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleNone
	normalUser := adminRequest(t, handler.Routes(), http.MethodPut, path, operatorToken, body)
	if normalUser.Code != http.StatusForbidden {
		t.Fatalf("normal-user quota update = %d, want 403: %s", normalUser.Code, normalUser.Body.String())
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	freshMFA := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	supportAdmin := adminRequest(t, handler.Routes(), http.MethodPut, path, operatorToken, body)
	if supportAdmin.Code != http.StatusForbidden {
		t.Fatalf("support_admin quota update = %d, want 403: %s", supportAdmin.Code, supportAdmin.Body.String())
	}
	if len(adminStore.auditRows) != 0 {
		t.Fatalf("denied quota update wrote audit rows: %+v", adminStore.auditRows)
	}

	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	staleMFA := time.Now().Add(-10 * time.Minute)
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &staleMFA
		}
	}
	stale := adminRequest(t, handler.Routes(), http.MethodPut, path, operatorToken, body)
	if stale.Code != http.StatusForbidden || !strings.Contains(stale.Body.String(), "admin_reauth_required") {
		t.Fatalf("quota update with stale MFA = %d, want reauthentication: %s", stale.Code, stale.Body.String())
	}
	freshMFA = time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	withoutReason := adminRequest(t, handler.Routes(), http.MethodPut, path, operatorToken,
		`{"streams":[{"stream":"marketing","daily_limit":500},{"stream":"transactional","daily_limit":null}]}`)
	if withoutReason.Code != http.StatusBadRequest || len(adminStore.auditRows) != 0 {
		t.Fatalf("quota update without reason = %d, audit rows=%d: %s", withoutReason.Code, len(adminStore.auditRows), withoutReason.Body.String())
	}

	updated := adminRequest(t, handler.Routes(), http.MethodPut, path, operatorToken, body)
	if updated.Code != http.StatusOK || apiClient.quotaUpdatedBy != operator.ID ||
		apiClient.quotaUpdateReason != "Approved sending increase" {
		t.Fatalf("super_admin quota update = %d, updated_by=%q reason=%q: %s",
			updated.Code, apiClient.quotaUpdatedBy, apiClient.quotaUpdateReason, updated.Body.String())
	}
	if len(adminStore.auditRows) != 1 {
		t.Fatalf("quota update audit rows = %d, want one", len(adminStore.auditRows))
	}
	audit := adminStore.auditRows[0]
	if audit.Action != "admin_domain_quotas_updated" || audit.TargetID != domainID ||
		audit.WorkspaceID == nil || *audit.WorkspaceID != workspaceID ||
		audit.Reason != "Approved sending increase" || !strings.Contains(string(audit.BeforeState), `"daily_limit":100`) ||
		!strings.Contains(string(audit.AfterState), `"daily_limit":500`) {
		t.Fatalf("quota update audit lacks before/after state or target metadata: %+v", audit)
	}
}

func TestSafeCSVCellPreventsSpreadsheetFormulaExecution(t *testing.T) {
	for _, value := range []string{"=1+1", " +SUM(A1:A2)", "-1+2", "@SUM(1,2)"} {
		if got := safeCSVCell(value); got != "'"+value {
			t.Errorf("safeCSVCell(%q) = %q, want formula-prefixed value", value, got)
		}
	}
	if got := safeCSVCell("ordinary audit reason"); got != "ordinary audit reason" {
		t.Errorf("safeCSVCell() changed plain text: %q", got)
	}
}

func TestAdminRoutesDenyAnonymousAndWorkspaceOwner(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore

	workspaceOwner := seedVerifiedUser(t, handler, baseStore, workspace, "owner@example.test")
	// A workspace owner remains an ordinary platform user.
	baseStore.usersByID[workspaceOwner.ID].PlatformRole = PlatformRoleNone
	token := createAdminTestSession(t, handler, baseStore, workspaceOwner.ID)

	anonymous := httptest.NewRecorder()
	handler.Routes().ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/admin/session", nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous admin status = %d, want 401", anonymous.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/session", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("workspace owner admin status = %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestRevokedAdminSessionCannotAccessAdminRoutes(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	user := seedVerifiedUser(t, handler, baseStore, workspace, "revoked-admin@example.test")
	baseStore.usersByID[user.ID].PlatformRole = PlatformRoleSuperAdmin
	token := createAdminTestSession(t, handler, baseStore, user.ID)
	for _, session := range baseStore.sessions {
		if session.UserID == user.ID {
			now := time.Now()
			session.RevokedAt = &now
		}
	}

	response := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/session", token, "")
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "session_revoked") {
		t.Fatalf("revoked admin session status = %d, want 401: %s", response.Code, response.Body.String())
	}
}

func TestAdminMFASetupAndVerificationProtectAuditAPI(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	user := seedVerifiedUser(t, handler, baseStore, workspace, "operator@example.test")
	baseStore.usersByID[user.ID].PlatformRole = PlatformRoleSuperAdmin
	token := createAdminTestSession(t, handler, baseStore, user.ID)

	setup := adminRequest(t, handler.Routes(), http.MethodPost, "/admin/mfa/setup", token, "")
	if setup.Code != http.StatusOK {
		t.Fatalf("MFA setup status = %d, want 200: %s", setup.Code, setup.Body.String())
	}

	var setupResponse struct {
		Secret        string   `json:"secret"`
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(setup.Body.Bytes(), &setupResponse); err != nil {
		t.Fatalf("decode MFA setup response: %v", err)
	}
	if setupResponse.Secret == "" || len(setupResponse.RecoveryCodes) != 10 {
		t.Fatalf("MFA setup response has no secret or recovery codes: %+v", setupResponse)
	}

	blocked := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/audit", token, "")
	if blocked.Code != http.StatusForbidden || !strings.Contains(blocked.Body.String(), "admin_mfa_required") {
		t.Fatalf("audit access before MFA status = %d, want 403 MFA-required: %s", blocked.Code, blocked.Body.String())
	}

	code, ok := totpCode(setupResponse.Secret, time.Now())
	if !ok {
		t.Fatal("generated TOTP secret could not be decoded")
	}
	verifyBody, _ := json.Marshal(map[string]string{"code": code})
	verified := adminRequest(t, handler.Routes(), http.MethodPost, "/admin/mfa/verify", token, string(verifyBody))
	if verified.Code != http.StatusOK {
		t.Fatalf("MFA verification status = %d, want 200: %s", verified.Code, verified.Body.String())
	}
	if !adminStore.mfa[user.ID].Enabled {
		t.Fatal("successful TOTP verification did not enable admin MFA")
	}

	audit := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/audit", token, "")
	if audit.Code != http.StatusOK {
		t.Fatalf("audit access after MFA status = %d, want 200: %s", audit.Code, audit.Body.String())
	}
	filteredAudit := adminRequest(t, handler.Routes(), http.MethodGet,
		"/admin/audit?action=admin_mfa_verified&limit=1", token, "")
	if filteredAudit.Code != http.StatusOK ||
		!strings.Contains(filteredAudit.Body.String(), `"total":1`) ||
		!strings.Contains(filteredAudit.Body.String(), `"action":"admin_mfa_verified"`) {
		t.Fatalf("filtered audit response = %d, want one matching event: %s", filteredAudit.Code, filteredAudit.Body.String())
	}
	missingExportReason := adminRequest(t, handler.Routes(), http.MethodPost, "/admin/audit/export", token, `{"limit":10}`)
	if missingExportReason.Code != http.StatusBadRequest {
		t.Fatalf("audit export without reason status = %d, want 400: %s", missingExportReason.Code, missingExportReason.Body.String())
	}
	export := adminRequest(t, handler.Routes(), http.MethodPost, "/admin/audit/export", token,
		`{"reason":"Incident review","action":"admin_mfa_verified","limit":10}`)
	if export.Code != http.StatusOK ||
		!strings.Contains(export.Body.String(), `"exported":1`) ||
		!strings.Contains(export.Body.String(), "admin_mfa_verified") {
		t.Fatalf("audit export status = %d, want one CSV record: %s", export.Code, export.Body.String())
	}
	if len(adminStore.auditRows) != 3 ||
		adminStore.auditRows[0].Action != "admin_mfa_setup_started" ||
		adminStore.auditRows[1].Action != "admin_mfa_verified" ||
		adminStore.auditRows[2].Action != "admin_audit_exported" ||
		adminStore.auditRows[2].Reason != "Incident review" {
		t.Fatalf("expected setup, verification, and reasoned export audit entries, got %+v", adminStore.auditRows)
	}
}

func TestAdminUsersRequiresFreshMFAAndReturnsOnlyMetadata(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	target := seedVerifiedUser(t, handler, baseStore, workspace, "person@example.test")
	baseStore.usersByID[target.ID].Name = "Sample Person"
	token := createAdminTestSession(t, handler, baseStore, operator.ID)

	blocked := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/users", token, "")
	if blocked.Code != http.StatusForbidden || !strings.Contains(blocked.Body.String(), "admin_mfa_required") {
		t.Fatalf("users access before MFA status = %d, want 403 MFA-required: %s", blocked.Code, blocked.Body.String())
	}

	setup := adminRequest(t, handler.Routes(), http.MethodPost, "/admin/mfa/setup", token, "")
	var setupResponse struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(setup.Body.Bytes(), &setupResponse); err != nil {
		t.Fatalf("decode MFA setup response: %v", err)
	}
	code, ok := totpCode(setupResponse.Secret, time.Now())
	if !ok {
		t.Fatal("generated TOTP secret could not be decoded")
	}
	verifyBody, _ := json.Marshal(map[string]string{"code": code})
	verified := adminRequest(t, handler.Routes(), http.MethodPost, "/admin/mfa/verify", token, string(verifyBody))
	if verified.Code != http.StatusOK {
		t.Fatalf("MFA verification status = %d, want 200: %s", verified.Code, verified.Body.String())
	}

	response := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/users?search=person&status=active&limit=1", token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("admin users status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data       []adminUserSummary `json:"data"`
		Pagination struct {
			Total  int `json:"total"`
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode admin users response: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].Email != target.Email ||
		body.Pagination.Total != 1 || body.Pagination.Limit != 1 {
		t.Fatalf("unexpected admin users response: %+v", body)
	}
	if strings.Contains(response.Body.String(), "password_hash") || strings.Contains(response.Body.String(), "PasswordHash") {
		t.Fatal("admin user response exposed a password hash")
	}
}

func TestAdminUserDetailRequiresFreshMFAAndReturnsOnlyMetadata(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "user-detail-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)

	target, err := baseStore.CreateUser(context.Background(), "private@example.test", "sensitive-hash", "Private User")
	if err != nil {
		t.Fatalf("create target user: %v", err)
	}
	now := time.Now()
	baseStore.usersByID[target.ID].EmailVerifiedAt = &now

	response := adminRequest(t, handler.Routes(), http.MethodGet,
		"/admin/users/"+target.ID, token, "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("without fresh MFA status = %d, want 403", response.Code)
	}
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}
	response = adminRequest(t, handler.Routes(), http.MethodGet,
		"/admin/users/"+target.ID, token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("admin user detail status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sensitive-hash") {
		t.Fatal("admin user detail exposed a password hash")
	}
	var body struct {
		Data adminUserSummary `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode admin user details: %v", err)
	}
	if body.Data.ID != target.ID || body.Data.Email != target.Email ||
		body.Data.Name != target.Name || body.Data.EmailVerifiedAt == nil {
		t.Fatalf("unexpected admin user detail: %+v", body.Data)
	}
}

func TestAdminOverviewRequiresMFAAndReturnsServiceOwnedCounts(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "overview-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	target, err := baseStore.CreateUser(context.Background(), "recent@example.test", "private-hash", "Recent User")
	if err != nil {
		t.Fatalf("create recent user: %v", err)
	}
	blocked := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/overview", token, "")
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("admin overview without MFA status = %d, want 403", blocked.Code)
	}
	now := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}
	workspace.adminOrgs = []WorkspaceAdminOrganization{{
		ID: fakeUUID(970), Name: "Real Workspace", Slug: "real-workspace", MemberCount: 1, CreatedAt: now,
	}}
	handler.legacyAPI = &fakeLegacyAPIAdmin{
		metrics: AdminOperationalMetrics{
			DomainCount: 3, CampaignCount: 4, MessagesToday: 2, Messages7Days: 8,
			Messages30Days: 10, DeliveryRate30d: 80, BounceRate30d: 10,
			ComplaintRate30d: 2, QueueDepth: 1, UpdatedAt: now,
		},
	}
	adminStore.auditRows = []adminAuditRecord{{
		ID: 1, ActorRole: PlatformRoleSuperAdmin, Action: "test_admin_action",
		TargetType: "user", TargetID: target.ID, Reason: "Test audit row", CreatedAt: now,
	}}

	response := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/overview", token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("admin overview status = %d, want 200: %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{
		`"total":2`, `"active":1`, `"pending":1`, `"workspace_count":1`,
		`"campaign_count":4`, `"messages_30_days":10`, `"delivery_rate_30_days":80`,
		`"test_admin_action"`, `"recent@example.test"`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("admin overview omitted %q: %s", expected, response.Body.String())
		}
	}
	if strings.Contains(response.Body.String(), "private-hash") {
		t.Fatal("admin overview exposed a password hash")
	}
}

func TestSuperAdminCanOnlyDemotePlatformRolesWithReason(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "role-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	adminToken := createAdminTestSession(t, handler, baseStore, operator.ID)
	target := seedVerifiedUser(t, handler, baseStore, workspace, "role-target@example.test")
	baseStore.usersByID[target.ID].PlatformRole = PlatformRoleSupportAdmin
	createAdminTestSession(t, handler, baseStore, target.ID)
	now := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}

	path := "/admin/users/" + target.ID + "/platform-role"
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	supportDenied := adminRequest(t, handler.Routes(), http.MethodPatch, path, adminToken,
		`{"platform_role":"none","reason":"Test role control"}`)
	if supportDenied.Code != http.StatusForbidden {
		t.Fatalf("support_admin role removal = %d, want forbidden: %s", supportDenied.Code, supportDenied.Body.String())
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	selfDemotion := adminRequest(t, handler.Routes(), http.MethodPatch, "/admin/users/"+operator.ID+"/platform-role", adminToken,
		`{"platform_role":"none","reason":"Test role control"}`)
	if selfDemotion.Code != http.StatusBadRequest || !strings.Contains(selfDemotion.Body.String(), "admin_self_role_change_forbidden") {
		t.Fatalf("self-demotion = %d, want 400: %s", selfDemotion.Code, selfDemotion.Body.String())
	}
	promotion := adminRequest(t, handler.Routes(), http.MethodPatch, path, adminToken,
		`{"platform_role":"super_admin","reason":"Test role control"}`)
	if promotion.Code != http.StatusBadRequest || !strings.Contains(promotion.Body.String(), "admin_role_promotion_forbidden") {
		t.Fatalf("admin promotion via API = %d, want 400: %s", promotion.Code, promotion.Body.String())
	}
	response := adminRequest(t, handler.Routes(), http.MethodPatch, path, adminToken,
		`{"platform_role":"none","reason":"Operator access ended"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"platform_role":"none"`) ||
		!strings.Contains(response.Body.String(), `"sessions_revoked":1`) {
		t.Fatalf("admin demotion = %d, want role none and one revoked session: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 1 || adminStore.auditRows[0].Action != "admin_platform_role_removed" ||
		adminStore.auditRows[0].Reason != "Operator access ended" {
		t.Fatalf("admin demotion was not audited: %+v", adminStore.auditRows)
	}
}

func TestWorkspaceNotesAreAuditedAndSuperAdminOnly(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "notes-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	now := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}
	const workspaceID = "00000000-0000-4000-8000-000000000001"
	workspace.adminOrgs = []WorkspaceAdminOrganization{{ID: workspaceID, Name: "Notes workspace", Slug: "notes"}}
	path := "/admin/workspaces/" + workspaceID + "/notes"

	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	denied := adminRequest(t, handler.Routes(), http.MethodPost, path, token,
		`{"note":"Customer escalation follow-up","reason":"Support follow-up"}`)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("support_admin creating workspace note = %d, want forbidden: %s", denied.Code, denied.Body.String())
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	created := adminRequest(t, handler.Routes(), http.MethodPost, path, token,
		`{"note":"Customer escalation follow-up","reason":"Support follow-up"}`)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), "Customer escalation follow-up") {
		t.Fatalf("create workspace note = %d, want created note: %s", created.Code, created.Body.String())
	}
	if len(adminStore.auditRows) != 1 || adminStore.auditRows[0].Action != "admin_workspace_note_added" ||
		adminStore.auditRows[0].Reason != "Support follow-up" {
		t.Fatalf("workspace note audit = %+v", adminStore.auditRows)
	}
	listed := adminRequest(t, handler.Routes(), http.MethodGet, path, token, "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "Customer escalation follow-up") {
		t.Fatalf("list workspace notes = %d, want saved note: %s", listed.Code, listed.Body.String())
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	supportNotes := adminRequest(t, handler.Routes(), http.MethodGet, path, token, "")
	if supportNotes.Code != http.StatusForbidden {
		t.Fatalf("support_admin reading workspace notes = %d, want forbidden: %s", supportNotes.Code, supportNotes.Body.String())
	}
	supportAudit := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/audit?limit=100", token, "")
	if supportAudit.Code != http.StatusOK ||
		strings.Contains(supportAudit.Body.String(), "Customer escalation follow-up") ||
		strings.Contains(supportAudit.Body.String(), "Support follow-up") ||
		!strings.Contains(supportAudit.Body.String(), "Restricted workspace note") {
		t.Fatalf("support_admin audit response did not redact workspace note content: %d %s", supportAudit.Code, supportAudit.Body.String())
	}
}

func TestSuperAdminCanSendPasswordResetToActiveVerifiedUser(t *testing.T) {
	handler, baseStore, workspace, mailer := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "reset-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	target := seedVerifiedUser(t, handler, baseStore, workspace, "reset-target@example.test")
	verifiedAt := time.Now().UTC()
	baseStore.usersByID[target.ID].Status = "active"
	baseStore.usersByID[target.ID].EmailVerifiedAt = &verifiedAt
	now := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}

	response := adminRequest(t, handler.Routes(), http.MethodPost, "/admin/users/"+target.ID+"/password-reset", token,
		`{"reason":"Account recovery requested through support"}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("admin password reset = %d, want accepted: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "token") || len(mailer.Sent()) != 1 ||
		mailer.Sent()[0].To != target.Email || mailer.Sent()[0].Subject != "Reset your password" {
		t.Fatalf("password reset response or mail was unsafe/unexpected: response=%s sent=%+v", response.Body.String(), mailer.Sent())
	}
	foundResetToken := false
	for _, reset := range baseStore.tokens {
		if reset.userID == target.ID && reset.purpose == "password_reset" && !reset.used {
			foundResetToken = true
			break
		}
	}
	if !foundResetToken {
		t.Fatal("password reset did not store an expiring password_reset token")
	}
	if len(adminStore.auditRows) != 1 ||
		adminStore.auditRows[0].Action != "admin_password_reset_requested" ||
		adminStore.auditRows[0].Reason != "Account recovery requested through support" {
		t.Fatalf("password reset audit = %+v", adminStore.auditRows)
	}
}

func TestAdminWarmupOverrideAuthorizationFreshMFAAndAudit(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "warmup-admin@example.test")
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	domainID := "9b2df23a-6c47-4e56-8648-927b1645d408"
	workspaceID := "a83bf6e5-2d12-4ca7-b31f-fcf8bf1277e4"
	before := json.RawMessage(`{"profile":"standard","curve_cap":50,"effective_cap_today":50,"override":null}`)
	apiClient := &fakeLegacyAPIAdmin{
		domain:           AdminSendingDomain{ID: domainID, WorkspaceID: workspaceID, Domain: "mail.example.test"},
		domainWarmupData: before,
	}
	handler.legacyAPI = apiClient
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	freshMFA := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	path := "/admin/domains/" + domainID + "/warmup"
	writePath := path + "-override"
	read := adminRequest(t, handler.Routes(), http.MethodGet, path, token, "")
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"profile":"standard"`) {
		t.Fatalf("admin warm-up read = %d, want 200 with profile: %s", read.Code, read.Body.String())
	}

	expiresAt := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	requestBody, err := json.Marshal(map[string]any{
		"daily_cap": 10000, "expires_at": expiresAt, "reason": "Temporary capacity for launch",
	})
	if err != nil {
		t.Fatal(err)
	}
	anonymous := adminRequest(t, handler.Routes(), http.MethodPut, writePath, "", string(requestBody))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous warm-up write = %d, want 401", anonymous.Code)
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleNone
	normalUser := adminRequest(t, handler.Routes(), http.MethodPut, writePath, token, string(requestBody))
	if normalUser.Code != http.StatusForbidden {
		t.Fatalf("normal-user warm-up write = %d, want 403", normalUser.Code)
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	freshMFA = time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	supportAdmin := adminRequest(t, handler.Routes(), http.MethodPut, writePath, token, string(requestBody))
	if supportAdmin.Code != http.StatusForbidden {
		t.Fatalf("support_admin warm-up write = %d, want 403", supportAdmin.Code)
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	staleMFA := time.Now().Add(-10 * time.Minute)
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &staleMFA
		}
	}
	stale := adminRequest(t, handler.Routes(), http.MethodPut, writePath, token, string(requestBody))
	if stale.Code != http.StatusForbidden || !strings.Contains(stale.Body.String(), "admin_reauth_required") {
		t.Fatalf("stale-MFA warm-up write = %d, want 403: %s", stale.Code, stale.Body.String())
	}
	freshMFA = time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	withoutReason, _ := json.Marshal(map[string]any{"daily_cap": 10000, "expires_at": expiresAt})
	rejected := adminRequest(t, handler.Routes(), http.MethodPut, writePath, token, string(withoutReason))
	if rejected.Code != http.StatusBadRequest || len(adminStore.auditRows) != 0 {
		t.Fatalf("warm-up write without reason = %d, audits=%d: %s", rejected.Code, len(adminStore.auditRows), rejected.Body.String())
	}

	set := adminRequest(t, handler.Routes(), http.MethodPut, writePath, token, string(requestBody))
	if set.Code != http.StatusOK {
		t.Fatalf("super_admin warm-up write = %d, want 200: %s", set.Code, set.Body.String())
	}
	if apiClient.warmupOverrideCap != 10000 || apiClient.warmupOverrideReason != "Temporary capacity for launch" {
		t.Fatalf("warm-up override API call = cap %d reason %q", apiClient.warmupOverrideCap, apiClient.warmupOverrideReason)
	}
	if len(adminStore.auditRows) != 1 {
		t.Fatalf("warm-up set audit rows = %d, want 1", len(adminStore.auditRows))
	}
	setAudit := adminStore.auditRows[0]
	if setAudit.Action != "admin_domain_warmup_override_set" || setAudit.WorkspaceID == nil ||
		*setAudit.WorkspaceID != workspaceID || setAudit.Reason != "Temporary capacity for launch" ||
		string(setAudit.BeforeState) != `{"override":null}` ||
		!strings.Contains(string(setAudit.AfterState), `"daily_cap":10000`) {
		t.Fatalf("warm-up set audit = %+v", setAudit)
	}

	clear := adminRequest(t, handler.Routes(), http.MethodDelete, writePath, token,
		`{"reason":"Launch window completed"}`)
	if clear.Code != http.StatusOK || !apiClient.warmupOverrideCleared {
		t.Fatalf("super_admin warm-up clear = %d cleared=%t: %s", clear.Code, apiClient.warmupOverrideCleared, clear.Body.String())
	}
	if len(adminStore.auditRows) != 2 || adminStore.auditRows[1].Action != "admin_domain_warmup_override_cleared" ||
		adminStore.auditRows[1].Reason != "Launch window completed" ||
		string(adminStore.auditRows[1].AfterState) != `{"override":null}` {
		t.Fatalf("warm-up clear audit = %+v", adminStore.auditRows)
	}
}

func TestAdminSendingDomainDirectoryUsesAPIServiceMetadata(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "domain-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	now := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}
	apiClient := &fakeLegacyAPIAdmin{
		domains: []AdminSendingDomain{{
			ID: "domain-1", WorkspaceID: "workspace-1", WorkspaceName: "Trading Workspace",
			WorkspaceSlug: "trading", Domain: "mail.example.test", Status: "pending",
			SPFStatus: "verified", DKIMStatus: "pending", DMARCStatus: "unknown",
		}},
		total: 7,
		domain: AdminSendingDomain{
			ID: "domain-1", Domain: "mail.example.test", Status: "pending",
			DNSCheckDetails: json.RawMessage(`{"records":{"spf":{"resolvers":[{"resolver":"1.1.1.1","status":"found"}]}}}`),
		},
	}
	handler.legacyAPI = apiClient

	response := adminRequest(t, handler.Routes(), http.MethodGet,
		"/admin/domains?search=trade&status=pending&limit=2&offset=4", token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("admin domain list = %d, want 200: %s", response.Code, response.Body.String())
	}
	if apiClient.search != "trade" || apiClient.status != "pending" || apiClient.limit != 2 || apiClient.offset != 4 {
		t.Fatalf("API admin query = %+v, want forwarded filters", apiClient)
	}
	for _, expected := range []string{"mail.example.test", "Trading Workspace", `"total":7`, `"platform_role":"support_admin"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("admin domain response missing %q: %s", expected, response.Body.String())
		}
	}
	detail := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/domains/domain-1", token, "")
	if detail.Code != http.StatusBadRequest {
		t.Fatalf("invalid admin domain ID = %d, want 400: %s", detail.Code, detail.Body.String())
	}
	domainID := "9b2df23a-6c47-4e56-8648-927b1645d408"
	apiClient.domain.ID = domainID
	detail = adminRequest(t, handler.Routes(), http.MethodGet, "/admin/domains/"+domainID, token, "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"resolver":"1.1.1.1"`) {
		t.Fatalf("admin domain detail = %d, want DNS resolver details: %s", detail.Code, detail.Body.String())
	}
	apiClient.recheck = AdminSendingDomain{ID: domainID, Domain: "mail.example.test", Status: "verified"}
	recheckPath := "/admin/domains/" + domainID + "/verify"
	deniedRecheck := adminRequest(t, handler.Routes(), http.MethodPost, recheckPath, token,
		`{"reason":"Support requested DNS verification"}`)
	if deniedRecheck.Code != http.StatusForbidden {
		t.Fatalf("support_admin domain re-check = %d, want forbidden: %s", deniedRecheck.Code, deniedRecheck.Body.String())
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	recheck := adminRequest(t, handler.Routes(), http.MethodPost, recheckPath, token,
		`{"reason":"Support requested DNS verification"}`)
	if recheck.Code != http.StatusOK || apiClient.recheckedID != domainID ||
		!strings.Contains(recheck.Body.String(), `"status":"verified"`) {
		t.Fatalf("admin domain re-check = %d, want updated status: %s", recheck.Code, recheck.Body.String())
	}
	if len(adminStore.auditRows) != 1 || adminStore.auditRows[0].Action != "admin_domain_recheck_requested" ||
		adminStore.auditRows[0].Reason != "Support requested DNS verification" {
		t.Fatalf("admin domain re-check audit = %+v", adminStore.auditRows)
	}
}

func TestAdminDomainDisableIsSuperAdminOnlyReasonedAndAudited(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "domain-disable-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	freshMFA := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	const domainID = "9b2df23a-6c47-4e56-8648-927b1645d408"
	const workspaceID = "a83bf6e5-2d12-4ca7-b31f-fcf8bf1277e4"
	disabledAt := time.Now()
	apiClient := &fakeLegacyAPIAdmin{
		domain: AdminSendingDomain{ID: domainID, WorkspaceID: workspaceID, Domain: "mail.example.test"},
		disableDomain: AdminSendingDomain{
			ID: domainID, WorkspaceID: workspaceID, Domain: "mail.example.test", AdminDisabledAt: &disabledAt,
		},
	}
	handler.legacyAPI = apiClient
	path := "/admin/domains/" + domainID + "/disabled"
	response := adminRequest(t, handler.Routes(), http.MethodPatch, path, token, `{"disabled":true,"reason":"Abuse review"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("support_admin domain disable = %d, want forbidden: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 0 {
		t.Fatalf("support_admin domain disable wrote audit: %+v", adminStore.auditRows)
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	response = adminRequest(t, handler.Routes(), http.MethodPatch, path, token, `{"disabled":true,"reason":"  "}`)
	if response.Code != http.StatusBadRequest || apiClient.domainDisabled {
		t.Fatalf("domain disable without reason = %d, changed=%t: %s", response.Code, apiClient.domainDisabled, response.Body.String())
	}
	response = adminRequest(t, handler.Routes(), http.MethodPatch, path, token, `{"disabled":true,"reason":"Abuse review"}`)
	if response.Code != http.StatusOK || !apiClient.domainDisabled || apiClient.recheckedID != domainID {
		t.Fatalf("super_admin domain disable = %d, disabled=%t: %s", response.Code, apiClient.domainDisabled, response.Body.String())
	}
	if len(adminStore.auditRows) != 1 || adminStore.auditRows[0].Action != "admin_domain_disabled" ||
		adminStore.auditRows[0].TargetID != domainID || adminStore.auditRows[0].Reason != "Abuse review" ||
		adminStore.auditRows[0].WorkspaceID == nil || *adminStore.auditRows[0].WorkspaceID != workspaceID {
		t.Fatalf("domain disable audit = %+v", adminStore.auditRows)
	}
}

func TestAdminDomainRemovalIsSuperAdminOnlyReasonedAndAudited(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "domain-remove-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	freshMFA := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	const domainID = "9b2df23a-6c47-4e56-8648-927b1645d408"
	const workspaceID = "a83bf6e5-2d12-4ca7-b31f-fcf8bf1277e4"
	apiClient := &fakeLegacyAPIAdmin{
		domain: AdminSendingDomain{
			ID: domainID, WorkspaceID: workspaceID, Domain: "mail.example.test", OwnerConflict: true,
		},
	}
	handler.legacyAPI = apiClient
	path := "/admin/domains/" + domainID
	response := adminRequest(t, handler.Routes(), http.MethodDelete, path, token, `{"reason":"Confirmed duplicate ownership"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("support_admin domain removal = %d, want forbidden: %s", response.Code, response.Body.String())
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	response = adminRequest(t, handler.Routes(), http.MethodDelete, path, token, `{"reason":" "}`)
	if response.Code != http.StatusBadRequest || apiClient.deletedDomainID != "" || len(adminStore.auditRows) != 0 {
		t.Fatalf("domain removal without reason = %d, deleted=%q audits=%d: %s", response.Code, apiClient.deletedDomainID, len(adminStore.auditRows), response.Body.String())
	}
	response = adminRequest(t, handler.Routes(), http.MethodDelete, path, token, `{"reason":"Confirmed duplicate ownership"}`)
	if response.Code != http.StatusOK || apiClient.deletedDomainID != domainID {
		t.Fatalf("super_admin domain removal = %d, deleted=%q: %s", response.Code, apiClient.deletedDomainID, response.Body.String())
	}
	if len(adminStore.auditRows) != 1 || adminStore.auditRows[0].Action != "admin_domain_remove_requested" ||
		adminStore.auditRows[0].TargetID != domainID || adminStore.auditRows[0].Reason != "Confirmed duplicate ownership" ||
		adminStore.auditRows[0].WorkspaceID == nil || *adminStore.auditRows[0].WorkspaceID != workspaceID ||
		!strings.Contains(string(adminStore.auditRows[0].BeforeState), `"owner_conflict":true`) {
		t.Fatalf("domain removal audit = %+v", adminStore.auditRows)
	}
}

func TestAdminDomainConflictResolutionIsSuperAdminOnlyReasonedAndAudited(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "domain-conflict-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	freshMFA := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	const selectedID = "9b2df23a-6c47-4e56-8648-927b1645d408"
	const conflictID = "a83bf6e5-2d12-4ca7-b31f-fcf8bf1277e4"
	const firstWorkspaceID = "542df6e5-2d12-4ca7-b31f-fcf8bf1277e4"
	const secondWorkspaceID = "b83bf6e5-2d12-4ca7-b31f-fcf8bf1277e4"
	apiClient := &fakeLegacyAPIAdmin{
		domain: AdminSendingDomain{
			ID: conflictID, WorkspaceID: secondWorkspaceID, Domain: "shared.example.test", OwnerConflict: true,
		},
		domains: []AdminSendingDomain{
			{ID: selectedID, WorkspaceID: firstWorkspaceID, WorkspaceName: "Original owner", Domain: "shared.example.test"},
			{ID: conflictID, WorkspaceID: secondWorkspaceID, WorkspaceName: "Conflicted owner", Domain: "shared.example.test", OwnerConflict: true},
		},
		total: 2,
		resolvedDomain: AdminSendingDomain{
			ID: selectedID, WorkspaceID: firstWorkspaceID, Domain: "shared.example.test",
		},
	}
	handler.legacyAPI = apiClient
	path := "/admin/domains/" + conflictID + "/resolve-conflict"
	body := `{"rightful_domain_id":"` + selectedID + `","reason":"Verified domain registration evidence"}`
	response := adminRequest(t, handler.Routes(), http.MethodPost, path, token, body)
	if response.Code != http.StatusForbidden {
		t.Fatalf("support_admin conflict resolution = %d, want forbidden: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 0 {
		t.Fatalf("support_admin conflict resolution wrote audit: %+v", adminStore.auditRows)
	}
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	response = adminRequest(t, handler.Routes(), http.MethodPost, path, token,
		`{"rightful_domain_id":"`+selectedID+`","reason":" "}`)
	if response.Code != http.StatusBadRequest || apiClient.resolvedDomainID != "" || len(adminStore.auditRows) != 0 {
		t.Fatalf("conflict resolution without reason = %d, resolved=%q audits=%d: %s",
			response.Code, apiClient.resolvedDomainID, len(adminStore.auditRows), response.Body.String())
	}
	response = adminRequest(t, handler.Routes(), http.MethodPost, path, token, body)
	if response.Code != http.StatusOK || apiClient.resolvedDomainID != conflictID || apiClient.rightfulDomainID != selectedID {
		t.Fatalf("super_admin conflict resolution = %d resolved=%q rightful=%q: %s",
			response.Code, apiClient.resolvedDomainID, apiClient.rightfulDomainID, response.Body.String())
	}
	if len(adminStore.auditRows) != 1 || adminStore.auditRows[0].Action != "admin_domain_owner_conflict_resolved" ||
		adminStore.auditRows[0].Reason != "Verified domain registration evidence" ||
		adminStore.auditRows[0].WorkspaceID == nil || *adminStore.auditRows[0].WorkspaceID != firstWorkspaceID ||
		!strings.Contains(string(adminStore.auditRows[0].BeforeState), conflictID) ||
		!strings.Contains(string(adminStore.auditRows[0].AfterState), selectedID) {
		t.Fatalf("domain conflict resolution audit = %+v", adminStore.auditRows)
	}
}

func TestAdminCampaignDirectoryRequiresPlatformPermissionAndForwardsFilters(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "campaign-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	now := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}
	apiClient := &fakeLegacyAPIAdmin{
		campaigns: []AdminCampaign{{
			ID: "campaign-1", WorkspaceID: "9b2df23a-6c47-4e56-8648-927b1645d408",
			WorkspaceName: "Trading Workspace", Name: "Weekly update", Status: "completed",
			RecipientCount: 12, SubmittedCount: 10, BouncedCount: 1, ComplaintCount: 1,
		}},
		total: 7,
	}
	handler.legacyAPI = apiClient
	workspaceID := "9b2df23a-6c47-4e56-8648-927b1645d408"
	response := adminRequest(t, handler.Routes(), http.MethodGet,
		"/admin/campaigns?search=weekly&status=completed&workspace_id="+workspaceID+"&limit=2&offset=4", token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("admin campaign list = %d, want 200: %s", response.Code, response.Body.String())
	}
	if apiClient.campaignSearch != "weekly" || apiClient.campaignStatus != "completed" ||
		apiClient.campaignWorkspaceID != workspaceID || apiClient.limit != 2 || apiClient.offset != 4 {
		t.Fatalf("API admin campaign query = %+v, want forwarded filters", apiClient)
	}
	for _, expected := range []string{"Weekly update", "Trading Workspace", `"submitted_count":10`, `"total":7`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("admin campaign response missing %q: %s", expected, response.Body.String())
		}
	}
	invalid := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/campaigns?workspace_id=not-a-uuid", token, "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid admin campaign workspace ID = %d, want 400: %s", invalid.Code, invalid.Body.String())
	}
}

func TestAdminCampaignStopIsSuperAdminOnlyReasonedAndAudited(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "campaign-stop-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	verifiedAt := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &verifiedAt
		}
	}
	const campaignID = "9b2df23a-6c47-4e56-8648-927b1645d408"
	apiClient := &fakeLegacyAPIAdmin{}
	handler.legacyAPI = apiClient
	path := "/admin/campaigns/" + campaignID + "/stop"
	response := adminRequest(t, handler.Routes(), http.MethodPost, path, token, `{"reason":"Confirmed abuse report"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("support_admin campaign stop = %d, want forbidden: %s", response.Code, response.Body.String())
	}

	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	staleMFA := time.Now().Add(-10 * time.Minute)
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &staleMFA
		}
	}
	response = adminRequest(t, handler.Routes(), http.MethodPost, path, token, `{"reason":"Confirmed abuse report"}`)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "admin_reauth_required") {
		t.Fatalf("campaign stop with stale MFA = %d, want re-authentication: %s", response.Code, response.Body.String())
	}
	freshMFA := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	response = adminRequest(t, handler.Routes(), http.MethodPost, path, token, `{"reason":"  "}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("campaign stop without reason = %d, want 400: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 0 || apiClient.stoppedCampaignID != "" {
		t.Fatalf("invalid stop request wrote audit or called API: audit=%+v stopped=%q", adminStore.auditRows, apiClient.stoppedCampaignID)
	}

	response = adminRequest(t, handler.Routes(), http.MethodPost, path, token, `{"reason":"Confirmed abuse report"}`)
	if response.Code != http.StatusOK || apiClient.stoppedCampaignID != campaignID {
		t.Fatalf("super_admin campaign stop = %d, want successful stop: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 1 || adminStore.auditRows[0].Action != "admin_campaign_stop_requested" ||
		adminStore.auditRows[0].TargetID != campaignID || adminStore.auditRows[0].Reason != "Confirmed abuse report" {
		t.Fatalf("campaign stop audit = %+v", adminStore.auditRows)
	}
}

func TestAdminSuppressionsAreSuperAdminOnlyReasonedAndAudited(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "suppression-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	freshMFA := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	const workspaceID = "9b2df23a-6c47-4e56-8648-927b1645d408"
	const suppressionID = "a83bf6e5-2d12-4ca7-b31f-fcf8bf1277e4"
	apiClient := &fakeLegacyAPIAdmin{
		suppressions: []AdminSuppression{{
			ID: suppressionID, WorkspaceID: workspaceID,
			WorkspaceName: "Trading Workspace", Email: "blocked@example.test", Reason: "manual",
		}},
		total: 1,
		createdSuppression: AdminSuppression{
			ID: suppressionID, WorkspaceName: "Trading Workspace", Reason: "manual", Source: "admin",
		},
	}
	handler.legacyAPI = apiClient
	listPath := "/admin/suppressions?search=blocked&workspace_id=" + workspaceID + "&limit=2&offset=4"
	response := adminRequest(t, handler.Routes(), http.MethodGet, listPath, token, "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("support_admin suppression list = %d, want forbidden: %s", response.Code, response.Body.String())
	}
	createPath := "/admin/suppressions"
	response = adminRequest(t, handler.Routes(), http.MethodPost, createPath, token,
		`{"workspace_id":"`+workspaceID+`","email":"blocked@example.test","reason":"Confirmed abuse report"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("support_admin suppression add = %d, want forbidden: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 0 {
		t.Fatalf("support_admin suppression request wrote audit: %+v", adminStore.auditRows)
	}

	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	response = adminRequest(t, handler.Routes(), http.MethodGet, listPath, token, "")
	if response.Code != http.StatusOK || apiClient.suppressionSearch != "blocked" ||
		apiClient.suppressionWorkspace != workspaceID || apiClient.limit != 2 || apiClient.offset != 4 {
		t.Fatalf("super_admin suppression list = %d, API client=%+v: %s", response.Code, apiClient, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "blocked@example.test") {
		t.Fatalf("super_admin suppression response missing address: %s", response.Body.String())
	}

	staleMFA := time.Now().Add(-10 * time.Minute)
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &staleMFA
		}
	}
	response = adminRequest(t, handler.Routes(), http.MethodPost, createPath, token,
		`{"workspace_id":"`+workspaceID+`","email":"blocked@example.test","reason":"Confirmed abuse report"}`)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "admin_reauth_required") {
		t.Fatalf("suppression add with stale MFA = %d: %s", response.Code, response.Body.String())
	}
	freshMFA = time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &freshMFA
		}
	}
	response = adminRequest(t, handler.Routes(), http.MethodPost, createPath, token,
		`{"workspace_id":"`+workspaceID+`","email":"blocked@example.test","reason":"  "}`)
	if response.Code != http.StatusBadRequest || len(adminStore.auditRows) != 0 {
		t.Fatalf("suppression add without reason = %d, audits=%d: %s", response.Code, len(adminStore.auditRows), response.Body.String())
	}
	response = adminRequest(t, handler.Routes(), http.MethodPost, createPath, token,
		`{"workspace_id":"`+workspaceID+`","email":"blocked@example.test","reason":"Confirmed abuse report"}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("super_admin suppression add = %d: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 1 || adminStore.auditRows[0].Action != "admin_suppression_add_requested" ||
		adminStore.auditRows[0].Reason != "Confirmed abuse report" ||
		adminStore.auditRows[0].WorkspaceID == nil || *adminStore.auditRows[0].WorkspaceID != workspaceID ||
		strings.Contains(string(adminStore.auditRows[0].AfterState), "blocked@example.test") {
		t.Fatalf("suppression add audit is missing reason or exposes raw email: %+v", adminStore.auditRows)
	}

	deletePath := "/admin/suppressions/" + suppressionID
	response = adminRequest(t, handler.Routes(), http.MethodDelete, deletePath, token, `{"reason":"False positive confirmed"}`)
	if response.Code != http.StatusOK || apiClient.deletedSuppressionID != suppressionID {
		t.Fatalf("super_admin suppression remove = %d, deleted=%q: %s", response.Code, apiClient.deletedSuppressionID, response.Body.String())
	}
	if len(adminStore.auditRows) != 2 || adminStore.auditRows[1].Action != "admin_suppression_remove_requested" ||
		adminStore.auditRows[1].Reason != "False positive confirmed" || adminStore.auditRows[1].WorkspaceID == nil ||
		*adminStore.auditRows[1].WorkspaceID != workspaceID {
		t.Fatalf("suppression remove audit = %+v", adminStore.auditRows)
	}
}

func TestAdminTransactionalMessageSearchUsesDeliveryPermissionAndMetadata(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "message-search-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	verifiedAt := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &verifiedAt
		}
	}
	apiClient := &fakeLegacyAPIAdmin{
		transactionalMessages: []AdminTransactionalMessage{{
			ID: "message-id", MessageID: "mail-id@example.test",
			WorkspaceID: "workspace-id", Workspace: "Trading Workspace",
			Status: "delivered",
		}},
		total: 1,
	}
	handler.legacyAPI = apiClient
	response := adminRequest(t, handler.Routes(), http.MethodGet,
		"/admin/transactional-messages?search=mail-id%40example.test&limit=1&offset=0", token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("admin transactional message search = %d, want 200: %s", response.Code, response.Body.String())
	}
	if apiClient.transactionalSearch != "mail-id@example.test" || apiClient.limit != 1 || apiClient.offset != 0 {
		t.Fatalf("transactional search query = %+v, want forwarded search and paging", apiClient)
	}
	for _, expected := range []string{"mail-id@example.test", "Trading Workspace", `"status":"delivered"`, `"total":1`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("transactional search response missing %q: %s", expected, response.Body.String())
		}
	}
	invalid := adminRequest(t, handler.Routes(), http.MethodGet, "/admin/transactional-messages?search=x", token, "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("short transactional search = %d, want 400: %s", invalid.Code, invalid.Body.String())
	}
}

func TestAdminUserSuspensionRequiresRecentMFAReasonAndRevokesSessions(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "user-action-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	adminToken := createAdminTestSession(t, handler, baseStore, operator.ID)
	staleForAction := time.Now().Add(-10 * time.Minute)
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &staleForAction
		}
	}

	target := seedVerifiedUser(t, handler, baseStore, workspace, "suspend-target@example.test")
	createAdminTestSession(t, handler, baseStore, target.ID)
	path := "/admin/users/" + target.ID + "/status"
	body := `{"status":"suspended","reason":"Confirmed abuse report"}`

	response := adminRequest(t, handler.Routes(), http.MethodPatch, path, adminToken, body)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "admin_reauth_required") {
		t.Fatalf("status change without recent MFA = %d, want 403 reauth: %s", response.Code, response.Body.String())
	}
	now := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}
	response = adminRequest(t, handler.Routes(), http.MethodPatch, path, adminToken,
		`{"status":"suspended"}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "admin_reason_required") {
		t.Fatalf("status change without reason = %d, want 400: %s", response.Code, response.Body.String())
	}

	response = adminRequest(t, handler.Routes(), http.MethodPatch, path, adminToken, body)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sessions_revoked":1`) {
		t.Fatalf("status change = %d, want suspended and one revoked session: %s", response.Code, response.Body.String())
	}
	if baseStore.usersByID[target.ID].Status != "suspended" || len(adminStore.auditRows) != 1 {
		t.Fatalf("target status or audit rows incorrect: status=%q audit=%+v", baseStore.usersByID[target.ID].Status, adminStore.auditRows)
	}
	audit := adminStore.auditRows[0]
	if audit.Action != "admin_user_status_changed" || audit.Reason != "Confirmed abuse report" ||
		string(audit.BeforeState) != `{"status":"active"}` ||
		string(audit.AfterState) != `{"status":"suspended","sessions_revoked":1}` {
		t.Fatalf("unexpected status change audit record: %+v", audit)
	}

	sessionTarget := seedVerifiedUser(t, handler, baseStore, workspace, "session-target@example.test")
	createAdminTestSession(t, handler, baseStore, sessionTarget.ID)
	response = adminRequest(t, handler.Routes(), http.MethodPost,
		"/admin/users/"+sessionTarget.ID+"/revoke-sessions", adminToken,
		`{"reason":"Account recovery request"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"sessions_revoked":1`) {
		t.Fatalf("session revocation = %d, want one revoked session: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 2 || adminStore.auditRows[1].Action != "admin_user_sessions_revoked" ||
		adminStore.auditRows[1].Reason != "Account recovery request" {
		t.Fatalf("session revocation was not audited: %+v", adminStore.auditRows)
	}
}

func TestSupportAdminCanReasonablyResendPendingUserVerification(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "support-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSupportAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	adminToken := createAdminTestSession(t, handler, baseStore, operator.ID)
	now := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}
	target, err := baseStore.CreateUser(context.Background(), "pending-user@example.test", "hash", "Pending User")
	if err != nil {
		t.Fatalf("create pending user: %v", err)
	}

	path := "/admin/users/" + target.ID + "/resend-verification"
	response := adminRequest(t, handler.Routes(), http.MethodPost, path, adminToken, `{}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "admin_reason_required") {
		t.Fatalf("verification resend without reason = %d, want 400: %s", response.Code, response.Body.String())
	}
	response = adminRequest(t, handler.Routes(), http.MethodPost, path, adminToken,
		`{"reason":"User requested a fresh link"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"delivery_status":"queued"`) {
		t.Fatalf("verification resend = %d, want queued: %s", response.Code, response.Body.String())
	}
	if len(handler.mailRetries) != 1 {
		t.Fatalf("queued verification emails = %d, want one", len(handler.mailRetries))
	}
	if len(adminStore.auditRows) != 1 ||
		adminStore.auditRows[0].ActorRole != PlatformRoleSupportAdmin ||
		adminStore.auditRows[0].Action != "admin_user_verification_requested" ||
		adminStore.auditRows[0].Reason != "User requested a fresh link" {
		t.Fatalf("verification resend not audited: %+v", adminStore.auditRows)
	}
}

func TestSuperAdminCanManuallyVerifyPendingUserEmailWithReason(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "verify-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	adminToken := createAdminTestSession(t, handler, baseStore, operator.ID)
	staleForAction := time.Now().Add(-10 * time.Minute)
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &staleForAction
		}
	}
	target, err := baseStore.CreateUser(context.Background(), "verify-target@example.test", "hash", "Verify Target")
	if err != nil {
		t.Fatalf("create pending user: %v", err)
	}
	path := "/admin/users/" + target.ID + "/verify-email"
	body := `{"reason":"Verified through support case"}`
	response := adminRequest(t, handler.Routes(), http.MethodPost, path, adminToken, body)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "admin_reauth_required") {
		t.Fatalf("manual verification without recent MFA = %d, want 403: %s", response.Code, response.Body.String())
	}
	now := time.Now()
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}
	response = adminRequest(t, handler.Routes(), http.MethodPost, path, adminToken, `{}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "admin_reason_required") {
		t.Fatalf("manual verification without reason = %d, want 400: %s", response.Code, response.Body.String())
	}
	response = adminRequest(t, handler.Routes(), http.MethodPost, path, adminToken, body)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"active"`) {
		t.Fatalf("manual verification = %d, want active user: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 1 || adminStore.auditRows[0].Action != "admin_user_email_verified" ||
		adminStore.auditRows[0].Reason != "Verified through support case" {
		t.Fatalf("manual verification was not audited: %+v", adminStore.auditRows)
	}
}

func TestAdminWorkspaceListingRequiresMFAAndUsesWorkspaceService(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "workspace-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	now := time.Now()
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}
	workspace.adminOrgs = []WorkspaceAdminOrganization{{
		ID: fakeUUID(940), Name: "Acme Partners", Slug: "acme-partners", MemberCount: 1, CreatedAt: now,
		Members: []WorkspaceAdminMember{{
			OrganizationID: fakeUUID(940), UserID: fakeUUID(941), Role: "owner", CreatedAt: now,
		}},
	}}

	response := adminRequest(t, handler.Routes(), http.MethodGet,
		"/admin/workspaces?search=acme&limit=1", token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("admin workspace listing status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data       []WorkspaceAdminOrganization `json:"data"`
		Pagination struct {
			Total int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode admin workspace response: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].Name != "Acme Partners" ||
		body.Data[0].MemberCount != 1 || body.Pagination.Total != 1 {
		t.Fatalf("unexpected admin workspace response: %+v", body)
	}
	details := adminRequest(t, handler.Routes(), http.MethodGet,
		"/admin/workspaces/"+fakeUUID(940), token, "")
	if details.Code != http.StatusOK {
		t.Fatalf("admin workspace detail status = %d, want 200: %s", details.Code, details.Body.String())
	}
	var detailBody struct {
		Data WorkspaceAdminOrganization `json:"data"`
	}
	if err := json.Unmarshal(details.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("decode admin workspace detail: %v", err)
	}
	if len(detailBody.Data.Members) != 1 || detailBody.Data.Members[0].Role != "owner" {
		t.Fatalf("unexpected admin workspace detail: %+v", detailBody)
	}
}

func TestAdminWorkspaceSuspensionRequiresFreshMFAAndAuditsChange(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	operator := seedVerifiedUser(t, handler, baseStore, workspace, "workspace-suspend-admin@example.test")
	baseStore.usersByID[operator.ID].PlatformRole = PlatformRoleSuperAdmin
	now := time.Now()
	adminStore.mfa[operator.ID] = adminMFAState{Secret: "configured", Enabled: true}
	token := createAdminTestSession(t, handler, baseStore, operator.ID)
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = &now
		}
	}
	workspaceID := fakeUUID(942)
	workspace.adminOrgs = []WorkspaceAdminOrganization{{
		ID: workspaceID, Name: "Suspendable Workspace", Slug: "suspendable", CreatedAt: now,
	}}
	handler.legacyAPI = &fakeLegacyAPIAdmin{workspaceStatus: "active"}

	response := adminRequest(t, handler.Routes(), http.MethodPatch,
		"/admin/workspaces/"+workspaceID+"/status", token,
		`{"status":"suspended","reason":"Confirmed abuse report"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"suspended"`) {
		t.Fatalf("suspend workspace status = %d: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 1 {
		t.Fatalf("workspace status audit count = %d, want 1", len(adminStore.auditRows))
	}
	audit := adminStore.auditRows[0]
	if audit.Action != "admin_workspace_status_changed" || audit.TargetType != "workspace" ||
		audit.TargetID != workspaceID || audit.WorkspaceID == nil || *audit.WorkspaceID != workspaceID ||
		audit.Reason != "Confirmed abuse report" ||
		string(audit.BeforeState) != `{"status":"active"}` ||
		string(audit.AfterState) != `{"status":"suspended"}` {
		t.Fatalf("unexpected workspace status audit: %+v", audit)
	}

	handler.legacyAPI = &fakeLegacyAPIAdmin{workspaceStatus: "active"}
	for _, session := range baseStore.sessions {
		if session.UserID == operator.ID {
			session.AdminMFAVerifiedAt = nil
		}
	}
	response = adminRequest(t, handler.Routes(), http.MethodPatch,
		"/admin/workspaces/"+workspaceID+"/status", token,
		`{"status":"suspended","reason":"Confirmed abuse report"}`)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status change without fresh MFA = %d, want 403: %s", response.Code, response.Body.String())
	}
	if len(adminStore.auditRows) != 1 {
		t.Fatalf("unauthorized status change wrote audit row: %+v", adminStore.auditRows)
	}
}

func TestTOTPUsesStandardSixDigitTimeSteps(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if got, ok := totpCode(secret, time.Unix(59, 0)); !ok || got != "287082" {
		t.Fatalf("TOTP at Unix 59 = %q, %v; want RFC 6238 six-digit value 287082", got, ok)
	}
	if !verifyTOTP(secret, "287082", time.Unix(59, 0)) {
		t.Fatal("verifyTOTP() rejected a valid TOTP")
	}
	if verifyTOTP(secret, "287083", time.Unix(59, 0)) {
		t.Fatal("verifyTOTP() accepted an invalid TOTP")
	}
}

func TestValidateAdminIPAllowlist(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{value: "", valid: true},
		{value: "192.0.2.10,2001:db8::/32", valid: true},
		{value: "192.0.2.10,", valid: false},
		{value: "not-an-address", valid: false},
	} {
		err := validateAdminIPAllowlist(test.value)
		if (err == nil) != test.valid {
			t.Errorf("validateAdminIPAllowlist(%q) error = %v, valid %v", test.value, err, test.valid)
		}
	}
}

func TestPlatformAdminRefreshSessionsAreShorterThanWorkspaceSessions(t *testing.T) {
	handler, store, _, _ := newTestHandler(t)
	membership := &WorkspaceMembership{
		OrganizationID: fakeUUID(920),
		Name:           "Test Org",
		Slug:           "test-org",
		Role:           "owner",
	}
	for _, test := range []struct {
		name         string
		platformRole string
		wantTTL      time.Duration
	}{
		{name: "platform admin", platformRole: PlatformRoleSuperAdmin, wantTTL: adminRefreshSessionTTL},
		{name: "workspace owner", platformRole: PlatformRoleNone, wantTTL: handler.cfg.RefreshTokenTTL},
	} {
		t.Run(test.name, func(t *testing.T) {
			user := &User{
				ID:           fakeUUID(store.nextID + 10),
				Email:        "operator@example.test",
				Status:       "active",
				PlatformRole: test.platformRole,
			}
			before := time.Now()
			session, _, _, err := handler.issueSession(httptest.NewRequest(http.MethodPost, "/auth/login", nil), user, membership)
			if err != nil {
				t.Fatalf("issueSession() error = %v", err)
			}
			if got := session.ExpiresAt.Sub(before); got < test.wantTTL-time.Second || got > test.wantTTL+time.Second {
				t.Fatalf("session TTL = %s, want approximately %s", got, test.wantTTL)
			}
		})
	}
}

func TestAdminMFARepeatedFailuresLockVerification(t *testing.T) {
	handler, baseStore, workspace, _ := newTestHandler(t)
	adminStore := newAdminTestStore(baseStore)
	handler.store = adminStore
	user := seedVerifiedUser(t, handler, baseStore, workspace, "locked-operator@example.test")
	baseStore.usersByID[user.ID].PlatformRole = PlatformRoleSuperAdmin
	token := createAdminTestSession(t, handler, baseStore, user.ID)
	secret, err := newTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := delivery.EncryptSecret(handler.cfg.JWTSecret+":admin-mfa:v1", secret)
	if err != nil {
		t.Fatal(err)
	}
	adminStore.mfa[user.ID] = adminMFAState{
		Secret:  base64.RawStdEncoding.EncodeToString(encrypted),
		Enabled: true,
	}
	routes := handler.Routes()
	for attempt := 1; attempt <= 5; attempt++ {
		response := adminRequest(t, routes, http.MethodPost, "/admin/mfa/verify", token, `{"code":"invalid-code"}`)
		wantStatus := http.StatusUnauthorized
		if attempt == 5 {
			wantStatus = http.StatusTooManyRequests
		}
		if response.Code != wantStatus {
			t.Fatalf("invalid code attempt %d status = %d, want %d: %s", attempt, response.Code, wantStatus, response.Body.String())
		}
	}
	locked := adminRequest(t, routes, http.MethodPost, "/admin/mfa/verify", token, `{"code":"invalid-code"}`)
	if locked.Code != http.StatusTooManyRequests {
		t.Fatalf("MFA status while locked = %d, want 429", locked.Code)
	}
}

func createAdminTestSession(t *testing.T, handler *Handler, store *fakeStore, userID string) string {
	t.Helper()
	session, err := store.CreateSession(context.Background(), SessionCreate{
		UserID:           userID,
		OrganizationID:   fakeUUID(900),
		OrganizationName: "Test Org",
		OrganizationSlug: "test-org",
		Role:             "owner",
		RefreshTokenHash: "admin-test-refresh-" + userID,
		ExpiresAt:        time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create admin test session: %v", err)
	}
	token, err := handler.tokens.IssueAccess(userID, "operator@example.test", session.OrganizationID, session.Role, session.ID)
	if err != nil {
		t.Fatalf("issue admin test access token: %v", err)
	}
	return token
}

func adminRequest(t *testing.T, handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
