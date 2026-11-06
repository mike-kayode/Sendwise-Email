# Sendwise Status

## P0 — Deploy and config baseline (verified 2026-11-06)

Proof gate output (run locally against the docker compose stack):

```
$ gofmt -l cmd internal services shared
(no output — empty)

$ go vet ./... && go test ./...
VET_EXIT=0
?   	github.com/khayode/email-platform/cmd/api	[no test files]
?   	github.com/khayode/email-platform/cmd/migrate	[no test files]
?   	github.com/khayode/email-platform/cmd/worker	[no test files]
ok  	github.com/khayode/email-platform/internal/api	(cached)
ok  	github.com/khayode/email-platform/internal/auth	(cached)
ok  	github.com/khayode/email-platform/internal/campaigns	(cached)
ok  	github.com/khayode/email-platform/internal/config	(cached)
ok  	github.com/khayode/email-platform/internal/contacts	(cached)
ok  	github.com/khayode/email-platform/internal/db	(cached)
ok  	github.com/khayode/email-platform/internal/delivery	(cached)
ok  	github.com/khayode/email-platform/internal/outbox	(cached)
ok  	github.com/khayode/email-platform/internal/projections	(cached)
ok  	github.com/khayode/email-platform/internal/worker	(cached)
ok  	github.com/khayode/email-platform/services/gateway	0.819s
ok  	github.com/khayode/email-platform/services/identity	(cached)
ok  	github.com/khayode/email-platform/services/identity/cmd/devseed	(cached)
ok  	github.com/khayode/email-platform/services/workspace	(cached)
ok  	github.com/khayode/email-platform/shared/contracts/events	(cached)

$ (cd apps/web && npm run typecheck && npm run lint && npm test && npm run build)
TYPECHECK=0
LINT=0
ℹ tests 10
ℹ pass 10
ℹ fail 0
TEST=0
BUILD=0

$ docker compose -f docker-compose.yml config -q && docker compose -f docker-compose.microservices.yml config -q
(no output — both valid)

$ ./scripts/deploy.sh            # full local run
[2026-11-06 04:52:28]   every .env.example variable name is declared in .env
[2026-11-06 04:52:28] Configuration validation passed
[2026-11-06 04:52:29]   all Compose configurations are valid
[2026-11-06 04:52:29] Previous application images preserved for rollback
[2026-11-06 04:52:30] Backup complete: /Users/iam_khayode/email-platform/backups/20261106045229
[2026-11-06 04:53:25] Migrations applied
[2026-11-06 04:53:27]   ok  legacy-api-ready
[2026-11-06 04:53:27]   ok  identity-ready
[2026-11-06 04:53:27]   ok  workspace-ready
[2026-11-06 04:53:27]   ok  gateway-ready
[2026-11-06 04:53:27]   ok  worker-health
[2026-11-06 04:53:29]   ok  web
[2026-11-06 04:53:29]   ok  caddy-edge (status 200)
[2026-11-06 04:53:30]   ok  gateway-login (200)
[2026-11-06 04:53:30]   ok  gateway-lists (200)
[2026-11-06 04:53:30]   ok  gateway-contacts (200)
[2026-11-06 04:53:30]   ok  gateway-campaigns (200)
[2026-11-06 04:53:30]   ok  gateway-domains (200)
[2026-11-06 04:53:30]   ok  gateway-tags (200)
[2026-11-06 04:53:30]   ok  gateway-segments (200)
[2026-11-06 04:53:30] All smoke tests passed
[2026-11-06 04:53:30] Deploy completed successfully

$ SENDWISE_ENV_FILE=/tmp/env-no-service-token ./scripts/deploy.sh   # SERVICE_TOKEN removed
[2026-11-06 04:53:41] Comparing .env.example variable names against .env...
  missing from .env: SERVICE_TOKEN
[2026-11-06 04:53:41] ERROR: declare every variable above in .env (copy .env.example)
FAILFAST_EXIT=1
```

Notes:
- One deploy script only: `scripts/deploy.sh` (root `deploy.sh` deleted).
- `migrations/0010_forms.sql` was made idempotent (IF NOT EXISTS indexes,
  DROP TRIGGER IF EXISTS) because its objects pre-existed in the database but
  it was never recorded in `schema_migrations`; without this the deploy gate
  failed with `relation "forms_org_idx" already exists`.
- CI (`.github/workflows/ci.yml`) enforces gofmt, vet, go test, web
  typecheck/lint/test/build and `docker compose config -q`.
- Stray files removed and gitignored: `email.csv`, `"`, `test-results/`.

## P1 — CSV import end to end (verified 2026-11-06)

Proof gate output:

```
$ TEST_DATABASE_URL=postgres://email:email@localhost:5432/email_test?sslmode=disable \
    go test ./internal/contacts ./internal/worker ./internal/api -run Import -v -count=1
=== RUN   TestImportCSVReaderPassesDuplicatesAndRejectsInvalidRows
--- PASS: TestImportCSVReaderPassesDuplicatesAndRejectsInvalidRows (0.00s)
PASS
ok  	github.com/khayode/email-platform/internal/contacts	0.681s
=== RUN   TestContactImportWorkerPersistsPendingContactsAndResults
--- PASS: TestContactImportWorkerPersistsPendingContactsAndResults (0.71s)
=== RUN   TestContactImportRecoversJobStuckInProcessing
--- PASS: TestContactImportRecoversJobStuckInProcessing (0.35s)
=== RUN   TestContactImportEndToEndFromUploadToContacts
--- PASS: TestContactImportEndToEndFromUploadToContacts (0.36s)
=== RUN   TestCountContactImportRows
--- PASS: TestCountContactImportRows (0.00s)
=== RUN   TestCountContactImportRowsStopsAtLimit
--- PASS: TestCountContactImportRowsStopsAtLimit (0.08s)
=== RUN   TestAppendImportErrorCapsStoredDetails
--- PASS: TestAppendImportErrorCapsStoredDetails (0.00s)
PASS
ok  	github.com/khayode/email-platform/internal/worker	2.290s
=== RUN   TestContactImportAPIQueuesAndScopesProgress
--- PASS: TestContactImportAPIQueuesAndScopesProgress (0.50s)
PASS
ok  	github.com/khayode/email-platform/internal/api	1.272s
(0 skipped; TEST_DATABASE_URL set)

$ cd apps/web && npx playwright test
Running 1 test using 1 worker
  ✓  1 [chromium] › e2e/import.spec.ts:8:1 › CSV import: upload a 3-row file, see Import complete and 2 contacts (15.7s)
  1 passed (16.4s)
```

Test coverage behind the gate:

- `TestContactImportEndToEndFromUploadToContacts` — POST /api/v1/contacts/import
  (multipart, header + 2 valid + 1 invalid + 1 duplicate row) -> worker run once
  -> GET /api/v1/contact-imports/{id}: completed, total=4, imported=2, invalid=1,
  skipped=1, single row error with row=4 (header counted), contacts exist only in
  the importing organization, 2 list memberships.
- `TestContactImportRecoversJobStuckInProcessing` — a job stuck in `processing`
  with an expired lease is re-claimed and completed (crash recovery; the claim
  query uses FOR UPDATE SKIP LOCKED with a 5-minute lease).
- `TestImportCSVReaderPassesDuplicatesAndRejectsInvalidRows` — parser passes
  duplicates through (DB de-dupes) and errors on invalid emails.
- `TestContactImportAPIQueuesAndScopesProgress` — upload contract + cross-tenant
  404 on the status endpoint.
- Playwright `e2e/import.spec.ts` — logs in, uploads a 3-row CSV, sees
  "Import Complete" with Imported=2, then sees both contacts in the table.

Bugs found and fixed while running the real path:

1. `internal/db/migrate.go` — concurrent migrators (parallel `go test` packages)
   raced on `CREATE EXTENSION` and failed with
   `duplicate key value violates unique constraint "pg_extension_name_index"`.
   Fixed with a session-level `pg_advisory_lock` around the migration run.
2. `services/gateway/proxy.go` — the gateway's CORS middleware set
   `Access-Control-Allow-Origin` and the reverse proxy then copied the legacy
   API's own CORS headers with `Add`, producing
   `Access-Control-Allow-Origin: http://localhost:3000, http://localhost:3000`.
   Browsers reject that, so every cross-origin browser call to legacy endpoints
   failed (the import POST reached the server, but the UI saw a network error).
   Fixed with `ModifyResponse: stripUpstreamCORSHeaders` — the gateway is the
   single CORS authority. Regression test: `TestGatewayStripsUpstreamCORSHeaders`.
3. `import-dialog.tsx` — added a progress bar from `processed_rows/total_rows`,
   a real failed state (rose icon + `last_error` instead of a green check),
   and a 16 MiB-specific size message. Terminal statuses (completed/failed) stop
   polling; per-row error table + CSV error report already existed.

NOT done / not verified in P1:

- The >16 MiB server rejection (413) and wrong-file-type rejection are enforced
  in code (`maxCSVImportBytes`, `.csv` check) but have no automated test yet.
- The progress bar's visual rendering is not asserted by Playwright (the worker
  finishes within one poll interval).

## P2 — Account sync, sessions, and email (verified locally 2026-11-06)

Each required P2 path below has a Postgres or browser proof:

1. **Backfill and self-healing — Done.** `TestRunProjectsIdentityAndWorkspaceIdempotently`
   ran twice against local Postgres and verified one user, organization, and
   membership. `TestLegacyActorSelfHealsThroughGateway` exercised GET lists
   (200), POST contacts (201), and POST contacts/import (202) through the gateway
   for an identity actor missing from the legacy tables.
2. **Sessions and logout — Done.** `TestPostgresLogoutRevokesAccessToken` proves
   an access token works before logout and returns `401 session_revoked` after.
   `TestSessionMiddlewareFailsClosed` and
   `TestOptionalSessionMiddlewareFailsClosed` prove unavailable, inactive, and
   sessionless tokens do not reach protected handlers. Staging/production
   configuration now requires the Identity URL and service token.
3. **Refresh rotation — Done.** `TestPostgresRefreshRotationAndReuseRevokesSessionFamily`
   proves rotation, Secure/HttpOnly/SameSite=Lax cookie attributes, and family
   revocation on reuse. The browser test below asserts simultaneous protected
   requests share one refresh and do not loop.
4. **Transactional email — Done for the configured SMTP/Mailpit path.**
   Registration verification, password reset, and workspace invitation mail
   all arrived through Mailpit in the browser test. The Mailpit service was
   already present in both Compose files. Production SMTP and Resend variables
   and validation are documented in `.env.example`. Live Resend delivery was
   not exercised because no external Resend account was provided.
5. **Rate limits — Done.** `TestGatewayAuthRateLimit` asserts `Retry-After: 60`;
   `TestGatewayNormalAuthPageLoadStaysUnderRateLimit` makes ten `/auth/me`
   requests under the configured 60/minute limit.

Proof output:

```
$ TEST_DATABASE_URL=<local Docker Postgres> go test ./internal/backfill ./internal/api ./services/identity -run 'TestRunProjectsIdentityAndWorkspaceIdempotently|TestLegacyActorSelfHealsThroughGateway|TestPostgres' -v
--- PASS: TestRunProjectsIdentityAndWorkspaceIdempotently
--- PASS: TestLegacyActorSelfHealsThroughGateway
--- PASS: TestPostgresLogoutRevokesAccessToken
--- PASS: TestPostgresRefreshRotationAndReuseRevokesSessionFamily
PASS
```

```
$ go test ./internal/kit -run 'Test(SessionMiddleware|OptionalSession)' -v
--- PASS: TestSessionMiddlewareFailsClosed
--- PASS: TestOptionalSessionMiddlewareFailsClosed
PASS

$ go test ./services/identity ./services/workspace -run 'TestInternalInvitationEmailUsesConfiguredMailer|TestInvitationDeliveryFailureRevokesInvitation' -v
--- PASS: TestInternalInvitationEmailUsesConfiguredMailer
--- PASS: TestInvitationDeliveryFailureRevokesInvitation
PASS

$ go test ./services/gateway -run 'TestGateway(AuthRateLimit|NormalAuthPageLoadStaysUnderRateLimit)|TestLoadConfig' -v
--- PASS: TestGatewayAuthRateLimit
--- PASS: TestGatewayNormalAuthPageLoadStaysUnderRateLimit
PASS
```

```
$ E2E_BASE_URL=http://localhost:3000 E2E_API_ORIGIN=http://localhost:8084 MAILPIT_API_URL=http://localhost:8025 npm run test:e2e -- --grep 'P2 account email'
✓  1 [chromium] › e2e/p2-auth.spec.ts › P2 account email, password reset, and logout journey
1 passed
```

The browser flow registers, reads and opens the Mailpit verification link,
signs in, reads and submits a reset link, signs in with the new password, sends
an invitation through the workspace/Identity mail path, simulates a stale access
token and checks single-flight refresh, signs out, and confirms the same access
token is rejected with `session_revoked`.

The P2 changes pass `go vet ./...` and `go test ./...`; the web typecheck,
lint, unit tests (10/10), and production build also pass. The repo-wide
`gofmt -l cmd internal services shared` still reports the untouched files
`internal/api/automations.go` and `internal/automations/service.go`; those are
outside P2. Every Go file changed for P2 is formatted.

## P3 — Lists, tags, and segments (verified locally 2026-11-06)

The Postgres integration test exercises list create/rename/delete, accurate
member counts, paginated membership, add/remove, CSV export, tag assignment and
unassignment with updated counts, six segment-rule shapes with expected
contacts, OR evaluation, a ten-contact preview cap, malformed-rule 400s, and
cross-tenant 404s. It ran against local Docker Postgres with no skipped test.
The browser journey creates and edits a list, imports three CSV contacts into
it, exports its members, creates and assigns a tag, previews the
`tag is X AND list is Y` segment as exactly one contact, saves it, and confirms
the saved segment count before deleting the list through its confirmation.

```
$ TEST_DATABASE_URL=<local Docker Postgres> go test ./internal/api -run TestP3AudienceEndpointsAndTenantIsolationIntegration -count=1 -v
=== RUN   TestP3AudienceEndpointsAndTenantIsolationIntegration
=== RUN   TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_email
=== RUN   TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_status
=== RUN   TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_tag
=== RUN   TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_list
=== RUN   TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_created_at
=== RUN   TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_custom_field
--- PASS: TestP3AudienceEndpointsAndTenantIsolationIntegration (0.92s)
    --- PASS: TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_email (0.01s)
    --- PASS: TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_status (0.01s)
    --- PASS: TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_tag (0.01s)
    --- PASS: TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_list (0.01s)
    --- PASS: TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_created_at (0.01s)
    --- PASS: TestP3AudienceEndpointsAndTenantIsolationIntegration/segment_custom_field (0.01s)
PASS
ok      github.com/khayode/email-platform/internal/api    1.724s
```

```
$ E2E_BASE_URL=http://localhost:3000 E2E_API_ORIGIN=http://localhost:8084 npm run test:e2e -- --grep 'P3 list import'
✓  1 [chromium] › e2e/audience.spec.ts › P3 list import, tag assignment, and segment preview
1 passed (22.0s)
```

The OpenAPI YAML parses successfully. `docs/api/openapi.yaml` and the API
catalog document the audience routes and grouped segment schema.

## P4 — Signup forms (verified 2026-11-06)

  Proof gate output:

  ```
  $ TEST_DATABASE_URL="postgres://email:email@localhost:5432/email?sslmode=disable" \
      go test ./internal/api -run 'FormsLifecyclePublicSubmitAndTenantIsolationIntegration' -v -count=1
  === RUN   TestFormsLifecyclePublicSubmitAndTenantIsolationIntegration
  --- PASS: TestFormsLifecyclePublicSubmitAndTenantIsolationIntegration (0.14s)
  PASS
  ok   	github.com/khayode/email-platform/internal/api	0.542s

  $ cd apps/web && npx playwright test e2e/forms.spec.ts --reporter=line
  Running 1 test using 1 worker
  [1/1] [chromium] › e2e/forms.spec.ts:7:1 › signup form: publish, accept a logged-out submission, and record its audience
    1 passed (2.3s)
  ```

  The Go integration test uses the local Postgres database and covers create/get/
  update/delete, publish/unpublish, cross-organization reads, public definition,
  required-field and consent validation, the honeypot, rate limiting, suppression,
  submission pagination, a one-time double-opt-in confirmation, and global public
  slug uniqueness. Migration `0012_forms_global_slugs.sql` resolves existing
  cross-organization slug collisions and enforces a global unique index.
  The browser flow logs in, creates a list and tag, publishes a form, submits from
  `/f/{slug}` without an access token, and verifies the audience counts and
  submission record. The web typecheck, lint, unit tests (10/10), and production
  build were also successful in the task proof gate.

  This task is considered complete based on the real database-backed API test and
  browser-level proof in the local stack; the confirmation email path is verified
  through the injected sender callback and contact state transitions, not by a live
  external inbox.

## P5 — Templates and live campaign delivery (verified 2026-11-06)

Proof gate output:

```
$ set -a && source .env && set +a && go test ./internal/delivery ./internal/worker -run 'Test(ParseEnvelopeAddress|CampaignTemplateDeliveryIntegration|EnsureSTARTTLS|ClassifySMTPResponse|FormatMessageID)$' -v
=== RUN   TestEnsureSTARTTLS
=== RUN   TestEnsureSTARTTLS/advertised
=== RUN   TestEnsureSTARTTLS/development_plaintext_fallback
=== RUN   TestEnsureSTARTTLS/production_requires_starttls
--- PASS: TestEnsureSTARTTLS (0.00s)
=== RUN   TestClassifySMTPResponse
--- PASS: TestClassifySMTPResponse (0.00s)
=== RUN   TestFormatMessageID
--- PASS: TestFormatMessageID (0.00s)
=== RUN   TestParseEnvelopeAddress
--- PASS: TestParseEnvelopeAddress (0.00s)
PASS
ok   github.com/khayode/email-platform/internal/delivery
=== RUN   TestCampaignTemplateDeliveryIntegration
... PASS: TestCampaignTemplateDeliveryIntegration (0.21s)
PASS
ok   github.com/khayode/email-platform/internal/worker
```

This verifies the real delivery path for the P5 campaign/template flow: the
worker creates a live campaign and template, sends the message through the SMTP
transport, and Mailpit receives the campaign email. The fixes included:

- `internal/worker/worker.go` — `unsubscribe_url` is now present in the
  personalization payload passed to the message renderer.
- `internal/worker/worker.go` — bounce metadata writes cast the enum values to
  text before inserting into `message_events` so the job can complete without a
  Postgres type error.
- `internal/delivery/smtp.go` — the SMTP envelope uses the raw email address for
  `MAIL FROM`/`RCPT TO` while preserving the display name in the header, which
  keeps Mailpit and real SMTP servers happy.
- `internal/worker/campaign_template_delivery_integration_test.go` — the proof
  harness now authenticates with a real token and uses unique test data so the
  full send path is exercised without collisions.

## P6 — Sending domains and DNS verification (done)

Proof gate (targeted package check):

```
$ go test ./internal/api -run 'TestCheckDomainDNS' -v
=== RUN   TestCheckDomainDNSVerified
--- PASS: TestCheckDomainDNSVerified (0.00s)
=== RUN   TestCheckDomainDNSReportsMismatchesAndMissing
--- PASS: TestCheckDomainDNSReportsMismatchesAndMissing (0.00s)
PASS
ok   github.com/khayode/email-platform/internal/api
```

Fix declaration: the sending-domain verification path is now enforced using a
real resolver-backed TXT check, including the Sendwise verification record
`_sendwise-verification.<domain>`, and the domain is only marked verified when the
verification token, SPF, DKIM, and DMARC checks all pass. The verification logic
uses a resolver interface with a configurable DNS server and short timeout, and
all mismatched or missing records are classified correctly. This closes the
remaining P6 verification gap that previously relied only on SPF/DKIM/DMARC while
ignoring the ownership token.

### Task D follow-up — domain records and page stability (2026-11-06)

The domains UI now renders the API's DNS record response, including the ownership
TXT record, and no longer asks users to paste a DKIM key. Domain creation
generates a 2048-bit RSA keypair and stores the private key encrypted with
`DKIM_ENCRYPTION_KEY`. SPF records use the configured `SPF_INCLUDE` or `mx`
mechanism; DNS lookup errors are shown separately from missing records.

Evidence:

```
$ TEST_DATABASE_URL=<local Compose Postgres URL> go test -count=1 ./internal/api -run 'Domain|DNSRecords|NormalizeDkim|GenerateDKIM' -v
--- PASS: TestDomainsIntegration
--- PASS: TestCheckDomainDNSVerified
--- PASS: TestCheckDomainDNSReportsMismatchesAndMissing
--- PASS: TestCheckDomainDNSRejectsUnconfiguredSPFInclude
--- PASS: TestCheckDomainDNSReportsLookupFailed
--- PASS: TestCheckDomainDNSNotFoundIsMissing
--- PASS: TestDNSRecordsForIncludesSendwiseVerificationRecord
--- PASS: TestDNSRecordsForTrackingCNAMEAndConfiguredSPF
--- PASS: TestNormalizeDkimTXTValuePreservesCaseAndJoinsChunks
--- PASS: TestGenerateDKIMKeyPairUses2048BitRSAAndEncryptedPrivateKey
PASS

$ cd apps/web && npm run test:e2e -- e2e/domains.spec.ts
✓ 1 [chromium] › sending domains: generated records, verification, and no browser errors
1 passed
```

Production-mode reproduction with pending, verified, misconfigured, NULL/empty
DKIM keys, NULL check timestamps, and mixed DNS statuses produced no browser
`pageerror` or `console.error`; the reported exception was not reproducible in
the current worktree. The Playwright test stubs the verify response to exercise
the UI state; the database-backed Go integration test exercises the resolver
with partial and fully matching fake DNS records. The browser gate now fails on
either browser error.

## P7 — Automation runtime and due-step execution (done)

Proof gate (real Postgres + Mailpit):

```
$ TEST_DATABASE_URL='postgres://email:email@127.0.0.1:5432/email?sslmode=disable' \
    go test ./internal/automations -run 'TestServiceProcessDue' -v -count=1
=== RUN   TestServiceProcessDueSendsEmailForActiveAutomation
--- PASS: TestServiceProcessDueSendsEmailForActiveAutomation (0.09s)
=== RUN   TestServiceProcessDueSkipsPausedAutomations
--- PASS: TestServiceProcessDueSkipsPausedAutomations (0.03s)
PASS
ok   github.com/khayode/email-platform/internal/automations 0.528s
```

Fix declaration: the automation runtime now respects the active/pause state,
starts a due execution in `running` state, processes pending steps in order, and
sends the one-step email workflow through the real SMTP transport to Mailpit. The
service also guards against nil loggers and fixes the template lookup to read the
actual `templates.name` / `html` columns instead of a non-existent `subject`
column. The new automation creation page and list redirects are wired to
`/automations/new` so the UI can actually create and activate a flow.

## P8 — Settings, billing, reports, and placeholder cleanup (done)

Proof gate:

```
$ grep -rniE 'coming soon|coming-soon|localhost|127\.0\.0\.1|lorem|TODO|demo' apps/web \
    --include='*.ts' --include='*.tsx' --exclude-dir=node_modules --exclude-dir=.next
(no output)

$ go test ./services/workspace ./internal/auth ./internal/api -count=1
ok   github.com/khayode/email-platform/services/workspace  0.465s
ok   github.com/khayode/email-platform/internal/auth       0.644s
ok   github.com/khayode/email-platform/internal/api       0.503s

$ cd apps/web && npm run typecheck && npm run lint && npm test -- --runInBand && npm run build
... pass ...
```

The settings page and selected workspace flows are wired; billing and reporting
remain partial. No claim is made here that every page is complete or that the
app-wide source is free of placeholders. The Playwright config is
host-overridable and defaults to the browser-visible local endpoint.

## P9 — End-to-end suite and release gate (partly verified)

Proof gate:

The repository does have Playwright E2E tests; the suite is not a complete
product-wide journey. Verified browser flows include forms, imports, P2 account
email/session behavior, P3 audience management, and the sending-domain UI
response path. Treat each flow as verified only to the scope of its recorded
test output below or in the corresponding P2/P3/P6 section.

```
$ make e2e
[...]
  ✓  1 [chromium] › e2e/forms.spec.ts:7:1 › signup form: publish, accept a logged-out submission, and record its audience
  ✓  1 [chromium] › e2e/import.spec.ts:8:1 › CSV import: upload a 3-row file, see Import complete and 2 contacts
  2 passed (7.0s)

$ scripts/smoke-prod.sh
[2026-11-06 11:03:59] Running production smoke checks against https://campaign.pipsleague.com
[2026-11-06 11:03:59] ok  https://campaign.pipsleague.com/healthz (200)
[2026-11-06 11:03:59] ok  page-signature https://campaign.pipsleague.com/login
[2026-11-06 11:03:59] ok  auth-protected https://campaign.pipsleague.com/api/v1/auth/me (401)
[2026-11-06 11:03:59] Production smoke checks passed
```

The browser E2E suite, `make e2e` shortcut, CI gate, and read-only production
smoke script exist and the outputs above passed. The full cross-module
registration-to-delivery journey is not yet covered, so P9 is not marked Done.

## Build / checks

- `go vet ./...` — pass
- `gofmt -l cmd internal services shared` — reports the pre-existing,
  untouched `internal/api/automations.go` and `internal/automations/service.go`;
  no P2/P3-modified Go files are reported.
- `go test ./...` — all packages pass; Postgres-backed integration proofs are
  run separately with `TEST_DATABASE_URL` and recorded above.
- web: `npm run typecheck` — pass
- web: `npm run lint` — pass (tsc --noEmit)
- web: `npm run build` — pass
- web: `npm test` — 10/10 pass

## Modules

- Auth: email verification, password reset, refresh rotation, logout, and
  fail-closed session revocation — P2 Postgres and Mailpit proofs above.
- Contacts CSV import: worker consumes queued imports; tests in `internal/contacts` pass; row errors reported.
- Forms: Done; CRUD, globally unique public slugs, hosted submissions,
  suppression/rate-limit handling, and double-opt-in confirmation are covered by
  `TestFormsLifecyclePublicSubmitAndTenantIsolationIntegration` and
  `apps/web/e2e/forms.spec.ts`.
- Automations: runtime engine and trigger flow are implemented and proven via `TestServiceProcessDueSendsEmailForActiveAutomation` and `TestServiceProcessDueSkipsPausedAutomations`.
- Campaigns: editor/preview/test send; backend campaigns package tests pass.
- Lists/tags/segments: list CRUD/membership/export, tag CRUD and contact
  assignments, grouped segment evaluation, preview, and pagination — proven by
  the P3 Postgres and Playwright outputs above.
- Templates: list/create UI and backend exist; update/delete, full design
  editing, and end-to-end template delivery from the UI remain incomplete or
  unverified.
- Sending domains: CRUD/DNS record UI and backend verification are implemented;
  DNS resolution has fake-resolver integration coverage, while live DNS and
  production-provider behavior remain unverified. See P6 evidence above.
- Settings: workspace settings and membership/invitation flows are wired and
  exercised in the P2 browser journey. Billing/usage is partial and has no
  billing-provider end-to-end proof.
- Reports: reporting page and API are present; report accuracy and complete
  campaign analytics remain unverified.
- Delivery: worker + suppression; tests pass.
- Projections backfill: `internal/projections` tests pass.

## Not working / known limitations

### Pre-deploy blockers

1. **Domains page crash — Done via the required fallback.** The reported crash
   was not reproducible against the local production build and live gateway/API.
   `StatusBadge` now accepts a missing or null status and renders `unknown`;
   the dashboard error boundary remains in place. The real-stack Playwright test
   seeds pending, verified, and misconfigured records, including NULL/empty
   DKIM keys and NULL `last_checked_at`, and fails on any page error or
   `console.error`.

   ```
   $ E2E_BASE_URL=http://localhost:3000 E2E_API_ORIGIN=http://localhost:8084 MAILPIT_API_URL=http://localhost:8025 npm run test:e2e -- e2e/domains.spec.ts --reporter=line
   Running 1 test using 1 worker
   [1/1] [chromium] › e2e/domains.spec.ts:92:1 › sending domains load from the real API without browser errors
   1 passed (3.7s)
   ```

   Screenshots: `apps/web/test-results/domains-sending-domains-lo-b5a4a--API-without-browser-errors-chromium/domains-empty.png`
   (0 domains) and `.../domains-one-unverified-one-verified.png` (1
   unverified, 1 verified). The API domain tests also pass with Postgres:
   `go test ./internal/api -run Domain -count=1 -v` — `TestDomainsIntegration`
   and all five DNS resolver tests pass.

2. **CI and formatting — Done for code SHA `6f44a9e` (Actions run #32).**
   `gofmt -l cmd internal services shared` returned no paths. The old
   integration-test registration flow and API-key creation defects were fixed;
   the concurrent 401 refresh race now reuses a token another request has
   already refreshed. `go vet ./...` and `go test ./...` passed against a
   newly created, isolated local Postgres database (`TEST_DATABASE_URL` set,
   no tests skipped). Web typecheck, lint, unit tests (10 passed), and
   production build passed. Full local Playwright passed all five tests:

   ```
   $ go vet ./...
   (no diagnostics; exit status 0)
   $ go test ./...
   ok  github.com/khayode/email-platform/internal/api
   ok  github.com/khayode/email-platform/internal/auth
   ok  github.com/khayode/email-platform/internal/automations
   ok  github.com/khayode/email-platform/internal/backfill
   ok  github.com/khayode/email-platform/internal/campaigns
   ok  github.com/khayode/email-platform/internal/config
   ok  github.com/khayode/email-platform/internal/contacts
   ok  github.com/khayode/email-platform/internal/db
   ok  github.com/khayode/email-platform/internal/delivery
   ok  github.com/khayode/email-platform/internal/kit
   ok  github.com/khayode/email-platform/internal/outbox
   ok  github.com/khayode/email-platform/internal/projections
   ok  github.com/khayode/email-platform/internal/worker
   ok  github.com/khayode/email-platform/services/gateway
   ok  github.com/khayode/email-platform/services/identity
   ok  github.com/khayode/email-platform/services/identity/cmd/devseed
   ok  github.com/khayode/email-platform/services/workspace
   ok  github.com/khayode/email-platform/shared/contracts/events

   $ npm run typecheck
   > tsc --noEmit
   (exit status 0)
   $ npm run lint
   > tsc --noEmit
   (exit status 0)
   $ npm test
   ℹ tests 10
   ℹ pass 10
   ℹ fail 0

   $ npm run test:e2e -- --reporter=line
   Running 5 tests using 1 worker
   [1/5] audience.spec.ts — passed
   [2/5] domains.spec.ts — passed
   [3/5] forms.spec.ts — passed
   [4/5] import.spec.ts — passed
   [5/5] p2-auth.spec.ts — passed
   5 passed (31.5s)
   ```

   One earlier full-suite attempt timed out at the second smoke-account login
   in `forms.spec.ts`; that test passed alone and the complete suite passed on
   rerun. GitHub Actions run [#26](https://github.com/mike-kayode/Sendwise-Email/actions/runs/42250453929)
   rejected the NATS service's sequence-form `command` before starting jobs;
   this is now scalar. Run [#27](https://github.com/mike-kayode/Sendwise-Email/actions/runs/42250703034)
   for `6bd9672` confirmed Go vet, formatting, and build, but failed Compose
   validation because the microservices file is an override and cannot be
   validated alone; the Node 20 web test job also lacks
   `--experimental-strip-types`. CI now validates the base and merged Compose
   files, uses Node 22, and installs Chromium before browser tests. Run
   [#28](https://github.com/mike-kayode/Sendwise-Email/actions/runs/42251089769)
   passed Compose validation and all web checks, but Go integration tests failed
   because CI had no Mailpit service (`SMTP 127.0.0.1:1025` refused connections;
   the automation send test processed zero executions). Run
   [#30](https://github.com/mike-kayode/Sendwise-Email/actions/runs/42251455277)
   passed Go, lint, web, and Docker-build jobs, but the aggregate E2E run hit
   HTTP 429 on its final revoked-token assertion after the five browser specs
   shared the gateway's production-default 60/minute limit. The E2E-only
   Compose override now raises the limit/burst; gateway unit tests still verify
   the production default and Retry-After behavior. A Mailpit service and
   explicit SMTP test settings are also included. Actions run
   [#31](https://github.com/mike-kayode/Sendwise-Email/actions/runs/42252436239)
   completed successfully for SHA `30c0866`; all five jobs passed:

   ```
   lint: success
   test-go: success
   test-web: success
   e2e: success
   docker-build: success
   ```

   The status-only commit was also verified by Actions run #32 on SHA
   `6f44a9e`; all five jobs passed.

3. **DKIM signing — Done with local Postgres and Mailpit proof.** Application-side
   signing was selected: the worker and automation runtime load the encrypted
   per-domain key and sign the serialized RFC message immediately before SMTP
   handoff. Compose passes `DKIM_ENCRYPTION_KEY` to both the API (automation
   runtime) and worker. The signing test sends a campaign through SMTP to
   Mailpit, fetches the raw received message, and verifies its signature
   against the matching published TXT value.

   ```
   $ TEST_DATABASE_URL='postgres://email:email@127.0.0.1:5432/email?sslmode=disable' \
       go test ./internal/worker -run TestCampaignTemplateDeliveryIntegration -v -count=1
   === RUN   TestCampaignTemplateDeliveryIntegration
   {"time":"2026-11-06T17:58:47.739976+01:00","level":"ERROR","msg":"send verification email","service":"worker","env":"test","error":"resend API key not configured"}
   {"time":"2026-11-06T17:58:47.740208+01:00","level":"INFO","msg":"http request","service":"worker","env":"test","method":"POST","path":"/api/v1/auth/register","status":201,"duration_ms":42,"request_id":"ed86f58e-9dda-48b1-b928-1c7cb7789b55"}
   {"time":"2026-11-06T17:58:47.820803+01:00","level":"INFO","msg":"http request","service":"worker","env":"test","method":"POST","path":"/api/v1/lists","status":201,"duration_ms":8,"request_id":"d8d0d054-ce5f-476a-8272-6f228a851aa7"}
   {"time":"2026-11-06T17:58:47.828209+01:00","level":"INFO","msg":"http request","service":"worker","env":"test","method":"POST","path":"/api/v1/contacts","status":201,"duration_ms":7,"request_id":"1b1ffaeb-c56e-43cd-9f7e-4c2397faa575"}
   {"time":"2026-11-06T17:58:47.834283+01:00","level":"INFO","msg":"http request","service":"worker","env":"test","method":"POST","path":"/api/v1/templates","status":201,"duration_ms":3,"request_id":"bcbd7e77-2385-4b77-a5af-bbaf56094b63"}
   {"time":"2026-11-06T17:58:47.838955+01:00","level":"INFO","msg":"http request","service":"worker","env":"test","method":"POST","path":"/api/v1/campaigns","status":201,"duration_ms":4,"request_id":"a71ac675-1873-4b6c-9567-81a662cb0e32"}
   {"time":"2026-11-06T17:58:47.854536+01:00","level":"WARN","msg":"no message publisher configured; delivery jobs remain queued","service":"worker","env":"test","campaign_id":"61bc1d0f-a344-4f04-8b02-e7b768967b7d"}
   {"time":"2026-11-06T17:58:47.855076+01:00","level":"INFO","msg":"http request","service":"worker","env":"test","method":"POST","path":"/api/v1/campaigns/61bc1d0f-a344-4f04-8b02-e7b768967b7d/send","status":202,"duration_ms":15,"request_id":"0ff5cd98-692f-4d1e-9a25-351b83daef9e"}
       campaign_template_delivery_integration_test.go:195: verified DKIM signature on Mailpit message 50GAtbssxT5qUdgQgSDeNw against campaign-test._domainkey.example.com
   --- PASS: TestCampaignTemplateDeliveryIntegration (0.23s)
   PASS
   ok  	github.com/khayode/email-platform/internal/worker 1.236s
   ```

   Full Go tests against local Postgres, `go vet ./...`, `gofmt -l .`, web
   typecheck/lint/tests/build, and the real-gateway domains Playwright spec also
   passed. The broader browser suite was not green: the audience scenario timed
   out waiting for dashboard navigation after submitting its default smoke
   credentials; only the domains spec was rerun and passed. The test's missing
   Resend key log is for the test account's separate verification email; no
   external email was sent. Production mail delivery remains unverified under
   blocker 4.

   GitHub Actions run [#33](https://github.com/mike-kayode/Sendwise-Email/actions/runs/42257868460)
   for SHA `ab57547c574525940f34c32d7b132e09a6c58564` completed successfully:

   ```
   lint: success
   test-go: success
   test-web: success
   e2e: success
   docker-build: success
   ```

   The subsequent status-only Actions run [#34](https://github.com/mike-kayode/Sendwise-Email/actions/runs/42258811907)
   passed `test-go`, `test-web`, `lint`, and `docker-build`, but its E2E job
   timed out in `p2-auth.spec.ts` waiting for a successful invitations response.
   The DKIM code was unchanged from run #33. The subsequent final-SHA Actions
   run [#35](https://github.com/mike-kayode/Sendwise-Email/actions/runs/42259993023)
   for SHA `4a542410b73fa1071105ebd5e26f57b33d11163d` passed all five jobs, so
   the run #34 failure did not reproduce.

4. **Production mail guard — Done (code SHA `7b144a7`, Actions run #37
   success).** `internal/kit/mailguard.go` exposes
   `kit.ValidateProductionMail(env, provider, smtpAddr, smtpHost)`, called
   unconditionally from `internal/config/config.go` and
   `services/identity/config.go` — unconditional because campaign delivery
   always uses SMTP even when transactional mail uses Resend. It rejects
   staging/production when the SMTP target is mailpit/localhost/127.0.0.1/
   ::1/0.0.0.0, when `MAIL_PROVIDER=log`, or when the provider is unknown.
   Compose passes `MAIL_PROVIDER` through to api and worker.

   ```
   $ go test ./internal/kit -run 'MailGuard|ValidateProductionMail' -count=1 -v
   === RUN   TestValidateProductionMail/production_smtp_relay_is_mailpit
   === RUN   TestValidateProductionMail/production_smtp_host_is_localhost
   === RUN   TestValidateProductionMail/production_smtp_address_is_loopback_ip
   === RUN   TestValidateProductionMail/production_provider_is_log
   === RUN   TestValidateProductionMail/production_provider_is_log_without_smtp_settings
   === RUN   TestValidateProductionMail/production_rejects_unknown_provider
   === RUN   TestValidateProductionMail/staging_rejects_log_provider
   === RUN   TestValidateProductionMail/staging_rejects_mailpit
   === RUN   TestValidateProductionMail/production_accepts_a_real_relay
   === RUN   TestValidateProductionMail/production_accepts_auto_with_a_real_relay
   === RUN   TestValidateProductionMail/production_accepts_resend_with_no_smtp_target
   === RUN   TestValidateProductionMail/development_allows_mailpit
   === RUN   TestValidateProductionMail/test_allows_the_log_mailer
   === RUN   TestValidateProductionMail/empty_environment_is_not_validated
   --- PASS: TestValidateProductionMail (0.00s)
       --- PASS: TestValidateProductionMail/production_smtp_relay_is_mailpit (0.00s)
       --- PASS: TestValidateProductionMail/production_smtp_host_is_localhost (0.00s)
       --- PASS: TestValidateProductionMail/production_smtp_address_is_loopback_ip (0.00s)
       --- PASS: TestValidateProductionMail/production_provider_is_log (0.00s)
       --- PASS: TestValidateProductionMail/production_provider_is_log_without_smtp_settings (0.00s)
       --- PASS: TestValidateProductionMail/production_rejects_unknown_provider (0.00s)
       --- PASS: TestValidateProductionMail/staging_rejects_log_provider (0.00s)
       --- PASS: TestValidateProductionMail/staging_rejects_mailpit (0.00s)
       --- PASS: TestValidateProductionMail/production_accepts_a_real_relay (0.00s)
       --- PASS: TestValidateProductionMail/production_accepts_auto_with_a_real_relay (0.00s)
       --- PASS: TestValidateProductionMail/production_accepts_resend_with_no_smtp_target (0.00s)
       --- PASS: TestValidateProductionMail/development_allows_mailpit (0.00s)
       --- PASS: TestValidateProductionMail/test_allows_the_log_mailer (0.00s)
       --- PASS: TestValidateProductionMail/empty_environment_is_not_validated (0.00s)
   PASS
   ok  	github.com/khayode/email-platform/internal/kit	0.375s
   ```

   `go run ./cmd/api` and `go run ./services/identity/cmd/identity` both exit
   1 against a real relay/log configuration, and the container-level
   `docker compose run` demos exit 1 the same way. Full `go test ./...` with
   `TEST_DATABASE_URL` set, web typecheck/lint/10 unit tests/build, and
   `e2e/p2-auth.spec.ts` all pass. **Not done:** no external verification
   email was sent — production is off-limits, so the send-to-an-external-
   address half is deferred to post-deploy.

5. **Server environment — Done for everything reachable without touching
   production (code SHA `82e4fc2`, Actions run #38 success).**
   `scripts/check-env.sh` now validates required names (`DKIM_ENCRYPTION_KEY`,
   `IDENTITY_URL`, `API_BASE_URL`, `SMTP_*`, …) without ever printing values,
   rejects a localhost `API_BASE_URL`, `MAIL_PROVIDER=log`, dev SMTP relays,
   missing SMTP creds and resend-without-Resend-keys in production, and
   `--no-local` applies server semantics. `scripts/deploy.sh` runs it on every
   path so it cannot be skipped and additionally requires
   `DKIM_ENCRYPTION_KEY`. `.env.example` now uses the public origin
   `https://campaign.pipsleague.com` for `API_BASE_URL`/`APP_BASE_URL`/
   `TRACKING_BASE_URL`/`NEXT_PUBLIC_API_URL`.

   ```
   $ ./scripts/check-env.sh
   env ok (APP_ENV=development, file=/Users/iam_khayode/email-platform/.env, required names: 12 declared, secure-mail checks: with-local-overrides)
   EXIT=0

   $ ./scripts/check-env.sh --no-local
   API_BASE_URL must be the public URL in APP_ENV=production, not a localhost address
   EXIT=1

   $ (with DKIM_ENCRYPTION_KEY removed from the env file)
   missing required env var: DKIM_ENCRYPTION_KEY
   EXIT=1

   $ ./scripts/deploy.sh backup
   ... Configuration validation passed
   ... Running scripts/check-env.sh...
   ...   check-env.sh passed
   ... backup written to backups/<stamp>
   EXIT=0
   ```

   `gofmt -l`, `go vet ./...`, `go test ./...` with `TEST_DATABASE_URL`
   (no tests skipped), web typecheck/lint/10 unit tests/build, and the full
   Playwright suite — `5 passed (28.0s)` — all pass. **Not done:** the
   server's own `.env` was never read or edited (production is off-limits);
   the gate plus the deploy checklist are the deliverable, and running it on
   the real server is post-deploy.

6. **Migrations 0013–0015 on a restored copy of real data — Done for a real
   backup; the production-server half is deferred.** A fresh backup was taken
   with `./scripts/deploy.sh backup` (`backups/20261106190651`), restored into
   a scratch database `email_migration_scratch`, and0013/0014/0015 were
   reverted to their pre-migration shape (columns dropped, single-group
   segment rules unwrapped to `{"match","conditions"}`, the three
   `schema_migrations` rows deleted) before replaying the migrator.

   BEFORE (restored backup) and the reverted baseline:

   ```
   == BEFORE counts ==
   users                  92
   organizations          101
   organization_members   99
   contacts               87
   segments               7
   sending_domains        66
   schema_migrations      16
   == BEFORE segment rules shapes ==
   null / groups=array|7
   == REVERTED (baseline) ==
   users                  92
   organizations          101
   organization_members   99
   contacts               87
   segments               7
   sending_domains        66
   schema_migrations      13
   == columns (reverted) ==
   created_at, dkim_public_key, dkim_selector, dkim_status, dmarc_status, domain, id, last_checked_at, organization_id, spf_status, status, updated_at, verified_at
   == rules shapes (reverted) ==
   array / groups=null|7
   ```

   Replay and AFTER:

   ```
   $ DATABASE_URL='postgres://email:email@localhost:5432/email_migration_scratch?sslmode=disable' \
       MIGRATIONS_DIR=./migrations APP_ENV=development go run ./cmd/migrate
   {"time":"2026-11-06T19:41:05.059302+01:00","level":"INFO","msg":"migrations applied","service":"migrate","env":"development","directory":"./migrations"}
   migrate exit=0

   == AFTER counts (must equal baseline) ==
   users                  92
   organizations          101
   organization_members   99
   contacts               87
   segments               7
   sending_domains        66
   == AFTER columns ==
   created_at, dkim_private_key, dkim_public_key, dkim_selector, dkim_status, dmarc_status, domain, id, last_checked_at, organization_id, spf_status, status, updated_at, verification_status, verified_at
   == AFTER migrations ==
   16 max=0015_segments_grouped_rules.sql dirty=3
   == AFTER rules shapes ==
   null / groups=array|7
   == AFTER verification_status backfill ==
   pending|36
   verified|30
   == AFTER status vs verification_status agreement ==
   misconfigured -> pending|15
   not_configured -> pending|1
   pending -> pending|20
   verified -> verified|30
   == AFTER dkim_private_key populated (real data) ==
   0|66
   ```

   Every row count is unchanged, `0014` backfilled exactly the 30 domains that
   were already `verified`, and no key material was invented (the column is
   NULL until each domain is generated). All 7 migrated segment rules compile
   through the app's own `contacts.CompileSegmentRules`:

   ```
   $ DATABASE_URL=...email_migration_scratch go run ./cmd/segcheck_tmp
   ok   156b7863: 2 args, ((EXISTS (SELECT 1 FROM tags ...)) AND EXISTS (SELECT 1 FROM contact_lists ...)))
   ok   580ba425: 2 args, ...
   ok   89c3faf8: 2 args, ...
   ok   a37d80e3: 2 args, ...
   ok   d6835643: 2 args, ...
   ok   dc3a5601: 2 args, ...
   ok   f633b662: 2 args, ...
   compiled 7/7 segments (0 failed)
   segcheck exit=0
   ```

   A second `go run ./cmd/migrate` is idempotent (still 16 rows). The three
   sample segment rows are byte-identical before and after. Gates after the
   change: `gofmt -l` empty, `go vet ./...` exit 0, `go test ./...` with
   `TEST_DATABASE_URL` all `ok`, web typecheck/lint/10 unit tests/build exit
   0, and `e2e/audience.spec.ts` (the segments spec) `1 passed (6.9s)`; the
   full suite `5 passed (28.0s)`. **Not done:** the production server's
   database was never accessed — production is off-limits, so this ran on a
   restored copy of a real local backup rather than on the server itself.

7. **Backfill on DB copies + deploy step — Done for restored copies of real
   data; the production-server half is deferred.** `backups/20261106190651`
   was restored into three scratch databases
   (`identity_migration_scratch`, `workspace_migration_scratch`,
   `legacy_migration_scratch`) and `cmd/backfill` was run against them.

   ```
   == SOURCE identity_migration_scratch ==
   users 39
   == SOURCE workspace_migration_scratch ==
   organizations 39
   memberships 40
   == TARGET legacy_migration_scratch BEFORE ==
   users                  92
   organizations          101
   organization_members   99
   contacts               87

   $ IDENTITY_DATABASE_URL=...identity_migration_scratch \
     WORKSPACE_DATABASE_URL=...workspace_migration_scratch \
     LEGACY_DATABASE_URL=...legacy_migration_scratch \
     go run ./cmd/backfill
   Backfill complete: users=39 organizations=39 memberships=40
   BACKFILL_EXIT=0

   == TARGET legacy AFTER run 1 ==
   users                  96
   organizations          105
   organization_members   104

   $ (same command, run 2 — idempotency)
   Backfill complete: users=39 organizations=39 memberships=40
   BACKFILL2_EXIT=0
   == TARGET legacy AFTER run 2 (must equal run 1) ==
   users                  96
   organizations          105
   organization_members   104
   ```

   Row counts move exactly by the source deltas (+4/+4/+5; the remaining
   source rows already existed) and the second run changes nothing. Every
   source record is projected verbatim (exported and compared, since Postgres
   cannot join across databases):

   ```
   identity users:       39 | projected verbatim into legacy:       39
   workspace memberships: 40 | projected verbatim into legacy:       40
   workspace organizations: 39 | projected verbatim into legacy:     39
   dup emails: 0 | dup org slugs: 0
   ```

   The same binary runs from the new Compose target against the same scratch
   databases:

   ```
   $ docker compose run --rm -e IDENTITY_DATABASE_URL=... -e WORKSPACE_DATABASE_URL=... -e LEGACY_DATABASE_URL=... backfill
   Backfill complete: users=39 organizations=39 memberships=40
   CONTAINER_BACKFILL_EXIT=0
   == counts after container run (must equal 96/105/104) ==
   users                  96
   organizations          105
   organization_members   104
   ```

   Deploy wiring: a `backfill` image target in `Dockerfile`, a `backfill`
   Compose service, and an opt-in `run_backfill` in `scripts/deploy.sh` that
   runs after migrations and before `start_stack` when `DEPLOY_BACKFILL=1`
   (off by default, because it writes to the legacy schema). Verified:

   ```
   $ bash -n scripts/deploy.sh
   BASH_SYNTAX_OK
   $ docker compose --env-file .env -f docker-compose.yml -f docker-compose.microservices.yml config -q
   COMPOSE_OK
   $ docker compose build backfill
   Image email-platform-backfill Built
   BUILD_EXIT=0
   $ go vet ./...
   VET_EXIT=0
   $ TEST_DATABASE_URL=... go test ./... -count=1
   ... all packages ok (including internal/backfill), TEST_EXIT=0
   ```

   **Not done:** the production database was never accessed and
   `DEPLOY_BACKFILL=1` has never been run during a real deployment — both are
   post-deploy, since production is off-limits.

### Remaining non-blocking limitations

- P6 live DNS verification has only fake-resolver coverage.
- P7–P9 lack a full registration-to-campaign-delivery browser journey.
- Templates do not have complete update/delete and full-editor workflows.
- Report accuracy is unverified; billing has no provider end-to-end proof.

## Deploy steps

1. Copy `.env.example` to `.env`, fill secrets (every variable name in
   `.env.example` must be declared in `.env`).
2. Run `scripts/check-env.sh`.
3. `scripts/deploy.sh` (backup, build, migrate, up, smoke).
4. Rollback: `scripts/deploy.sh rollback --yes` (destructive, restores DB backup).
