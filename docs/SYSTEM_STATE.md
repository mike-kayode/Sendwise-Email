# System State Audit — Sendwise

**Status:** T0 COMPLETE (evidence below); production readiness NOT claimed
**Date:** 2026-11-04 (T0 run)
**Repo:** /Users/iam_khayode/email-platform
**Server:** 72.61.144.73 (/opt/email-platform), https://campaign.pipsleague.com (not inspected in T0)

Status vocabulary used below:

- **VERIFIED** — exercised live in the T0 run with the cited result.
- **BROKEN** — exercised live and failed; defect recorded in section E.
- **PARTIAL** — some paths verified, specific gaps named.
- **SOURCE-ONLY** — present in source, not exercised live.
- **STATIC-MOCK** — page renders but has no data wiring (hardcoded content).

## T0 verification record (2026-11-04)

### Environment

Local Docker Desktop (Docker 29.9.2, Compose v5.6.1) on macOS. Everything below ran
against the checked-in compose files plus the T0 fixes listed here (all committed).
Secrets were generated locally into the gitignored `.env`; localhost overrides live in
the gitignored `.env.local`. No secret is printed or committed.

### T0 fixes required to make the checked-in stack start

| # | Problem (from prior audit) | Fix |
|---|---------------------------|-----|
| 1 | `internal/api/contact_imports.go` (uncommitted) declared `package main` inside package `api`; tree did not build | Restored to `package api` with valid imports; `go build ./...` passes |
| 2 | `infrastructure/postgres/init-databases.sh` not executable → container init exited 126 (`bad interpreter: Permission denied`) | `chmod +x`; git mode now 100755 |
| 3 | `docker-compose.microservices.yml` postgres service did not receive `IDENTITY_DB_*`/`WORKSPACE_DB_*` → init script failed under `set -eu` | Env vars added to the postgres service |
| 4 | Gateway service omitted `LEGACY_API_URL` (and `SERVICE_TOKEN`) | `LEGACY_API_URL` default `http://api:8080` added; `SERVICE_TOKEN` passed through |
| 5 | `minio/minio` image pull denied in this environment (repository unavailable), blocking `up` | MinIO moved behind Compose profile `storage` in both files; default stack starts without it. MinIO remains unused by code |
| 6 | Identity verification links were unrecoverable in local runs (hashed tokens; Stalwart in bootstrap mode refuses SMTP on 587) | `LogMailer` now logs the message link (as its doc comment already claimed); local `.env.local` sets `MAIL_PROVIDER=log`. This is a development-only path and must not be used in production |

### How the two compose files combine (documented stack)

Both files declare project name `email-platform`. **Neither file alone is the full
stack**; the canonical invocation merges them:

```sh
docker compose --env-file .env --env-file .env.local \
  -f docker-compose.yml -f docker-compose.microservices.yml up -d
```

| Invocation | Services | Serves |
|---|---|---|
| `-f docker-compose.yml` only | postgres, redis, nats, stalwart, minio(profile), migrate, api, worker, web, caddy, observability(profile) | **Broken for `/api/*`**: Caddyfile hard-routes `/api/*` to `gateway:8080`, which does not exist in this file |
| `-f docker-compose.microservices.yml` only | postgres, redis, nats, stalwart, minio(profile), identity(-db-migrate), workspace(-db-migrate), gateway, web, caddy | Auth/org/invitation routes only; all other `/api/*` proxy to a nonexistent `api` host |
| **both files (canonical)** | all of the above | Full stack — this is what T0 verified |

`--env-file .env --env-file .env.local` merges files (later keys win). `.env` holds
secrets and shared settings; `.env.local` holds localhost-only overrides
(`APP_ENV=development`, `SITE_ADDRESS=:80`, `APP_BASE_URL`, `CORS_ORIGINS`,
`API_BASE_URL`, `NEXT_PUBLIC_API_URL`, `MAIL_PROVIDER=log`).

### Stack start results (canonical invocation)

- `postgres`, `redis` healthy; `nats`, `stalwart`, `caddy`, `web` up.
- Migration containers `migrate`, `identity-db-migrate`, `workspace-db-migrate` all **exited 0** on a fresh volume.
- `api`, `identity`, `workspace`, `gateway`: `/healthz` and `/readyz` **HTTP 200** (direct ports 8080/8082/8083/8084).
- `worker` metrics `:9091/healthz` **200**; `:9091/readyz` **404** (no readiness route).
- `web` `:3000` **200**; Caddy `:80` **200** and proxies `/api/*` → gateway, `/t/*` → legacy api (verified by live calls through port 80).

### Test results (T0 run, this tree)

| Suite | Command | Result |
|---|---|---|
| Go unit tests | `go test ./...` | **PASS** (all packages) |
| Go vet | `go vet ./...` | **PASS** |
| Contact import API (DB) | `TEST_DATABASE_URL=… go test ./internal/api/ -run ContactImport` | **PASS** (`TestContactImportAPIQueuesAndScopesProgress`) |
| Contact import worker (DB) | `TEST_DATABASE_URL=… go test ./internal/worker/ -run ContactImport` | **FAIL** — `TestContactImportWorkerPersistsPendingContactsAndResults`: reports invalid row 3, test expects 4 |
| Legacy API integration group | `INTEGRATION_DATABASE_URL=… go test ./internal/api/ -run Integration` (fresh DB) | **FAIL (6/6)** — root cause: tests expect `POST /api/v1/auth/register` to return `access_token`/`user`/`organization`; the handler returns only `{message, email}` (verified+login is the working journey). `TestAPIKeyLifecycleIntegration` would additionally hit the api-key panic (defect D1) |
| Frontend typecheck | `cd apps/web && npm run typecheck` | **PASS** |
| Frontend unit tests | `cd apps/web && npm test` | **FAIL (3 pass / 1 fail, exit 1)** — `tests/auth-utils.test.mjs` expects “Cannot reach the server. Please try again.” but code returns “…Please check your connection and try again.” |
| Frontend production build | image build `web` via compose | **PASS** (Next.js production build inside Docker) |
| Compose config | `docker compose -f docker-compose.yml -f docker-compose.microservices.yml config -q` | **PASS** |

No skipped or unavailable check is counted as passing.

### Authenticated journeys exercised through Caddy (:80) → Gateway

| Journey | Result |
|---|---|
| Register (identity) → mail link → `POST /auth/verify` → login → `GET /auth/me` | **VERIFIED**: 201 → 200 → 200 (access token + `sendwise_refresh` cookie) → 200 with user + organization |
| Login before verification | **VERIFIED**: 403 `email_not_verified` |
| Refresh (rotate cookie) → replay old cookie → logout → refresh after logout | **VERIFIED**: 200 (new access + rotated cookie) → 401 reuse detection → 200 `revoked:true` → 401 |
| Forgot-password → reset → login with new password → reuse old reset token | **VERIFIED**: 200 → 200 → 200 (access issued) → 400 `invalid_token` |
| Workspace: list/get/patch org, list members | **VERIFIED**: all 200; PATCH rename persisted |
| Invitations: create → list → second user accepts → revoke third invite → revoked accept rejected | **VERIFIED**: 201 → 200 → 200 (role manager) → 204 → 400 `invalid_invitation` |
| Workspace internal endpoints with/without `X-Service-Token` | **VERIFIED**: 200 / 200 / **401** with wrong token |
| Identity `resend-verification` | **VERIFIED**: 200 (link emitted by configured mailer) |

### Legacy API routes exercised with the identity-issued access token (claims are cross-compatible)

| Area | Result |
|---|---|
| Contacts create (pending), create subscribed w/ consent, list, get, patch, export CSV, delete | **VERIFIED**: 201/201/200/200/200/200 (CSV bytes) /204 |
| Contact import `POST /contacts/import` (multipart CSV, 3 rows incl. 1 bad) | **VERIFIED**: 202 → worker processed → `GET /contact-imports/{id}` `completed`: `total_rows 3, imported 2, invalid 1, errors:[{row:3}]` |
| Lists / Segments / Templates create+list (+delete for lists/segments) | **VERIFIED**: 201/200/204. Templates: create+list only (no update/delete routes exist) |
| Tags create/list | **VERIFIED**: 201/200 |
| `DELETE /api/v1/tags/{id}` | **BROKEN**: 404 `page not found` (route not registered) |
| Domains create/list/dns-records/verify | **VERIFIED**: 201/200/200/200; `dns-records` still contains static placeholder DKIM value; `verify` reports `missing` for the fake domain (DNS check runs) |
| Campaigns create/get/list/patch/validate/schedule/pause/send/cancel/delete | **VERIFIED**: 201/200/200/200/200 (validation checks returned)/200/200/**409 `campaign_paused` (correct state guard)**/200/204 |
| Campaign `resume` while domain unverified | **BROKEN**: 500 `internal_error` (queue error “sending domain is not verified” is not mapped; defect D2) |
| Campaign create with unknown `list_id` | **BROKEN**: 500 FK violation instead of 400 (defect D3) |
| Suppressions create/list/delete | **VERIFIED**: 204/200/204 |
| `GET /analytics/overview` | **VERIFIED**: 200 with live counts |
| `GET /analytics/campaigns/{real-id}` / `{invalid-uuid}` | **VERIFIED** 200 / **BROKEN** 500 (defect D4) |
| API keys: list / create | list **VERIFIED** 200; create **BROKEN** — panic `randomHex` `slice bounds out of range [:64] with length 43`, recovered to 500 (defect D1) |
| Dead-letter list / replay unknown id | **VERIFIED** 200 / 404 `not_found` (mapping works for missing id; replay of a real DLQ row untested — no DLQ rows) |
| `POST /public/subscribe` | **VERIFIED**: 200 `subscribed` |
| `GET /public/unsubscribe?token=…` (valid token) | **BROKEN — mutates**: contact `subscribed` → GET → `unsubscribed` (link-scanner simulation; defect D9) |
| Tracking `/t/open`, `/t/click` invalid token | dispatch **VERIFIED** 400; valid-token tracking untested (needs a real send) |
| Webhooks `POST /webhooks/bounce`, `/complaint` (no credentials) | **BROKEN**: both **204**; the forged bounce created a suppression row (`source=bounce_webhook`) visible via authenticated `GET /suppressions` (defect D10) |
| Legacy auth direct (`:8080`, not gateway-reachable): register → login → verify (raw token from DB) → login → forgot/reset (active user) → reuse | **VERIFIED**: 201 → 403 `account_inactive` → 200 → 200 (access) → 200 → 200 → login 200 → reuse 404 `invalid_token`. Email send fails (`resend API key not configured`) — no delivery (defect D12) |
| Service-local `/healthz`, `/readyz`, `/metrics` (direct) | **VERIFIED** 200; via gateway 404 (gateway does not proxy them — expected) |

### Gateway readiness baseline (for T1)

With the Legacy API container **stopped**, gateway `GET /readyz` still returned
**200 `{"status":"ready"}`** (direct api port refused connections). Legacy
reachability is not a readiness gate yet — T1 must add it and re-run this check.

### Mail delivery baseline (for T5)

- Stalwart runs in **bootstrap mode**: only `:8080` (setup UI) is listening; SMTP
  `:587` refuses connections. Identity (smtp provider) logged
  `smtp dial … connection refused`; register returned **503
  `verification_email_unavailable`** until `MAIL_PROVIDER=log` was set locally.
- Legacy API uses Resend directly; without `RESEND_API_KEY` it logs
  `resend API key not configured` and does not deliver (register still returns 201;
  forgot-password still returns 200).
- System from-address was `noreply@pipsleague.com` (in use elsewhere); changed to
  **`hello@pipsleague.com`** in `.env`, compose defaults, and `.env.example`, and
  confirmed live inside the restarted `api`/`identity`/`worker` containers.
- `List-Unsubscribe` + one-click `List-Unsubscribe-Post` headers already exist in
  `internal/worker/worker.go` (source only; not observed in a delivered message).

### auth_audit (for T1 migration)

Fresh identity schema has `metadata jsonb NOT NULL DEFAULT '{}'::jsonb` and the
writer always marshals a non-nil object (never relies on the default). Live T0 runs
wrote **21 `auth_audit` rows** (register/login/verify/reset/refresh/reuse-detection),
so writes work against the fresh schema. A forward, idempotent
`ALTER … SET DEFAULT`/backfill migration for databases created before the default
existed is still owed (T1).

## A. Architecture Map

### Service inventory

| Service | Code location | Port (container) | Port (host) | Database | Responsibilities |
|---------|--------------|------------------|-------------|----------|-----------------|
| Legacy API | `cmd/api`, `internal/api/*` | 8080 | 127.0.0.1:8080 | `email` | Campaigns, contacts, audiences (lists/tags/segments), templates, sending domains, delivery jobs, suppressions, analytics, tracking (/t/*), public subscribe/unsubscribe, webhooks (bounce/complaint), API keys, legacy auth (register/login/verify/reset) |
| Legacy worker | `cmd/worker`, `internal/worker/*` | 9091 | 127.0.0.1:9091 | `email` | Delivery processing, SMTP submission, **contact import jobs (wired and verified live)**, scheduler |
| Identity | `services/identity/` | 8080 | 127.0.0.1:8082 | `identity` | User registration, login, refresh, email verification, password reset, logout, me, MFA, sessions |
| Workspace | `services/workspace/` | 8080 | 127.0.0.1:8083 | `workspace` | Organizations, members, invitations, roles, internal service lookups |
| Gateway | `services/gateway/` | 8080 | 127.0.0.1:8084 | None | Reverse proxy; /api/v1/auth/* → identity, /api/v1/organizations\|members\|invitations/* → workspace, everything else → legacy API |
| Web (Next.js) | `apps/web/` | 3000 | 127.0.0.1:3000 | None | Frontend dashboard; calls APIs via `apiFetch` |

### Infrastructure

| Service | Image | Port (host) | Notes |
|---------|-------|-------------|-------|
| PostgreSQL | postgres:16-alpine | 127.0.0.1:5432 | Databases: `email`, `identity`, `workspace`; init script creates per-service roles (T0-verified on fresh volume) |
| Redis | redis:7-alpine | 127.0.0.1:6379 | Caching, rate limiting |
| NATS JetStream | nats:2.10-alpine | 127.0.0.1:4222, 8222 | Event bus; business events stream |
| Stalwart SMTP | stalwartlabs/stalwart:latest | 127.0.0.1:25/465/587/8081 | **Bootstrap mode: only :8080 (setup UI) listens; no SMTP submission available.** No verification/reset delivery established |
| MinIO | minio/minio:latest | (profile `storage`) | Object storage (unused by code; image pull denied in T0 environment, so profiled out of the default stack) |
| Caddy | caddy:2-alpine | 80, 443 | Edge proxy; `/api/*` → Gateway, `/t/*` → Legacy API, other paths → Web. T0 verified on `SITE_ADDRESS=:80` (`.env.local`); `.env` ships `SITE_ADDRESS=campaign.pipsleague.com` for the server |

### Inter-service communication (T0-verified where noted)

- **Gateway → Identity / Workspace / Legacy:** verified live for all three routes classes (auth, org/member/invite, everything else).
- **Identity → Workspace:** `GET /internal/users/{userID}/memberships` etc. with `X-Service-Token` — verified live (200 with token, 401 without/wrong).
- **All → NATS JetStream:** business events published (identity.user_registered.v1 etc.); started, but publish/consume recovery not exercised in T0.
- **Legacy API → Stalwart:** connection refused (bootstrap mode) — T5.
- **Caddy → Gateway (`/api/*`) and → Legacy (`/t/*`):** verified live through port 80.
- **Caddy → Web:** verified live (all pages 200).

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

**Identity** (`services/identity/config.go`): JWT_SECRET (required), SERVICE_TOKEN (required), DATABASE_URL, NATS_URL, APP_BASE_URL, WORKSPACE_URL, MAIL_PROVIDER (auto|resend|smtp|log), SMTP_*, RESEND_*, ACCESS_TOKEN_TTL, IDENTITY_REFRESH_TOKEN_TTL, COOKIE_SECURE, AUTO_MIGRATE, MIGRATIONS_DIR

**Workspace** (`services/workspace/config.go`): JWT_SECRET (required), SERVICE_TOKEN (required), DATABASE_URL, NATS_URL, APP_BASE_URL, InvitationTTL, AUTO_MIGRATE, MIGRATIONS_DIR

**Gateway** (`services/gateway/config.go`): IDENTITY_URL, WORKSPACE_URL, LEGACY_API_URL (all three required when `APP_ENV=production`), CORS_ORIGINS, AuthRatePerMinute, AuthBurst. `SERVICE_TOKEN` is passed by compose but **not validated by gateway config** (T1).

**Legacy API** (`cmd/api/main.go`): DATABASE_URL, NATS_URL, REDIS_URL, JWT_SECRET, JWT_ISSUER, APP_BASE_URL, API_BASE_URL, TRACKING_BASE_URL, SMTP_*, CORS_ORIGINS, REQUIRE_VERIFIED_DOMAIN, MAX_DELIVERY_ATTEMPTS, AUTO_MIGRATE, MIGRATIONS_DIR

## B. Route Inventory

### Identity service (`services/identity/handlers.go`) — via gateway `/api/v1/auth/*`

| Path | Method | Auth | Status | Notes |
|------|--------|------|--------|-------|
| /auth/register | POST | rate-limited | **VERIFIED** | 201 `{message,email}`; account + workspace created; verification mail required before login |
| /auth/login | POST | rate-limited | **VERIFIED** | 403 `email_not_verified` pre-verify; 200 with access token + `sendwise_refresh` cookie post-verify |
| /auth/refresh | POST | refresh cookie | **VERIFIED** | Rotates cookie; old-cookie replay → 401 (reuse detection kills session family) |
| /auth/verify | POST | rate-limited | **VERIFIED** | 200 with valid token (link recovered from log mailer locally) |
| /auth/resend-verification | POST | rate-limited | **VERIFIED** | 200 |
| /auth/forgot-password | POST | rate-limited | **VERIFIED** | 200 (enumeration-safe message); link emitted by mailer |
| /auth/reset-password | POST | rate-limited | **VERIFIED** | 200; new password logs in; token reuse → 400 |
| /auth/logout | POST | refresh cookie | **VERIFIED** | `revoked:true`; subsequent refresh 401 |
| /auth/me | GET | JWT | **VERIFIED** | 200 user + organization |
| /healthz, /readyz | GET | none | **VERIFIED** | 200 direct |

### Workspace service (`services/workspace/handlers.go`)

| Path | Method | Auth | Status | Notes |
|------|--------|------|--------|-------|
| /internal/users/{userID}/memberships | GET | SERVICE_TOKEN | **VERIFIED** | 200 with token; 401 with wrong token |
| /internal/organizations/{organizationID} | GET | SERVICE_TOKEN | **VERIFIED** | 200 with token |
| /organizations | GET | JWT | **VERIFIED** | 200 (multi-membership list included) |
| /organizations/{organizationID} | GET | JWT | **VERIFIED** | 200 |
| /organizations/{organizationID} | PATCH | JWT + owner/admin | **VERIFIED** | 200 rename persisted |
| /members | GET | JWT | **VERIFIED** | 200 |
| /invitations | GET | JWT | **VERIFIED** | 200 |
| /invitations | POST | JWT + owner/admin | **VERIFIED** | 201 with `accept_url` |
| /invitations/{invitationID} | DELETE | JWT + owner/admin | **VERIFIED** | 204; later accept → 400 |
| /invitations/accept | POST | JWT | **VERIFIED** | 200 joins organization as invited role |
| /healthz, /readyz | GET | none | **VERIFIED** | 200 direct |

### Legacy API (`internal/api/server.go`) — via gateway unless noted

| Path | Method | Auth | Status | Notes |
|------|--------|------|--------|-------|
| /api/v1/auth/register, login, verify(GET), forgot-password, reset-password, me | — | — | **VERIFIED (direct :8080 only)** | Shadowed at the gateway by Identity; reachable only on the loopback-bound legacy port. Full journeys pass; mail does not deliver (D12) |
| /api/v1/contacts | GET/POST | JWT | **VERIFIED** | CRUD + consent evidence enforced for `subscribed` |
| /api/v1/contacts/import | POST | JWT | **VERIFIED (backend)** | Multipart CSV accepted (16 MiB limit), queued, **worker processed it live** (2 imported / 1 invalid). **No UI** (contacts page Import button is a toast stub) |
| /api/v1/contact-imports/{id} | GET | JWT | **VERIFIED** | Progress/counters/error rows returned |
| /api/v1/contacts/export | GET | JWT | **VERIFIED** | CSV bytes returned (UI export button is a toast stub) |
| /api/v1/contacts/{id} | GET/PATCH/DELETE | JWT | **VERIFIED** | 200/200/204 |
| /api/v1/lists | GET/POST | JWT | **VERIFIED** | |
| /api/v1/lists/{id} | DELETE | JWT | **VERIFIED** | 204 |
| /api/v1/tags | GET/POST | JWT | **VERIFIED** | |
| /api/v1/tags/{id} | DELETE | JWT | **BROKEN** | 404; no route registered (D11) |
| /api/v1/segments | GET/POST | JWT | **VERIFIED** | Rules compile validated |
| /api/v1/segments/{id} | DELETE | JWT | **VERIFIED** | 204 |
| /api/v1/templates | GET/POST | JWT | **VERIFIED** | No update/delete routes exist |
| /api/v1/campaigns | GET/POST | JWT | **PARTIAL** | 201 with valid FKs; unknown `list_id` → 500 (D3) |
| /api/v1/campaigns/{id} | GET/PATCH/DELETE | JWT | **VERIFIED** | |
| /api/v1/campaigns/{id}/validate | POST | JWT | **VERIFIED** | Returned per-check results |
| /api/v1/campaigns/{id}/schedule | POST | JWT | **VERIFIED** | Future timestamp accepted |
| /api/v1/campaigns/{id}/send | POST | JWT | **VERIFIED** | 409 `campaign_paused` (state machine enforced); actual delivery blocked by unverified domain |
| /api/v1/campaigns/{id}/pause, cancel | POST | JWT | **VERIFIED** | 200 |
| /api/v1/campaigns/{id}/resume | POST | JWT | **BROKEN** | 500 — domain-not-verified not mapped (D2) |
| /api/v1/domains | GET/POST | JWT | **VERIFIED** | |
| /api/v1/domains/{id}/verify | POST | JWT | **VERIFIED** | DNS check runs; fake domain → `missing` |
| /api/v1/domains/{id}/dns-records | GET | JWT | **VERIFIED** | Static placeholder DKIM value remains (T5) |
| /api/v1/domains/{id}/dkim-key | POST | JWT | **VERIFIED** | 400 without a key (validation correct) |
| /api/v1/delivery/dead-letter | GET | JWT | **VERIFIED** | Empty list 200 |
| /api/v1/delivery/dead-letter/{id}/replay | POST | JWT | **PARTIAL** | Unknown id → 404 `not_found` (correct); replay of a real row untested (no DLQ rows) |
| /api/v1/suppressions | GET/POST | JWT | **VERIFIED** | |
| /api/v1/suppressions/{email} | DELETE | JWT | **VERIFIED** | 204 |
| /api/v1/webhooks/bounce | POST | none | **BROKEN** | Unauthenticated; forged event accepted (204) and created a suppression (D10) |
| /api/v1/webhooks/complaint | POST | none | **BROKEN** | Unauthenticated; 204 (D10) |
| /api/v1/analytics/overview | GET | JWT | **VERIFIED** | Live counts |
| /api/v1/analytics/campaigns/{id} | GET | JWT | **PARTIAL** | Real id 200; invalid UUID → 500 (D4) |
| /api/v1/api-keys | GET/POST | JWT + owner/admin | **BROKEN** | GET 200; POST panics in `randomHex` → 500 (D1) |
| /api/v1/api-keys/{id} | DELETE | JWT + owner/admin | SOURCE-ONLY | Not exercised (no key could be created) |
| /api/v1/public/subscribe | POST | none | **VERIFIED** | 200 `subscribed` |
| /api/v1/public/unsubscribe | GET | none | **BROKEN** | GET mutates: contact went `subscribed` → `unsubscribed` (D9) |
| /api/v1/public/unsubscribe | POST | none | PARTIAL | Handler dispatches (400 without body); valid-token POST not exercised |
| /t/open/{token}, /t/click/{token} | GET | none | **PARTIAL** | Invalid token → 400 (dispatch verified); valid-token behavior needs a real send (T6) |
| /healthz, /readyz, /metrics | GET | none | **VERIFIED** | 200 direct; not proxied by gateway (404) |

**Web** (`apps/web/`): `NEXT_PUBLIC_API_URL` (build-time + runtime). T0 used
`http://localhost` via `.env.local`; `.env` ships
`https://campaign.pipsleague.com` for the server.

### Frontend pages (`apps/web/app/`) — all fetched live through Caddy

| Page | Route | Status | T0 evidence |
|------|-------|--------|-------------|
| Login / Register / Verify / Reset / Forgot | /login, /register, /auth/verify, /auth/reset-password, /forgot-password | **VERIFIED (render)** | All 200; API journeys verified server-side (browser e2e is T6) |
| Dashboard | /(dashboard)/ | PARTIAL | 200; real API calls in source |
| Contacts | /(dashboard)/audience/contacts | **PARTIAL (stubs)** | 200; Import/Export/Add buttons are toast stubs (“coming in Phase 2”) despite working backend |
| Lists / Segments / Tags | …/audience/* | PARTIAL | 200; source shows create/delete wiring |
| Campaigns, new, detail, report | /campaigns… | PARTIAL | 200 with real campaign id; report page 200 |
| Campaign editor / preview | /campaigns/[id]/editor, /preview | **STATIC-MOCK** | 200; **zero** `fetch`/`apiFetch` calls in source; hardcoded `templateBlocks` |
| Templates / Domains | /templates, /domains | PARTIAL | 200 |
| Forms | /forms | **STATIC-MOCK** | 200; zero fetches; hardcoded `forms` array |
| Automations (+detail) | /automations | **STATIC-MOCK** | 200; zero fetches; `fallbackAutomations` |
| Reports | /reports | PARTIAL | 200 |
| Delivery DLQ / suppressions | /delivery/* | PARTIAL | 200 |
| Settings | /settings | **STATIC-MOCK** | 200; zero fetches; hardcoded team (“Tola Adeyemi”, …) present in HTML |
| Billing | /billing | **STATIC-MOCK** | 200; zero fetches; “Visa ending in 4242” present in HTML |

## C. Backend Inventory

### Handlers with corresponding routes

**Legacy API:** register, login, verify, forgot-password, reset-password, me, contacts CRUD,
contact import (+ worker pipeline, verified live), lists, tags (no delete route), segments,
templates (create/list only), campaigns (full lifecycle; resume error-mapping broken),
domains, dead-letter, suppressions, analytics, api-keys (create panics), public
subscribe/unsubscribe (GET mutates), bounce/complaint webhooks (unauthenticated),
tracking open/click.

**Identity:** register, login, refresh, verify, resend-verification, forgot-password,
reset-password, logout, me — all journeys verified live.

**Workspace:** internal memberships/org lookups (service-token verified), organizations
CRUD, members, invitations CRUD/accept — verified live.

**Gateway:** proxy only; all three route classes verified. `/readyz` does not check
upstreams (T1).

### Tables with no code

From `migrations/0001_init.sql`: `webhooks`, `usage_records`, `audit_logs`,
`automations`, `automation_executions`, `automation_steps` — schema only, no API handlers.

### Unused code

- `demo-data.ts` — not imported by any page (dead code with hardcoded demo data).
- Legacy auth handlers (`/api/v1/auth/*`) — duplicated by Identity; kept as a rollback
  path, not gateway-reachable.
- MinIO — running profile exists; no code references object storage.

## D. Data Model and Tenant Isolation

### Schema (`migrations/0001_init.sql`)

All tenant-owned tables carry `organization_id` (see prior audit list; unchanged in T0).

### Tenant isolation enforcement

- **Application layer:** handlers scope queries by `s.organizationID(r)` from JWT
  claims; identity/workspace tokens are claim-compatible with legacy validation
  (verified live: same token works across services).
- **Database layer:** no RLS; no composite tenant-aware foreign keys.
- **Known gaps (unchanged):** no RLS; API keys do not authenticate requests (and
  creation currently panics — D1); webhooks accept unauthenticated cross-org writes
  (D10 — a forged bounce for an arbitrary email created a suppression row).

## E. Verified gaps and status

| # | Finding | T0 evidence | Status |
|---|---------|-------------|--------|
| 1 | Working-tree build | `go build ./...`, `go vet ./...`, `go test ./...` all pass after the package-mismatch fix | **RESOLVED** |
| 2 | Combined Compose startup | Canonical two-file invocation starts the full stack on a fresh volume; all three migration containers exit 0; health/ready 200 across services | **RESOLVED (with T0 fixes listed above)** |
| 3 | Gateway configuration/readiness | Compose now supplies `LEGACY_API_URL`; production fail-fast covers the three URLs (source). `/readyz` still 200 with Legacy API stopped (re-tested in T0); gateway does not validate `SERVICE_TOKEN` | **OPEN (T1)** |
| 4 | `auth_audit.metadata` upgrade safety | Fresh schema has default; writer never passes nil; 21 audit rows written live. No forward migration for pre-default databases | **OPEN (T1 migration)** |
| 5 | Tag deletion route | `DELETE /api/v1/tags/{id}` → 404 live (no registration) | **BROKEN (D11)** |
| 6 | Contact import | API queue + **worker completion verified live** (2 imported, 1 invalid, error row reported); API tenant-scope test passes; worker integration test FAILS row assertion (3 vs 4); **no UI**; stored in PostgreSQL `bytea`, MinIO unused | **PARTIAL → T2** |
| 7 | Bounce/complaint webhooks | Forged unauthenticated bounce → 204 + suppression row created (live) | **BROKEN → T3 (D10)** |
| 8 | GET unsubscribe | Valid-token GET flipped contact `subscribed` → `unsubscribed` (live) | **BROKEN (D9)** |
| 9 | Verification/reset email | Identity: SMTP refused (Stalwart bootstrap), 503 until log mailer; Legacy: `resend API key not configured`. Journeys verified only via log mailer / DB-read tokens | **OPEN → T5 (D12)** |
| 10 | Customer-facing static pages | Settings/billing/editor/preview/automations/forms: zero fetch calls, hardcoded data confirmed in HTML/source | **STATIC-MOCK → T4** |
| 11 | Tenant isolation/RLS | No `CREATE POLICY` in any migration; app-level scoping verified only for the journeys run | **OPEN** |
| 12 | CI | `.github/workflows/ci.yml` exists but was not executed in T0 | **PRESENT; NOT EXECUTED** |
| 13 | Production deployment parity | No production server inspection in T0 | **UNVERIFIED** |
| **D1** | API key creation panics | `POST /api/v1/api-keys` → panic `randomHex`: `base64…[:64]` on a 43-char string → 500 (stack in api logs) | **BROKEN (new)** |
| **D2** | Campaign resume error mapping | `resume` with unverified domain → 500 `internal_error` instead of 409/422; log: `sending domain is not verified` | **BROKEN (new)** |
| **D3** | Campaign FK validation | `POST /campaigns` with unknown `list_id` → 500 FK violation instead of 400 | **BROKEN (new)** |
| **D4** | Analytics invalid id | `GET /analytics/campaigns/not-a-uuid` → 500 instead of 400/404 | **BROKEN (new)** |
| **D5** | Frontend unit test failure | `npm test` exit 1: 1/4 failing (stale network-error message expectation) | **BROKEN (new)** |
| **D6** | API integration group | 6/6 FAIL on fresh DB: tests expect register to return a session; handler returns `{message,email}` (plus D1 for the api-key test) | **BROKEN (new, test-vs-handler contract)** |
| **D7** | Worker import row assertion | Test expects invalid row 4, worker reports 3 (header-excluded indexing); live import agrees with 3 | **BROKEN (new, test-vs-code contract)** |
| **D8** | Gateway readiness | `/readyz` 200 with Legacy API stopped (re-verified in T0) | **OPEN (T1)** |
| **D12** | Mail delivery | Stalwart bootstrap (no SMTP); legacy Resend key absent; from-address corrected to `hello@pipsleague.com` | **OPEN → T5** |
| **D13** | Base-only compose cannot serve `/api/*` | Caddyfile hard-routes `/api/*` to `gateway:8080`; that service exists only in the microservices file | **DOCUMENTED (use combined invocation)** |

T0 is signed off as a documentation-and-evidence task only. The defects above are
open work; no phase beyond T0 is claimed complete.

## F. Mock/Hardcoded Data in Customer-Facing Screens

| Location | What | Status |
|----------|------|--------|
| `apps/web/lib/demo-data.ts` | demo workspace/stats/campaigns/invoices | **NOT IMPORTED** by any page (dead code) |
| Settings page | hardcoded team members | **STATIC-MOCK** (live-confirmed) |
| Billing page | hardcoded invoices/usage/plans, “Visa ending in 4242” | **STATIC-MOCK** (live-confirmed) |
| Automations page | `fallbackAutomations` | **STATIC-MOCK** (live-confirmed) |
| Forms page | hardcoded `forms` | **STATIC-MOCK** (live-confirmed) |
| Campaign editor/preview | `templateBlocks`, hardcoded campaigns | **STATIC-MOCK** (live-confirmed) |
| `internal/api/domains.go` `dnsRecordsFor()` | static DKIM placeholder value | PARTIAL (real DB status, static values) |

## Phase status (evidence-based, T0)

### Phase 1 — Foundation: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 1 | `NEXT_PUBLIC_API_URL` defaults | Local build used `http://localhost` via `.env.local`; server value in `.env` is `https://campaign.pipsleague.com`; browser DevTools not checked | **NOT LIVE-VERIFIED** |
| 2 | Login query-string credentials | Source cleanup exists; browser/history behavior not tested (T6) | **NOT LIVE-VERIFIED** |
| 3 | Gateway production configuration | Three upstream URLs fail fast in production (source); compose now supplies them; `SERVICE_TOKEN` not validated by gateway | **PARTIAL (T1)** |
| 4 | `auth_audit.metadata` | Writes verified live (21 rows); forward migration for old schemas still owed | **PARTIAL (T1)** |
| 5 | Identity/workspace migration paths | All three migration containers exit 0 on fresh volume (canonical stack) | **VERIFIED (fresh install)** |
| 8 | Caddy routes `/api/*` to Gateway | Live calls through `:80` hit gateway-backed routes; `/t/*` hits legacy | **VERIFIED (local, SITE_ADDRESS=:80)** |
| 9 | Server code parity | Not inspected | **UNVERIFIED** |
| 10 | Public ports | Compose binds services to loopback; Caddy `:80/:443` all interfaces | **SOURCE-ONLY** |
| 11 | Secret handling | `.env`/`.env.local` gitignored; no committed secrets; no automated secret scan run | **PARTIAL** |
| 15 | CI/CD | Workflow present; not executed | **PRESENT; NOT EXECUTED** |

### Phase 2 — Auth and Accounts: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 2 | Login query-string credentials | Source only | **NOT LIVE-VERIFIED** |
| 6 | Refresh flow | **Live-verified**: rotation, reuse detection, logout revocation | **VERIFIED (API)**; browser flow is T6 |
| 7 | Email verification | Journeys verified via log mailer; real delivery blocked (D12) | **OPEN (T5)** |

### Phase 3 — Audience: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 12 | Contact import | Backend + worker verified live; UI missing; bytea storage; worker test row assertion fails | **PARTIAL → T2** |

### Phase 4 — Sending: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 20 | No email editor | Editor page has zero fetches + hardcoded blocks | **STATIC-MOCK → T4** |

### Phase 5 — Tracking: PARTIAL

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 17 | Unauthenticated webhooks | Live forged bounce accepted + suppression written | **BROKEN → T3** |
| 18 | GET unsubscribe mutates | Live status flip confirmed | **BROKEN** |
| 19 | API key scopes unenforced | No API-key auth middleware; key creation panics (D1) | **BROKEN** |

### Phase 6 — Production: NOT STARTED

| # | Gap | Evidence | Status |
|---|-----|----------|--------|
| 14 | No RLS | No `CREATE POLICY` in migrations | **OPEN** |
| 16 | No billing service | No `services/billing/`; page hardcoded | **OPEN → T4** |
| 17 | No automation service | No `services/automation/`; page hardcoded | **OPEN → T4** |
| 18–21 | Tracking/bounce/domain/delivery services | Handled by legacy API/worker (webhooks unauthenticated) | **OPEN** |

## Next steps (in approved order)

- **T1** — Deploy hygiene: gateway `/readyz` upstream gates + `SERVICE_TOKEN`
  validation, fail-fast verification (runtime), unified/documented stack entrypoint
  (`deploy.sh` must cover the combined stack — it currently only builds
  identity/workspace/gateway), `.env.example` completeness, auth_audit forward
  migration, fix deploy/rollback/smoke-test flow.
- **T2** — Contact import end to end (UI, storage decision, mapping, dedupe,
  consent/suppression, async progress, error report, 10k-row test, fix D7).
- **T3** — Webhook security (HMAC/replay/idempotency; fix D10).
- **T4** — Settings real via workspace service; editor/ forms/ automations/ billing
  either minimal-real or removed with “coming soon” (fix D5 while there).
- **T5** — Mail delivery (provider abstraction, working verification/reset,
  sending-domain DNS display/verification, test send, List-Unsubscribe observation).
- **T6** — Playwright e2e (register → verify → login → refresh → import → campaign →
  test send → tracking).
- Deferred/decide: D1–D4 (route defects) and D6 (integration test contract) need a
  home — proposed as a T1b fix batch or folded into the task where they surface.
