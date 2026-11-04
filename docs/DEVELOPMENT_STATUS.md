# Development Status

**Last audited:** 2026-11-03  
**Evidence basis:** Repository source/configuration and available unit tests. PostgreSQL, Docker Compose, NATS and SMTP integration were not available for end-to-end verification.

Legend:

- `[x] Completed` - meaningful implementation exists; this does not imply production verification.
- `[~] Partially completed` - some backend/UI behavior exists but significant required behavior is absent, disconnected, or unverified.
- `[ ] Pending` - an implementation or verification task remains.
- `Missing` - no functional implementation was found.

## Module checklist

| # | Module | Status | Evidence summary |
|---|---|---|---|
| 1 | Authentication and authorization | `[~]` | Login/JWT and refresh-cookie flow exist; API-key auth, access-token revocation freshness, and live database flow are incomplete. |
| 2 | User registration and login | `[~]` | API and verification UI routes are wired; mail delivery and activation against PostgreSQL remain unverified end to end. |
| 3 | Organization/workspace management | `[~]` | Workspace is created at registration; no member/invitation/settings management API. |
| 4 | Multi-tenant architecture | `[~]` | Most queries use organization scopes; no RLS/composite tenant constraints; cross-tenant reference validation gaps. |
| 5 | User roles and permissions | `[~]` | Role claims and selected route gates exist; no role-management UI/API or live membership refresh. |
| 6 | Contact management | `[~]` | CRUD/list backend and contact list UI exist; several visible operations are placeholders. |
| 7 | Contact import/export | `[~]` | Durable import/export backend exists; UI and database integration verification incomplete; CSV formula injection risk. |
| 8 | Audience and mailing lists | `[~]` | Basic list operations exist; membership management is incomplete. |
| 9 | Tags and custom fields | `[~]` | Tag list/create and JSON custom fields exist; tag/contact assignment and full field CRUD missing. |
| 10 | Contact segmentation | `[~]` | Bounded backend rule compiler exists; front-end rule editor and predicates incomplete. |
| 11 | Consent and subscription management | `[~]` | Consent evidence and public APIs exist; GET unsubscribe and tenant/list consistency need work. |
| 12 | Campaign creation and management | `[~]` | Backend lifecycle exists; creation/editor UI has inert controls and static data. |
| 13 | Email template management | `[~]` | Backend list/create exists; update/delete/editor are incomplete. |
| 14 | Drag-and-drop email builder | `Missing` | Existing editor page is a prototype; save/test/schedule actions are not wired. |
| 15 | Email rendering and personalization | `[~]` | Structured rendering exists; comprehensive HTML-safety/client-compatibility tests absent. |
| 16 | Campaign scheduling | `[~]` | API and worker scheduling exist; coordination/recovery not integration-tested. |
| 17 | Email delivery orchestration | `[~]` | Durable jobs, NATS, retries, quotas and DLQ exist; duplicate-send and recovery defects remain. |
| 18 | SMTP integration with Stalwart | `[~]` | Generic SMTP transport and Compose service exist; live relay and production Stalwart config unverified. |
| 19 | Sending domain management | `[~]` | Tenant domain CRUD/verification/DKIM storage exist; real deployment and comprehensive validation absent. |
| 20 | SPF, DKIM and DMARC verification | `[~]` | DNS prefix/presence checks exist; alignment/validity/signature behavior not proven. |
| 21 | Email queue and background workers | `[~]` | JetStream and worker exist; outbox, continuous reconciliation and distributed recovery need work. |
| 22 | Bounce and complaint processing | `[~]` | Handlers exist; unauthenticated input and event/job association defects. |
| 23 | Unsubscribe management | `[~]` | Signed tokens, RFC 8058, suppression updates exist; GET changes state and footer address absent. |
| 24 | Open and click tracking | `[~]` | Tracking endpoints persist events; no event deduplication. |
| 25 | Campaign analytics and reporting | `[~]` | Backend endpoints exist; unique-event semantics incomplete and frontend reports mostly demo values. |
| 26 | Email automation and workflows | `Missing` | Schema and demo UI exist; no API/runtime was found. |
| 27 | Subscription forms | `[~]` | Public subscribe endpoint exists; form-management backend/UI absent. |
| 28 | API management and webhooks | `[~]` | API-key lifecycle exists but keys do not authenticate requests; outbound webhook system absent; inbound hooks untrusted. |
| 29 | Billing and usage management | `[~]` | Usage/quota schema and some worker checks exist; no billing provider/API or real billing UI. |
| 30 | Platform administration | `Missing` | No platform administration API or functional interface found. |
| 31 | Audit logs and system monitoring | `[~]` | Audit table, logs, metrics/config exist; audit coverage, dashboards, alerts, backups and restore are incomplete. |

## Verified

- No major product module qualifies as fully complete end-to-end; the completed items below are verified implementation slices, not production-readiness claims.
- [x] Go unit-test suite passes with `CGO_ENABLED=0` and Go 1.27.3.
- [x] Frontend TypeScript check passes.
- [x] Frontend production build passes.
- [x] API/worker tests that do not need PostgreSQL pass in an uncached run.
- [x] Argon2id password hashing, JWT validation, selected role checks, parameterized segment compilation, and durable import/queue code have implementation and unit-test coverage.

## Partially verified

- [~] Source-level route, handler, migration, worker and infrastructure inspection completed.
- [~] Local browser smoke test confirms the login page renders; no live API login was possible.
- [~] Unit tests exist for auth, config, delivery, campaign, contacts, and worker behavior.

## Pending verification

- [x] Convert the duplicate `contact_imports` migration to additive/idempotent ALTERs and add a forward repair migration.
- [ ] Apply all migrations to disposable PostgreSQL; source changes are not a substitute for execution.
- [ ] Verify verification email activation, login, refresh, reset, and token consumption against PostgreSQL and a working mail provider.
- [ ] Run API/worker PostgreSQL integration tests with `TEST_DATABASE_URL`.
- [ ] Start full Compose stack; source fixes now avoid the API/Stalwart port collision, but Docker validation is pending.
- [ ] Verify NATS outage, worker crash, duplicate delivery, retry, and DLQ replay behavior.
- [ ] Verify authenticated SMTP/STARTTLS, Stalwart DKIM signing, real bounce/complaint bridge, and unsubscribe.
- [ ] Verify tenant isolation and role/revocation behavior.
- [ ] Configure production HTTPS, secrets, backups, restore, alerts and CI.
- [ ] Benchmark stated user/contact/campaign/event workloads; no capacity targets are currently evidenced.

## Critical gates

1. Fresh database migration must complete; migration files are repaired but have not been run against PostgreSQL.
2. Compose must start without port binding errors.
3. Account verification must activate users through the emailed link in an end-to-end database/provider test.
4. Inbound delivery events must be authenticated and correctly correlated.
5. SMTP side effects, queue recovery and DLQ replay must have explicit tested semantics.
6. No production deployment until TLS, tenant isolation, secrets, and recovery procedures are verified.
