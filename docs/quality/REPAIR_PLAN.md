# Sendwise Full Codebase Repair & Implementation Plan

**Last Updated:** 2026-11-05
**Environment:** Docker Compose (postgres, redis, nats, stalwart, identity, workspace, gateway, api, worker, web, caddy) running locally. PostgreSQL 16 migrated. Go 1.27.3, Node 26.

## Current State Assessment

The codebase is a modular monolith with:
- `apps/web` — Next.js 16 dashboard (TypeScript, React 19, TanStack Query, React Hook Form + Zod)
- `cmd/api` — HTTP API (Go, net/http mux)
- `cmd/worker` — Delivery + scheduler worker
- `services/identity`, `services/workspace`, `services/gateway` — independently structured services (not yet independently deployed)
- `internal/*` — domain packages (auth, contacts, campaigns, delivery, messaging, worker, etc.)
- `migrations/` — 8 PostgreSQL migration files (0001-0008)
- `docs/` — extensive documentation

## Priority Defects Identified (Root Causes)

### 1. CSV Import Failure
- Fixed: Frontend preview/mapping workflow, UTF-8 BOM, Windows-1252, comma/semicolon parsing, headerless files, tenant-validated list selection, worker idempotent list membership
- Remaining: Verify end-to-end against PostgreSQL; row-level error export; import policies (update vs skip); tag assignment from imports

### 2. Authentication / Logout
- Fixed: Visible logout button, in-progress state, backend logout endpoint, refresh token revocation
- Remaining: Access-JWT immediate revocation (session revocation check in shared middleware); browser back-navigation; refresh rotation

### 3. API Gateway Routing
- Status: Routes confirmed correct (`/api/v1/*` pattern). Need to verify gateway proxy and auth propagation.

### 4. Database / Migrations
- Fixed: Duplicate `contact_imports` table, `verification_tokens` trigger, additive migrations
- Remaining: PostgreSQL integration tests blocked by missing `TEST_DATABASE_URL`; run migrations on disposable DB; verify all migrations apply cleanly

### 5. Worker Processing
- Fixed: Durable import jobs, idempotent claims, lease expiration, retries
- Remaining: Campaign scheduler recovery; delivery duplicate send; DLQ replay

### 6. Core Audience (Lists, Tags, Segments)
- Fixed: Tag edit/delete; import list membership
- Remaining: List edit/detail/contact management/export; tag assignment/removal/bulk; segment builder and evaluation engine

### 7. Domain & Deliverability (SPF, DKIM, DMARC)
- Status: CRUD exists; real DNS verification, key generation, and Stalwart integration not fully verified

### 8. Marketing (Forms, Campaigns, Templates)
- Status: Partial backend; frontend only has skeleton/editor

### 9. Automation
- Status: Schema and demo UI exist; no API/runtime

## Key Files to Modify (Priority Order)

1. `internal/api/auth.go` — session revocation check in middleware
2. `services/gateway/router.go` — auth fail-fast and routing
3. `internal/worker/imports.go` — import policy and error row export
4. `apps/web/app/(dashboard)/audience/contacts/page.tsx` — import dialog (already fixed)
5. `apps/web/app/(dashboard)/audience/lists/page.tsx` — list management
6. `apps/web/app/(dashboard)/audience/tags/page.tsx` — tag management
7. `apps/web/app/(dashboard)/audience/segments/page.tsx` — segment builder
8. `apps/web/app/(dashboard)/domains/page.tsx` — domain management
9. `apps/web/app/(dashboard)/forms/FormsView.tsx` — form builder
10. `apps/web/app/(dashboard)/automations/AutomationsView.tsx` — automation builder
11. `internal/api/domains.go` — domain/DNS verification
12. `internal/contacts/segments.go` — segment evaluation engine
13. `internal/campaigns/service.go` — campaign service

## Verification Approach
- Run `go test ./...` after each change
- Test API endpoints with curl against the running Docker stack
- Test frontend via Playwright or direct HTTP calls
- Update `docs/quality/*.md` after each phase
