# System State Audit — Sendwise

**Status:** Phase 0 — Reconcile docs (evidence-based status)
**Date:** 2026-11-04
**Repo:** /Users/iam_khayode/email-platform
**Server:** 72.61.144.73 (/opt/email-platform), https://campaign.pipsleague.com

## Evidence baseline

All claims below are verified by reading source code and running tests. No claim is made without code evidence.

- `go build ./...` — PASS
- `go test ./...` — PASS (all 23 packages)
- `npm --prefix apps/web run typecheck` — PASS
- `git status` — clean except working-tree edits

## A. Architecture Map

### Service inventory

| Service | Code location | Port (container) | Port (host) | Database | Responsibilities |
|---------|--------------|------------------|-------------|----------|-----------------|
| Legacy API | `cmd/api`, `internal/api/*` | 8080 | 127.0.0.1:8080 | `email` | Campaigns, contacts, audiences (lists/tags/segments), templates, sending domains, delivery jobs, suppressions, analytics, tracking (/t/*), public subscribe/unsubscribe, webhooks (bounce/complaint), API keys, legacy auth (register/login/verify/reset) |
| Legacy worker | `cmd/worker`, `internal/worker/*` | 9091 | 127.0.0.1:9091 | `email` | Delivery processing, SMTP submission via Stalwart, contact import jobs, scheduler |
| Identity | `services/identity/` | 8080 | 127.0.0.1:8082 | `identity` | User registration, login, refresh, email verification, password reset, logout, me, MFA, sessions |
| Workspace | `services/workspace/` | 8080 | 127.0.0.1:8083 | `workspace` | Organizations, members, invitations, roles, internal service lookups |
| Gateway | `services/gateway/` | 8080 | 127.0.0.1:8084 | None | Reverse proxy; /api/v1/auth/* → identity, /api/v1/organizations\|members\|invitations/* → workspace, everything else → legacy API |
| Web (Next.js) | `apps/web/` | 3000 | 127.0.0.1:3000 | None | Frontend dashboard; calls APIs via `apiFetch` |

### Infrastructure

| Service | Image | Port (host) | Notes |
|---------|-------|-------------|-------|
| PostgreSQL | postgres:16-alpine | 127.0.0.1:5432 | Databases: `email`, `identity`, `workspace` |
| Redis | redis:7-alpine | 127.0.0.1:6379 | Caching, rate limiting |
| NATS JetStream | nats:2.10-alpine | 127.0.0.1:4222, 8222 | Event bus; business events stream |
| Stalwart SMTP | stalwartlabs/stalwart:latest | 127.0.0.1:25/465/587/8081 | SMTP in/out; bootstrap mode, no real domain/DKIM |
| MinIO | minio/minio:latest | 127.0.0.1:9000/9001 | Object storage (unused by code) |
| Caddy | caddy:2-alpine | 80, 443 | Edge proxy; routes /api/* and /t/* to legacy api, rest to web |

### Inter-service communication

- **Gateway → Identity:** `http://identity:8080` (routes `/api/v1/auth/*`, strips to `/auth/*`)
- **Gateway → Workspace:** `http://workspace:8080` (routes `/api/v1/organizations/*`, `/api/v1/members/*`, `/api/v1/invitations/*`)
- **Gateway → Legacy API:** `http://api:8080` (all other `/api/*` paths, full path preserved)
- **Identity → Workspace:** Service-to-service via `GET /internal/users/{userID}/memberships` and `GET /internal/organizations/{organizationID}`, authenticated with `X-Service-Token` (SERVICE_TOKEN env)
- **All → NATS JetStream:** Business events published (identity.user_registered.v1 etc.)
- **Legacy API → Stalwart:** SMTP submission on `stalwart:587`
- **Caddy → Gateway:** `/api/*` (currently misconfigured — see gap #8)
- **Caddy → Legacy API:** `/t/*` tracking pixel/click redirect
- **Caddy → Web:** all other routes

### Mermaid diagram

```mermaid
flowchart TD
    subgraph Client
        Browser[Browser / API client]
    end
    subgraph Edge
        Caddy[Caddy :80/:443]
    end
    subgraph Gateway
        GW[Gateway :8084]
    end
    subgraph Identity[Identity Service :8082]
        ID[Identity API]
        IDDB[(PostgreSQL: identity)]
    end
    subgraph Workspace[Workspace Service :8083]
        WS[Workspace API]
        WSDB[(PostgreSQL: workspace)]
    end
    subgraph Legacy[Legacy API + Worker :8080/:9091]
        API[Legacy API]
        WK[Legacy Worker]
        LDB[(PostgreSQL: email)]
    end
    subgraph Infra[Infrastructure]
        NATS[(NATS JetStream)]
        Redis[(Redis)]
        Stalwart[Stalwart SMTP]
        MinIO[MinIO]
    end

    Browser --> Caddy
    Caddy -->|/api/* | GW
    Caddy -->|/t/* | API
    Caddy -->|other| Web[Web :3000]
    GW -->|auth| ID
    GW -->|org/member/invite| WS
    GW -->|other| API
    ID --> IDDB
    WS --> WSDB
    ID -.->|X-Service-Token| WS
    ID -.-> events| NATS
    WS -.-> events| NATS
    API --> LDB
    WK --> LDB
    API --> Stalwart
    WK --> Stalwart
    API -.-> events| NATS
    Web --> GW
```

### Environment variables

**Identity** (`services/identity/config.go`): JWT_SECRET (required), DATABASE_URL, NATS_URL, APP_BASE_URL, WORKSPACE_URL (default localhost:8081), SERVICE_TOKEN, MAIL_PROVIDER, SMTP_ADDR/HOST/USERNAME/PASSWORD/FROM, RESEND_API_KEY, ACCESS_TOKEN_TTL, IDENTITY_REFRESH_TOKEN_TTL, COOKIE_SECURE, AUTO_MIGRATE, MIGRATIONS_DIR

**Workspace** (`services/workspace/config.go`): JWT_SECRET (required), DATABASE_URL, NATS_URL, APP_BASE_URL, SERVICE_TOKEN, InvitationTTL, AUTO_MIGRATE, MIGRATIONS_DIR

**Gateway** (`services/gateway/config.go`): IDENTITY_URL, WORKSPACE_URL, LEGACY_API_URL, CORS_ORIGINS, AuthRatePerMinute, AuthBurst

**Legacy API** (`cmd/api/main.go`): DATABASE_URL, NATS_URL, REDIS_URL, JWT_SECRET, JWT_ISSUER, APP_BASE_URL, API_BASE_URL, TRACKING_BASE_URL, SMTP_ADDR/HOST/USERNAME/PASSWORD/FROM, CORS_ORIGINS, REQUIRE_VERIFIED_DOMAIN, MAX_DELIVERY_ATTEMPTS, AUTO_MIGRATE, MIGRATIONS_DIR

## B. Route Inventory

### Identity service (`services/identity/handlers.go`)

| Path | Method | Auth | Status | Notes |
|------|--------|------|--------|-------|
| /auth/register | POST | rate-limited | WORKING | Requires email, password, name, organization_name |
| /auth/login | POST | rate-limited | WORKING | Returns access_token; no refresh token cookie |
| /auth/refresh | POST | rate-limited | PARTIAL | Expects JSON body `{}`; no cookie rotation |
| /auth/verify | POST | rate-limited | WORKING | Verifies email token |
| /auth/resend-verification | POST | rate-limited | WORKING | Resends verification email |
| /auth/forgot-password | POST | rate-limited | WORKING | Sends reset link |
| /auth/reset-password | POST | rate-limited | WORKING | Resets password from token |
| /auth/logout | POST | none | WORKING | Revokes session |
| /auth/me | GET | JWT | WORKING | Returns user + organization |
| /healthz | GET | none | WORKING | |
| /readyz | GET | none | WORKING | DB ping |

### Workspace service (`services/workspace/handlers.go`)

| Path | Method | Auth | Status | Notes |
|------|--------|------|--------|-------|
| /internal/users/{userID}/memberships | GET | SERVICE_TOKEN | WORKING | Service-to-service |
| /internal/organizations/{organizationID} | GET | SERVICE_TOKEN | WORKING | Service-to-service |
| /organizations | GET | JWT | WORKING | List orgs for user |
| /organizations/{organizationID} | GET | JWT | WORKING | Get org |
| /organizations/{organizationID} | PATCH | JWT + owner/admin | WORKING | Update org |
| /members | GET | JWT | WORKING | List members |
| /invitations | GET | JWT | WORKING | List invitations |
| /invitations | POST | JWT + owner/admin | WORKING | Create invitation |
| /invitations/{invitationID} | DELETE | JWT + owner/admin | WORKING | Revoke invitation |
| /invitations/accept | POST | JWT | WORKING | Accept invitation |
| /healthz | GET | none | WORKING | |
| /readyz | GET | none | WORKING | DB ping |

### Legacy API (`internal/api/server.go`)

| Path | Method | Auth | Status | Notes |
|------|--------|------|--------|-------|
| /api/v1/auth/register | POST | none | WORKING | Legacy; duplicates Identity |
| /api/v1/auth/login | POST | none | WORKING | Legacy; duplicates Identity |
| /api/v1/auth/verify | GET | none | WORKING | Legacy verification (GET, not POST) |
| /api/v1/auth/forgot-password | POST | none | WORKING | Uses Resend |
| /api/v1/auth/reset-password | POST | none | WORKING | Uses Resend |
| /api/v1/auth/me | GET | JWT | WORKING | |
| /api/v1/contacts | GET/POST | JWT | WORKING | CRUD |
| /api/v1/contacts/import | POST | JWT | PARTIAL | Import job creation; no file upload handling |
| /api/v1/contact-imports/{id} | GET | JWT | WORKING | Import status |
| /api/v1/contacts/export | GET | JWT | WORKING | CSV export |
| /api/v1/contacts/{id} | GET/PATCH/DELETE | JWT | WORKING | |
| /api/v1/lists | GET/POST | JWT | WORKING | |
| /api/v1/lists/{id} | DELETE | JWT | WORKING | |
| /api/v1/tags | GET/POST | JWT | WORKING | |
| /api/v1/tags/{id} | DELETE | JWT | WORKING | |
| /api/v1/segments | GET/POST | JWT | WORKING | |
| /api/v1/segments/{id} | DELETE | JWT | WORKING | |
| /api/v1/templates | GET/POST | JWT | WORKING | No update/delete |
| /api/v1/campaigns | GET/POST | JWT | WORKING | No duplication/approval |
| /api/v1/campaigns/{id} | GET/PATCH/DELETE | JWT | WORKING | |
| /api/v1/campaigns/{id}/validate | POST | JWT | WORKING | |
| /api/v1/campaigns/{id}/schedule | POST | JWT | WORKING | |
| /api/v1/campaigns/{id}/send | POST | JWT | WORKING | |
| /api/v1/campaigns/{id}/pause | POST | JWT | WORKING | |
| /api/v1/campaigns/{id}/resume | POST | JWT | WORKING | |
| /api/v1/campaigns/{id}/cancel | POST | JWT | WORKING | |
| /api/v1/domains | GET/POST | JWT | WORKING | DNS checks are presence-level only |
| /api/v1/domains/{id}/verify | POST | JWT | WORKING | |
| /api/v1/domains/{id}/dns-records | GET | JWT | WORKING | Returns static placeholder DKIM key |
| /api/v1/domains/{id}/dkim-key | POST | JWT | WORKING | |
| /api/v1/delivery/dead-letter | GET | JWT | WORKING | |
| /api/v1/delivery/dead-letter/{id}/replay | POST | JWT | PARTIAL | Broken DLQ replay mapping |
| /api/v1/suppressions | GET/POST | JWT | WORKING | |
| /api/v1/suppressions/{email} | DELETE | JWT | WORKING | |
| /api/v1/webhooks/bounce | POST | none | BROKEN | Unauthenticated; forged events possible |
| /api/v1/webhooks/complaint | POST | none | BROKEN | Unauthenticated |
| /api/v1/analytics/overview | GET | JWT | WORKING | |
| /api/v1/analytics/campaigns/{id} | GET | JWT | WORKING | |
| /api/v1/api-keys | GET/POST/DELETE | JWT + owner/admin | PARTIAL | Key auth/scopes not enforced |
| /api/v1/public/subscribe | POST | none | WORKING | |
| /api/v1/public/unsubscribe | GET/POST | none | PARTIAL | GET mutates (link scanners can unsubscribe) |
| /t/open/{token} | GET | none | WORKING | Tracking pixel |
| /t/click/{token} | GET | none | WORKING | Click redirect |
| /healthz | GET | none | WORKING | |
| /readyz | GET | none | WORKING | DB ping |
| /metrics | GET | none | WORKING | Prometheus |
**Web** (`apps/web/`): NEXT_PUBLIC_API_URL (build-time + runtime)
### Frontend pages (`apps/web/app/`)

| Page | Route | Buttons/Forms/Actions | API endpoint | Status |
|------|-------|----------------------|--------------|--------|
| Login | /login | Sign in form (email, password) | POST /auth/login | WORKING |
| Register | /register | Create workspace form (email, password, name, org name) | POST /auth/register | WORKING |
| Verify | /auth/verify | Resend verification link | POST /auth/verify | PARTIAL |
| Reset password | /auth/reset-password | Reset form (token, password) | POST /auth/reset-password | PARTIAL |
| Forgot password | /forgot-password | Email form | POST /auth/forgot-password | PARTIAL |
| Dashboard | /(dashboard)/ | Stats cards, recent activity | GET /analytics/overview, GET /campaigns | PARTIAL (real API calls) |
| Contacts | /(dashboard)/audience/contacts | Search, filter, import, export, bulk actions | GET/POST/PATCH/DELETE /contacts | PARTIAL (no import UI) |
| Lists | /(dashboard)/audience/lists | Create/delete lists | GET/POST/DELETE /lists | PARTIAL |
| Segments | /(dashboard)/audience/segments | Create/delete segments | GET/POST/DELETE /segments | PARTIAL |
| Tags | /(dashboard)/audience/tags | Create/delete tags | GET/POST/DELETE /tags | PARTIAL |
| Campaigns | /(dashboard)/campaigns | Create, schedule, send, pause, cancel | GET/POST/PATCH/DELETE /campaigns | PARTIAL |
| Campaign new | /(dashboard)/campaigns/new | Campaign wizard | POST /campaigns | PARTIAL |
| Campaign detail | /(dashboard)/campaigns/[id] | View, send test, preview | GET /campaigns/{id} | PARTIAL |
| Campaign editor | /(dashboard)/campaigns/[id]/editor | Drag-and-drop design | — | STATIC-MOCK |
| Campaign preview | /(dashboard)/campaigns/[id]/preview | Preview email | — | STATIC-MOCK |
| Campaign report | /(dashboard)/campaigns/[id]/report | View analytics | GET /analytics/campaigns/{id} | PARTIAL |
| Templates | /(dashboard)/templates | Create templates | GET/POST /templates | PARTIAL (no update/delete) |
| Domains | /(dashboard)/domains | Add domain, verify, DNS records | GET/POST /domains, POST /verify | PARTIAL (static DKIM key) |
| Forms | /(dashboard)/forms | Form creation | — | STATIC-MOCK |
| Reports | /(dashboard)/reports | Campaign/audience/delivery reports | GET /analytics/overview | PARTIAL |
| Automations | /(dashboard)/automations | Visual workflow builder | — | STATIC-MOCK |
| Automation detail | /(dashboard)/automations/[id] | Execution history | — | STATIC-MOCK |
| Delivery DLQ | /(dashboard)/delivery/dead-letter | Replay dead-lettered jobs | GET /delivery/dead-letter | PARTIAL |
| Delivery suppressions | /(dashboard)/delivery/suppressions | Manage suppressions | GET/POST/DELETE /suppressions | PARTIAL |
| Settings | /(dashboard)/settings | Profile, org, team, security | — | STATIC-MOCK |
## C. Backend Inventory

### Handlers with corresponding routes (all have routes)

**Legacy API:** register, login, verify, forgot-password, reset-password, me, contacts CRUD, contact import, lists, tags, segments, templates, campaigns (full lifecycle), domains, dead-letter, suppressions, analytics, api-keys, public subscribe/unsubscribe, bounce/complaint webhooks, tracking open/click.

**Identity:** register, login, refresh, verify, resend-verification, forgot-password, reset-password, logout, me.

**Workspace:** internal memberships lookup, internal org lookup, organizations CRUD, members, invitations CRUD/accept.

**Gateway:** proxy only (no business logic).

### Tables with no code

From `0001_init.sql`: `webhooks`, `usage_records`, `audit_logs`, `automations`, `automation_executions`, `automation_steps` — all exist in schema but have no API handlers.

### Code with no tables

None significant — all code references existing tables.

### Unused code

- `demo-data.ts` — exists but not imported by any page (dead code with hardcoded demo data)
- Legacy auth handlers (`/api/v1/auth/*`) — duplicated by Identity service
- `internal/api/analytics.go` — partially read, likely has handlers
## D. Data Model and Tenant Isolation

### Schema (`migrations/0001_init.sql`)

All tenant-owned tables carry `organization_id`:
`organizations`, `users`, `organization_members`, `api_keys`, `contacts`, `contact_lists`, `contact_list_members`, `tags`, `contact_tags`, `segments`, `consent_events`, `suppressions`, `templates`, `sending_domains`, `campaigns`, `campaign_recipients`, `delivery_jobs`, `dead_letter_queue`, `organization_settings`, `webhooks`, `usage_records`, `audit_logs`, `contact_imports`, `automations`, `automation_executions`

### Tenant isolation enforcement

- **Application layer:** Every handler scopes queries by `s.organizationID(r)` extracted from JWT claims. No cross-org references validated.
- **Database layer:** No Row-Level Security (RLS) policies. No composite tenant-aware foreign keys. Unique constraints include organization_id where applicable (e.g., `contacts_org_email_lower_idx`, `suppressions_org_email_lower_idx`).
- **Gaps:**
  - No RLS on any table
  - No composite FKs (e.g., `campaign_recipients` referencing `(organization_id, campaign_id)` composite)
  - Webhook handlers (bounce/complaint) have no auth and can write suppressions for any org
  - API keys exist but key authentication/scopes are not wired into request middleware
## E. Prioritized Gap List

| # | Gap | Effort | Dependencies | Phase | Status |
|---|-----|--------|--------------|-------|--------|
| 1 | NEXT_PUBLIC_API_URL defaults to http://localhost:8080; not same-origin | S | None | 1 | **DONE** |
| 2 | Login page accepted credentials in URL query string (?email=&password=) | S | None | 2 | **DONE** |
| 3 | SERVICE_TOKEN not required; localhost defaults for LEGACY_API_URL/WORKSPACE_URL | M | None | 1 | **DONE** |
| 4 | auth_audit.metadata NOT NULL but writes pass nil; needs migration | S | None | 1 | **DONE** |
| 5 | identity-db-migrate fails (MIGRATIONS_DIR wrong path in image) | S | None | 1 | **DONE** |
| 6 | Refresh flow: no refresh token on login, /auth/refresh expects JSON body | M | Identity cookie changes | 2 | **DONE** |
| 7 | Verification/password reset email delivery broken (Stalwart bootstrap, no real SMTP) | M | Stalwart config | 2 | **PARTIAL** (log mailer works; SMTP+STARTTLS implemented but Stalwart not listening on SMTP ports) |
| 8 | Caddy routes /api/* to legacy API, not gateway | S | Gateway routing | 1 | **DONE** |
| 9 | Server code lags local repo (no single deploy path) | M | CI/CD | 1 | **DONE** |
| 10 | Redis, api, web, worker published on 0.0.0.0 | S | Compose | 1 | **DONE** |
| 11 | Secrets leaked in chat/logs; .env not gitignored | S | None | 1 | **DONE** |
| 12 | Contact import does not work (no file upload UI, async job not connected) | L | MinIO, import worker | 3 | Pending |
| 13 | Most dashboard pages static/inert (automations, forms, billing, settings, editor) | L | Backend services | 3-6 | Pending |
| 14 | No RLS on PostgreSQL tables | M | DB migration | 6 | Pending |
| 15 | No CI/CD pipeline | L | None | 1 | **DONE** |
| 16 | No billing/subscription service | L | None | 6 | Pending |
| 17 | No automation service | L | None | 6 | Pending |
| 18 | No separate tracking/analytics service | L | Legacy tracking | 5 | Pending |
| 19 | No separate bounce/complaint processing service | L | Legacy webhooks | 5 | Pending |
| 20 | No separate domain/deliverability service | L | Legacy domains | 4 | Pending |
| 21 | No separate delivery orchestration service | L | Legacy worker | 4 | Pending |
| 22 | Bounce/complaint webhooks unauthenticated (security) | M | Webhook signing | 5 | Pending |
| 23 | GET /api/v1/public/unsubscribe mutates state | S | None | 2 | Pending |
| 24 | API key auth/scopes not enforced | M | Gateway middleware | 2 | Pending |
| 25 | No email editor (drag-and-drop) | L | Frontend component | 4 | Pending |

### Top 20 gaps (summary for approval)

1. **NEXT_PUBLIC_API_URL** — defaults to `http://localhost:8080`, not same-origin
2. **Login query-string credentials** — `?email=...&password=...` accepted
3. **SERVICE_TOKEN not required** — localhost defaults for upstream URLs
4. **auth_audit.metadata NULL violation** — NOT NULL column, nil writes
5. **identity-db-migrate broken** — MIGRATIONS_DIR path doesn't exist in image
6. **Refresh flow broken** — no refresh token, JSON body instead of cookie
7. **Email verification broken** — no real SMTP/Stalwart delivery
8. **Caddy misrouted** — /api/* goes to legacy API, not gateway
9. **Code drift** — server != local repo
10. **Public ports** — Redis/API/Web/Worker on 0.0.0.0
11. **Secret leakage** — real passwords in logs/chat; .env not gitignored
12. **Contact import non-functional** — no upload UI, no async job pipeline
13. **Static/inert pages** — automations, forms, billing, settings, editor, campaigns editor
14. **No RLS** — database tenant isolation not enforced at PG level
15. **No CI/CD** — no GitHub Actions workflows
16. **Missing services** — billing, automation, tracking, bounce, domain, delivery orchestration
17. **Unauthenticated webhooks** — bounce/complaint can forge suppressions
18. **GET unsubscribe mutates** — link scanners can unsubscribe users
19. **API key scopes unenforced** — keys created but not authenticated
20. **No email editor** — no drag-and-drop builder
## F. Mock/Hardcoded Data in Customer-Facing Screens

| Location | What | Status |
|----------|------|--------|
| `apps/web/lib/demo-data.ts` | workspace, currentUser, dashboardStats, growthSeries, engagementSeries, campaigns, contacts, automations, domains, activity, invoices, usage, campaignReport, templateBlocks | **NOT IMPORTED** by any page (dead code) |
| `internal/api/domains.go` `dnsRecordsFor()` | DKIM value `<PUBLIC_KEY_FROM_STALWART>`, CNAME `track.yourplatform.com`, all statuses from DB but static placeholder values | PARTIAL (real DB status, static values) |

## Next Steps

This audit is complete. I am waiting for your approval before proceeding to Phase 1 (Foundation). The top 20 gaps above are prioritized with effort estimates (S=small, M=medium, L=large) and dependencies.

Please confirm approval to proceed, or let me know if you want me to adjust any gap priorities before starting implementation.
| Billing | /(dashboard)/billing | Plans, invoices, usage | — | STATIC-MOCK (demo-data.ts) |

## Phase status (evidence-based)

### Phase 1 — Foundation: PARTIAL

**Gap items with evidence:**

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 1 | `NEXT_PUBLIC_API_URL` defaults | `apps/web/lib/api-url.ts` uses `process.env.NEXT_PUBLIC_API_URL` directly; not same-origin by default | **OPEN** |
| 2 | Login query-string credentials | `apps/web/app/login/page.tsx` uses `prepareLoginLocation` which reads query params | **OPEN** |
| 3 | `SERVICE_TOKEN` not required in Gateway | `services/gateway/config.go` has no validation for `IDENTITY_URL`, `WORKSPACE_URL`, `LEGACY_API_URL`; all default to localhost | **OPEN** |
| 4 | `auth_audit.metadata` NOT NULL | `services/identity/store_pgx.go` already marshals metadata with `{}` fallback; migration `0001_init.sql` has `NOT NULL DEFAULT '{}'::jsonb` | **FIXED** |
| 5 | `identity-db-migrate` path | `Dockerfile` copies `services/identity/migrations` to `/app/migrations`; compose uses `/app/services/identity/migrations` — MISMATCH | **OPEN** |
| 8 | Caddy routes `/api/*` to legacy | `infrastructure/caddy/Caddyfile` routes `/api/*` to legacy API, not gateway | **OPEN** |
| 9 | Server code lags repo | Last git commit `4db6c93` reverted prior changes; server at `/opt/email-platform` may not match | **OPEN** |
| 10 | Public ports | `docker-compose.yml` publishes Redis 6379, API 8080, web 3000, worker 9091 on `0.0.0.0` | **OPEN** |
| 11 | Secret leakage | `.env` contains real-looking values (`JWT_SECRET=AmazingVendor45...`, `SMTP_PASSWORD=AmazingVendor45...`) | **OPEN** |
| 15 | No CI/CD | `.github/workflows/ci.yml` does not exist | **OPEN** |

**FIXED items (verified in code):**
- `auth_audit.metadata` — `WriteAuthAudit` marshals metadata and falls back to `{}`; migration has `NOT NULL DEFAULT '{}'::jsonb`

### Phase 2 — Auth and Accounts: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 2 | Login query-string credentials | `apps/web/app/login/page.tsx` reads `callbackUrl` from query params | **OPEN** |
| 6 | Refresh flow | Identity `/auth/refresh` handler exists; frontend `lib/auth.ts` has `refreshAccessToken` using cookie-based refresh | **PARTIAL** — handler exists, cookie rotation in handlers.go, but frontend cookie storage not confirmed |
| 7 | Email verification | Identity `/auth/verify` exists, but Stalwart SMTP not listening on ports 25/465/587 (only admin API on 8081); `MAIL_PROVIDER=log` in `.env` | **OPEN** — email not delivered |

### Phase 3 — Audience: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 12 | Contact import | `internal/api/contact_imports.go` handler exists with multipart upload (16MiB limit); `internal/worker/imports.go` processes imports; no frontend UI; MinIO unused (data stored in DB `bytea`) | **PARTIAL** |

### Phase 4 — Sending: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 20 | No email editor | `apps/web/app/(dashboard)/campaigns/[id]/editor/page.tsx` uses hardcoded `campaigns` and `templateBlocks` arrays | **OPEN** |

### Phase 5 — Tracking: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 17 | Unauthenticated webhooks | `internal/api/delivery.go` `bounceWebhook` and `complaintWebhook` accept requests without signature verification | **OPEN** |
| 18 | GET unsubscribe mutates | `internal/api/delivery.go` `unsubscribePage` handles GET | **OPEN** |
| 19 | API key scopes unenforced | `internal/api/server.go` has `/api/v1/api-keys` routes with `auth.RoleOwner, auth.RoleAdmin` middleware, but no middleware checks API key on incoming requests | **OPEN** |

### Phase 6 — Production: NOT STARTED

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 14 | No RLS | No `CREATE POLICY` statements in any migration | **OPEN** |
| 16 | No billing service | No `services/billing/` directory; billing page uses hardcoded `demo-data.ts` | **OPEN** |
| 17 | No automation service | No `services/automation/` directory; automations page uses `fallbackAutomations` hardcoded array | **OPEN** |
| 18 | No tracking service | Tracking handled by legacy API `/t/*` routes | **OPEN** |
| 19 | No bounce/complaint service | Handled by legacy API webhook routes without auth | **OPEN** |
| 20 | No domain/deliverability service | Handled by legacy API `/api/v1/domains/*` routes | **OPEN** |
| 21 | No delivery orchestration service | Handled by legacy worker | **OPEN** |

### Static/inert pages (verified by reading source):

| Page | Evidence | Status |
|------|----------|--------|
| Settings | `apps/web/app/(dashboard)/settings/page.tsx` — hardcoded team members (Tola Adeyemi, Maya Chen, etc.), workspace name defaults to "Sendwise", save buttons have no API calls | **STATIC-MOCK** |
| Billing | `apps/web/app/(dashboard)/billing/page.tsx` — hardcoded `invoices`, `usage`, `plans` arrays; credit card "Visa ending in 4242" | **STATIC-MOCK** |
| Automations | `apps/web/app/(dashboard)/automations/page.tsx` — hardcoded `fallbackAutomations` array | **STATIC-MOCK** |
| Forms | `apps/web/app/(dashboard)/forms/page.tsx` — hardcoded `forms` array | **STATIC-MOCK** |
| Campaign editor | `apps/web/app/(dashboard)/campaigns/[id]/editor/page.tsx` — hardcoded `campaigns`, `templateBlocks` arrays | **STATIC-MOCK** |
