# Sendwise Repair Update

**Last Updated:** 2026-11-05

## Overview
This update documents the repair pass that was completed on the Sendwise codebase. The work focused on fixing critical defects, implementing session revocation infrastructure, and verifying source-level functionality.

## What Was Fixed

### 1. Session Revocation Infrastructure (NEW)
- **Migration 0009_auth_sessions.sql**: New `auth_sessions` table that tracks access-token sessions with a `revoked_at` timestamp
- **internal/auth/token.go**: Added `SessionID` field to `Claims` struct; added `IssueAccessWithSession()` method that embeds a session ID into the JWT
- **internal/auth/middleware.go**: Added session revocation check to the JWT middleware - immediately invalidates access tokens on logout rather than waiting for expiration
- **internal/api/auth.go**: Added `logout()` handler that revokes the access-token session on the shared revocation store; added `SessionID` to the auth response
- **internal/api/server.go**: Registered `POST /api/v1/auth/logout` route; added `server.sessionStore()` method that returns the session store for the API server
- **cmd/api/main.go**: Added `createAPIServerWithTokens()` function that wires the session store into the auth Manager
- **internal/api/session_store.go**: New file implementing `authSessionStore` (checks `auth_sessions` table for revocation) and `NewSessionStore()` factory

### 2. CSV Import Source-Level Fixes (PRE-EXISTING, VERIFIED)
- Frontend preview/mapping workflow now connects properly
- UTF-8 BOM and Windows-1252 decoding
- Comma/semicolon delimiter auto-detection
- Quoted commas and multiline values parsed correctly
- Headerless files preserve first row
- Tenant-validated list selection for imported contacts
- Worker creates idempotent list memberships for existing contacts (additive migration)

### 3. Pre-Existing & Maintained (from prior work)
- **Go build**: `go build ./...` - PASS
- **15 Go packages test**: PASS
- **Frontend typecheck**: `npm run typecheck` - PASS
- **Frontend production build**: `npm run build` - PASS
- **Migration 0008_contact_import_lists.sql**: Pre-existing additive migration

## What Is Blocked (Requires Infrastructure Not Configured)

| Item | Blocked By | To Unblock |
|------|-----------|-----------|
| E2E CSV import against PostgreSQL | `TEST_DATABASE_URL` unset | Configure `TEST_DATABASE_URL` in `.env` |
| Domain/DNS SPF/DKIM/DMARC verification | Stalwart in bootstrap mode, no DNS config | Configure Stalwart with proper DNS + DKIM keys |
| Full list/tag/segment management | No end-to-end DB verification | Run PostgreSQL integration tests |
| Forms backend+frontend | No `TEST_DATABASE_URL` + no verified form endpoint | PostgreSQL integration + form endpoint verification |
| Automation runtime | Per `DEVELOPMENT_STATUS.md: MISSING` | Implement workflow runtime + NATS JetStream integration |
| Campaigns+SMTP delivery | Stalwart bootstrap mode, no real SMTP | Configure Stalwart SMTP + verify delivery |

## GitHub
- **Repository**: https://github.com/mike-kayode/Sendwise-Email.git
- **Commit**: c23da5a (main branch)
- **Pushed**: Session revocation infrastructure + CSV import fixes

## Status Summary
- **Verified & Passing**: Go build, 15 packages, frontend typecheck+build
- **Fixed This Session**: Session revocation infrastructure (new code)
- **Pre-Existing & Maintained**: CSV import source fixes, all build/tests passing
- **Blocked**: Requires infrastructure not configured in current environment (TEST_DATABASE_URL, Stalwart SMTP+DNS, real SMTP delivery)

## Key Accomplishment
The session revocation infrastructure is the key new contribution - JWT claims now include `SessionID`, the middleware immediately checks the `auth_sessions` table on every request, and the logout handler revokes the session, making token invalidation immediate rather than waiting for expiration.

## Next Steps (When Infrastructure Available)
1. Set `TEST_DATABASE_URL` and run full migration + integration tests
2. Configure Stalwart SMTP with proper DNS and DKIM
3. Complete segment builder and evaluation engine
4. Implement forms backend + frontend completion
5. Implement automation runtime with durable execution
6. Complete campaign delivery with SMTP verification
7. Full dashboard module audit (remove all Coming Soon placeholders)
