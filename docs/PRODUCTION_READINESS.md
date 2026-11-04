# Production Readiness

**Current assessment: NOT READY for production SaaS or bulk marketing email.** This is a gate checklist, not a target-state claim.

## Blocking gates

- [ ] All migrations apply reliably from empty and supported prior versions; migration history reconciled. **T0:** all three migration sets exit 0 from empty on the canonical Compose stack (fresh volume); supported prior versions and unmodified server provisioning remain unverified.
- [ ] Registration outbox/inbox migration and relay are validated against PostgreSQL and JetStream; consumer idempotency, retention/cleanup, NATS permissions, and replay operations are implemented and tested.
- [x] Compose/local integration environment starts; corrected port bindings and build-time frontend API URL are validated with Docker. **T0:** canonical two-file stack starts on a fresh volume; health/ready 200; web image builds; all pages serve 200 through Caddy. (Port bindings verified only on local loopback; production server not inspected.)
- [ ] Production web image is rebuilt with the same-origin API URL default and login is verified in browser DevTools. **T0:** production image builds; no production host or browser DevTools check.
- [ ] Login requests and URLs are checked for credential leakage in browser history, referrers, proxy/access logs, and monitoring. Source no longer reads login credentials from query parameters and posts them as JSON; production browser verification is pending.
- [ ] Account registration, verified activation, login, reset and revocation work end to end. **T0:** API-level journeys pass (identity incl. refresh rotation/reuse/logout revocation; legacy auth on the loopback port); real email delivery and browser flows are open (T5/T6).
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

## Current blockers verified from source and T0 run

1. Migrations exit 0 from empty on the canonical two-file Compose stack (T0, fresh volume); supported-version upgrades and unmodified server provisioning remain unverified.
2. The working tree builds again (T0 fixed the package mismatch in `internal/api/contact_imports.go`); the canonical Compose stack starts after the committed T0 fixes (init-script exec bit, postgres DB env vars, gateway `LEGACY_API_URL`, MinIO profiled out). The two files must be invoked together — neither standalone mode serves `/api/*` (documented in README).
3. Production DNS/TLS issuance and renewal are unverified; local Caddy runs HTTP on `:80` (`.env.local` override).
4. The refresh-cookie lifecycle is live-verified at the API level (rotation, reuse detection, logout revocation); no browser-to-API refresh flow has been exercised (T6).
5. Identity register→verify→login→reset journeys are live-verified (via the development log mailer); legacy auth register/verify/login/reset are live-verified on the loopback port. Real mail delivery is broken: Stalwart is in bootstrap mode (no SMTP listener) and the legacy Resend key is absent — the system from-address is now `hello@pipsleague.com` (T5 owns delivery).
6. Tenant isolation (no RLS), inbound bounce/complaint authentication (forged webhook accepted and wrote a suppression in T0), delivery recovery, backups/restore, and incident response are not production-validated. The import API tenant-scope test passed; the worker import integration test fails its row-number assertion (D7).
7. Gateway readiness does not gate on upstreams: `/readyz` returned 200 with the Legacy API stopped (re-tested in T0). This is not a readiness pass (T1).
8. Major UI sections are static (settings/billing/editor/preview/automations/forms confirmed zero fetches in T0), and the working contacts import/export UI is toast stubs despite a verified backend (T2/T4).
9. Frontend production image builds with same-origin defaults (T0 image build passed); no production image or browser request verified against a real hostname.
10. The login page no longer reads credential query parameters (source-level); browser history/referrer behavior is pending T6.
11. The login handler returns `email_not_verified` only after a correct password (live-verified in T0). `401 invalid_credentials` does not establish that the account is unverified.
12. Newly verified route defects are open: api-key creation panics (D1), campaign resume 500 on unverified domain (D2), unknown campaign FK → 500 (D3), analytics invalid uuid → 500 (D4), `DELETE /tags/{id}` missing (D11), GET unsubscribe mutates (D9), unauthenticated webhooks (D10), `npm test` 1/4 fail (D5), legacy API integration group 6/6 fail on the register response contract (D6).

See the [verification report](./quality/verification-report.md) for command-level evidence and blocked checks. T0 (run + docs reconciliation) is complete; no implementation phase beyond T0 has started.

## Evidence policy

Do not mark a gate complete because the source contains an interface, config entry, table, test that skips, or a passing compile. Attach test environment, command, result, logs/metrics, schema version, and operator sign-off. Never record invented DNS, credentials, provider success or throughput.
