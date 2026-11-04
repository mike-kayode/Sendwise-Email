# Production Readiness

**Current assessment: NOT READY for production SaaS or bulk marketing email.** This is a gate checklist, not a target-state claim.

## Blocking gates

- [ ] All migrations apply reliably from empty and supported prior versions; migration history reconciled. Migrations exited successfully in an isolated baseline stack after test-only Compose fixes; unmodified PostgreSQL provisioning and supported-version upgrades remain unverified.
- [ ] Registration outbox/inbox migration and relay are validated against PostgreSQL and JetStream; consumer idempotency, retention/cleanup, NATS permissions, and replay operations are implemented and tested.
- [ ] Compose/local integration environment starts; corrected port bindings and build-time frontend API URL are validated with Docker.
- [ ] Production web image is rebuilt with the same-origin API URL default and login is verified in browser DevTools. Source defaults are corrected; no production image rebuild was performed.
- [ ] Login requests and URLs are checked for credential leakage in browser history, referrers, proxy/access logs, and monitoring. Source no longer reads login credentials from query parameters and posts them as JSON; production browser verification is pending.
- [ ] Account registration, verified activation, login, reset and revocation work end to end.
- [ ] Production TLS terminates correctly; internal services are private and secrets are managed externally. Caddy is hostname-configurable in source, but certificate issuance/renewal has not been tested.
- [ ] Tenant authorization and database isolation are tested with restricted roles and adversarial cross-tenant fixtures.
- [ ] Inbound bounces/complaints are authenticated, correlated, replay-protected and idempotent.
- [ ] Delivery claims, retries, ambiguous SMTP outcomes, worker recovery, scheduler coordination and DLQ replay are tested.
- [ ] Stalwart SMTP authentication/STARTTLS, DKIM, SPF/DMARC alignment, DSN/FBL bridge and controlled inbox provider tests pass.
- [ ] Consent, one-click unsubscribe, physical address policy and applicable privacy/regulatory review pass.
- [ ] No critical/high security findings remain; dependency/secret scans and threat review are complete.
- [ ] Backups and restore, incident response, RPO/RTO, monitoring/alerts and rollback are tested.
- [ ] Customer-facing workflows do not misrepresent demo data as live functionality.
- [ ] Load/fault results establish supported limits and service SLOs.

## Current blockers verified from source

1. Fresh migrations exited 0 in the isolated baseline stack after test-only Compose fixes; unmodified provisioning and supported-version upgrades remain unverified.
2. The current working tree does not build because the pre-existing uncommitted `internal/api/contact_imports.go` declares package `main` inside the `api` package. The committed baseline stack required test-only fixes to the init-script permission, PostgreSQL init environment, and Gateway legacy upstream; the checked-in full stack has not passed startup.
3. Production DNS/TLS issuance and renewal are unverified; local Caddy defaults to HTTP on `:80`.
4. The refresh-cookie and frontend refresh lifecycle have handler tests and typecheck coverage, but no live browser-to-API flow has been exercised.
5. Registration verification, workspace projection, and other database-backed auth journeys remain unverified end to end.
6. Tenant isolation, inbound bounce/complaint authentication, delivery recovery, backups/restore, and operational incident response are not production-validated. The import API tenant-scope test passed, but the worker integration test failed its row-number assertion.
7. Baseline containers reported HTTP 200 on health/readiness endpoints only in the overridden local test stack; Gateway readiness remained 200 while Legacy API was stopped. This is not a clean deployment or readiness pass.
8. Major UI sections are static or have inert actions.
9. The frontend build previously defaulted `NEXT_PUBLIC_API_URL` to `http://localhost:8080`, causing deployed browser requests to target the visitor's own machine. Dockerfile and Compose defaults now use same-origin paths; a fresh production image and browser request are not yet verified.
10. The login page previously accepted legacy credential query parameters for cleanup and could retain unrelated search parameters. It now reads only a safe `callbackUrl` and replaces the address with the path before submission. A previously received URL may already have reached upstream request logs before browser-side cleanup.
11. The login handler returns `email_not_verified` only after a correct password. `401 invalid_credentials` means the email lookup failed or the password did not match in the currently running Identity service; it does not establish that the account is unverified.

See the [verification report](./quality/verification-report.md) for command-level evidence and blocked checks. T0 remains unsigned-off; no implementation phase beyond T0 has started.

## Evidence policy

Do not mark a gate complete because the source contains an interface, config entry, table, test that skips, or a passing compile. Attach test environment, command, result, logs/metrics, schema version, and operator sign-off. Never record invented DNS, credentials, provider success or throughput.
