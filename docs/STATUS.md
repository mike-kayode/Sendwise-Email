# Sendwise Status

Local evidence below was recorded 2026-11-09 from the development stack and
GitHub Actions run #69. Production and follow-up evidence is dated separately.

## Part 1 — transactional registration email

**Locally verified.** Identity registration creates a pending account and durable
verification-mail job without waiting for the SMTP relay. The worker retries
delivery; provider errors are retained for diagnosis. Resend requests are rate
limited per account and IP, and mail readiness reports invalid sender
configuration as degraded. The legacy API registration and password-recovery
routes return `410 Gone`; the gateway sends these flows to Identity, avoiding a
second synchronous mail path.

The Postgres-backed Go suite and browser flow passed. The browser test stopped
the local Mailpit relay, registered successfully, restarted the relay, and
observed the retry arrive in Mailpit. This proves local relay acceptance only,
not delivery to an external inbox.

## Part 2 — backend hardening

**Locally verified.** Go integration tests exercise sender-identity ownership,
campaign send validation, sender-profile compliance, per-record DNS verification
and resolver parallelism. Unknown or foreign senders are rejected server-side.
The DNS timing test uses a fake resolver; the times below are simulated and are
not a measurement against public or authoritative DNS.

The domain-duplicate precheck passes against the local database. A missing
sending-domain table is treated as an empty pre-migration database; detected
duplicates fail the check. The CI workflow runs this check before and after the
database migration.

## Local verification output

### Go, vet, and build

Command:

```text
$ go test -count=1 -p 1 -timeout 45m ./...
```

Output:

```text
?   	github.com/khayode/email-platform/cmd/api	[no test files]
?   	github.com/khayode/email-platform/cmd/backfill	[no test files]
?   	github.com/khayode/email-platform/cmd/migrate	[no test files]
?   	github.com/khayode/email-platform/cmd/worker	[no test files]
ok  	github.com/khayode/email-platform/internal/api	8.067s
ok  	github.com/khayode/email-platform/internal/auth	0.706s
ok  	github.com/khayode/email-platform/internal/automations	0.503s
ok  	github.com/khayode/email-platform/internal/backfill	5.948s
ok  	github.com/khayode/email-platform/internal/campaigns	0.350s
ok  	github.com/khayode/email-platform/internal/config	0.311s
ok  	github.com/khayode/email-platform/internal/contacts	0.297s
ok  	github.com/khayode/email-platform/internal/db	0.411s
ok  	github.com/khayode/email-platform/internal/delivery	0.520s
?   	github.com/khayode/email-platform/internal/httpx	[no test files]
ok  	github.com/khayode/email-platform/internal/kit	0.322s
?   	github.com/khayode/email-platform/internal/messaging	[no test files]
?   	github.com/khayode/email-platform/internal/observability	[no test files]
ok  	github.com/khayode/email-platform/internal/outbox	1.074s
ok  	github.com/khayode/email-platform/internal/projections	0.339s
?   	github.com/khayode/email-platform/internal/session	[no test files]
?   	github.com/khayode/email-platform/internal/testutil	[no test files]
ok  	github.com/khayode/email-platform/internal/worker	3.234s
ok  	github.com/khayode/email-platform/services/gateway	0.546s
?   	github.com/khayode/email-platform/services/gateway/cmd/gateway	[no test files]
ok  	github.com/khayode/email-platform/services/identity	3.674s
ok  	github.com/khayode/email-platform/services/identity/cmd/devseed	0.444s
?   	github.com/khayode/email-platform/services/identity/cmd/identity	[no test files]
ok  	github.com/khayode/email-platform/services/workspace	0.361s
?   	github.com/khayode/email-platform/services/workspace/cmd/workspace	[no test files]
ok  	github.com/khayode/email-platform/shared/contracts/events	0.284s
```

`go vet ./...`, `go build ./...`, and `gofmt -l cmd internal services shared`
also completed successfully; the formatting check printed no files.

After removing Identity verification delivery from the Stalwart campaign test,
the focused local Stalwart SMTP and app-side DKIM integration test passed:

```text
=== RUN   TestStalwartAcceptsAppSignedTenantCampaign
    stalwart_integration_test.go:116: verified one app DKIM signature for authenticated submission from hosted domain hosted-tenant.example.test
--- PASS: TestStalwartAcceptsAppSignedTenantCampaign (0.75s)
PASS
ok  	github.com/khayode/email-platform/services/identity	1.649s
```

## Transactional email API and dashboard

**Backend locally verified against the Compose Postgres database.** Transactional
API keys support creation, rotation, revocation, scopes, test mode, and quotas.
Workspace templates have tenant-scoped CRUD; content changes append immutable
versions, and sends render and record the selected version. Delivery feedback,
webhook dispatch, and their retry/outbox data are covered by Go tests.

The web dashboard now reads live workspace settings, usage, keys, webhooks, and
recent messages. It passed typecheck and production build. A browser-driven
transactional credential lifecycle journey is recorded in the follow-up
evidence below.
Sending domains expose an owner/admin control for transactional use; it can only
be enabled after SPF and DKIM verification, while DMARC remains advisory.
Focused Postgres tests include
`TestTransactionalAPIKeySendReadRotateAndRevoke`,
`TestTransactionalTemplateVersionsRenderInMessageSend`, and
`TestTransactionalSendingDomainRequiresVerifiedSPFAndDKIM`.

Verification:

```text
$ set -o pipefail && TEST_DATABASE_URL='postgres://email:email@127.0.0.1:55432/email?sslmode=disable' go test -count=1 -p 1 -timeout 45m ./... 2>&1 | grep -E '^(\?|ok|FAIL)'
?   	github.com/khayode/email-platform/cmd/api	[no test files]
?   	github.com/khayode/email-platform/cmd/backfill	[no test files]
?   	github.com/khayode/email-platform/cmd/migrate	[no test files]
?   	github.com/khayode/email-platform/cmd/worker	[no test files]
ok  	github.com/khayode/email-platform/internal/api	21.354s
ok  	github.com/khayode/email-platform/internal/auth	0.303s
ok  	github.com/khayode/email-platform/internal/automations	0.784s
ok  	github.com/khayode/email-platform/internal/backfill	8.260s
ok  	github.com/khayode/email-platform/internal/campaigns	0.577s
ok  	github.com/khayode/email-platform/internal/config	0.554s
ok  	github.com/khayode/email-platform/internal/contacts	0.431s
ok  	github.com/khayode/email-platform/internal/db	0.620s
ok  	github.com/khayode/email-platform/internal/delivery	1.224s
?   	github.com/khayode/email-platform/internal/httpx	[no test files]
ok  	github.com/khayode/email-platform/internal/kit	0.502s
?   	github.com/khayode/email-platform/internal/messaging	[no test files]
?   	github.com/khayode/email-platform/internal/observability	[no test files]
ok  	github.com/khayode/email-platform/internal/outbox	1.610s
ok  	github.com/khayode/email-platform/internal/projections	0.523s
?   	github.com/khayode/email-platform/internal/session	[no test files]
?   	github.com/khayode/email-platform/internal/testutil	[no test files]
ok  	github.com/khayode/email-platform/internal/transactional	5.188s
ok  	github.com/khayode/email-platform/internal/worker	4.733s
ok  	github.com/khayode/email-platform/services/gateway	0.603s
?   	github.com/khayode/email-platform/services/gateway/cmd/gateway	[no test files]
ok  	github.com/khayode/email-platform/services/identity	3.369s
ok  	github.com/khayode/email-platform/services/identity/cmd/devseed	0.569s
?   	github.com/khayode/email-platform/services/identity/cmd/identity	[no test files]
ok  	github.com/khayode/email-platform/services/workspace	0.591s
?   	github.com/khayode/email-platform/services/workspace/cmd/workspace	[no test files]
ok  	github.com/khayode/email-platform/shared/contracts/events	0.616s

$ go vet ./... && echo 'go vet: exit 0' && go build ./... && echo 'go build: exit 0'
go vet: exit 0
go build: exit 0

$ cd apps/web && npm run typecheck && npm run lint && npm test && npm run build
> @email-platform/web@0.1.0 typecheck
> tsc --noEmit

> @email-platform/web@0.1.0 lint
> tsc --noEmit
ℹ tests 11
ℹ pass 11
ℹ fail 0
✓ Compiled successfully in 1638ms
✓ Finished TypeScript in 5.4s
✓ Generating static pages using 7 workers (27/27) in 642ms
├ ○ /transactional

```

The SMTP listener and local Stalwart failure-DSN path are verified in the
follow-up evidence below. No external-inbox delivery was performed, and no
production systems were accessed or changed. Production DSN collection remains
unconfigured; the local test polls Stalwart over JMAP and submits the report to
the signed webhook directly.

### Web checks

Commands: `npm --prefix apps/web run typecheck`,
`npm --prefix apps/web run lint`, `npm --prefix apps/web test`, and
`npm --prefix apps/web run build`.

Raw unit-test output:

```text
✔ apiUrl uses same-origin paths by default and normalizes configured bases
✔ login location removes all query values while retaining a safe callback
✔ login submits credentials only in a POST JSON body with cookies enabled
✔ network fetch failures become a clear login message without hiding HTTP errors
✔ CSV decoding accepts UTF-8 BOM and Windows-1252 content
✔ CSV preview handles UTF-8 BOM, semicolons, quoted commas, and multiline fields
✔ headerless email files keep their first row and receive a usable email mapping
✔ headerless files can have the email column after other fields
✔ column mapping uses the API contract and maps optional fields as custom fields
✔ custom columns receive validated, stable field keys
ℹ tests 10
ℹ suites 0
ℹ pass 10
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
```

### Playwright

Command: `npm --prefix apps/web run test:e2e -- --reporter=line`, with the local
Compose project and Mailpit recovery enabled.

```text
Running 12 tests using 1 worker
[1/12] P3 list import, tag assignment, and segment preview
[2/12] campaign send plan enforces warm-up cap with a verified sender profile
[3/12] DNS copy fallback works from a real insecure HTTP origin
[4/12] sending domains load from the real API without browser errors
[5/12] signup form: publish, accept a logged-out submission, and record its audience
[6/12] CSV import: upload a 3-row file, see Import complete and 2 contacts
[7/12] P2 account email, password reset, and logout journey
[8/12] register, import an audience, edit a template, send, and verify campaign reports
[9/12] mail worker retries queued verification after the SMTP relay recovers
[10/12] queued verification delivery offers a resend action
[11/12] expired or already-used verification link offers a helpful recovery
[12/12] successful verification displays a confirmation
1 skipped
11 passed (1.3m)
```

The full suite's insecure-origin test was skipped because
`E2E_INSECURE_ORIGIN` was not configured for that run. A separate real
insecure-origin run is recorded below. The fake-resolver timing test printed:

```text
=== RUN   TestDomainDNSParallelLatencyComparedWithSequentialLookups
    domains_test.go:61: simulated DNS check latency: sequential_before=164ms parallel_after=41ms
--- PASS: TestDomainDNSParallelLatencyComparedWithSequentialLookups (0.20s)
PASS
ok  	github.com/khayode/email-platform/internal/api	1.651s
```

### Deployment and cleanup checks

`./scripts/deploy.sh validate`, shell syntax checks, and
`scripts/check-domain-duplicates.sh` passed locally:

```text
[2026-11-09 23:28:54] Comparing .env.example variable names against .env...
[2026-11-09 23:28:54]   every .env.example variable name is declared in .env
[2026-11-09 23:28:54] Validating configuration (environment: development)...
[2026-11-09 23:28:54] Configuration validation passed
[2026-11-09 23:28:54] Running scripts/check-env.sh...
env ok (APP_ENV=development, file=/var/folders/1z/k9hrrg297rdcyym2r11vxy480000gn/T/tmp.hZoycEEGfV, required names: 15 declared, secure-mail checks: strict)
[2026-11-09 23:28:54]   check-env.sh passed
[2026-11-09 23:28:54] Validating docker-compose.yml...
[2026-11-09 23:28:55] Validating the combined Compose configuration...
[2026-11-09 23:28:55]   all Compose configurations are valid
[2026-11-09 23:28:55] Deploy validation checks passed
Duplicate sending domains (domain, organization_id, status):
No duplicate sending domains found.
```

Repository cleanup search output:

```text
Legacy hardcoded footer: No matches found.
Retired mail configuration in .env.example and scripts/check-env.sh: No matches found.
Provider-specific references in campaign code: No matches found.
```

### GitHub Actions

Run [#69](https://github.com/mike-kayode/Sendwise-Email/actions/runs/42716796181)
for commit `c9bc94e` completed successfully. `deploy-validate`, `lint`,
`test-go`, `test-web`, `e2e`, and `docker-build` all succeeded.

The browser suite and dedicated relay-recovery Playwright proof reported:

```text
  2 skipped
  10 passed (51.1s)
...
  1 passed (3.4s)
```

The two skipped cases require optional E2E settings; the separate relay-recovery
spec ran and passed.

## Production and follow-up evidence — 2026-11-10

- Production Stalwart is running and reports `healthy`. Its active platform
  DKIM key is `v1-rsa-20261110`; the public record is available from the
  provisioner output. External STARTTLS certificate validation succeeded.
- An authenticated SMTP test using `hello@pipsleague.com` was accepted by
  Stalwart (`SENT-OK`). This proves submission acceptance only; inbox delivery
  and placement have not been confirmed.
- Both authoritative DNS servers and Cloudflare's resolver still return the
  existing SPF and DMARC values, with no platform DKIM TXT record and no
  `bounces.pipsleague.com` MX record. The existing apex MX records remain
  unchanged. The required DNS changes have not been published.
- Hostinger's DNS-zone GET returns HTTP 403 with
  `[DNS:4002] Customer does not own pipsleague.com domain`; its ownership
  verifier reports `is_accessible=false`. The hPanel browser page is
  unauthenticated, so the DNS records could not be changed from this session.
- The dedicated clipboard fallback test passed on a real insecure HTTP origin:

  ```text
  E2E_INSECURE_ORIGIN=http://192.168.0.188:3000 npx playwright test clipboard-insecure-origin.spec.ts --reporter=line
  1 passed (1.6s)
  ```

- Current local validation passed: `go vet ./...`, `go test ./...`,
  `go build ./...`, web typecheck/lint/tests/build, Compose configuration,
  shell syntax, and `git diff --check`. The web tests reported 10 passed and
  0 failed.

## Not done / not independently verified

- Production-provider delivery to an external inbox; local Mailpit is not that
  proof.
- Publication and propagation of the required production DKIM, SPF, bounce MX,
  and DMARC records. The API ownership error currently prevents programmatic
  updates, and hPanel changes require an authenticated account session.
- Production bounce collection. The Stalwart inbound route/collector is not
  configured to forward real DSNs or ARFs to Sendwise.
- Redistribution rights and user-provided Figma exports/images for any bundled
  artwork.
- Any unrelated legacy module not demonstrated by the commands above. Prior
  status claims outside this report are not treated as current evidence.

## Follow-up verification — 2026-11-14

The local authenticated SMTP submission path is implemented. Its
Postgres-backed tests, `TestTransactionalSMTPCredentialLifecycle` and
`TestTransactionalSMTPSubmissionRequiresTLSAuthAndEnqueues`, ran as part of the
full Go suite. They cover credential lifecycle, TLS-required authentication,
sender validation, and enqueuing. The SMTP listener is disabled by default;
Compose uses loopback-only host bindings on ports 2587 (STARTTLS) and 2465
(implicit TLS), distinct from Stalwart's ports.

The opt-in integration test exercised a real local Stalwart failure report:
the test submitted a message to a reserved `.invalid` recipient, retrieved the
resulting DSN via JMAP, posted it to the feedback webhook, and checked the
Postgres message and recipient statuses. Temporary credentials were generated
for this loopback-only run and were not recorded.

```text
$ RUN_STALWART_INTEGRATION=1 TEST_DATABASE_URL='postgres://email:email@127.0.0.1:5432/email?sslmode=disable' go test -count=1 -p 1 -timeout 45m ./internal/api -run '^TestLocalStalwartDSNUpdatesTransactionalRecipient$' -v
=== RUN   TestLocalStalwartDSNUpdatesTransactionalRecipient
{"time":"2026-11-14T11:02:56.337284+01:00","level":"INFO","msg":"http request","service":"api","env":"test","method":"POST","path":"/api/v1/webhooks/stalwart-feedback","status":204,"duration_ms":17,"request_id":"a0ec899e-bfec-46a5-9ab5-68aa7d2bcd93"}
--- PASS: TestLocalStalwartDSNUpdatesTransactionalRecipient (0.83s)
PASS
ok  	github.com/khayode/email-platform/internal/api	1.534s
```

The SMTP credential and authenticated submission integration checks also
passed against local Postgres:

```text
$ TEST_DATABASE_URL='postgres://email:email@127.0.0.1:5432/email?sslmode=disable' go test -count=1 -p 1 -timeout 45m ./internal/api -run '^TestTransactionalSMTP(CredentialLifecycle|SubmissionRequiresTLSAuthAndEnqueues)$' -v
=== RUN   TestTransactionalSMTPCredentialLifecycle
--- PASS: TestTransactionalSMTPCredentialLifecycle (0.12s)
=== RUN   TestTransactionalSMTPSubmissionRequiresTLSAuthAndEnqueues
--- PASS: TestTransactionalSMTPSubmissionRequiresTLSAuthAndEnqueues (0.07s)
PASS
ok  	github.com/khayode/email-platform/internal/api	1.545s
```

The browser-driven transactional dashboard journey registered and verified a
local owner through Mailpit, signed in, created an SMTP credential, confirmed
the one-time username/password display and dismissal, then revoked the
credential.

```text
$ cd apps/web && npm run test:e2e -- e2e/transactional-dashboard.spec.ts --reporter=line
Running 1 test using 1 worker
[1/1] [chromium] › e2e/transactional-dashboard.spec.ts:30:1 › owner manages transactional SMTP credentials in the dashboard
  1 passed (7.1s)
```

Final local validation:

```text
$ TEST_DATABASE_URL='postgres://email:email@127.0.0.1:5432/email?sslmode=disable' go test -count=1 -p 1 -timeout 45m ./...
?   	github.com/khayode/email-platform/cmd/api	[no test files]
?   	github.com/khayode/email-platform/cmd/backfill	[no test files]
?   	github.com/khayode/email-platform/cmd/migrate	[no test files]
?   	github.com/khayode/email-platform/cmd/worker	[no test files]
ok  	github.com/khayode/email-platform/internal/api	16.237s
ok  	github.com/khayode/email-platform/internal/auth	0.872s
ok  	github.com/khayode/email-platform/internal/automations	3.313s
ok  	github.com/khayode/email-platform/internal/backfill	8.789s
ok  	github.com/khayode/email-platform/internal/campaigns	0.668s
ok  	github.com/khayode/email-platform/internal/config	0.498s
ok  	github.com/khayode/email-platform/internal/contacts	0.475s
ok  	github.com/khayode/email-platform/internal/db	0.583s
ok  	github.com/khayode/email-platform/internal/delivery	0.890s
?   	github.com/khayode/email-platform/internal/httpx	[no test files]
ok  	github.com/khayode/email-platform/internal/kit	0.597s
?   	github.com/khayode/email-platform/internal/messaging	[no test files]
?   	github.com/khayode/email-platform/internal/observability	[no test files]
ok  	github.com/khayode/email-platform/internal/outbox	1.448s
ok  	github.com/khayode/email-platform/internal/projections	0.548s
?   	github.com/khayode/email-platform/internal/session	[no test files]
?   	github.com/khayode/email-platform/internal/testutil	[no test files]
ok  	github.com/khayode/email-platform/internal/transactional	5.146s
ok  	github.com/khayode/email-platform/internal/worker	4.976s
ok  	github.com/khayode/email-platform/services/gateway	0.675s
?   	github.com/khayode/email-platform/services/gateway/cmd/gateway	[no test files]
ok  	github.com/khayode/email-platform/services/identity	3.451s
ok  	github.com/khayode/email-platform/services/identity/cmd/devseed	0.571s
?   	github.com/khayode/email-platform/services/identity/cmd/identity	[no test files]
ok  	github.com/khayode/email-platform/services/workspace	0.565s
?   	github.com/khayode/email-platform/services/workspace/cmd/workspace	[no test files]
ok  	github.com/khayode/email-platform/shared/contracts/events	0.615s
$ go vet ./... && go build ./... && test -z "$(gofmt -l cmd internal services shared)"
go vet, build, and gofmt: passed
$ cd apps/web && npm run typecheck && npm run lint && npm test && npm run build
typecheck and lint passed
tests: 11 passed, 0 failed
Next.js production build completed successfully
```

This proves only the local Compose/Stalwart flow and dashboard journey. It is
not proof of external inbox delivery, production SMTP configuration, or
automatic production DSN collection; those remain unverified/not configured.

## HTML templates and admin security follow-up — 2026-11-14

The HTML editor now retains original full-document imports without GrapesJS
parsing, offers exact find-and-replace for source text/links/image URLs/colors,
and shows server-rendered desktop/mobile send previews with the editable
compliance footer. A delayed visual-editor update is prevented from replacing
an original-mode design. The browser journey uses the repository's
representative NairaTrader-style fixture; the original supplied attachment was
not present in the workspace, so that exact file is not independently
verified.

Focused browser proof against local Compose, Gateway, API, worker, Postgres,
and Mailpit:

```text
$ E2E_COMPOSE_PROJECT=email-platform npm --prefix apps/web run test:e2e -- --grep "register, import an audience"
Running 1 test using 1 worker
  ✓  [chromium] › e2e/template-delivery.spec.ts:86:1 › register, import an audience, edit a template, send, and verify campaign reports (17.1s)
  1 passed (18.5s)
```

The complete Go suite ran with `TEST_DATABASE_URL` set against local Postgres
and passed. `services/identity` also passed its append-only audit integration
test against an isolated Postgres schema:

```text
$ TEST_DATABASE_URL='<local Identity Postgres URL>' go test ./services/identity -run TestPostgresAdminAuditIsAppendOnly -count=1 -v
=== RUN   TestPostgresAdminAuditIsAppendOnly
--- PASS: TestPostgresAdminAuditIsAppendOnly (0.24s)
PASS
ok  	github.com/khayode/email-platform/services/identity	1.580s
```

The Super Admin security foundation, a limited Identity/Workspace-backed
overview, read-only user search/list/detail,
reasoned verification resend and manual verification, user activation/
suspension and session revocation, workspace and
membership metadata, global domain metadata/details and audited DNS re-check,
audited domain disable/enable enforced across campaign and transactional
sending paths, audited Sendwise record/sender-identity removal, and
super_admin domain-conflict resolution,
privacy-safe global campaign metadata/delivery counts and super_admin campaign
cancellation, limited live operational counts/rates/queue depth, metadata search
across users/workspaces/domains/campaigns and delivery identifiers, and
super_admin-only global suppression listing/add/removal with reasoned, fresh-MFA
writes are verified, as are audit-log filtering/export, but the
full console is **not done**. User reads require fresh MFA and exclude password
hashes and customer content. Account status, manual verification, and session
revocation require super_admin, a reason, and MFA re-verification within five
minutes; support_admin can request a reasoned verification resend. Suspension
revokes active sessions, and state changes/session revocation commit atomically
with their audit entry.
The Workspace service exposes privacy-safe name, slug, creation time, member
counts, and membership roles/user IDs through a service-token-protected
internal endpoint. The web UI provides MFA enrollment/verification, user
metadata and account controls, workspace detail, audit views, global metadata
search, and reasoned campaign cancellation.

```text
$ TEST_DATABASE_URL='<local Identity Postgres URL>' go test ./services/identity -run 'TestPostgresAdmin(UserStatusChangeRevokesSessionsAndAuditsAtomically|EmailVerificationWritesOutboxAndAuditAtomically|UsersSearchIsPaginatedAndMetadataOnly)|Test(AdminUser(SuspensionRequiresRecentMFAReasonAndRevokesSessions|DetailRequiresFreshMFAAndReturnsOnlyMetadata)|SupportAdminCanReasonablyResendPendingUserVerification|SuperAdminCanManuallyVerifyPendingUserEmailWithReason|RevokedAdminSessionCannotAccessAdminRoutes)' -count=1 -v
=== RUN   TestPostgresAdminUsersSearchIsPaginatedAndMetadataOnly
--- PASS: TestPostgresAdminUsersSearchIsPaginatedAndMetadataOnly
=== RUN   TestPostgresAdminUserStatusChangeRevokesSessionsAndAuditsAtomically
--- PASS: TestPostgresAdminUserStatusChangeRevokesSessionsAndAuditsAtomically
=== RUN   TestPostgresAdminEmailVerificationWritesOutboxAndAuditAtomically
--- PASS: TestPostgresAdminEmailVerificationWritesOutboxAndAuditAtomically
=== RUN   TestRevokedAdminSessionCannotAccessAdminRoutes
--- PASS: TestRevokedAdminSessionCannotAccessAdminRoutes
=== RUN   TestAdminUserDetailRequiresFreshMFAAndReturnsOnlyMetadata
--- PASS: TestAdminUserDetailRequiresFreshMFAAndReturnsOnlyMetadata
=== RUN   TestAdminUserSuspensionRequiresRecentMFAReasonAndRevokesSessions
--- PASS: TestAdminUserSuspensionRequiresRecentMFAReasonAndRevokesSessions
=== RUN   TestSupportAdminCanReasonablyResendPendingUserVerification
--- PASS: TestSupportAdminCanReasonablyResendPendingUserVerification
=== RUN   TestSuperAdminCanManuallyVerifyPendingUserEmailWithReason
--- PASS: TestSuperAdminCanManuallyVerifyPendingUserEmailWithReason
PASS
ok  	github.com/khayode/email-platform/services/identity

$ TEST_DATABASE_URL='<local Postgres URL>' go test ./internal/api -run 'TestInternalAdminCampaign(DirectoryRequiresServiceTokenAndReturnsMetadataOnly|StopRequiresServiceTokenAndCancelsPendingWork)' -count=1 -v
=== RUN   TestInternalAdminCampaignDirectoryRequiresServiceTokenAndReturnsMetadataOnly
--- PASS: TestInternalAdminCampaignDirectoryRequiresServiceTokenAndReturnsMetadataOnly (0.27s)
=== RUN   TestInternalAdminCampaignStopRequiresServiceTokenAndCancelsPendingWork
--- PASS: TestInternalAdminCampaignStopRequiresServiceTokenAndCancelsPendingWork (0.12s)
PASS
ok  	github.com/khayode/email-platform/internal/api 2.380s
$ TEST_DATABASE_URL='<local Identity Postgres URL>' go test ./services/identity -run 'TestAdminCampaign(DirectoryRequiresPlatformPermissionAndForwardsFilters|StopIsSuperAdminOnlyReasonedAndAudited)|TestHTTPAdminCampaign' -count=1 -v
=== RUN   TestAdminCampaignDirectoryRequiresPlatformPermissionAndForwardsFilters
--- PASS: TestAdminCampaignDirectoryRequiresPlatformPermissionAndForwardsFilters (0.31s)
=== RUN   TestAdminCampaignStopIsSuperAdminOnlyReasonedAndAudited
--- PASS: TestAdminCampaignStopIsSuperAdminOnlyReasonedAndAudited (0.20s)
=== RUN   TestHTTPAdminCampaignClientUsesServiceAuthAndFilters
--- PASS: TestHTTPAdminCampaignClientUsesServiceAuthAndFilters (0.00s)
=== RUN   TestHTTPAdminCampaignStopUsesServiceAuthAndMapsConflicts
--- PASS: TestHTTPAdminCampaignStopUsesServiceAuthAndMapsConflicts (0.00s)
PASS
ok  	github.com/khayode/email-platform/services/identity 1.698s

$ TEST_DATABASE_URL='<local Identity Postgres URL>' go test ./services/identity -run 'TestPostgresAdminAuditIsAppendOnly|TestAdminWorkspaceListingRequiresMFAAndUsesWorkspaceService' -count=1 -v
=== RUN   TestPostgresAdminAuditIsAppendOnly
--- PASS: TestPostgresAdminAuditIsAppendOnly
=== RUN   TestAdminWorkspaceListingRequiresMFAAndUsesWorkspaceService
--- PASS: TestAdminWorkspaceListingRequiresMFAAndUsesWorkspaceService
PASS
ok  	github.com/khayode/email-platform/services/identity

$ TEST_DATABASE_URL='<local Workspace Postgres URL>' go test ./services/workspace -run 'TestPostgresAdminOrganizationListingFiltersAndCountsMembers|TestInternalAdminOrganizationListingRequiresServiceToken' -count=1 -v
=== RUN   TestPostgresAdminOrganizationListingFiltersAndCountsMembers
--- PASS: TestPostgresAdminOrganizationListingFiltersAndCountsMembers
=== RUN   TestInternalAdminOrganizationListingRequiresServiceToken
--- PASS: TestInternalAdminOrganizationListingRequiresServiceToken
PASS
ok  	github.com/khayode/email-platform/services/workspace

$ TEST_DATABASE_URL='<local Identity Postgres URL>' go test ./services/identity -run 'TestAdminOverviewRequiresMFAAndReturnsServiceOwnedCounts|TestPostgresAdminUsersSearchIsPaginatedAndMetadataOnly' -count=1 -v
=== RUN   TestPostgresAdminUsersSearchIsPaginatedAndMetadataOnly
--- PASS: TestPostgresAdminUsersSearchIsPaginatedAndMetadataOnly
=== RUN   TestAdminOverviewRequiresMFAAndReturnsServiceOwnedCounts
--- PASS: TestAdminOverviewRequiresMFAAndReturnsServiceOwnedCounts
PASS
ok  	github.com/khayode/email-platform/services/identity

$ npm --prefix apps/web run typecheck
> tsc --noEmit

$ npm --prefix apps/web test
ℹ tests 11
ℹ pass 11
ℹ fail 0

$ npm --prefix apps/web run build
✓ Compiled successfully
✓ Generating static pages
Routes include /admin/campaigns and /admin/search.
```

Workspace suspend/resume is now wired through the admin detail page, Identity's
super_admin/fresh-MFA/reason/audit boundary, and the API service's
service-token-protected status endpoint. The campaign worker rejects suspended
workspaces; transactional API/SMTP enqueue and worker validation also reject
them. Identity unit tests and HTTP client contract tests pass. The API's real
Postgres integration case is present but was skipped because no
`TEST_DATABASE_URL`/`INTEGRATION_DATABASE_URL` was available:

```text
$ go test ./internal/api -run TestInternalAdminWorkspaceStatusRequiresServiceTokenAndCanSuspendWorkspace -count=1 -v
=== RUN   TestInternalAdminWorkspaceStatusRequiresServiceTokenAndCanSuspendWorkspace
    api_integration_test.go:489: no database available
--- SKIP: TestInternalAdminWorkspaceStatusRequiresServiceTokenAndCanSuspendWorkspace (0.00s)
PASS
ok  	github.com/khayode/email-platform/internal/api
```

Full workspace management beyond audited suspension/resumption, Stalwart/NATS/worker health and sender rankings,
campaign controls beyond stop/cancel and message operations, remaining
capability navigation, transactional
message actions beyond metadata search, impersonation,
destructive-action flows beyond implemented user status/session/role removal,
and admin Playwright flows remain unimplemented. Admin role promotion remains
operator-CLI-only; super_admin role removal is available through the audited
user detail action and cannot remove the last active super_admin. The role
demotion handler and store have unit and local Postgres integration coverage:

```text
$ go test ./services/identity -run TestSuperAdminCanOnlyDemotePlatformRolesWithReason -count=1 -v
=== RUN   TestSuperAdminCanOnlyDemotePlatformRolesWithReason
--- PASS: TestSuperAdminCanOnlyDemotePlatformRolesWithReason (0.09s)
PASS
ok  	github.com/khayode/email-platform/services/identity 1.268s

$ TEST_DATABASE_URL='<local Identity Postgres URL>' go test ./services/identity -run TestPostgresAdminRoleDemotionRevokesSessionsAndProtectsLastSuperAdmin -count=1 -v
=== RUN   TestPostgresAdminRoleDemotionRevokesSessionsAndProtectsLastSuperAdmin
--- PASS: TestPostgresAdminRoleDemotionRevokesSessionsAndProtectsLastSuperAdmin (0.17s)
PASS
ok  	github.com/khayode/email-platform/services/identity 0.664s
```

Workspace internal notes are now append-only records in the Identity admin
audit log. Only super_admin can read or add them; adding a note requires a
separate reason and recent MFA. Support-admin audit results redact note text and
reasons. The handler authorization/redaction test and real-Postgres workspace
scoping test are included in the Identity suite:

```text
$ TEST_DATABASE_URL='<local Identity Postgres URL>' go test ./services/identity -run 'TestWorkspaceNotesAreAuditedAndSuperAdminOnly|TestPostgresAdminWorkspaceNotesAreAuditedAndWorkspaceScoped' -count=1 -v
=== RUN   TestPostgresAdminWorkspaceNotesAreAuditedAndWorkspaceScoped
--- PASS: TestPostgresAdminWorkspaceNotesAreAuditedAndWorkspaceScoped (0.65s)
=== RUN   TestWorkspaceNotesAreAuditedAndSuperAdminOnly
--- PASS: TestWorkspaceNotesAreAuditedAndSuperAdminOnly (0.10s)
PASS
ok  	github.com/khayode/email-platform/services/identity 1.444s
```

Super admins can also request a standard one-hour password-reset link for an
active, verified account. The action is rate limited, recent-MFA protected, and
audited with its required reason; only the token hash is persisted. The
`TestSuperAdminCanSendPasswordResetToActiveVerifiedUser` Identity handler test
and real-Postgres single-use token test passed:

```text
$ TEST_DATABASE_URL='<local Identity Postgres URL>' go test ./services/identity -run TestPostgresAdminPasswordResetUsesHashedSingleUseToken -count=1 -v
=== RUN   TestPostgresAdminPasswordResetUsesHashedSingleUseToken
--- PASS: TestPostgresAdminPasswordResetUsesHashedSingleUseToken (0.18s)
PASS
ok  	github.com/khayode/email-platform/services/identity 1.029s
```

The global domain directory now reads from the API service's database through
an internal service-token route and exposes only workspace/domain status
metadata. A separate detail endpoint returns stored resolver results, and
super_admin can request an audited DNS re-check. List/detail responses omit
verification tokens and direct DKIM key material. These flows were verified
against local Postgres:

```text
$ TEST_DATABASE_URL='<local legacy Postgres URL>' go test ./internal/api -run TestInternalAdminDomainDirectoryRequiresServiceTokenAndReturnsMetadataOnly -count=1 -v
=== RUN   TestInternalAdminDomainDirectoryRequiresServiceTokenAndReturnsMetadataOnly
--- PASS: TestInternalAdminDomainDirectoryRequiresServiceTokenAndReturnsMetadataOnly (0.13s)
PASS
ok  	github.com/khayode/email-platform/internal/api 1.513s

$ TEST_DATABASE_URL='<local Identity Postgres URL>' go test ./services/identity -run 'TestAdminSendingDomainDirectoryUsesAPIServiceMetadata|TestHTTPAdminDomainDetailsClientReturnsResolversAndMapsNotFound' -count=1 -v
=== RUN   TestAdminSendingDomainDirectoryUsesAPIServiceMetadata
--- PASS: TestAdminSendingDomainDirectoryUsesAPIServiceMetadata (0.07s)
=== RUN   TestHTTPAdminDomainDetailsClientReturnsResolversAndMapsNotFound
--- PASS: TestHTTPAdminDomainDetailsClientReturnsResolversAndMapsNotFound (0.00s)
PASS
ok  	github.com/khayode/email-platform/services/identity 1.065s
```

No production systems were accessed or changed.

Global suppression management was verified against local Postgres and the
Identity permission/audit tests:

```text
$ TEST_DATABASE_URL='<local Postgres URL>' go test ./internal/api -run TestInternalAdminSuppressionsRequireServiceTokenAndSupportAuditedOperations -count=1 -v
=== RUN   TestInternalAdminSuppressionsRequireServiceTokenAndSupportAuditedOperations
--- PASS: TestInternalAdminSuppressionsRequireServiceTokenAndSupportAuditedOperations (0.15s)
PASS
ok  	github.com/khayode/email-platform/internal/api	1.370s

$ go test ./services/identity -run 'TestAdminSuppressionsAreSuperAdminOnlyReasonedAndAudited|TestHTTPAdminSuppressionClientUsesServiceAuthAndMapsConflicts|TestAdminPermissionMatrixSeparatesPlatformAndWorkspaceRoles' -count=1 -v
=== RUN   TestAdminPermissionMatrixSeparatesPlatformAndWorkspaceRoles
--- PASS: TestAdminPermissionMatrixSeparatesPlatformAndWorkspaceRoles (0.00s)
=== RUN   TestAdminSuppressionsAreSuperAdminOnlyReasonedAndAudited
--- PASS: TestAdminSuppressionsAreSuperAdminOnlyReasonedAndAudited (0.21s)
=== RUN   TestHTTPAdminSuppressionClientUsesServiceAuthAndMapsConflicts
--- PASS: TestHTTPAdminSuppressionClientUsesServiceAuthAndMapsConflicts (0.01s)
PASS
ok  	github.com/khayode/email-platform/services/identity	1.695s

$ npm --prefix apps/web run typecheck
# exited 0
$ npm --prefix apps/web run lint
# exited 0
```

Earlier full verification for the campaign, global-search, and overview
changes:

```text
$ TEST_DATABASE_URL='<local Postgres URL>' go test ./internal/api ./services/identity ./services/workspace ./cmd/admin-bootstrap -count=1
ok  	github.com/khayode/email-platform/internal/api	48.667s
ok  	github.com/khayode/email-platform/services/identity	38.707s
ok  	github.com/khayode/email-platform/services/workspace	2.018s
?   	github.com/khayode/email-platform/cmd/admin-bootstrap	[no test files]

$ go vet ./internal/api ./services/identity ./services/workspace ./cmd/admin-bootstrap
$ git diff --check
$ gofmt -l <changed Go files>
# all exited successfully; gofmt reported no files

$ npm --prefix apps/web run typecheck
# exited 0
$ npm --prefix apps/web run lint
# exited 0
$ npm --prefix apps/web test
ℹ tests 11
ℹ pass 11
ℹ fail 0
$ npm --prefix apps/web run build
✓ Compiled successfully
✓ Generating static pages
```

Latest full verification, including migration `0031_admin_disable_sending_domains.sql`,
global suppression management, domain disable/enable/removal, and delivery guards:

```text
$ TEST_DATABASE_URL='<local Postgres URL>' go test ./... -count=1
ok   github.com/khayode/email-platform/internal/api	53.656s
ok   github.com/khayode/email-platform/internal/auth	2.544s
ok   github.com/khayode/email-platform/internal/automations	10.898s
ok   github.com/khayode/email-platform/internal/backfill	62.371s
ok   github.com/khayode/email-platform/internal/campaigns	0.790s
ok   github.com/khayode/email-platform/internal/config	0.735s
ok   github.com/khayode/email-platform/internal/contacts	0.714s
ok   github.com/khayode/email-platform/internal/db	1.142s
ok   github.com/khayode/email-platform/internal/delivery	2.257s
ok   github.com/khayode/email-platform/internal/kit	0.760s
ok  	github.com/khayode/email-platform/internal/outbox	4.716s
ok  	github.com/khayode/email-platform/internal/projections	0.646s
ok  	github.com/khayode/email-platform/internal/transactional	52.598s
ok  	github.com/khayode/email-platform/internal/worker	49.423s
ok  	github.com/khayode/email-platform/services/gateway	0.661s
ok  	github.com/khayode/email-platform/services/identity	54.491s
ok  	github.com/khayode/email-platform/services/identity/cmd/devseed	1.874s
ok  	github.com/khayode/email-platform/services/workspace	6.888s
ok  	github.com/khayode/email-platform/shared/contracts/events	0.497s
# all remaining packages reported [no test files]

$ go vet ./...
# exited 0
$ git diff --check
# exited 0
$ gofmt -l <changed Go files>
# no files reported

$ npm --prefix apps/web run typecheck
# exited 0
$ npm --prefix apps/web run lint
# exited 0
$ npm --prefix apps/web test
ℹ tests 11
ℹ pass 11
ℹ fail 0
$ npm --prefix apps/web run build
✓ Compiled successfully
✓ Generating static pages
Route table includes /admin/suppressions and /admin/domains/[domainID].
```

## Per-domain warm-up override

Migration `0034_domain_warmup_overrides.sql` adds the bounded, expiring
per-domain warm-up cap fields. Active overrides replace the marketing
warm-up curve, are capped by the domain's daily marketing quota unless it is
unlimited, and expire at read/send time. Worker quota, per-second, reputation,
verification, suppression, suspension, recipient-domain rate, and message-age
checks remain in their existing paths. No environment variables were added,
and no production database, service, or deployment was touched.

The admin API and detail page support read-only support-admin visibility and
super-admin-only set/clear writes with fresh MFA, typed reasons, and immutable
before/after audit state. Customer domain details expose only today's effective
warm-up cap and whether an override is active. No override data is written to
warm-up usage history and successful-day qualification is unchanged.

Local checks passed: the full `go test ./... -count=1` suite and `go vet ./...`
using an isolated local PostgreSQL 17 database, web typecheck and unit tests,
the production web build, and the Playwright warm-up set/clear journey. Next.js
was upgraded from 16.4.4 to patched 16.4.6 after the prior version blocked
builds with a security fatal error; `next.config.ts` enables its security
upgrade notice. No production service or database was changed. Before rollout,
follow the server verification checklist for migration `0034` and
authorization, effective-cap, expiry, deferral, and audit behavior.

## Per-domain sending quotas — 2026-11-14

**Locally verified against PostgreSQL; not production verified.** Migration
`0033_domain_quotas.sql` adds platform defaults, per-domain overrides,
UTC-window usage counters, rate counters, reservation records, and customer
increase requests. Transactional REST returns `429 quota_exceeded` with a
window, reset time, and `Retry-After`; SMTP quota exhaustion returns temporary
`452 4.5.3`. Campaign deliveries defer and can reserve again after quota
capacity is available. Super-admin writes require a typed reason and fresh MFA,
with before/after state in the Identity audit log.

The admin console includes domain quota progress/edit controls, reputation
warnings, request visibility, usage percentages and near-limit filtering,
overview links, and a platform defaults page. Customers can view effective
limits and usage and record an increase request. Environment quota values are
seed defaults; the database is authoritative after seeding. No production
server or data was accessed.

Focused local proof gates:

```text
$ TEST_DATABASE_URL='<local Compose Postgres URL>' go test ./internal/quota -run 'TestConcurrentReservationsNeverExceedDomainLimit|TestQuotaInheritanceUnlimitedAndUTCWindowRollover' -count=1 -v
=== RUN   TestConcurrentReservationsNeverExceedDomainLimit
--- PASS: TestConcurrentReservationsNeverExceedDomainLimit
=== RUN   TestQuotaInheritanceUnlimitedAndUTCWindowRollover
--- PASS: TestQuotaInheritanceUnlimitedAndUTCWindowRollover
PASS

$ TEST_DATABASE_URL='<local Compose Postgres URL>' go test ./internal/transactional -run TestSMTPQuotaExhaustionReturnsTemporary452 -count=1 -v
=== RUN   TestSMTPQuotaExhaustionReturnsTemporary452
--- PASS: TestSMTPQuotaExhaustionReturnsTemporary452
PASS

$ TEST_DATABASE_URL='<local Compose Postgres URL>' go test ./internal/worker -run TestCampaignQuotaDefersAndReservesAfterQuotaIncrease -count=1 -v
=== RUN   TestCampaignQuotaDefersAndReservesAfterQuotaIncrease
--- PASS: TestCampaignQuotaDefersAndReservesAfterQuotaIncrease
PASS

$ TEST_DATABASE_URL='<local Compose Postgres URL>' go test ./internal/api -run TestInternalAdminDomainQuotaUpdateChangesAtomicEnforcement -count=1 -v
=== RUN   TestInternalAdminDomainQuotaUpdateChangesAtomicEnforcement
--- PASS: TestInternalAdminDomainQuotaUpdateChangesAtomicEnforcement
PASS

$ TEST_DATABASE_URL='<local Compose Postgres URL>' go test ./services/identity -run TestAdminDomainQuotaWriteRequiresSuperAdminFreshMFAReasonAndAudit -count=1 -v
=== RUN   TestAdminDomainQuotaWriteRequiresSuperAdminFreshMFAReasonAndAudit
--- PASS: TestAdminDomainQuotaWriteRequiresSuperAdminFreshMFAReasonAndAudit
PASS
```

Full local checks also passed: `go test ./...`, `go vet ./...`,
`gofmt -l` on changed Go files, web typecheck/lint/unit tests and production
build, and all three tests in
`apps/web/e2e/admin-navigation.spec.ts`. The Mintlify documentation site
validated with `cd docs && npx --yes mint@latest validate --no-build`.

Before any server rollout, an operator still needs to verify the migration and
seed values against that server's database/environment, then smoke-test REST
429 headers, SMTP 452 retry handling, deferred campaign resumption, admin MFA
and audit records, and customer usage/request visibility in staging. `PLATFORM_MAX_DOMAIN_RATE`
is the new environment key; `SENDING_RATE_PER_SECOND`, `MAX_DAILY_EMAILS`,
`TRANSACTIONAL_REQUESTS_PER_SECOND`, `TRANSACTIONAL_DAILY_LIMIT`, and
`TRANSACTIONAL_MONTHLY_LIMIT` are documented as initial seed defaults.

PostgreSQL-backed verification of the newly added conflict-resolution
integration case could not be rerun in the current environment: the local
Postgres listener at `127.0.0.1:5432` refused connections, and the Docker
daemon socket was unavailable. The new API integration test is present but is
not claimed as passed. The full Go suite below passes with database-dependent
integration tests skipped; targeted Identity policy/client tests and web
checks ran normally:

```text
$ go test ./... -count=1
ok   github.com/khayode/email-platform/internal/api 2.659s
ok   github.com/khayode/email-platform/internal/auth 1.631s
ok   github.com/khayode/email-platform/internal/automations 1.276s
ok   github.com/khayode/email-platform/internal/backfill 1.198s
ok   github.com/khayode/email-platform/internal/campaigns 1.221s
ok   github.com/khayode/email-platform/internal/config 1.190s
ok   github.com/khayode/email-platform/internal/contacts 1.104s
ok   github.com/khayode/email-platform/internal/db 1.196s
ok   github.com/khayode/email-platform/internal/delivery 1.351s
ok   github.com/khayode/email-platform/internal/kit 0.707s
ok   github.com/khayode/email-platform/internal/outbox 0.807s
ok   github.com/khayode/email-platform/internal/projections 0.749s
ok   github.com/khayode/email-platform/internal/transactional 0.800s
ok   github.com/khayode/email-platform/internal/worker 1.330s
ok   github.com/khayode/email-platform/services/gateway 0.622s
ok   github.com/khayode/email-platform/services/identity 6.452s
ok   github.com/khayode/email-platform/services/identity/cmd/devseed 0.628s
ok   github.com/khayode/email-platform/services/workspace 0.744s
ok   github.com/khayode/email-platform/shared/contracts/events 0.443s
# all remaining packages reported [no test files]

$ go vet ./...
# exited 0
$ git diff --check
# exited 0
$ gofmt -l <changed Go files>
# no files reported

$ npm --prefix apps/web run typecheck
# exited 0
$ npm --prefix apps/web run lint
# exited 0
$ npm --prefix apps/web test
ℹ tests 11
ℹ pass 11
ℹ fail 0
$ npm --prefix apps/web run build
✓ Compiled successfully
✓ Generating static pages
Route table includes /admin/suppressions and /admin/domains/[domainID].
```
