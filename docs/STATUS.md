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
