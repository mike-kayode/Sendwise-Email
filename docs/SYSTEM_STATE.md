# System State Audit — Sendwise

**Status:** T0 verification incomplete; no production-readiness claim
**Date:** 2026-11-04
**Repo:** /Users/iam_khayode/email-platform
**Server:** 72.61.144.73 (/opt/email-platform), https://campaign.pipsleague.com

## T0 verification record

The local working tree is not buildable at this audit: `go test ./...` and the Docker build fail because the existing uncommitted `internal/api/contact_imports.go` declares package `main` while the rest of `internal/api` declares package `api`. This file and the other pre-existing working-tree changes were left untouched.

To gather baseline evidence without altering those changes, the committed snapshot at `2066258` was tested in an isolated temporary checkout:

- `go test ./...` — PASS; this run did not enable database-backed integration tests.
- Combined Compose YAML (`docker-compose.yml` then `docker-compose.microservices.yml`) parses, but the unmodified stack did not start. The MinIO pull was denied; after omitting MinIO, PostgreSQL init failed because the mounted script was not executable, then because the PostgreSQL service did not receive the identity/workspace DB credentials required by that script. The gateway definition also omitted `LEGACY_API_URL` and `SERVICE_TOKEN`.
- The baseline services did start only after temporary, uncommitted test overrides supplied the missing DB variables and gateway upstream, and made the init script executable. The MinIO service was omitted. The three migration containers exited 0. This is not a pass for the checked-in full-stack deployment.
- API, Identity, Workspace, and Gateway `/healthz` and `/readyz` returned HTTP 200 in that overridden local stack. Caddy returned HTTP 308 to a request on port 80; the redirect was not followed.
- An unauthenticated route sweep reached the registered protected routes (401) and request handlers for public/webhook paths. `DELETE /api/v1/tags/{id}` returned 404 and has no registered route. These probes are dispatch checks, not successful business-operation tests.
- `TestContactImportAPIQueuesAndScopesProgress` — PASS against an isolated PostgreSQL schema. `TestContactImportWorkerPersistsPendingContactsAndResults` — FAIL: the processor reported the invalid CSV row as row 3 while the test expects row 4. The DB-backed API integration group also failed: its registration assertions expect an access token, while the legacy registration handler returns only a message and email; subsequent protected requests therefore used an empty token.
- With the Legacy API stopped, Gateway `/readyz` still returned HTTP 200. Legacy reachability is not a readiness gate.

No untested route or skipped integration test is counted as passing. The complete full-stack and authenticated route verification requested for T0 remains open.

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
| Stalwart SMTP | stalwartlabs/stalwart:latest | 127.0.0.1:25/465/587/8081 | Local bootstrap service; no successful verification/reset delivery was established |
| MinIO | minio/minio:latest | 127.0.0.1:9000/9001 | Object storage (unused by code) |
| Caddy | caddy:2-alpine | 80, 443 | Edge proxy; routes /api/* to Gateway, /t/* to Legacy API, and other paths to Web |

### Inter-service communication

- **Gateway → Identity:** `http://identity:8080` (routes `/api/v1/auth/*`, strips to `/auth/*`)
- **Gateway → Workspace:** `http://workspace:8080` (routes `/api/v1/organizations/*`, `/api/v1/members/*`, `/api/v1/invitations/*`)
- **Gateway → Legacy API:** `http://api:8080` (all other `/api/*` paths, full path preserved)
- **Identity → Workspace:** Service-to-service via `GET /internal/users/{userID}/memberships` and `GET /internal/organizations/{organizationID}`, authenticated with `X-Service-Token` (SERVICE_TOKEN env)
- **All → NATS JetStream:** Business events published (identity.user_registered.v1 etc.)
- **Legacy API → Stalwart:** SMTP submission on `stalwart:587`
- **Caddy → Gateway:** `/api/*` (configured in source; live proxy response was not verified)
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

Status values below describe the source inventory, not blanket runtime passes. The T0 route sweep only confirmed dispatch/auth boundaries; successful authenticated business behavior is unverified unless a specific passing test is cited above.

### Identity service (`services/identity/handlers.go`)

| Path | Method | Auth | Status | Notes |
|------|--------|------|--------|-------|
| /auth/register | POST | rate-limited | SOURCE-ONLY | Requires email, password, name, organization_name |
| /auth/login | POST | rate-limited | SOURCE-ONLY | Returns access token and sets HttpOnly refresh cookie; live journey not tested |
| /auth/refresh | POST | rate-limited | PARTIAL | Accepts refresh cookie (JSON fallback exists) and rotates cookie; handler tests only |
| /auth/verify | POST | rate-limited | SOURCE-ONLY | Verifies email token |
| /auth/resend-verification | POST | rate-limited | SOURCE-ONLY | Resends verification email |
| /auth/forgot-password | POST | rate-limited | SOURCE-ONLY | Sends reset link |
| /auth/reset-password | POST | rate-limited | SOURCE-ONLY | Resets password from token |
| /auth/logout | POST | none | SOURCE-ONLY | Revokes session |
| /auth/me | GET | JWT | SOURCE-ONLY | Returns user + organization |
| /healthz | GET | none | SOURCE-ONLY | |
| /readyz | GET | none | SOURCE-ONLY | DB ping |

### Workspace service (`services/workspace/handlers.go`)

| Path | Method | Auth | Status | Notes |
|------|--------|------|--------|-------|
| /internal/users/{userID}/memberships | GET | SERVICE_TOKEN | SOURCE-ONLY | Service-to-service |
| /internal/organizations/{organizationID} | GET | SERVICE_TOKEN | SOURCE-ONLY | Service-to-service |
| /organizations | GET | JWT | SOURCE-ONLY | List orgs for user |
| /organizations/{organizationID} | GET | JWT | SOURCE-ONLY | Get org |
| /organizations/{organizationID} | PATCH | JWT + owner/admin | SOURCE-ONLY | Update org |
| /members | GET | JWT | SOURCE-ONLY | List members |
| /invitations | GET | JWT | SOURCE-ONLY | List invitations |
| /invitations | POST | JWT + owner/admin | SOURCE-ONLY | Create invitation |
| /invitations/{invitationID} | DELETE | JWT + owner/admin | SOURCE-ONLY | Revoke invitation |
| /invitations/accept | POST | JWT | SOURCE-ONLY | Accept invitation |
| /healthz | GET | none | SOURCE-ONLY | |
| /readyz | GET | none | SOURCE-ONLY | DB ping |

### Legacy API (`internal/api/server.go`)

| Path | Method | Auth | Status | Notes |
|------|--------|------|--------|-------|
| /api/v1/auth/register | POST | none | SOURCE-ONLY | Legacy; duplicates Identity |
| /api/v1/auth/login | POST | none | SOURCE-ONLY | Legacy; duplicates Identity |
| /api/v1/auth/verify | GET | none | SOURCE-ONLY | Legacy verification (GET, not POST) |
| /api/v1/auth/forgot-password | POST | none | SOURCE-ONLY | Uses Resend |
| /api/v1/auth/reset-password | POST | none | SOURCE-ONLY | Uses Resend |
| /api/v1/auth/me | GET | JWT | SOURCE-ONLY | |
| /api/v1/contacts | GET/POST | JWT | SOURCE-ONLY | CRUD |
| /api/v1/contacts/import | POST | JWT | PARTIAL | Multipart CSV upload (16 MiB limit) and import-job creation; API queue/progress tenant-isolation test passed; UI and full worker pipeline are incomplete/unverified |
| /api/v1/contact-imports/{id} | GET | JWT | SOURCE-ONLY | Import status |
| /api/v1/contacts/export | GET | JWT | SOURCE-ONLY | CSV export |
| /api/v1/contacts/{id} | GET/PATCH/DELETE | JWT | SOURCE-ONLY | |
| /api/v1/lists | GET/POST | JWT | SOURCE-ONLY | |
| /api/v1/lists/{id} | DELETE | JWT | SOURCE-ONLY | |
| /api/v1/tags | GET/POST | JWT | SOURCE-ONLY | |
| /api/v1/tags/{id} | DELETE | JWT | BROKEN | 404 in local route sweep; no DELETE route is registered in `internal/api/server.go` |
| /api/v1/segments | GET/POST | JWT | SOURCE-ONLY | |
| /api/v1/segments/{id} | DELETE | JWT | SOURCE-ONLY | |
| /api/v1/templates | GET/POST | JWT | SOURCE-ONLY | No update/delete |
| /api/v1/campaigns | GET/POST | JWT | SOURCE-ONLY | No duplication/approval |
| /api/v1/campaigns/{id} | GET/PATCH/DELETE | JWT | SOURCE-ONLY | |
| /api/v1/campaigns/{id}/validate | POST | JWT | SOURCE-ONLY | |
| /api/v1/campaigns/{id}/schedule | POST | JWT | SOURCE-ONLY | |
| /api/v1/campaigns/{id}/send | POST | JWT | SOURCE-ONLY | |
| /api/v1/campaigns/{id}/pause | POST | JWT | SOURCE-ONLY | |
| /api/v1/campaigns/{id}/resume | POST | JWT | SOURCE-ONLY | |
| /api/v1/campaigns/{id}/cancel | POST | JWT | SOURCE-ONLY | |
| /api/v1/domains | GET/POST | JWT | SOURCE-ONLY | DNS checks are presence-level only |
| /api/v1/domains/{id}/verify | POST | JWT | SOURCE-ONLY | |
| /api/v1/domains/{id}/dns-records | GET | JWT | SOURCE-ONLY | Returns static placeholder DKIM key |
| /api/v1/domains/{id}/dkim-key | POST | JWT | SOURCE-ONLY | |
| /api/v1/delivery/dead-letter | GET | JWT | SOURCE-ONLY | |
| /api/v1/delivery/dead-letter/{id}/replay | POST | JWT | PARTIAL | Broken DLQ replay mapping |
| /api/v1/suppressions | GET/POST | JWT | SOURCE-ONLY | |
| /api/v1/suppressions/{email} | DELETE | JWT | SOURCE-ONLY | |
| /api/v1/webhooks/bounce | POST | none | BROKEN | Unauthenticated; forged events possible |
| /api/v1/webhooks/complaint | POST | none | BROKEN | Unauthenticated |
| /api/v1/analytics/overview | GET | JWT | SOURCE-ONLY | |
| /api/v1/analytics/campaigns/{id} | GET | JWT | SOURCE-ONLY | |
| /api/v1/api-keys | GET/POST | JWT + owner/admin | PARTIAL | Key authentication/scopes are not enforced |
| /api/v1/api-keys/{id} | DELETE | JWT + owner/admin | SOURCE-ONLY | |
| /api/v1/public/subscribe | POST | none | SOURCE-ONLY | |
| /api/v1/public/unsubscribe | GET/POST | none | PARTIAL | GET mutates (link scanners can unsubscribe) |
| /t/open/{token} | GET | none | SOURCE-ONLY | Tracking pixel |
| /t/click/{token} | GET | none | SOURCE-ONLY | Click redirect |
| /healthz | GET | none | SOURCE-ONLY | |
| /readyz | GET | none | SOURCE-ONLY | DB ping |
| /metrics | GET | none | SOURCE-ONLY | Prometheus |
**Web** (`apps/web/`): NEXT_PUBLIC_API_URL (build-time + runtime)
### Frontend pages (`apps/web/app/`)

| Page | Route | Buttons/Forms/Actions | API endpoint | Status |
|------|-------|----------------------|--------------|--------|
| Login | /login | Sign in form (email, password) | POST /auth/login | SOURCE-ONLY |
| Register | /register | Create workspace form (email, password, name, org name) | POST /auth/register | SOURCE-ONLY |
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
## E. Verified gaps and status

| # | Finding | T0 evidence | Status |
|---|---------|-------------|--------|
| 1 | Current working tree build | `go test ./...` and Docker build fail on the existing uncommitted `internal/api/contact_imports.go` package mismatch | **BLOCKED** |
| 2 | Combined Compose startup | Compose syntax parses, but the unmodified stack fails on MinIO pull, PostgreSQL init script permissions/missing DB variables, and Gateway configuration | **BLOCKED** |
| 3 | Gateway configuration/readiness | Production config requires the three upstream URLs, Compose omits `LEGACY_API_URL`; `SERVICE_TOKEN` is not validated; `/readyz` stayed 200 with Legacy API stopped | **OPEN** |
| 4 | `auth_audit.metadata` upgrade safety | Fresh schema has `NOT NULL DEFAULT '{}'::jsonb` and writer marshals metadata; no forward migration or upgrade application was verified | **OPEN** |
| 5 | Tag deletion route | `DELETE /api/v1/tags/{id}` returned 404 and has no handler registration | **BROKEN** |
| 6 | Contact import | Multipart queue/progress and tenant-isolation test passed; worker integration test fails its row-number assertion; no UI; database `bytea` storage, MinIO unused | **PARTIAL** |
| 7 | Bounce/complaint webhooks | Route sweep and source show unauthenticated handlers; no signature/replay protection | **OPEN** |
| 8 | GET unsubscribe | Source invokes the mutating unsubscribe operation from GET | **OPEN** |
| 9 | Verification/reset email | Integration log reported no Resend API key; no successful delivery was confirmed | **OPEN** |
| 10 | Customer-facing static pages | Settings, billing, automations, forms, editor, and preview remain static/mock | **STATIC-MOCK** |
| 11 | Tenant isolation/RLS | No PostgreSQL RLS was verified; application-level tenant tests are not a substitute | **OPEN** |
| 12 | CI | `.github/workflows/ci.yml` exists but was not run in T0 | **PRESENT; NOT EXECUTED** |
| 13 | Production deployment parity | No production server inspection or deployment was performed | **UNVERIFIED** |

T0 is not signed off. The source inventory and partial local evidence above do not establish that the full stack or all authenticated routes work.

## F. Mock/Hardcoded Data in Customer-Facing Screens

| Location | What | Status |
|----------|------|--------|
| `apps/web/lib/demo-data.ts` | workspace, currentUser, dashboardStats, growthSeries, engagementSeries, campaigns, contacts, automations, domains, activity, invoices, usage, campaignReport, templateBlocks | **NOT IMPORTED** by any page (dead code) |
| `internal/api/domains.go` `dnsRecordsFor()` | DKIM value `<PUBLIC_KEY_FROM_STALWART>`, CNAME `track.yourplatform.com`, all statuses from DB but static placeholder values | PARTIAL (real DB status, static values) |

## Next Steps

T0 remains open. Do not start T1 until the current build blocker is resolved and the full-stack and authenticated-route checks are completed or explicitly scoped out.

## Phase status (evidence-based)

### Phase 1 — Foundation: PARTIAL

**Gap items with evidence:**

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 1 | `NEXT_PUBLIC_API_URL` defaults | Source config is present; live browser routing was not tested in T0 | **NOT LIVE-VERIFIED** |
| 2 | Login query-string credentials | Source cleanup exists; browser/history/referrer behavior was not tested in T0 | **NOT LIVE-VERIFIED** |
| 3 | Gateway production configuration | Checks three upstream URLs, not `SERVICE_TOKEN`; Compose omits `LEGACY_API_URL` and `SERVICE_TOKEN` | **OPEN** |
| 4 | `auth_audit.metadata` | Fresh-install migration has a default and writer marshals metadata; no forward migration or upgrade application was verified | **OPEN** |
| 5 | Identity/workspace migration paths | Migration containers exited 0 only in the overridden baseline stack | **PARTIAL** |
| 8 | Caddy routes `/api/*` to Gateway | Source is configured correctly; local HTTP request redirected with 308, so proxy routing remains unverified | **NOT LIVE-VERIFIED** |
| 9 | Server code parity | No production server inspection was performed | **UNVERIFIED** |
| 10 | Public ports | Compose source binds most local services to loopback; Caddy binds ports 80/443 on all interfaces | **SOURCE-ONLY** |
| 11 | Secret handling | `.env` is ignored and untracked; no secret scan or log exposure check was performed | **PARTIAL** |
| 15 | CI/CD | `.github/workflows/ci.yml` exists; CI was not executed in T0 | **PRESENT; NOT EXECUTED** |

### Phase 2 — Auth and Accounts: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 2 | Login query-string credentials | Source reads a safe callback URL; browser/history/referrer behavior was not tested in T0 | **NOT LIVE-VERIFIED** |
| 6 | Refresh flow | Handler tests cover refresh-cookie behavior; no live browser/API journey was run | **PARTIAL** |
| 7 | Email verification | Routes exist; integration logs reported no Resend API key and no verification/reset delivery was confirmed | **OPEN** |

### Phase 3 — Audience: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 12 | Contact import | Multipart upload and worker loop exist; API queue/progress tenant-scope test passed; worker integration test fails its row-number assertion; no UI; data is stored in PostgreSQL `bytea`, not MinIO | **PARTIAL** |

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
| Campaign preview | `apps/web/app/(dashboard)/campaigns/[id]/preview/page.tsx` — no verified live preview workflow | **STATIC-MOCK** |
