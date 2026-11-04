# System Architecture and Development Audit

> **Follow-up status:** This is the source-backed audit snapshot. Subsequent source changes repaired the documented Compose port conflict, Next.js API URL build-time wiring, Go Docker toolchain mismatch, and Caddy hostname configuration. A shared transactional outbox now carries the first registration event; this does not establish service-owned databases or deployed consumers. These changes have not been verified by building/running Docker or applying migrations to PostgreSQL; see [DEVELOPMENT_PROGRESS.md](./DEVELOPMENT_PROGRESS.md) and [PRODUCTION_READINESS.md](./PRODUCTION_READINESS.md) for current gates.

> **Latest follow-up:** Identity, workspace, and gateway service implementations and a separate microservices Compose stack now exist. Identity refresh tokens are delivered through an HttpOnly cookie and the web client refreshes expired access tokens; Go handler tests and web typecheck pass. Compose YAML and database-init shell syntax pass, but Docker, PostgreSQL, JetStream, and browser-to-API execution remain unavailable. See the [verification report](./quality/verification-report.md). This does not change the NOT READY production assessment.

**Assessment date:** 2026-11-03  
**Scope:** Repository source, migrations, frontend, Docker/operations configuration, and available tests.  
**Method:** Read-only code inspection plus Go tests and frontend typecheck/build. This report does not certify a live deployment.

**Follow-up status (2026-11-03):** The duplicate import-table migration and verification-token trigger defects noted below have since been repaired in migration source (`0002`, `0005`, `0006`). PostgreSQL execution remains pending, so these changes are not marked verified.

## 1. Executive Summary

Sendwise is a single-repository MVP with a substantial Go backend for contacts, campaigns, tenant-scoped API operations, delivery jobs, SMTP submission, and tracking. PostgreSQL is the intended source of truth; NATS JetStream distributes durable delivery work; a separate Go worker performs scheduling and SMTP submission. The Next.js dashboard contains both API-connected pages and many static prototypes.

The implementation is **not production-ready**. Immediate blockers and high-impact findings:

1. **Migration-chain blocker (source repair pending live validation).** At audit time `0001_init.sql` and `0002_contact_imports.sql` both created `contact_imports`, blocking fresh startup. Migration 0002 is now additive/idempotent and migration 0006 is a forward repair; no PostgreSQL execution has been performed yet.
2. **The Compose stack maps Stalwart and the API to the same host port (8080)**, so both cannot bind the declared host port on one Docker host.
3. **Account verification was not end-to-end connected at the original audit.** The frontend verification route and API route now exist, and identity mail is configurable through Resend or SMTP. Delivery/activation against PostgreSQL and a real provider remain unverified.
4. **Inbound bounce/complaint endpoints accept unauthenticated caller-supplied events.** Forged events can poison suppressions; the bounce handler also associates the wrong submitted field with a message ID.
5. **The dead-letter replay path appears to publish the dead-letter row ID as a delivery-job ID**, while those are separate identifiers; publish/update errors are not reliably surfaced.
6. **Delivery submission is not fully idempotent under concurrency or uncertain SMTP outcomes.** A database status guard occurs after SMTP submission, leaving a duplicate-send window.
7. **Production TLS is not configured in the provided Caddyfile.** HTTPS automation is explicitly disabled, the site listens on port 80, and no TLS listener is configured despite port 443 being published.
8. **Database tenant isolation has no RLS or composite tenant-aware foreign keys.** Application queries generally include organization scopes, but integrity/isolation is not enforced by PostgreSQL; some cross-organization references are not validated consistently.
9. **Many dashboard controls do not call the backend.** Campaign creation/editor, reports, automation, forms, settings, and billing have significant hard-coded or inert behavior.

Validation performed:

- `CGO_ENABLED=0 go test ./...` passed using Go 1.27.3.
- An uncached API/worker test run passed its unit tests; PostgreSQL integration tests explicitly skipped because no database was available/configured.
- `npm run typecheck` passed in `apps/web`.
- `npm run build` passed in `apps/web`.
- The login page was opened in the local browser smoke test and rendered with a single form. No live API login or database-backed authentication was verified.
- Docker and a live PostgreSQL instance were unavailable; migrations, Compose startup, SMTP relay, NATS recovery, and real delivery were not executed.

The current working tree contains the prior login form/API-client correction and subsequent migration/event-contract foundation work. The migration changes are source-level repairs only; no live schema was altered or verified.

## 2. Project Overview

The repository is a monorepo for **Sendwise**, a self-hosted email-marketing platform. Its intended flow is:

1. A browser loads the Next.js app.
2. The browser calls a versioned Go REST API.
3. The API reads/writes PostgreSQL and creates campaign recipient snapshots and delivery jobs.
4. The API or worker publishes job references to NATS JetStream.
5. The worker reloads durable state from PostgreSQL, checks eligibility/suppression, renders email content, and submits via SMTP to the configured relay (intended to be Stalwart).
6. Open/click tracking and inbound bounce/complaint endpoints record events and update suppression state.

This is a **modular monolith**, not a set of independently deployed domain microservices: one API binary and one worker binary use shared Go packages and a shared PostgreSQL database. The `services/` and `shared/` folders do not represent running services.

Redis and MinIO are declared in Docker Compose but are not used by the Go runtime. The production/runtime status of PostgreSQL, NATS, Stalwart, and Caddy cannot be confirmed from repository source alone.

## 3. Technology Stack

| Layer | Actual repository technology | Evidence / qualification |
|---|---|---|
| Frontend | Next.js 16 App Router, React 19, TypeScript, Tailwind CSS | `apps/web/package.json`, `apps/web/app/` |
| Forms and validation | React Hook Form, Zod, `@hookform/resolvers` | Auth pages and dashboard forms |
| Client state/data | Zustand persistence; TanStack React Query | `apps/web/stores/auth.ts`, `apps/web/components/providers.tsx` |
| API | Go 1.26 module; Go 1.27.3 installed in inspected environment | `go.mod`; local test run |
| HTTP/router | Go `net/http` method/path mux | `internal/api/server.go` |
| Database | PostgreSQL 16 in Compose; `pgx/v5` pool and SQL | `docker-compose.yml`, `internal/db/db.go`; no ORM |
| Queue | NATS 2.10 JetStream | `internal/messaging/nats.go`; delivery source of truth remains PostgreSQL |
| Cache/rate-limit service | Redis 7 declared only | No Redis client dependency or runtime use found |
| SMTP relay | Stalwart container declared; worker uses generic Go SMTP transport | `internal/delivery/smtp.go`; no Stalwart-specific API client |
| Verification/reset email | Resend HTTP API when configured | `internal/api/auth.go`; separate from marketing delivery |
| Object storage | MinIO declared only | No runtime client or S3-compatible integration found |
| Reverse proxy | Caddy | `infrastructure/caddy/Caddyfile`; currently HTTP-only |
| Observability | `slog`, Prometheus client, Prometheus/Grafana/Loki configs | Metrics/logging exist, but no production dashboards/alerts or verified log pipeline |
| Deployment | Docker Compose and multi-stage Dockerfiles | No CI workflow or hosted deployment configuration found |

No evidence was found for a production deployment, external database, SMTP credentials, or live sender-domain setup. Secret values are intentionally not reproduced in this report.

## 4. Current Development Status

| Capability | Status | Summary |
|---|---|---|
| User login/JWT issuance | Partial | API and frontend call exist; actual DB integration was skipped. Current frontend login form is wired, but account-verification flow is broken. |
| Registration and email verification | Partial / blocked | User/org/member creation exists; verification link does not route to the registered API endpoint. |
| Organizations and memberships | Partial | One owner membership created at registration; no membership/invitation/workspace-management API. |
| Roles and route authorization | Implemented, limited | Role middleware gates several mutations; token claims are not revalidated against live membership. |
| Tenant isolation | Partial | Most API queries use organization scopes, but no RLS/composite tenant constraints and some references are not checked. |
| Contact CRUD | Implemented, with gaps | Core API and frontend listing exist. Several UI actions remain placeholders. |
| Contact import/export | Partial-to-implemented backend | Durable import jobs and CSV export exist; DB-backed integration was skipped; CSV formula injection risk exists. |
| Lists, tags, custom fields | Partial | Basic create/list/delete subset exists; important mutation and membership routes are absent. |
| Segmentation | Partial | Bounded rule compiler and campaign targeting exist; frontend builder submits empty rules and supported predicates are limited. |
| Consent/subscriptions | Partial | Consent evidence and public subscribe/unsubscribe flows exist; GET unsubscribe mutates state and public list/org consistency is incomplete. |
| Campaign lifecycle | Backend implemented; frontend partial | Backend create/validate/schedule/send/pause/resume/cancel exists; several frontend creation/editor actions are inert. |
| Templates and editor | Partial | Template list/create backend exists; dashboard is read-only/placeholder for editing and builder workflows. |
| Email rendering | Partial-to-implemented | Structured content rendering and personalization exist; full safety/compatibility testing is not demonstrated. |
| Scheduling and delivery | Partial | Scheduler, durable jobs, NATS consumer, retries, and DLQ exist, with recovery/idempotency defects. |
| Stalwart SMTP | Partial | Generic SMTP submission targets configured host; no live Stalwart integration/configuration was verified. |
| Sending domains | Partial | DNS presence checks and DKIM key storage exist; DNS checks are not a complete deliverability or alignment validation. |
| Bounce/complaint handling | Partial / unsafe | Routes and suppression updates exist but are unauthenticated and have event-association/recording defects. |
| Tracking and analytics | Partial | Open/click event capture and reports exist; event deduplication and unique-recipient metrics are missing. |
| Automations | Missing runtime | Schema and UI prototypes exist; no registered API/runtime was found. |
| Signup forms | Partial | Public subscription API exists; form management/embed UI is a prototype. |
| API keys | Partial / unusable for API auth | Create/list/revoke and key hashing exist, but API-key authentication is not wired into request middleware. |
| Webhooks | Partial | Inbound bounce/complaint handlers exist; tenant-configurable outbound webhooks are not implemented and inbound handlers lack trust verification. |
| Billing/usage | Partial schema only | Usage tables/settings exist; no billing API/provider/real frontend data. |
| Platform administration | Missing | No admin API or implemented admin surface found. |
| Audit logs | Schema only / incomplete | Table exists; no broad audit-event writing pipeline was demonstrated. |
| Monitoring | Partial | Health/metrics and configs exist; no alerting/production dashboard/restore verification. |
| Production readiness | Not ready | Migration and Compose blockers, insecure deployment defaults, and integration gaps remain. |

## 5. Feature-by-Feature Implementation Audit

1. **Authentication and authorization — partial.** Registration, verification, login, reset, `/auth/me`, JWTs, and role middleware are present in `internal/api/auth.go`, `internal/auth/`, and routes in `internal/api/server.go`. Login requires an active user. The verification link generated by registration is not wired to the API route, and role/membership changes do not revoke already-issued JWT claims. Bearer tokens are persisted in browser local storage.
2. **User registration and login — partial.** Registration transactionally creates user, organization, and owner membership; account begins pending and requires verification. The UI invokes the API. Registration verification email uses Resend; the link target `/auth/verify` differs from the only API route `/api/v1/auth/verify`, and there is no frontend verification route. Mail-send failure is logged but the response still states a verification email was sent.
3. **Organization/workspace management — partial.** Workspace creation is coupled to registration. No routes were found to update organizations, invite users, manage memberships, choose among multiple workspaces, or change roles.
4. **Multi-tenant architecture — partial.** Tokens carry organization and role claims; protected handlers generally constrain queries by organization. There are no PostgreSQL RLS policies and no composite foreign keys that guarantee linked rows share the same organization. Campaign ID relationships and public subscription organization/list combinations need stronger same-tenant validation.
5. **User roles and permissions — partial.** Database roles and route-level role checks exist. Reads generally allow any authenticated role. There is no role-management API/UI, and authorization relies on token claims until expiration.
6. **Contact management — partial.** Tenant-scoped CRUD/list/search/export APIs exist. Contact status/consent transitions have validation. Frontend contact list is connected, but Add, import/export, and profile actions are marked or behave as placeholders.
7. **Contact import/export — partial.** Imports are durable PostgreSQL jobs with mapping, status/progress, bounded errors and lease/retry fields. CSV export exists. Integration tests skip without a test database. Spreadsheet formula injection is possible if untrusted values are exported without neutralization.
8. **Audience and mailing lists — partial.** List create/list/delete and membership tables exist. List membership-management routes and UI are incomplete.
9. **Tags and custom fields — partial.** Tags can be listed/created; tag update/delete and contact-tag assignment endpoints were not found. Custom-field JSON exists, but no complete field-definition management surface was found.
10. **Contact segmentation — partial.** Segment CRUD subset and a bounded SQL compiler exist. Tag/list/event predicates and typed custom-field comparisons are limited or absent. Frontend segment “Add condition” is inert and submits an empty rule set.
11. **Consent and subscription management — partial.** Consent records and evidence validation are implemented, and public subscribe/unsubscribe APIs exist. GET unsubscribe changes state, which allows scanners to opt out users accidentally. Public subscribe does not consistently validate that a supplied list belongs to the supplied organization.
12. **Campaign creation and management — backend partial/full lifecycle; frontend prototype in parts.** Backend routes cover lifecycle operations and durable recipient/job creation. Campaign list/detail pages read real APIs; new campaign, editor, preview, and per-campaign report use hard-coded data or inert actions.
13. **Email template management — partial.** API list/create and design JSON storage exist. Update/delete and a usable editor are missing; New template is explicitly a placeholder.
14. **Drag-and-drop email builder — missing.** A static editor page is not evidence of a functional builder; save, test-send, and schedule controls are not connected to backend actions.
15. **Email rendering and personalization — partial.** Worker renders structured blocks and personalization. Full client rendering, HTML-safety, accessibility, and deliverability test coverage are not established by this audit.
16. **Campaign scheduling — partial.** API and worker scheduler paths exist, using database status/due times and NATS. Multi-worker scheduler coordination and restart behavior need integration testing.
17. **Email delivery orchestration — partial.** PostgreSQL jobs are durable and NATS distributes references. Retry, suppression, quotas, per-domain limits, and dead lettering exist in code; concurrency idempotency and recovery weaknesses remain.
18. **SMTP integration with Stalwart — partial.** Worker uses generic SMTP and STARTTLS policy; Compose declares a Stalwart service. No repository Stalwart configuration proves DKIM signing, production TLS certificates, outbound relay policy, or live delivery.
19. **Sending-domain management — partial.** DNS TXT checks and records UI exist. The checks establish record presence/prefix, not full SPF validity, DMARC policy/alignment, SMTP identity, or inbox placement. DKIM key storage does not itself configure Stalwart.
20. **SPF, DKIM, and DMARC verification — partial.** DNS lookups exist; implementation checks are limited and unverified against real sending domains.
21. **Email queue and background workers — partial.** NATS JetStream and Postgres-backed work exist. Scheduler/consumer behavior, duplicate sends, republish frequency, and DLQ replay require fixes and database-backed integration coverage.
22. **Bounce and complaint processing — partial / security risk.** Handler routes and suppression effects exist. They do not authenticate/signature-verify incoming events; bounce association uses the wrong value, and complaint handling does not fully update pending jobs/event history.
23. **Unsubscribe management — partial.** Signed tokens, footer links, RFC 8058 headers, suppression updates, and pending-job cancellation exist. GET changes state and is vulnerable to link-scanner side effects; footer lacks a configurable physical address.
24. **Open/click tracking — partial.** Tokenized URLs and pixel endpoints record events. Repeated/proxy-prefetched events are not deduplicated, so metrics can overcount.
25. **Campaign analytics/reporting — partial.** Backend endpoints are scoped and query data. Frontend overview is API-backed with hard-coded trend percentages; report pages/charts are mostly constants; counts/rates are not consistently unique recipients.
26. **Email automation/workflows — missing runtime.** Automation tables and UI demos exist, but no automation routes or execution worker were found.
27. **Subscription forms — partial.** Public subscribe endpoint exists; no form CRUD/embed backend or working form-management UI was found.
28. **API management and webhooks — partial.** API-key management exists but keys are not accepted by API authentication middleware and scopes are not enforced. Outbound tenant webhooks are not implemented. Inbound mail-event handlers are unauthenticated.
29. **Billing and usage management — missing/partial.** Usage and quota tables exist; billing pages display constants and no billing-provider integration/API was found.
30. **Platform administration — missing.** No super-admin routes or platform management experience was found.
31. **Audit logs and system monitoring — partial.** Audit table and metrics/logging infrastructure exist, but the audit table is not demonstrably populated across operations, and production dashboards, alerts, log shipping, and tested backup/restore are absent.

## 6. Database Architecture and Schema

### Connection and migration management

- **Engine:** PostgreSQL 16 image in Compose. A live database version/schema was not queried.
- **Driver:** `pgx/v5` direct SQL; no ORM or repository abstraction layer is visible.
- **Pool:** `pgxpool`, configured with max 20 connections, min 2, 30-minute lifetime, 5-minute idle timeout, and 30-second health check in `internal/db/db.go`. API and worker each create their own pool, so replicas multiply total possible connections.
- **Migrations:** `internal/db/migrate.go` creates `schema_migrations`, sorts `.sql` files lexically, runs each file in its own transaction, and records the filename. It has no rollback/down migration facility.
- **Critical migration conflict:** `migrations/0001_init.sql` creates `contact_imports`; `migrations/0002_contact_imports.sql` creates that table again without `IF NOT EXISTS`. Because migration 0001 is committed before 0002 starts, the ordinary migration runner fails at 0002 and cannot proceed to 0003-0005. This was identified by source inspection; it was not run against PostgreSQL because no database was available.
- **Additional migration defect:** `0005_verification_tokens.sql` installs `set_updated_at()` on `verification_tokens`, but that table has no `updated_at` column. The trigger function's `NEW.updated_at` assignment will fail when an update fires the trigger.
- `verification_tokens.token` is stored as plaintext and has both a UNIQUE constraint and an extra token index. High-value reset/verification token storage should be reviewed.
- Initial schema includes `contact_imports` without the lease/retry fields that the duplicate 0002 definition adds. The intended canonical definition is ambiguous until migrations are repaired.

### Table catalog

The following is the schema declared by migrations, not a claim that all migrations successfully apply. `schema_migrations` is created at runtime by the migration runner.

| Table | Primary key / important columns | Foreign keys and relationships | Index/constraint highlights | Application use/status |
|---|---|---|---|---|
| `schema_migrations` | `version` | None | PK on version | Migration runner; runtime metadata |
| `organizations` | UUID `id`; name, slug, plan, status | None | unique slug; status CHECK | Registration, tenant root |
| `users` | UUID `id`; email, password_hash, status, email_verified_at | None | unique lower(email) | Auth |
| `organization_members` | `(organization_id,user_id)` | organization -> organizations; user -> users | PK; index user_id; role CHECK | Auth, JWT organization/role |
| `api_keys` | UUID `id`; key_prefix/hash, scopes, expiry/revocation | organization -> organizations; created_by -> users | unique key_prefix; org index | Key-management APIs; auth not wired |
| `contacts` | UUID `id`; organization_id, email, status, custom_fields | organization -> organizations | unique `(organization_id, lower(email))`; status/created indexes; GIN custom_fields | Contacts, consent, campaigns |
| `custom_fields` | UUID `id`; key, label, field_type | organization -> organizations | unique `(organization_id,key)` | Schema exists; API incomplete |
| `contact_lists` | UUID `id`; organization_id, name | organization -> organizations | unique `(organization_id,name)` | Audience lists |
| `contact_list_members` | `(contact_id,list_id)`; organization_id, subscription timestamps | organization, contact, list -> parent rows | PK; index list_id | Lists/subscribe; no composite tenant FK |
| `tags` | UUID `id`; organization_id, name, color | organization -> organizations | unique `(organization_id,name)` | Tags |
| `contact_tags` | `(contact_id,tag_id)`; organization_id | organization, contact, tag -> parent rows | PK; index tag_id | Tag relations; no composite tenant FK |
| `segments` | UUID `id`; rules JSONB | organization -> organizations | organization index | Segment compiler |
| `consent_events` | BIGSERIAL `id`; contact_id, event_type, metadata | organization -> organizations; contact_id has no FK | contact/time index; event CHECK | Consent/subscriptions |
| `suppressions` | UUID `id`; email, reason | organization -> organizations | unique `(organization_id,lower(email))` | Suppression and delivery |
| `templates` | UUID `id`; design JSONB, html/text | organization -> organizations; created_by -> users | organization index | Template API and rendering input |
| `sending_domains` | UUID `id`; domain, SPF/DKIM/DMARC status, public key | organization -> organizations | unique `(organization_id,lower(domain))` | Domain verification |
| `campaigns` | UUID `id`; organization_id, status, list/segment/template IDs, content | organization -> organizations; list/segment/template -> their tables; created_by -> users | org/status; partial due-scheduled index | Campaign API/service |
| `campaign_recipients` | UUID `id`; campaign/contact IDs, email snapshot, status | organization -> organizations; campaign -> campaigns; `contact_id` intentionally has no FK | unique `(campaign_id,contact_id)`; campaign/status and email indexes | Immutable audience snapshot |
| `delivery_jobs` | UUID `id`; campaign/contact IDs, message_id, status, retries | organization -> organizations; campaign -> campaigns; contact_id has no FK | unique message_id and `(campaign_id,contact_id)`; due/campaign/org indexes | Worker source of truth |
| `message_events` | BIGSERIAL `id`; organization, event type, campaign/contact/job IDs | organization -> organizations; event identifiers otherwise have no FKs | campaign/type, org/time, job indexes | Tracking, analytics; unpartitioned |
| `automations` | UUID `id`; trigger/status | organization -> organizations; created_by -> users | org/status index | Schema only; no runtime API |
| `automation_steps` | UUID `id`; automation, position, type, config | organization -> organizations; automation -> automations | unique `(automation_id,position)` | Schema only |
| `automation_executions` | UUID `id`; automation/contact, status, next_run_at | organization -> organizations; automation -> automations; contact_id has no FK | partial due index; automation/contact index | Schema only; no runtime |
| `webhooks` | UUID `id`; URL, secret_hash, events/status | organization -> organizations | organization index | Schema only; outbound webhooks absent |
| `usage_records` | BIGSERIAL `id`; metric, quantity, occurred_at | organization -> organizations | org/metric/time index | Quota/usage code, no billing |
| `audit_logs` | BIGSERIAL `id`; actor/action/target | organization -> organizations; actor -> users | org/time index | Audit writes not comprehensive |
| `contact_imports` | UUID `id`; source_data, mapping, status/progress | organization -> organizations; created_by -> users | org/status indexes in 0001; extra due/lease indexes in 0002/0006 | Durable import jobs; duplicate-table migration is repaired in source, pending PostgreSQL verification |
| `dead_letter_queue` | UUID `id`; delivery_job_id, campaign, reason, attempts | organization -> organizations; campaign and job -> parent rows | org/time and unreplayed indexes | DLQ list/replay; replay path defective |
| `organization_settings` | organization_id; quotas, thresholds, pause, concurrency | organization -> organizations | PK organization_id | Worker delivery guardrails |
| `verification_tokens` | UUID `id`; token, purpose, expires_at, used_at | user -> users | unique token + redundant token index; expiry partial index | Auth email verification/reset; trigger column repaired in source, pending DB verification |

### Data integrity, indexing, retention, and recovery

- IDs are generally UUIDs; event/log/usage identifiers use `bigserial`. Time values use `timestamptz`.
- Several tenant-owned relationship tables carry `organization_id`, but PostgreSQL does not enforce that their contact/list/tag/campaign/template/segment references belong to that same organization. The constraints reference entity IDs individually rather than composite `(organization_id,id)` keys.
- `campaign_recipients.contact_id` deliberately lacks an FK to preserve historical snapshots. `delivery_jobs.contact_id`, consent event contact IDs, message event references, and automation execution contact IDs are also not protected by FKs.
- Most tables have useful tenant/status/time indexes, but there is no partitioning, archival job, retention policy, or demonstrated query-plan/load analysis for `message_events`, `delivery_jobs`, imports, audit, or usage.
- Deletes are generally permanent and use `ON DELETE CASCADE` for organization-owned data. There is no uniform soft-delete policy; campaign/contact history can be deleted through cascades.
- No backup job or tested restore process is wired into Compose. Docs describe desired `pg_dump`/WAL backup operations but do not implement them.
- Parameterized SQL is used in the reviewed handlers/services, which reduces SQL injection exposure. Database role separation and least-privilege grants are not defined by the repository.

## 7. Multi-Tenant Data Management

The access token includes organization and role claims. Middleware verifies the JWT; handlers generally obtain the organization ID from token context and include it in query predicates. This is a meaningful application-layer control, but it is not database-enforced.

**Not present:** PostgreSQL RLS, `SET LOCAL` tenant context, composite tenant-aware foreign keys, tenant-specific database roles, or a systematic database audit proving every read/write query has a tenant predicate.

**Observed gaps/risks:**

- Campaign creation/update accepts list/segment/template identifiers without consistently proving that each referenced row belongs to the caller's organization. Query-level org scopes reduce exposure but do not guarantee relational integrity.
- Public subscription accepts organization and list identifiers without rejecting a list from another organization; this creates inconsistent consent/list association behavior.
- `contact_list_members`, `contact_tags`, and other ID-linked records can contain mismatched `organization_id` values because the FK constraints do not include that column.
- JWT membership/role claims are not refreshed against `organization_members` for each request. Revoked or changed access can remain usable until the access token expires.
- API-key scopes are stored but are not enforced, and API-key authentication is not connected to the route middleware.

No exploit test was performed against a live database. These are source-level design and validation findings; they are not proof that arbitrary tenant rows are presently readable through every endpoint.

## 8. Backend Architecture

### Executables and modules

| Entry point / package | Responsibility |
|---|---|
| `cmd/api/main.go` | Loads config, connects PostgreSQL, optionally migrates, connects NATS if available, constructs API and serves HTTP |
| `cmd/worker/main.go` | Connects PostgreSQL and NATS, builds SMTP transport, runs scheduler/consumer, serves worker metrics/health |
| `cmd/migrate/main.go` | Applies SQL files in lexical order |
| `internal/api` | Versioned HTTP routes and handler logic |
| `internal/auth` | Argon2id password hashing, JWT and role middleware, API-key service |
| `internal/campaigns` | Campaign recipient snapshots, queue/job creation, republishing |
| `internal/contacts` | Import and segment helpers |
| `internal/delivery` | SMTP transport, rendering, bounce classification, click/open token helpers |
| `internal/messaging` | NATS JetStream stream/consumer/publish wrapper |
| `internal/worker` | Delivery processing, scheduler, retries, suppression/quota checks, completion |
| `internal/db` | pgx pool and migration runner |
| `internal/config` | Environment-based config validation/defaults |
| `internal/observability` | Structured logging and Prometheus metrics |
| `internal/httpx` | JSON/error responses and HTTP middleware |

### API surface and control flow

`internal/api/server.go` registers public health/readiness/metrics, auth, public subscribe/unsubscribe/tracking, protected audience/campaign/template/domain/delivery/analytics/API-key routes, and inbound bounce/complaint hooks. Role sets are attached to selected mutations. Middleware includes request ID, recovery, access log, CORS, and metrics.

Major missing backend route families include organization/member management, automation runtime, billing, form CRUD, outbound tenant webhooks, campaign test-send, and API-key request authentication.

### Error handling and configuration

Handlers generally use structured error envelopes and log internal failures. Config validates production JWT secret, SMTP credentials, access-token lifetime, and verified-domain enforcement. Runtime defaults remain development-oriented. API startup tolerates NATS connection failure and logs that campaigns remain queued; worker startup fails if NATS is unavailable.

### Backend quality and verification

Go unit tests passed in this environment. DB-dependent API and import tests explicitly skipped because no PostgreSQL test database was available. There is no evidence of a database repository integration test suite covering cross-tenant references, idempotency under concurrency, live SMTP, migrations, or restart/recovery behavior.

## 9. Frontend Architecture

### Routing/state/API

- Next.js App Router routes are under `apps/web/app`; dashboard pages share `(dashboard)/layout.tsx` and `AppShell`.
- `apiFetch` sets request headers, disables caching, and parses non-success API responses.
- Zustand persists token, user, and organization in local storage. React Query is configured globally with a 30-second stale time.
- Root/dashboard protection is client-side. It is not a server-side authorization boundary; the API must remain authoritative.
- Role does not consistently control navigation or interface actions. The workspace role is displayed as a “plan” in the shell.
- The frontend API base URL is read from `NEXT_PUBLIC_API_URL`, which Next.js inlines at build time. The web Dockerfile does not provide a build argument/environment for it; Compose sets it only at container runtime. Consequently, changing `API_BASE_URL` at runtime may not change the browser bundle's API target.

### Page status

| Page/feature | Status and actual behavior |
|---|---|
| Login/register/forgot/reset | Forms use client validation and API calls. Registration depends on the broken verification URL described above. |
| Dashboard | API-backed overview and recent campaigns; hard-coded trend deltas and some unavailable stats. |
| Contacts | API-backed list/search/pagination; add/import/export/profile actions remain placeholders. |
| Lists/tags/segments | Basic list/create calls are connected; management actions and segment rule builder are incomplete. |
| Campaign list/detail | Reads campaign/analytics APIs. |
| Campaign create/editor/preview/report | Prototypes with hard-coded data, fixed campaign IDs, or inert Save/Test/Schedule controls. |
| Templates | Reads API; creation/editing flow is a placeholder. |
| Domains | API-backed list/add/verify-one/DKIM update; verify-all and some DNS content are placeholder. |
| Delivery dead letters/suppressions | API-backed list and mutation actions with loading/error handling. |
| Automations | Hard-coded demo flows and controls; no backend runtime routes. |
| Forms | Hard-coded records/metrics and inert create/embed/edit/copy actions; no form CRUD API. |
| Reports | Literal series/KPI values; date/export controls do not produce real report data. |
| Settings | Inputs/buttons are not wired to settings APIs; team roles are sample data. |
| Billing | Plans/invoices/usage are constants; no billing integration or real payment action. |
| Header search/workspace selector | Presentational controls without functional search or workspace switching. |

API-backed screens usually provide loading and error feedback. Some query errors are surfaced through toast calls during render. There is no automated frontend test script in the package manifest. The audited frontend passed typecheck and production build; that verifies compilation, not user journeys against a real API.

## 10. Email Delivery Infrastructure

The worker implements a generic SMTP transport intended to connect to Stalwart. It is not a Stalwart-specific API integration. It requires STARTTLS outside development/test, optionally authenticates, renders structured content, and submits SMTP `DATA`.

The observed path is:

1. Campaign send/schedule operations produce recipient snapshots and one durable `delivery_jobs` row per recipient.
2. Job references are published to the `delivery.jobs` JetStream stream after database writes.
3. The worker reloads job/campaign/contact data and checks terminal state, suppression, active campaign/contact state, sender-domain verification, and quota.
4. It renders and personalizes content, adds tracking/unsubscribe tokens, then submits to the configured SMTP relay.
5. A successful response means **the configured SMTP relay accepted the submission**. The code records a `submitted` result; it does not prove recipient-MX acceptance, inbox delivery, or inbox placement.
6. Open/click requests produce `message_events`; separate bounce/complaint webhooks can update suppression state.

### What is and is not configured

- Compose declares `stalwartlabs/stalwart:latest`, ports 25/465/587/8080, and a persistent volume.
- No Stalwart configuration file, production certificate/key provisioning, DKIM signing setup, return-path/bounce relay, outbound port 25 deployment, PTR, IP warm-up, or reputation monitor is present in repository infrastructure.
- Domain API DNS checks look for SPF/DKIM/DMARC TXT record prefixes. DNS presence alone does not establish correct SPF evaluation, DKIM signature validity, DMARC alignment, acceptance, or inbox placement.
- Registration/password-reset mail uses Resend HTTP API if configured; this is separate from campaign SMTP delivery. The app can log a mail-send error and still return a success-shaped registration response.
- There is no verified bridge that forwards real Stalwart DSN/FBL messages into the HTTP webhook routes.

### Failure behavior and important defects

- Temporary SMTP errors are deferred with exponential backoff; permanent errors are classified and can suppress contacts; exhausted/aged jobs enter the DLQ.
- Worker restart recovery republishes due queued/deferred jobs at startup. Publish failures during a long-running process are not shown to be recovered on a periodic interval.
- A database status check alone does not make the external SMTP side effect idempotent. Concurrent duplicate messages or an uncertain SMTP/DB outcome can cause a second send.
- DLQ replay appears to use the dead-letter record ID as the job ID, and publish errors/affected-row counts are not reliably checked.
- Bounce and complaint endpoints are unauthenticated and lack a request signature. Forged callbacks can create suppressions. The bounce handler uses the supplied email in a comparison against `message_id`; complaint handling does not fully update pending jobs or add a complaint event.
- GET unsubscribe mutates state; automated link scanners can unsubscribe recipients.
- Open/click events are not deduplicated; proxies, privacy features, and repeated requests inflate events. Analytics should not be read as unique inbox engagement.

## 11. Queue and Background Processing

NATS JetStream is configured with a file-backed `DELIVERY` stream, a 24-hour max age, explicit acknowledgements, a two-minute ACK wait, and up to ten deliveries. PostgreSQL remains the durable record for jobs and is used to republish pending work.

The worker has scheduler/consumer behavior, SMTP retry/backoff, maximum attempts/message age, per-domain concurrency, tenant quota checks, and dead-letter storage. These are source-level features, not validated under production load.

| Failure scenario | Implemented behavior / caveat |
|---|---|
| NATS unavailable at API startup | API logs a warning; database jobs may remain queued until republished. |
| NATS unavailable at worker startup | Worker startup fails. |
| SMTP temporary response | Job defers and retries using backoff. |
| SMTP permanent response | Classified as bounce/failure; hard-bounce path suppresses contact. |
| Worker restart | Startup republishes due queued/deferred database jobs. |
| Message redelivery | Status checks avoid some repeats, but do not close the concurrent/uncertain SMTP submission window. |
| Job older than max age / retries exhausted | Job is marked failed and moved to DLQ. |
| Pause encountered | Message may be NAKed without DB status/attempt change; repeated NAKs can exhaust broker delivery attempts. |
| DLQ replay | Current handler's ID mapping/publish behavior appears incorrect. |
| Scheduler replicas | No evidence of distributed scheduler lease/leader election was verified; multi-worker duplicate scheduling needs testing. |

There is no transactional outbox between PostgreSQL and NATS. The DB-first approach plus republishing mitigates some publish failures, but startup-only recovery is weaker than a continuously reconciled outbox/dispatcher.

## 12. Security and Data Protection Audit

### Positive controls

- Password hashing uses Argon2id.
- JWT parsing restricts algorithm/issuer and requires expiration.
- SQL in reviewed paths is parameterized.
- API key secrets are hashed before storage.
- Role middleware gates selected mutations.
- Production config checks JWT secret strength, SMTP credentials, verified-domain enforcement, and token lifetime.
- Consent evidence and suppression records exist.

### Findings

| Severity | Finding | Evidence / impact |
|---|---|---|
| High | Inbound bounce/complaint routes are unauthenticated and do not verify a provider signature. Caller-supplied domain/email can cause forged suppression updates. | `internal/api/server.go` registers both handlers without `protect`; `internal/api/delivery.go` parses caller data and mutates tenant suppression state. |
| High (if deployed as configured) | No HTTPS/TLS listener is configured in Caddy while the API carries passwords and Bearer tokens. | `infrastructure/caddy/Caddyfile` disables automatic HTTPS and only declares `:80`; Compose publishes 443 without a matching site configuration. |
| High | Delivery can be duplicated when duplicate job deliveries race or SMTP accepted the message but the app failed before recording status. | Worker performs external SMTP submission before the persistent status guard/commit. |
| Medium | CSV export can expose spreadsheet formula injection through attacker-controlled contact values. | `internal/api/contacts.go` writes contact fields to CSV without formula neutralization. |
| Medium | GET unsubscribe performs a state-changing opt-out; link scanners can unsubscribe recipients without user intent. | Public GET unsubscribe invokes the mutation path. |
| Medium | API keys can be created but are not accepted by request authentication middleware; scopes are stored but unenforced. This is a functional auth boundary gap if clients assume keys work. | API-key service/management routes exist; authentication is not wired to the API handler chain. |
| Medium | Verification/reset bearer tokens are stored in plaintext in the database; a read-only DB disclosure exposes still-valid tokens. | `verification_tokens.token` is a plaintext `text` column. |
| Medium | Authentication account-activation flow does not complete through the generated verification link, leaving new accounts pending/unable to log in. | Registration link target is not a registered UI/API endpoint. |
| Low | JWT is persisted in browser local storage. This avoids cookie CSRF but increases token theft impact if an XSS exists; no CSP configuration was identified in the inspected deployment files. | `apps/web/stores/auth.ts`; Caddy/deployment config. |

No claim is made that the repository has a known exploitable XSS. No live penetration test, TLS scan, secret-manager review, database permission audit, dependency scan, or production privacy/compliance review was performed. No secrets are reproduced here.

## 13. Infrastructure and Deployment

### Compose inventory

Compose declares PostgreSQL 16, Redis 7, NATS 2.10 with JetStream file storage, Stalwart latest, MinIO latest, one-shot migration, API, worker, web, Caddy, and optional Prometheus/Grafana/Loki. Named volumes exist for most stateful containers.

### Deployment blockers and security concerns

- **Host port collision:** API and Stalwart both publish host port `8080:8080`; a single host cannot bind both simultaneously.
- **TLS absent:** Caddy config contains `auto_https off` and only a `:80` site. Port 443 is exposed in Compose but not configured for HTTPS.
- **Frontend API URL build/runtime mismatch:** `NEXT_PUBLIC_API_URL` is compiled into the Next bundle during `npm run build`, but the web Dockerfile does not pass a build-time value; Compose only supplies a runtime environment value.
- **Go build-version mismatch:** the backend Dockerfile starts from `golang:1.24-alpine`, while `go.mod` declares Go 1.26. The Go tool may attempt to download a newer toolchain automatically; the image build was not run, so reproducibility/network dependence needs verification. The README also lists Go 1.24+.
- **Migration dependency:** API and worker wait for successful migration service; migration 0002 duplicate-table conflict prevents the normal stack from reaching that state.
- **Development defaults exposed:** Compose publishes Postgres, Redis, NATS, SMTP/admin and MinIO ports. Example/default credentials are suitable only for local development and must not be used in production.
- NATS has no Compose health check; API depends on its start state rather than broker readiness.
- Redis and MinIO have persistent services but no application clients.
- Prometheus scrapes API and worker. Grafana has datasources but no dashboards found. Loki has local filesystem storage/configuration but no verified log shipping pipeline or HA.
- No `.github` workflow/CI configuration was found.
- No automated backup/restore scripts or tested disaster-recovery procedure were found.

The deployment environment is therefore a local Compose development design, not an evidenced production topology. Caddy, Stalwart, database, broker, DNS, certificates, and backup behavior could not be exercised because Docker/live infrastructure was unavailable.

## 14. Mermaid Architecture Diagrams

The source diagrams are maintained separately:

- [System overview](./architecture/system-overview.mmd)
- [Database relationships](./architecture/database-relationships.mmd)
- [Email delivery flow](./architecture/email-delivery-flow.mmd)
- [Authentication flow](./architecture/authentication-flow.mmd)
- [Deployment architecture](./architecture/deployment-architecture.mmd)

Dashed lines in the diagrams denote declared-but-not-integrated services or unverified external event paths; they should not be interpreted as functioning connections.

## 15. Scalability and Performance Assessment

There are no load-test results or measured service-level objectives in the repository. The following is an architectural assessment, not a throughput guarantee.

| Hypothetical workload | What exists now | Likely bottlenecks / required evidence |
|---|---|---|
| 1,000 users | Stateless API JWTs, one PostgreSQL database, one worker architecture | Plausible MVP range but unbenchmarked. Validate pool usage, login rates, query latency, and tenant authorization. |
| 10,000 users | Same architecture can be replicated at the HTTP layer in principle | Pool multiplication, token revocation/membership freshness, API query/index load, and lack of deployment automation require testing. |
| 100,000 users | No demonstrated scale-out or database topology | Requires API/worker horizontal tests, connection pooling/proxying, per-tenant fairness, and database capacity planning. |
| 1 million contacts | Tenant/email/status indexes and batched import logic exist | No query-plan/load evidence; contact search/export/segment scans and JSONB filters may dominate. Benchmark import, dedupe, segment materialization and pagination. |
| 10 million contacts | No sharding/partitioning or high-volume retention strategy | Requires production PostgreSQL tuning, partition/index design, archival, query profiling, and likely read/write scaling. |
| 100,000 recipients in one campaign | Recipient snapshot and one durable job per contact; JetStream distribution and SMTP worker exist | Work is O(recipients) in PostgreSQL and broker; queueing duration depends on SMTP and configured worker rate. Benchmark memory, DB writes, broker lag, suppression lookups, and recipient acceptance. |
| 1 million emails across campaigns | Durable design provides a starting point, not demonstrated capacity | Single SMTP relay, per-worker rate/concurrency, NATS single node, DB write amplification, and retry storms are major bottlenecks. Requires multi-node broker/relay strategy and load/recovery tests. |
| 100,000 message events/day | Indexed time/event fields; append-only event table | Event table is unpartitioned and events are not deduplicated. Benchmark writes and analytics; add retention/partition/aggregation as needed. |
| 1 million message events/day | No demonstrated hot/cold event storage or aggregation pipeline | Requires partitioning/retention, pre-aggregated analytics, batch ingestion, query isolation, and database/broker sizing from measured tests. |

Before any capacity claim: run representative load tests with concurrency, fault injection, a real PostgreSQL schema, JetStream durability, and a controlled SMTP sink. Measure p50/p95/p99 latency, DB pool saturation, queue lag, duplicate sends, retries, and per-domain throughput.

## 16. Technical Debt and Architectural Risks

Priority order:

1. **P0 - Migration sequence fails:** duplicate `contact_imports` declaration blocks fresh installs and Compose-dependent API/worker startup.
2. **P0 - Compose host port collision:** API and Stalwart both claim host 8080.
3. **P1 - Auth verification link mismatch:** new accounts cannot use the generated link to activate; login denies pending accounts.
4. **P1 - Inbound event trust gap:** unauthenticated bounce/complaint requests can alter suppression state.
5. **P1 - SMTP duplicate window:** delivery processing is not externally idempotent or serialized before SMTP side effect.
6. **P1 - DLQ replay wrong ID path:** replay may send a DLQ record ID as a delivery job ID and ignore failures.
7. **P1 - No production HTTPS:** credentials/JWTs can traverse plaintext HTTP if deployed as provided.
8. **P1 - Frontend prototypes misrepresent completeness:** major campaign, automation, forms, report, settings, and billing interactions are unconnected.
9. **P2 - Tenant consistency is application-only:** no RLS/composite keys; references and public subscribe inputs need same-org checks.
10. **P2 - DB verification trigger (source repaired, validation pending):** migration 0005 now defines `updated_at`; 0006 repairs previously migrated databases, but has not been applied against PostgreSQL.
11. **P2 - API key scopes/auth incomplete:** keys can be issued but not used to authenticate requests.
12. **P2 - Analytics event duplication/denominator issues:** engagement counts/rates may misstate unique-user behavior.
13. **P2 - Runtime recovery gaps:** startup-only republish and NAK behavior can delay or exhaust jobs.
14. **P2 - Event/log tables lack retention and partitioning:** growth and analytics latency are unbounded.
15. **P2 - No automated backup/restore, production CI, or deployment validation.**
16. **P2 - Build inputs are inconsistent:** backend Dockerfile Go 1.24 base differs from `go.mod` Go 1.26; frontend API URL is configured at runtime despite Next.js build-time inlining.
17. **P3 - Docs diverge from source:** architecture/schema/roadmap/operations documents contain stale claims, contradictory statuses, and features described as integrated that are only declared or partial.

## 17. Missing Features

- Organization settings, invitations, membership and role management, multi-workspace selection.
- Fully usable verification/resend flow and API-key authentication with scope enforcement.
- Contact/list membership management, tag assignment/update/delete, complete custom-field API, full segment builder.
- Functional campaign wizard/editor/preview/test send and template editing.
- Automation definition API, scheduler/runtime, condition branching, execution recovery, and UI integration.
- Form builder/API/embed management.
- Billing provider, invoicing, subscriptions, metering reconciliation, and actual billing UI.
- Outbound webhooks with signing/retry/delivery logs.
- Trusted provider bounce/complaint ingestion and robust message-ID correlation.
- SMTP/DNS production configuration, DKIM key deployment/rotation, bounce mailbox/DSN bridge, IP warm-up and reputation operations.
- DB RLS/composite tenant constraints, DB least-privilege role setup, data retention/partitioning.
- Search/header behavior, working workspace switcher, role-aware UI, populated report/export/date controls.
- Automated backup/restore, alerts/dashboards, CI, staged deploy/rollback, end-to-end and load test suites.

## 18. Recommended Development Roadmap

### Phase 1 - Critical fixes (gate all integration work)

| Task | Why / dependencies | Priority | Complexity | Acceptance criteria |
|---|---|---|---|---|
| Reconcile contact-import migrations and add migration regression test | Blocks any fresh database; choose one canonical table definition before applying later migrations | P0 | Medium | Apply all migrations twice on disposable PostgreSQL; schema matches expected columns; migration runner is repeatable. |
| Correct the Compose host-port plan | API and Stalwart collide on 8080 | P0 | Low | Full `docker compose up` reaches healthy API/worker/web without port bind failures. |
| Complete account verification URL/UI/API wiring; make email failure explicit/recoverable | New user cannot activate by emailed link | P0 | Medium | Registration -> received link -> active account -> successful login; resend/error behavior is honest and tested. |
| Authenticate/sign inbound bounce and complaint events; fix message association and event updates | Prevent forged suppressions and corrupt delivery state | P0 | High | Requests without valid signature are rejected; valid event updates intended job/contact/org and event exactly once. |
| Make SMTP job claim/submission/reconciliation safe; repair DLQ replay | Prevent duplicate sends and broken replay | P0 | High | Concurrent duplicate NATS deliveries submit at most once under defined semantics; DLQ replay uses actual job ID and surfaces all failures. |
| Configure real HTTPS and safe production secrets/ports | Protect credentials/tokens and close exposed dev ports | P0 | Medium | External HTTPS only, TLS certificate verified, no dev credentials/defaults, DB/broker/admin ports not publicly exposed. |

### Phase 2 - Core system completion

| Task | Why / dependencies | Priority | Complexity | Acceptance criteria |
|---|---|---|---|---|
| Complete tenant relationship validation and DB constraints/RLS | Depends on migration repair and tenant-query inventory | P1 | High | Cross-tenant references rejected by DB/API; RLS tests prove isolation under app DB role. |
| Finish organization/member/workspace APIs and authorization freshness | Needed for real multi-user workspaces | P1 | High | Invitations, role changes, revocation, and multi-workspace selection have integration tests. |
| Complete contact/list/tag/custom-field/segment UI workflows | Backend surface exists only in part | P1 | Medium | Every visible control performs an API operation with success/error/loading feedback; segment conditions persist. |
| Implement campaign wizard/editor/preview/test-send and template CRUD | Current UI mostly static for campaign composition | P1 | High | Draft persists, content round-trips, audience preview matches snapshot, test send is explicit and audited. |
| Wire import/export UX and sanitize spreadsheet values | API exists; UI and export safety need completion | P1 | Medium | Import status/errors are visible; exported formula-like cells are safe in spreadsheet software. |

### Phase 3 - Email delivery reliability

| Task | Why / dependencies | Priority | Complexity | Acceptance criteria |
|---|---|---|---|---|
| Establish SMTP/DSN/FBL production configuration and real integration tests | Generic SMTP code alone does not configure Stalwart or inbound events | P1 | High | Test relay proves STARTTLS/auth, verified domain, controlled bounce and complaint flow. |
| Add transactional outbox or continuous DB/NATS reconciler and worker coordination | Prevent lost jobs and multi-replica scheduler conflicts | P1 | High | Kill/restart/fail broker tests converge DB jobs without manual restart or duplicates. |
| Add event idempotency and accurate delivery state semantics | Prevent repeated tracking/bounce counts and state confusion | P1 | Medium | Duplicate callbacks and pixel/click requests do not inflate unique engagement metrics. |
| Implement domain/IP rate shaping, warm-up, reputation monitoring, and abuse controls | Required before bulk SMTP sending | P1 | High | Per-domain/provider limits are measured; pause and alert actions are tested. |
| Provide required marketing footer/address and compliance review | Deliverability/legal operational requirement | P1 | Medium | Configurable organization address and tested RFC 8058 unsubscribe behavior; GET does not silently opt out. |

### Phase 4 - Analytics and automation

| Task | Why / dependencies | Priority | Complexity | Acceptance criteria |
|---|---|---|---|---|
| Replace report/chart constants with real aggregated endpoints | Avoid misleading dashboard output | P1 | Medium | Date ranges, exports, unique recipients, and denominators have integration tests. |
| Build durable automation runtime/API | Schema/UI alone do not execute workflows | P2 | High | Trigger/step state survives restart, delays are durable, and suppression is honored. |
| Add signed outbound webhooks with retries and delivery logs | Tenant integrations are currently absent | P2 | High | Signing, replay protection, retry/backoff, pause, and per-tenant event filters verified. |

### Phase 5 - Security and production readiness

| Task | Why / dependencies | Priority | Complexity | Acceptance criteria |
|---|---|---|---|---|
| Wire API-key authentication, enforce scopes, and test revocation | Management-only keys are unusable/incomplete | P1 | Medium | Keys authenticate expected routes only; scope and expiry/revocation are enforced. |
| Store verification/reset token hashes; audit session/token lifecycle | Reduce impact of DB read disclosure and stale JWTs | P2 | Medium | Raw one-time token is never persisted; consumed/expired/revoked behavior tested. |
| Add rate limiting/abuse controls and security headers | Public registration/login/subscribe endpoints are exposed | P1 | Medium | Automated tests cover limits and production headers; no secrets/tokens in logs. |
| Add backups, restore drills, monitoring dashboards/alerts, and CI | Required operational safety and repeatable releases | P1 | High | Automated pipeline, restore exercise, actionable alerts, and deploy rollback documented/tested. |
| Review consent/privacy/retention and audit logging | Personal data and event history need governance | P1 | High | Retention/deletion/export policies implemented and security-sensitive actions audited. |

### Phase 6 - Scalability and optimization

| Task | Why / dependencies | Priority | Complexity | Acceptance criteria |
|---|---|---|---|---|
| Benchmark realistic contact/campaign/event loads | No measured capacity currently exists | P1 | High | Published reproducible load results and defined supported limits. |
| Partition/archive high-volume event and delivery data | Tables are currently unpartitioned/unbounded | P2 | High | Retention jobs preserve required reporting while bounding query/storage growth. |
| Tune PostgreSQL pools, indexes, and query paths; add pooler if justified | Each API/worker replica adds a 20-connection pool | P2 | Medium | Load test demonstrates safe pool saturation and p95/p99 targets. |
| Plan HA for PostgreSQL, NATS, and SMTP relays | Current Compose is single-node and has SPOFs | P2 | High | Failure/restore/leader behavior tested with documented RPO/RTO. |

## 19. Production Readiness Assessment

**Verdict: Not ready for production bulk email or multi-tenant SaaS use.**

Blocking reasons: fresh-install migrations fail; Compose has a host-port collision; account verification is not connected; production HTTPS is absent; inbound event endpoints are untrusted; delivery has duplicate/recovery defects; major UI workflows are static; no real PostgreSQL/NATS/Stalwart integration or load/restore validation is available.

Do not treat passing unit tests or a successful Next.js build as evidence of a working end-to-end installation. A production gate should require a clean migration, full Compose/deployment smoke test, account lifecycle, tenant isolation tests, live controlled SMTP acceptance/bounce tests, queue crash/replay tests, TLS/security review, and backup restore.

## 20. Final Technical Recommendations

1. Fix and test the migration chain before attempting application-level debugging against a fresh database.
2. Resolve Compose port and TLS configuration, and define an explicit production API URL build strategy for Next.js.
3. Repair account verification and event-ingress trust before onboarding real users or sending real campaigns.
4. Treat the current frontend as a mixed prototype/backend client; clearly disable or label nonfunctional controls until completed.
5. Build a tenant-isolation test matrix and database-level guardrails before adding more tenant features.
6. Make delivery semantics explicit: broker retry is at-least-once; SMTP is an external side effect and needs a claim/reconciliation design, not merely unique DB rows.
7. Add integration tests against the actual migrated PostgreSQL schema and controlled SMTP/NATS services; then benchmark and document supported limits.
8. Reconcile older architecture, schema, operations, progress, and roadmap docs with source after the critical path is verified.
