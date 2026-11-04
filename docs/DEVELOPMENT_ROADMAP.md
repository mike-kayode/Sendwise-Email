# Development Roadmap

This plan follows the reviewed repository audit and the target architecture. The work is dependency-ordered and must not claim completion based on a UI, table, or process skeleton.

## Phase 1 - Architecture and migration foundation

- [x] Document service boundaries, ownership matrix, event envelope, trust model and target deployment; implementation approval and operation-specific reviews remain outstanding.
- [ ] Repair legacy migrations and add fresh/upgrade migration tests without destructive schema changes.
- [~] Resolve Compose port/API URL/build toolchain issues; Docker config changes are in source, but the stack still needs Docker-based validation.
- [ ] Add service template: independent binary/image, config, health/readiness, shutdown, migrations, observability and test harness.
- [~] Implement versioned event envelope, transactional outbox/inbox foundation and JetStream event stream; business-event contracts beyond registration, consumer integration, NATS permissions and CI compatibility checks remain.
- [ ] Create DB role/schema conventions and migration/backup test process.

## Phase 2 - Distributed systems foundation

- [ ] Establish private networking, service identity, secrets, TLS, CI artifacts and deployment workflow.
- [ ] Prove one low-risk independently deployable service with a separate DB/schema, migration, APIs/events, metrics, tracing, failure/replay tests, and rollback.
- [ ] Build gateway contract/routing and per-service auth context propagation.
- [ ] Add OpenTelemetry propagation, dashboards/SLOs, queue-lag alerts and backup restore.

## Phase 3 - Identity, workspace and core business ownership

- [ ] Extract identity and workspace through compatibility APIs/events with preserved IDs and staged backfill.
- [ ] Complete verification, sessions, refresh rotation, MFA roadmap, invitations, membership and role revocation.
- [ ] Extract Audience and Consent as owners; add tenant constraints/RLS, import, lists/tags/segments, suppression decision API.
- [ ] Extract Design and Campaign; complete real campaign wizard/editor/preview/test send and versioned snapshots.

## Phase 4 - Delivery and deliverability

- [ ] Extract delivery orchestration, workers and SMTP transport behind stable contracts.
- [ ] Add durable claims, outbox/inbox, periodic reconciler, idempotent DLQ replay and ambiguous SMTP attempt reconciliation.
- [ ] Configure/test Stalwart nodes, DSN/FBL trust, DNS authentication, warm-up, provider throttling, quota and abuse responses.
- [ ] Extract tracking/analytics with event dedupe, bot/proxy classification and partitioned retention after load tests.

## Phase 5 - Marketing and commercial product

- [ ] Implement automation service and visual editor with durable execution and restart recovery.
- [ ] Implement forms service, embed/hosted forms, double opt-in and form analytics.
- [ ] Implement billing, plans, usage ledger, entitlements, payment provider abstraction, subscription reconciliation and real UI.
- [ ] Implement outbound webhook/integration service, API key scopes and platform administration.

## Phase 6 - Production hardening and scale

- [ ] Tenant isolation and authorization audits, threat modeling, dependency/security scanning and penetration tests.
- [ ] Load/fault tests at declared organization/contact/campaign/event workload targets; publish measured limits.
- [ ] Automated backup/PITR and restore drills, HA/failover, incident runbooks and tested rollback.
- [ ] Set SLOs, alerts, support operations, abuse policy and production release gates.
- [ ] Cut over from monolith only when service parity, data reconciliation and rollback windows pass; retire legacy paths deliberately.

## Current progress

See [DEVELOPMENT_PROGRESS.md](./DEVELOPMENT_PROGRESS.md) for verified and pending work. Detailed historical source audit: [SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md](./SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md).
