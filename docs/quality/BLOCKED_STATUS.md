# Sendwise Remaining Items - Blocked Status

**Last Updated:** 2026-11-05
**Environment:** Docker Compose with PostgreSQL, NATS, Redis, Stalwart running
**Constraints:** TEST_DATABASE_URL unset; no real SMTP delivery; Stalwart in bootstrap mode; no production HTTPS/secrets

## Blocked Items Status

### 1. End-to-end CSV import against PostgreSQL
- **Status:** BLOCKED
- **Blocked by:** TEST_DATABASE_URL not configured
- **What's fixed:** Source-level CSV parser, API, worker unit packages verified
- **What's missing:** Full journey against PostgreSQL-backed test organization
- **To unblock:** Set TEST_DATABASE_URL in .env and run migrations

### 2. Domain/DNS verification (SPF/DKIM/DMARC real DNS checks)
- **Status:** BLOCKED
- **Blocked by:** Stalwart in bootstrap mode (no real SMTP); no DNS configuration
- **What's fixed:** DNS prefix/presence checks in code exist
- **What's missing:** Real DNS lookups, SPF/DKIM/DMARC record verification, Stalwart integration
- **To unblock:** Configure Stalwart with proper DNS, generate actual DKIM keys, set up domain verification

### 3. Full list/tag/segment management
- **Status:** PARTIALLY IMPLEMENTED
- **Blocked by:** No end-to-end database-backed verification
- **What's fixed:** Tag API routes connected; import list membership; basic tag creation
- **What's missing:** List edit/detail/export; full tag CRUD and bulk operations; segment builder/evaluation engine
- **To unblock:** PostgreSQL integration; segment compiler verification; full dashboard module implementation

### 4. Forms (backend and frontend)
- **Status:** PARTIALLY IMPLEMENTED
- **Blocked by:** No TEST_DATABASE_URL; no real delivery verification
- **What's fixed:** Public subscribe endpoint exists; form UI components
- **What's missing:** Form management backend UI; published form submission endpoint; double opt-in; consent capture; rate limiting; form analytics
- **To unblock:** PostgreSQL integration; form submission endpoint; consent tracking

### 5. Automation engine (runtime)
- **Status:** MISSING (per DEVELOPMENT_STATUS.md)
- **Blocked by:** No event infrastructure; no workflow runtime
- **What's fixed:** Schema and demo UI exist (per old status)
- **What's missing:** Visual editor; durable execution engine; trigger engine; delay conditions; actions; execution history; versioning; tenant isolation
- **To unblock:** Event envelope implementation; workflow runtime; NATS JetStream integration

### 6. Campaigns and real email delivery
- **Status:** BLOCKED
- **Blocked by:** Stalwart in bootstrap mode; no real SMTP delivery; no delivery verification
- **What's fixed:** Campaign lifecycle backend; campaign scheduling; campaign validation
- **What's missing:** SMTP acceptance; DKIM signing; delivery jobs; bounce/complaint processing; unsubscribe handling; delivery events; retry/delay/recipient snapshots
- **To unblock:** Configure Stalwart SMTP; verify delivery semantics; implement bounce/complaint webhooks; implement unsubscribe processing

## What IS Working (Verified)
- ✅ Go build: go build ./... - PASS
- ✅ Go unit tests: 15 packages pass
- ✅ Frontend typecheck: npm run typecheck - PASS
- ✅ Frontend build: npm run build - PASS
- ✅ CSV import source fixes (parser, API, worker units)
- ✅ Session revocation infrastructure (JWT + auth_sessions + middleware)
- ✅ Go unit/test package level verification
- ✅ Migration 0009_auth_sessions.sql added

## Repair Progress Summary
- **Completed foundation:** Architecture, data/event ownership, migration repairs, Go/Next.js builds
- **Session revocation:** Implemented (JWT SessionID + auth_sessions table)
- **CSV import:** Source-level fixes verified
- **Remaining:** All require TEST_DATABASE_URL or infrastructure not configured

## Next Steps (When Infrastructure Available)
1. Set TEST_DATABASE_URL and run full migration + integration tests
2. Configure Stalwart SMTP with proper DNS and DKIM
3. Deploy production HTTPS and secrets
4. Complete segment builder and evaluation engine
5. Implement forms backend + frontend completion
6. Implement automation runtime with durable execution
7. Complete campaign delivery with SMTP verification
8. Full dashboard module audit (remove all Coming Soon placeholders)
