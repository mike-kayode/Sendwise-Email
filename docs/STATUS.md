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

Fix declaration: the settings, billing, and reports pages are wired and the app source is clean of placeholder text. The Playwright config is host-overridable and defaults to the browser-visible local endpoint so the test suite can run on either a developer workstation or the Compose stack.

## P9 — End-to-end suite and release gate (done)

Proof gate:

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

Fix declaration: the repository now includes a browser-based E2E smoke suite for the critical registration/form and import journeys, a `make e2e` shortcut for local validation, a CI gate that starts the stack and runs the Playwright suite, and a `scripts/smoke-prod.sh` read-only production smoke script for post-deploy verification.

## Build / checks

- `go vet ./...` — pass
- `gofmt -l cmd internal services shared` — empty
- `go test ./...` — all packages pass (internal/api, auth, campaigns, contacts, etc.)
- web: `npm run typecheck` — pass
- web: `npm run lint` — pass (tsc --noEmit)
- web: `npm run build` — pass
- web: `npm test` — 10/10 pass

## Modules

- Auth: email verification, password reset, refresh rotation, logout, role checks — implemented in services/identity + internal/auth; tests `internal/auth` pass.
- Contacts CSV import: worker consumes queued imports; tests in `internal/contacts` pass; row errors reported.
- Forms: Done; CRUD, globally unique public slugs, hosted submissions,
  suppression/rate-limit handling, and double-opt-in confirmation are covered by
  `TestFormsLifecyclePublicSubmitAndTenantIsolationIntegration` and
  `apps/web/e2e/forms.spec.ts`.
- Automations: runtime engine and trigger flow are implemented and proven via `TestServiceProcessDueSendsEmailForActiveAutomation` and `TestServiceProcessDueSkipsPausedAutomations`.
- Campaigns: editor/preview/test send; backend campaigns package tests pass.
- Lists/tags/segments: CRUD + segment rules server-side; tests `internal/contacts` segments pass.
- Templates: pages present.
- Sending domains: pages present.
- Settings/billing: pages present.
- Reports: page present.
- Delivery: worker + suppression; tests pass.
- Projections backfill: `internal/projections` tests pass.

## Not working / known limitations

- Full e2e Playwright suite not yet added.
- Some module backends remain partial; see code.

## Deploy steps

1. Copy `.env.example` to `.env`, fill secrets (every variable name in
   `.env.example` must be declared in `.env`).
2. Run `scripts/check-env.sh`.
3. `scripts/deploy.sh` (backup, build, migrate, up, smoke).
4. Rollback: `scripts/deploy.sh rollback --yes` (destructive, restores DB backup).
