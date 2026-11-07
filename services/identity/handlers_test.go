package identity

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khayode/email-platform/internal/kit"
)

// --- fakes -----------------------------------------------------------------

type fakeTokenRow struct {
	userID    string
	email     string
	purpose   string
	expiresAt time.Time
	used      bool
}

type fakeAuditRow struct {
	userID *string
	action string
}

type failingMailer struct{ err error }

func (m failingMailer) Provider() string { return "test" }
func (m failingMailer) Send(context.Context, MailMessage) error {
	return m.err
}

type retryMailer struct {
	mu        sync.Mutex
	failures  int
	attempts  int
	delivered chan struct{}
}

func (m *retryMailer) Provider() string { return "test" }
func (m *retryMailer) Send(context.Context, MailMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts++
	if m.attempts <= m.failures {
		return errors.New("relay unavailable")
	}
	select {
	case m.delivered <- struct{}{}:
	default:
	}
	return nil
}

type registrationCall struct {
	email            string
	organizationName string
	correlationID    string
}

type fakeStore struct {
	mu               sync.Mutex
	usersByID        map[string]*User
	userByEmail      map[string]string
	sessions         map[string]*Session
	sessionByRefresh map[string]string
	tokens           map[string]*fakeTokenRow
	audit            []fakeAuditRow
	registrations    []registrationCall
	revokedUsers     []string
	revokeSessionErr error
	revokeTokenErr   error
	nextID           int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		usersByID:        map[string]*User{},
		userByEmail:      map[string]string{},
		sessions:         map[string]*Session{},
		sessionByRefresh: map[string]string{},
		tokens:           map[string]*fakeTokenRow{},
	}
}

func (f *fakeStore) Ping(context.Context) error { return nil }
func (f *fakeStore) Close()                     {}

func (f *fakeStore) CreateUserWithRegistration(_ context.Context, email, passwordHash, name, organizationName, correlationID string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.userByEmail[email]; ok {
		return nil, ErrEmailInUse
	}
	f.nextID++
	user := &User{
		ID:           fakeUUID(f.nextID),
		Email:        email,
		Name:         name,
		Status:       "pending",
		PasswordHash: passwordHash,
		CreatedAt:    time.Now().UTC(),
	}
	f.usersByID[user.ID] = user
	f.userByEmail[email] = user.ID
	f.registrations = append(f.registrations, registrationCall{email, organizationName, correlationID})
	return cloneUser(user), nil
}

func (f *fakeStore) CreateUser(_ context.Context, email, passwordHash, name string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.userByEmail[email]; ok {
		return nil, ErrEmailInUse
	}
	f.nextID++
	user := &User{ID: fakeUUID(f.nextID), Email: email, Name: name, Status: "pending", PasswordHash: passwordHash, CreatedAt: time.Now().UTC()}
	f.usersByID[user.ID] = user
	f.userByEmail[email] = user.ID
	return cloneUser(user), nil
}

func (f *fakeStore) GetUserByEmail(_ context.Context, email string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.userByEmail[strings.ToLower(email)]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneUser(f.usersByID[id]), nil
}

func (f *fakeStore) GetUserByID(_ context.Context, id string) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.usersByID[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneUser(user), nil
}

func (f *fakeStore) SetPassword(_ context.Context, userID, passwordHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.usersByID[userID]
	if !ok {
		return ErrNotFound
	}
	user.PasswordHash = passwordHash
	return nil
}

func (f *fakeStore) RecordLogin(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.usersByID[userID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	user.LastLoginAt = &now
	return nil
}

func (f *fakeStore) CreateVerificationToken(_ context.Context, userID, email, tokenHash, purpose string, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[tokenHash] = &fakeTokenRow{userID: userID, email: email, purpose: purpose, expiresAt: expiresAt}
	return nil
}

func (f *fakeStore) ConsumeVerificationToken(_ context.Context, tokenHash, purpose string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.tokens[tokenHash]
	if !ok || row.used || row.purpose != purpose || time.Now().After(row.expiresAt) {
		return "", "", ErrInvalidToken
	}
	row.used = true
	return row.userID, row.email, nil
}

func (f *fakeStore) MarkEmailVerified(_ context.Context, userID, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.usersByID[userID]
	if !ok {
		return ErrNotFound
	}
	now := time.Now().UTC()
	user.EmailVerifiedAt = &now
	user.Status = "active"
	return nil
}

func (f *fakeStore) CreateSession(_ context.Context, params SessionCreate) (*Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	session := &Session{
		ID:               fakeUUID(f.nextID),
		UserID:           params.UserID,
		OrganizationID:   params.OrganizationID,
		OrganizationName: params.OrganizationName,
		OrganizationSlug: params.OrganizationSlug,
		Role:             params.Role,
		RefreshTokenHash: params.RefreshTokenHash,
		ExpiresAt:        params.ExpiresAt,
	}
	f.sessions[session.ID] = session
	f.sessionByRefresh[session.RefreshTokenHash] = session.ID
	return cloneSession(session), nil
}

func (f *fakeStore) GetSession(_ context.Context, sessionID string) (*Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneSession(session), nil
}

func (f *fakeStore) LookupRefreshToken(_ context.Context, tokenHash string) (*Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.sessionByRefresh[tokenHash]; ok {
		session := f.sessions[id]
		if session.RevokedAt != nil || time.Now().After(session.ExpiresAt) {
			return nil, false, ErrInvalidToken
		}
		return cloneSession(session), false, nil
	}
	for _, session := range f.sessions {
		if session.PreviousRefreshTokenHash == tokenHash && session.RevokedAt == nil && !time.Now().After(session.ExpiresAt) {
			return cloneSession(session), true, nil
		}
	}
	return nil, false, ErrInvalidToken
}

func (f *fakeStore) RotateRefreshToken(_ context.Context, sessionID, presentedHash, newHash string, expiresAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[sessionID]
	if !ok {
		return ErrInvalidToken
	}
	if session.RefreshTokenHash != presentedHash || session.RevokedAt != nil || time.Now().After(session.ExpiresAt) {
		now := time.Now().UTC()
		for _, other := range f.sessions {
			if other.UserID == session.UserID && other.RevokedAt == nil {
				other.RevokedAt = &now
			}
		}
		return ErrRefreshReuse
	}
	delete(f.sessionByRefresh, presentedHash)
	session.PreviousRefreshTokenHash = presentedHash
	session.RefreshTokenHash = newHash
	session.ExpiresAt = expiresAt
	f.sessionByRefresh[newHash] = sessionID
	return nil
}

func (f *fakeStore) RevokeSession(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revokeSessionErr != nil {
		return f.revokeSessionErr
	}
	session, ok := f.sessions[sessionID]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	session.RevokedAt = &now
	return nil
}

func (f *fakeStore) RevokeSessionByToken(_ context.Context, tokenHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.revokeTokenErr != nil {
		return f.revokeTokenErr
	}
	id, ok := f.sessionByRefresh[tokenHash]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	f.sessions[id].RevokedAt = &now
	return nil
}

func (f *fakeStore) RevokeAllUserSessions(_ context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	f.revokedUsers = append(f.revokedUsers, userID)
	for _, session := range f.sessions {
		if session.UserID == userID && session.RevokedAt == nil {
			session.RevokedAt = &now
		}
	}
	return nil
}

func (f *fakeStore) WriteAuthAudit(_ context.Context, userID *string, action, _, _ string, _ map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audit = append(f.audit, fakeAuditRow{userID: userID, action: action})
	return nil
}

func cloneUser(u *User) *User {
	c := *u
	return &c
}

func cloneSession(s *Session) *Session {
	c := *s
	return &c
}

func fakeUUID(n int) string {
	return strings.Repeat("0", 32-len(itoa(n))) + itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

type fakeWorkspace struct {
	mu           sync.Mutex
	memberships  map[string]WorkspaceMembership
	orgs         map[string]WorkspaceOrganization
	defaultErr   error
	orgErr       error
	defaultCalls int
}

func newFakeWorkspace() *fakeWorkspace {
	return &fakeWorkspace{
		memberships: map[string]WorkspaceMembership{},
		orgs:        map[string]WorkspaceOrganization{},
	}
}

func (f *fakeWorkspace) DefaultMembership(_ context.Context, userID string) (*WorkspaceMembership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaultCalls++
	if f.defaultErr != nil {
		return nil, f.defaultErr
	}
	m, ok := f.memberships[userID]
	if !ok {
		return nil, ErrNoOrganization
	}
	return &m, nil
}

func (f *fakeWorkspace) Organization(_ context.Context, orgID string) (*WorkspaceOrganization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.orgErr != nil {
		return nil, f.orgErr
	}
	org, ok := f.orgs[orgID]
	if !ok {
		return nil, ErrNotFound
	}
	return &org, nil
}

// --- helpers ---------------------------------------------------------------

func newTestHandler(t *testing.T) (*Handler, *fakeStore, *fakeWorkspace, *LogMailer) {
	t.Helper()
	store := newFakeStore()
	workspace := newFakeWorkspace()
	mailer := NewLogMailer(kit.NewTestLogger())
	cfg := Config{
		BaseConfig:      kit.BaseConfig{JWTSecret: "test-secret", JWTIssuer: "email-platform"},
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 24 * time.Hour,
		AppBaseURL:      "http://localhost:3000",
	}
	tokens := kit.NewManager(cfg.JWTSecret, cfg.JWTIssuer, cfg.AccessTokenTTL)
	handler := NewHandler(store, tokens, workspace, mailer, cfg, kit.NewTestLogger())
	handler.retryDelay = time.Millisecond
	handler.mailRetryDelay = time.Millisecond
	return handler, store, workspace, mailer
}

func seedVerifiedUser(t *testing.T, handler *Handler, store *fakeStore, workspace *fakeWorkspace, email string) *User {
	t.Helper()
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := store.CreateUserWithRegistration(context.Background(), email, hash, "Test User", "Test Org", "")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := store.MarkEmailVerified(context.Background(), user.ID, user.Email, ""); err != nil {
		t.Fatalf("verify user: %v", err)
	}
	workspace.memberships[user.ID] = WorkspaceMembership{
		OrganizationID: fakeUUID(900),
		Name:           "Test Org",
		Slug:           "test-org",
		Role:           "owner",
	}
	workspace.orgs[fakeUUID(900)] = WorkspaceOrganization{ID: fakeUUID(900), Name: "Test Org", Slug: "test-org"}
	return user
}

func doJSON(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeBody(t, rec, &envelope)
	return envelope.Error.Code, envelope.Error.Message
}

// --- tests -----------------------------------------------------------------

func TestRegisterCreatesAccountAndSendsVerification(t *testing.T) {
	handler, store, _, mailer := newTestHandler(t)

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/register", `{
		"email": "Ada@Example.com",
		"password": "correct horse battery staple",
		"name": "Ada Lovelace",
		"organization_name": "Analytical Engines"
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Message string `json:"message"`
		Email   string `json:"email"`
	}
	decodeBody(t, rec, &resp)
	if resp.Email != "ada@example.com" {
		t.Fatalf("email = %q, want normalized address", resp.Email)
	}
	if resp.Message == "" {
		t.Fatal("expected a message")
	}

	if len(store.registrations) != 1 {
		t.Fatalf("registrations = %d, want 1", len(store.registrations))
	}
	if store.registrations[0].organizationName != "Analytical Engines" {
		t.Fatalf("organization_name = %q", store.registrations[0].organizationName)
	}

	sent := mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("sent mails = %d, want 1", len(sent))
	}
	if !strings.Contains(sent[0].Text, "/auth/verify?token=") {
		t.Fatalf("verification mail missing link: %q", sent[0].Text)
	}
}

func TestRegisterQueuesVerificationRetryWhenDeliveryFails(t *testing.T) {
	handler, store, _, _ := newTestHandler(t)
	handler.mailer = failingMailer{err: errors.New("mail transport unavailable")}

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/register", `{
		"email": "delivery@example.com",
		"password": "correct horse battery staple",
		"name": "Delivery Failure",
		"organization_name": "Test Org"
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"delivery_status":"retry_queued"`) {
		t.Fatalf("response does not report queued retry: %s", rec.Body.String())
	}
	if _, err := store.GetUserByEmail(context.Background(), "delivery@example.com"); err != nil {
		t.Fatalf("account should remain available for resend: %v", err)
	}
}

func TestRegisterRetriesVerificationDelivery(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	mailer := &retryMailer{failures: 2, delivered: make(chan struct{}, 1)}
	handler.mailer = mailer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		handler.RunMailRetries(ctx)
		close(done)
	}()

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/register", `{
		"email": "retry@example.com",
		"password": "correct horse battery staple",
		"name": "Retry User",
		"organization_name": "Test Org"
	}`)
	if rec.Code != http.StatusCreated {
		cancel()
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	select {
	case <-mailer.delivered:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("verification delivery did not succeed after retries")
	}
	cancel()
	<-done
	mailer.mu.Lock()
	defer mailer.mu.Unlock()
	if mailer.attempts != 3 {
		t.Fatalf("mailer attempts = %d, want 3 (initial plus two retries)", mailer.attempts)
	}
}

func TestMailRetriesStopAfterFiveAttempts(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	mailer := &retryMailer{failures: 10, delivered: make(chan struct{}, 1)}
	handler.mailer = mailer
	err := handler.deliverMail(context.Background(), "verification", MailMessage{
		To: "retry-limit@example.com", Subject: "Verify", Text: "link",
	})
	if !errors.Is(err, ErrMailRetryQueued) {
		t.Fatalf("deliverMail() error = %v, want queued retry", err)
	}
	handler.retryMail(context.Background(), <-handler.mailRetries)

	mailer.mu.Lock()
	defer mailer.mu.Unlock()
	if mailer.attempts != 5 {
		t.Fatalf("mailer attempts = %d, want five total attempts", mailer.attempts)
	}
}

func TestRegisterRejectsDuplicateEmail(t *testing.T) {
	handler, store, _, _ := newTestHandler(t)
	hash, _ := HashPassword("correct horse battery staple")
	if _, err := store.CreateUserWithRegistration(context.Background(), "ada@example.com", hash, "Ada", "Org", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/register", `{
		"email": "ada@example.com",
		"password": "correct horse battery staple",
		"name": "Ada",
		"organization_name": "Org"
	}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if code, _ := errorOf(t, rec); code != "email_in_use" {
		t.Fatalf("code = %q", code)
	}
}

func TestRegisterValidation(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	routes := handler.Routes()

	cases := []struct {
		name string
		body string
	}{
		{"invalid email", `{"email":"nope","password":"correct horse battery staple","name":"A","organization_name":"O"}`},
		{"short password", `{"email":"a@example.com","password":"short","name":"A","organization_name":"O"}`},
		{"missing name", `{"email":"a@example.com","password":"correct horse battery staple","name":"","organization_name":"O"}`},
		{"missing organization", `{"email":"a@example.com","password":"correct horse battery staple","name":"A","organization_name":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, routes, http.MethodPost, "/auth/register", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestLoginReturnsSession(t *testing.T) {
	handler, store, workspace, _ := newTestHandler(t)
	user := seedVerifiedUser(t, handler, store, workspace, "ada@example.com")

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/login", `{
		"email": "ada@example.com",
		"password": "correct horse battery staple"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp sessionResponse
	decodeBody(t, rec, &resp)
	if resp.AccessToken == "" || resp.TokenType != "bearer" {
		t.Fatalf("unexpected token response: %+v", resp)
	}
	if strings.Contains(rec.Body.String(), "refresh_token") {
		t.Fatal("login response must not expose the refresh token")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != refreshCookieName || !cookies[0].HttpOnly {
		t.Fatalf("refresh cookie = %+v, want HttpOnly refresh cookie", cookies)
	}
	if resp.User.ID != user.ID {
		t.Fatalf("user id = %q", resp.User.ID)
	}
	if resp.Organization.ID != fakeUUID(900) || resp.Organization.Role != "owner" {
		t.Fatalf("organization = %+v", resp.Organization)
	}

	tokens := kit.NewManager("test-secret", "email-platform", 15*time.Minute)
	claims, err := tokens.Parse(resp.AccessToken)
	if err != nil {
		t.Fatalf("parse access token: %v", err)
	}
	if claims.Subject != user.ID || claims.OrganizationID != fakeUUID(900) || claims.SessionID == "" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestRefreshRotatesCookieToken(t *testing.T) {
	handler, store, workspace, _ := newTestHandler(t)
	seedVerifiedUser(t, handler, store, workspace, "cookie@example.com")

	login := doJSON(t, handler.Routes(), http.MethodPost, "/auth/login", `{
		"email": "cookie@example.com",
		"password": "correct horse battery staple"
	}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies = %d, want one", len(cookies))
	}

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", strings.NewReader("{}"))
	req.AddCookie(cookies[0])
	rec := httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rotated := rec.Result().Cookies()
	if len(rotated) != 1 || rotated[0].Value == cookies[0].Value || !rotated[0].HttpOnly {
		t.Fatalf("rotated cookie = %+v", rotated)
	}
}

func TestLoginRejectsBadPassword(t *testing.T) {
	handler, store, workspace, _ := newTestHandler(t)
	seedVerifiedUser(t, handler, store, workspace, "ada@example.com")

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/login", `{
		"email": "ada@example.com",
		"password": "wrong password"
	}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if code, _ := errorOf(t, rec); code != "invalid_credentials" {
		t.Fatalf("code = %q", code)
	}
}

func TestLoginUnknownUserIsUniform(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/login", `{
		"email": "ghost@example.com",
		"password": "whatever password"
	}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if code, _ := errorOf(t, rec); code != "invalid_credentials" {
		t.Fatalf("code = %q", code)
	}
}

func TestLoginRequiresVerifiedEmail(t *testing.T) {
	handler, store, workspace, _ := newTestHandler(t)
	hash, _ := HashPassword("correct horse battery staple")
	user, err := store.CreateUserWithRegistration(context.Background(), "pending@example.com", hash, "Pending", "Org", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	workspace.memberships[user.ID] = WorkspaceMembership{OrganizationID: fakeUUID(901), Name: "Org", Slug: "org", Role: "owner"}

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/login", `{
		"email": "pending@example.com",
		"password": "correct horse battery staple"
	}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if code, _ := errorOf(t, rec); code != "email_not_verified" {
		t.Fatalf("code = %q", code)
	}
}

func TestLoginReportsWorkspacePending(t *testing.T) {
	handler, store, _, _ := newTestHandler(t)
	hash, _ := HashPassword("correct horse battery staple")
	user, err := store.CreateUserWithRegistration(context.Background(), "late@example.com", hash, "Late", "Org", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.MarkEmailVerified(context.Background(), user.ID, user.Email, ""); err != nil {
		t.Fatalf("verify: %v", err)
	}

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/login", `{
		"email": "late@example.com",
		"password": "correct horse battery staple"
	}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if code, _ := errorOf(t, rec); code != "workspace_pending" {
		t.Fatalf("code = %q", code)
	}
}

func TestLoginWorkspaceUnavailable(t *testing.T) {
	handler, store, workspace, _ := newTestHandler(t)
	user := seedVerifiedUser(t, handler, store, workspace, "ada@example.com")
	workspace.defaultErr = ErrWorkspaceUnavailable

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/login", `{
		"email": "ada@example.com",
		"password": "correct horse battery staple"
	}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if code, _ := errorOf(t, rec); code != "workspace_unavailable" {
		t.Fatalf("code = %q", code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
	_ = user
}

func TestRefreshRotatesToken(t *testing.T) {
	handler, store, workspace, _ := newTestHandler(t)
	seedVerifiedUser(t, handler, store, workspace, "ada@example.com")

	login := doJSON(t, handler.Routes(), http.MethodPost, "/auth/login", `{
		"email": "ada@example.com",
		"password": "correct horse battery staple"
	}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d", login.Code)
	}

	// Mint a refresh token the same way issueSession does.
	session, err := store.CreateSession(context.Background(), SessionCreate{
		UserID:           firstUserID(store),
		OrganizationID:   fakeUUID(900),
		OrganizationName: "Test Org",
		OrganizationSlug: "test-org",
		Role:             "owner",
		RefreshTokenHash: HashToken("refresh-secret-1"),
		ExpiresAt:        time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/refresh", `{"refresh_token":"refresh-secret-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp sessionResponse
	decodeBody(t, rec, &resp)
	if resp.AccessToken == "" {
		t.Fatal("expected access token")
	}

	stored, err := store.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if stored.RefreshTokenHash == HashToken("refresh-secret-1") {
		t.Fatal("refresh token was not rotated")
	}
}

func TestRefreshRejectsUnknownToken(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/refresh", `{"refresh_token":"nope"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if code, _ := errorOf(t, rec); code != "invalid_refresh_token" {
		t.Fatalf("code = %q", code)
	}
}

func TestRefreshReuseRevokesSession(t *testing.T) {
	handler, store, _, _ := newTestHandler(t)
	user, err := store.CreateUserWithRegistration(context.Background(), "reuse@example.com", "hash", "Reuse", "Org", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	session, err := store.CreateSession(context.Background(), SessionCreate{
		UserID:           user.ID,
		OrganizationID:   fakeUUID(902),
		OrganizationName: "Org",
		OrganizationSlug: "org",
		Role:             "owner",
		RefreshTokenHash: HashToken("first"),
		ExpiresAt:        time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	// Legitimate rotation: "first" is superseded by "second".
	if err := store.RotateRefreshToken(context.Background(), session.ID, HashToken("first"), HashToken("second"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	// Replaying the superseded token must be detected and kill the session family.
	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/refresh", `{"refresh_token":"first"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if code, _ := errorOf(t, rec); code != "refresh_token_reuse" {
		t.Fatalf("code = %q", code)
	}
	if len(store.revokedUsers) != 1 {
		t.Fatalf("revokedUsers = %v, want all sessions for the user", store.revokedUsers)
	}
}

func TestMeReturnsUserAndOrganization(t *testing.T) {
	handler, store, workspace, _ := newTestHandler(t)
	user := seedVerifiedUser(t, handler, store, workspace, "ada@example.com")

	login := doJSON(t, handler.Routes(), http.MethodPost, "/auth/login", `{
		"email": "ada@example.com",
		"password": "correct horse battery staple"
	}`)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d", login.Code)
	}
	var sessionResp sessionResponse
	decodeBody(t, login, &sessionResp)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+sessionResp.AccessToken)
	rec := httptest.NewRecorder()
	handler.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		User         userResponse         `json:"user"`
		Organization organizationResponse `json:"organization"`
	}
	decodeBody(t, rec, &resp)
	if resp.User.ID != user.ID {
		t.Fatalf("user id = %q", resp.User.ID)
	}
	if resp.Organization.Name != "Test Org" || resp.Organization.Role != "owner" {
		t.Fatalf("organization = %+v", resp.Organization)
	}
}

func TestVerifyEmail(t *testing.T) {
	handler, store, workspace, mailer := newTestHandler(t)
	user := seedVerifiedUser(t, handler, store, workspace, "fresh@example.com")
	// Make the user unverified again for this test.
	store.mu.Lock()
	store.usersByID[user.ID].EmailVerifiedAt = nil
	store.mu.Unlock()

	// Request a fresh verification link.
	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/resend-verification", `{"email":"fresh@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("resend status = %d", rec.Code)
	}

	sent := mailer.Sent()
	if len(sent) == 0 {
		t.Fatal("expected verification mail")
	}

	// Extract token from the recorded message.
	link := extractQueryParam(t, sent[len(sent)-1].Text, "token")

	// Seed the token directly to avoid parsing HTML mail.
	tokenHash := HashToken(link)
	if err := store.CreateVerificationToken(context.Background(), user.ID, user.Email, tokenHash, "account_verification", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	rec = doJSON(t, handler.Routes(), http.MethodPost, "/auth/verify", `{"token":"`+link+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status = %d, body = %s", rec.Code, rec.Body.String())
	}

	updated, err := store.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.EmailVerifiedAt == nil {
		t.Fatal("email was not marked verified")
	}
}

func TestResendVerificationRateLimitsByEmail(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	for attempt := 1; attempt <= 3; attempt++ {
		rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/resend-verification", `{"email":"limited@example.com"}`)
		if attempt < 3 && rec.Code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want 200", attempt, rec.Code)
		}
		if attempt == 3 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("attempt %d status = %d, want 429", attempt, rec.Code)
			}
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("rate limited response has no Retry-After header")
			}
		}
	}
}

func TestVerifyRejectsInvalidToken(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/verify", `{"token":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code, _ := errorOf(t, rec); code != "invalid_token" {
		t.Fatalf("code = %q", code)
	}
}

func TestForgotPasswordAlwaysSucceeds(t *testing.T) {
	handler, store, workspace, mailer := newTestHandler(t)
	seedVerifiedUser(t, handler, store, workspace, "ada@example.com")

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/forgot-password", `{"email":"ada@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if len(mailer.Sent()) != 1 {
		t.Fatalf("sent = %d, want 1", len(mailer.Sent()))
	}
	if !strings.Contains(mailer.Sent()[0].Text, "/auth/reset-password?token=") {
		t.Fatal("reset mail missing link")
	}

	rec = doJSON(t, handler.Routes(), http.MethodPost, "/auth/forgot-password", `{"email":"ghost@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unknown address status = %d, want 200 (no enumeration)", rec.Code)
	}
	if len(mailer.Sent()) != 1 {
		t.Fatalf("sent = %d, want no extra mail for unknown address", len(mailer.Sent()))
	}
}

func TestResetPasswordUpdatesAndRevokesSessions(t *testing.T) {
	handler, store, workspace, _ := newTestHandler(t)
	user := seedVerifiedUser(t, handler, store, workspace, "ada@example.com")

	// Create a session that must be revoked after the reset.
	if _, err := store.CreateSession(context.Background(), SessionCreate{
		UserID: user.ID, OrganizationID: fakeUUID(900), Role: "owner",
		RefreshTokenHash: HashToken("old-session"), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	const token = "reset-token-abc"
	if err := store.CreateVerificationToken(context.Background(), user.ID, user.Email, HashToken(token), "password_reset", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/reset-password", `{"token":"reset-token-abc","password":"a brand new password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	updated, err := store.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if ok, err := VerifyPassword(updated.PasswordHash, "a brand new password"); err != nil || !ok {
		t.Fatalf("password was not updated: ok=%v err=%v", ok, err)
	}
	if len(store.revokedUsers) != 1 {
		t.Fatalf("revokedUsers = %v", store.revokedUsers)
	}

	// Token is single use.
	rec = doJSON(t, handler.Routes(), http.MethodPost, "/auth/reset-password", `{"token":"reset-token-abc","password":"another new password"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("reuse status = %d, want 400", rec.Code)
	}
}

func TestLogoutRevokesByRefreshToken(t *testing.T) {
	handler, store, _, _ := newTestHandler(t)
	user, err := store.CreateUserWithRegistration(context.Background(), "bye@example.com", "hash", "Bye", "Org", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := store.CreateSession(context.Background(), SessionCreate{
		UserID: user.ID, OrganizationID: fakeUUID(903), Role: "owner",
		RefreshTokenHash: HashToken("logout-token"), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("session: %v", err)
	}

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/logout", `{"refresh_token":"logout-token"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp struct {
		Revoked bool `json:"revoked"`
	}
	decodeBody(t, rec, &resp)
	if !resp.Revoked {
		t.Fatal("expected session to be revoked")
	}
}

func TestLogoutReportsSessionRevocationFailure(t *testing.T) {
	handler, store, _, _ := newTestHandler(t)
	store.revokeTokenErr = errors.New("database unavailable")

	rec := doJSON(t, handler.Routes(), http.MethodPost, "/auth/logout", `{"refresh_token":"logout-token"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	if cookies := rec.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("logout cleared cookies after failed revocation: %#v", cookies)
	}
}

func TestHealthAndReady(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	routes := handler.Routes()

	rec := doJSON(t, routes, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d", rec.Code)
	}

	rec = doJSON(t, routes, http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("readyz status = %d", rec.Code)
	}
}

type smtpProbeMailer struct {
	err error
}

func (m smtpProbeMailer) Provider() string { return "smtp" }
func (m smtpProbeMailer) Send(context.Context, MailMessage) error {
	return nil
}
func (m smtpProbeMailer) Probe(context.Context) error { return m.err }

func TestReadyFailsWhenSMTPProbeFails(t *testing.T) {
	handler, _, _, _ := newTestHandler(t)
	handler.mailer = smtpProbeMailer{err: errors.New("SMTP AUTH returned 535")}

	rec := doJSON(t, handler.Routes(), http.MethodGet, "/readyz", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", rec.Code, rec.Body.String())
	}
	if code, _ := errorOf(t, rec); code != "smtp_unavailable" {
		t.Fatalf("error code = %q, want smtp_unavailable", code)
	}
}

func TestInternalInvitationEmailUsesConfiguredMailer(t *testing.T) {
	handler, _, _, mailer := newTestHandler(t)
	handler.cfg.ServiceToken = "service-secret"
	request := httptest.NewRequest(http.MethodPost, "/internal/mail/invitation", strings.NewReader(`{
		"email":"invitee@example.test",
		"role":"admin",
		"organization_name":"Example Workspace",
		"accept_url":"https://app.example.test/auth/accept-invite?token=abc"
	}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Service-Token", "service-secret")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("invitation mail status = %d, body = %s", response.Code, response.Body.String())
	}
	sent := mailer.Sent()
	if len(sent) != 1 || sent[0].To != "invitee@example.test" ||
		!strings.Contains(sent[0].Text, "Example Workspace") ||
		!strings.Contains(sent[0].Text, "accept-invite?token=abc") {
		t.Fatalf("sent invitation mail = %#v", sent)
	}

	request = httptest.NewRequest(http.MethodPost, "/internal/mail/invitation", strings.NewReader(`{}`))
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated invitation mail status = %d, want 401", response.Code)
	}
}

// --- helpers ---------------------------------------------------------------

func firstUserID(store *fakeStore) string {
	store.mu.Lock()
	defer store.mu.Unlock()
	for id := range store.usersByID {
		return id
	}
	return ""
}

func extractQueryParam(t *testing.T, text, key string) string {
	t.Helper()
	marker := key + "="
	idx := strings.Index(text, marker)
	if idx < 0 {
		t.Fatalf("parameter %q not found in %q", key, text)
	}
	rest := text[idx+len(marker):]
	end := strings.IndexAny(rest, "& \n\r\"")
	if end < 0 {
		return rest
	}
	return rest[:end]
}
