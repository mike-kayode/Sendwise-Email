# Testing Strategy

Compilation is necessary but insufficient. The audit verified Go unit tests, frontend typecheck and build; DB-dependent tests skipped because PostgreSQL was unavailable. No claim of end-to-end product completion is made.

## Per-service test pyramid

1. **Unit:** business rules, validators, state transitions, retry classification, render escaping, authorization policy.
2. **Database integration:** migrations from empty and previous release, real PostgreSQL, transaction/locking, tenant role/RLS, constraints and query behavior.
3. **Contract:** OpenAPI and protobuf compatibility; NATS schema compatibility, subject authorization, consumer compatibility.
4. **Service integration:** service DB + NATS + fake dependencies; outbox/inbox replay, duplicate events, timeout and restart.
5. **End-to-end:** browser journeys against a real local Compose environment and controlled SMTP sink.
6. **Performance/resilience/security:** load, fault injection, abuse, static/dependency/secret scanning, threat-focused testing.

## Critical journey suite

- Register, verify using delivered link, log in, reset password, revoke session.
- Create organization, invite member, change role, revoke membership, switch workspaces.
- Import contacts with consent evidence; verify dedupe/errors/progress/export privacy.
- Manage list, tags, segment; prove tenant isolation with two organizations.
- Verify sender DNS and validate domain identity.
- Create campaign, design/version, preview/test send, schedule, cancel/pause/resume.
- Confirm durable dispatch, SMTP submission status semantics, retry, crash recovery and DLQ replay.
- Inject signed/forged/duplicate bounces and complaints; only trusted correlated event can suppress.
- Verify one-click unsubscribe and GET scanner do not mutate without intent; ensure future campaign/automation delivery is blocked.
- Verify analytics dedup, bots/proxy caveats and report denominator against fixture events.
- Execute automation with durable waits across restart.
- Exercise billing/idempotency/entitlements and operator audit.

## Migration and tenant suite

- Apply all service migrations to an empty database; apply upgrades from each supported prior release; retry failed migration safely.
- Run with restricted service roles, not database owner.
- Assert prohibited cross-tenant references fail; RLS denies cross-tenant reads/writes; pooled connection context does not leak.
- Snapshot/backfill parity counts, checksums and sampled semantic comparisons before cutover.
- Test export/delete/retention and audit policy.

## Release gate

CI must fail on format, static analysis, unit, migration, contract, integration, security scan, typecheck/build, or artifact build failures. Infrastructure-dependent test skips are surfaced as skips and block production release unless an equivalent controlled environment run is attached.

