# Implementation Roadmap & Checklist

> **Status note (2026-11-03):** This document contains a historical mixed-stack checklist whose completion labels are stale and must not be used as current status. The authoritative architecture transition plan is [DEVELOPMENT_ROADMAP.md](../DEVELOPMENT_ROADMAP.md); verified work and open gates are in [DEVELOPMENT_PROGRESS.md](../DEVELOPMENT_PROGRESS.md) and [DEVELOPMENT_STATUS.md](../DEVELOPMENT_STATUS.md).

Current execution scope: backend engine only. The checklist below is authoritative for this work; frontend items in the historical plan are out of scope.

## Backend Checklist
- [x] Inspect existing API, worker, migration runner, schema, Compose wiring, and environment.
- [x] Reconcile architecture, database, API, operations, progress, and roadmap documentation.
- [x] Strictly parse config values and reject unsafe production secrets, SMTP, token, and domain settings.
- [x] Remove insecure Compose JWT fallback; pass SMTP credentials and domain enforcement to the worker.
- [x] Require STARTTLS in production and cover policy/classification helpers with Go unit tests.
- [x] Bound Argon2 verification to the configured password-hash profile and add malformed-hash tests.
- [x] Apply role-based authorization to audience, campaign, template, and domain mutations.
- [x] Add unit tests for role middleware and JWT issuer/signature/expiry validation.
- [x] Align OpenAPI with registered API routes; validate YAML and route parity.
- [x] Run all unit tests, vet, and race tests with Go 1.27.3 and `CGO_ENABLED=0`.
- [x] Add real-PostgreSQL API/worker import and API-key lifecycle integration tests (compiled; execution requires configured database URLs).
- [ ] Run CGO-enabled tests/builds (blocked on missing Xcode command-line tools).
- [ ] Verify PostgreSQL migrations and NATS startup/recovery with integration tests.
- [ ] Add/execute integration tests for registration, tenant scoping, consent/suppression, campaign lifecycle, retry/idempotency, and SMTP failure behavior.
- [ ] Execute integration tests for API imports/API keys plus tenant isolation, consent/suppression, campaign lifecycle, retry/idempotency, and SMTP failure behavior against a disposable PostgreSQL instance.
- [ ] Complete production safeguards, observability, backups, and deploy/restore procedures.

## Phase 2 — Audience and consent engine (active)
- [x] New contacts default to pending; CSV status columns cannot opt contacts in.
- [x] Require marketing purpose, source, and non-empty evidence for manual subscription transitions.
- [x] Persist subscription consent atomically with contact changes; block suppressed contacts from subscription.
- [x] Record public-form consent metadata and list membership in one database transaction.
- [x] Compile bounded, validated segment rules to parameterized SQL and apply them to campaign recipient snapshots.
- [ ] Add tag/list/event-based segment predicates and typed custom-field comparisons.
- [ ] Add database-backed tests for consent transactions, suppression races, segment snapshots, and tenant boundaries.
- [x] Move CSV import processing to durable background jobs with mapping, progress, duplicate/invalid counts, bounded errors, lease recovery, and retry.
- [x] Add real-PostgreSQL integration tests for import API organization scoping and worker persistence/recovery (compiled; not run without TEST_DATABASE_URL).
- [ ] Run those integration tests against a disposable PostgreSQL database.

## Historical Cross-Stack Plan
The sections below preserve the earlier mixed frontend/backend plan; they are not the active checklist for the current backend-only task.

### Phase 0 — Foundation (historical)
- [x] Inspect repository and environment
- [x] Initialize git repo (blocked: Xcode CLI tools unavailable; deferred)
- [x] Create architecture documents (overview, decisions, schema, openapi)
- [x] Create operations documents (email-delivery, deployment)
- [x] Create implementation checklist (this file)
- [x] Confirm development environment (Node available; Go/Docker unavailable — write but cannot compile/run backend)

### Phase 1 — Monorepo foundation, auth, organizations
- [ ] Backend: config validation, structured logging, pgx pool, migration runner (exists)
- [ ] Backend: Argon2id passwords, JWT access tokens, register/login/me (exists)
- [ ] Backend: organizations, members, roles, API keys (exists)
- [ ] Backend: contacts CRUD, import, export, suppressions (exists)
- [ ] Backend: campaigns CRUD, lifecycle, snapshots, delivery jobs (exists)
- [ ] Backend: delivery Transport interface + SMTP/Stalwart adapter, renderer, tracking links (exists)
- [ ] Backend: NATS JetStream messaging, worker + scheduler (exists)
- [ ] Backend: domains DNS verification, analytics, public subscribe/unsubscribe/tracking (exists)
- [ ] Backend: unit tests for auth, contacts, campaigns, delivery, worker (missing)
- [ ] Frontend: install TanStack Query, Zustand, React Hook Form, Zod, shadcn/ui (missing)
- [ ] Frontend: auth state (Zustand), login/register pages, token storage, 401 handling (missing)
- [ ] Frontend: wire dashboard to real API statistics (replace demo data)
- [ ] Frontend: wire contacts CRUD, lists, tags, segments to real API
- [ ] Frontend: wire campaigns CRUD + lifecycle to real API
- [ ] Frontend: protected routes, redirect to login when unauthenticated
- [ ] Run frontend typecheck + build
- [ ] Verify application runs end-to-end against local Postgres/Redis/NATS/Stalwart

### Phase 2 — Contacts, imports, lists, tags, custom fields, consent, suppression
- [ ] Contact profiles with consent history view
- [ ] CSV import with column mapping, validation, duplicate handling, progress, error report
- [ ] Dynamic segment builder (SQL translation, no user SQL)
- [ ] Preference management and one-click unsubscribe (RFC 8058)
- [ ] Suppression checks before every send
- [ ] Tests: tenant isolation, dedup, consent, suppression

### Phase 3 — Campaigns, templates, editor, rendering, preview, test sending
- [ ] Campaign creation wizard (details, recipients, design, schedule)
- [ ] Drag-and-drop email builder (heading, paragraph, image, button, divider, spacer, social, footer)
- [ ] HTML source editor with sanitization and validation
- [ ] Desktop/mobile preview
- [ ] Test email sending
- [ ] Template management

### Phase 4 — Stalwart, NATS, scheduler, delivery workers, domain verification, bounces
- [ ] Stalwart configuration: TLS, DKIM keys, return-path, bounce processing
- [ ] Domain verification: SPF/DKIM/DMARC actual DNS checks
- [ ] Delivery workers: suppression, quotas, rendering, SMTP submission, retries, DLQ
- [ ] Bounce classification: hard/soft/deferral/complaint
- [ ] Exponential backoff, max attempts, message age
- [ ] Idempotency, uncertain SMTP outcomes, worker crash recovery

### Phase 5 — Analytics, tracking, reporting, live status
- [ ] Click tracking redirect + open pixel
- [ ] Event deduplication and aggregation
- [ ] Campaign dashboards and time-series summaries
- [ ] Live campaign status via SSE/WebSocket
- [ ] Reports: deliverability, bounces, complaints, unsubscribes

### Phase 6 — Automation engine, workflow editor, durable execution
- [ ] Automation definitions (triggers: subscribe, tag, form, click, date, custom)
- [ ] Wait/delay, conditions, branches, actions
- [ ] Durable execution state, restart recovery
- [ ] Suppression overrides automation actions

### Phase 7 — Forms, API keys, webhooks, billing, admin
- [ ] Signup forms and embed codes
- [ ] API key management (hashed, scoped)
- [ ] Webhook management (delivery, retry, signing)
- [ ] Billing plans, quotas, usage metering, invoices (provider abstraction)
- [ ] MFA, email verification, password reset
- [ ] Platform administration, abuse prevention
- [ ] Tests: authz, permission enforcement, API keys, webhooks

### Phase 8 — Security, load testing, deliverability, backups, monitoring, deployment
- [ ] Security hardening: CSRF, CORS, rate limiting, HTML sanitization, secret management
- [ ] Dependency scanning and security tests
- [ ] Load tests: concurrent API, large imports, delivery queues
- [ ] Deliverability testing against Gmail/Outlook/Yahoo test accounts
- [ ] Backup and restore procedures
- [ ] Prometheus/Grafana/Loki dashboards and alerts
- [ ] Production deployment documentation (Docker, Caddy, env separation, secrets)

### Product Definition of Done
A user can register, create a workspace, import contacts with consent, verify a sending domain,
build an email, create and schedule a campaign, submit real messages through Stalwart, process
delivery feedback, unsubscribe recipients, view accurate analytics, manage automations, and
operate securely. Automated tests, deployment docs, monitoring, and recovery procedures exist.
