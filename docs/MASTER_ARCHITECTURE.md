# Sendwise Master Architecture

**Status:** Target design, not an assertion that the repository already runs microservices.  
**Current implementation:** One Go API binary, one Go worker binary, shared PostgreSQL, NATS JetStream, and one Next.js application. See [the verified audit](./SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md).

## Architecture principles

1. Split along durable business ownership and independent scaling needs, not directory names.
2. A service is independently deployable only when it has its own process/image, configuration, health/readiness, owned schema/database and credentials, contract tests, and operational signals.
3. Only the owning service reads/writes its private tables. Other services use versioned REST/gRPC contracts or versioned NATS events.
4. PostgreSQL transactions remain local to one service. Cross-service workflows use outbox/inbox, idempotent handlers, retries, and compensating actions where needed.
5. NATS is transport, not canonical business state. Every event publication is recoverable from the producer's transactional outbox.
6. Preserve current API behavior and customer data during extraction. Do not copy/move/drop existing tables until inventory, backfill, dual-read/write window, reconciliation, rollback, and ownership cutover are tested.
7. Prefer a modular monolith during a domain's extraction preparation; deploy a service only when its operational boundary is genuinely independent.
8. No target diagram is evidence of current production readiness.

## Target service domains

The target inventory, ownership assignments, interfaces and current-state labels are in [MICROSERVICE_CATALOG.md](./MICROSERVICE_CATALOG.md) and [DATA_OWNERSHIP_MATRIX.md](./DATA_OWNERSHIP_MATRIX.md). Initial independently deployable candidates:

- **Edge/API gateway:** browser/external REST, authentication enforcement, request limits, routing, request IDs.
- **Identity:** user credentials, verification, reset, sessions, MFA, auth events.
- **Workspace:** organizations, members, roles, settings and tenant context.
- **Audience:** contacts, imports/exports, lists, tags, custom fields, segments.
- **Consent:** consent history and global/tenant suppression decisions.
- **Campaign:** campaign lifecycle, approvals and recipient snapshot intent.
- **Design:** structured templates, editor assets, rendering and versioning.
- **Domain & deliverability:** sender domains, DNS evidence, reputation and throttling policy.
- **Delivery orchestration:** durable recipient plans, quotas, priorities, schedule and reconciliation.
- **Delivery workers:** independently scaled consumers that execute jobs through service contracts.
- **SMTP transport:** relay selection, connection/TLS/auth, SMTP result classification and health.
- **Bounce/complaint ingress:** trusted provider/DSN ingestion, dedupe and suppression events.
- **Tracking/analytics:** event collection, bot/privacy classification, aggregates and reports.
- **Automation:** durable workflow definitions and execution.
- **Forms:** hosted/embed forms and submission validation.
- **Notifications:** verification/reset/security/billing and operational messages, isolated from marketing policy.
- **Billing/entitlements:** subscriptions, invoices, usage and plan limits.
- **Integrations/webhooks:** outbound signed webhooks, retry/replay and inbound integration verification.
- **Platform administration:** controlled operator workflows and audit trails.

Closely related capabilities may share a deployment when they share ownership and failure/scaling characteristics; e.g., bounce ingestion can begin as a component of delivery operations, and a distinct SMTP transport process should be justified by operational isolation rather than taxonomy alone. The target catalog records these consolidation rules.

## Communication model

- Browser and third-party clients use versioned REST through the gateway.
- Internal synchronous calls use REST initially; introduce gRPC only where typed low-latency contracts and measured call patterns justify its operational cost.
- NATS JetStream carries asynchronous facts/commands with explicit ownership, schema version, event ID, aggregate ID, tenant context, occurrence time and trace context.
- Producers write state and outbox rows in one local DB transaction. A relay publishes and marks delivery; consumers persist inbox/idempotency state with local side effects in one transaction.
- Use request/reply only for bounded, latency-sensitive queries. Never use a chain of synchronous calls for bulk campaign delivery.
- Events are immutable facts. Commands have explicit target, expiry/deadline, retry policy and idempotency key.

See [EVENT_CATALOG.md](./EVENT_CATALOG.md) and [the communication diagram](./architecture/service-communication.mmd).

## Data ownership and consistency

The target ownership matrix is [DATA_OWNERSHIP_MATRIX.md](./DATA_OWNERSHIP_MATRIX.md). Existing tables remain in the legacy database until a service's extraction is approved. A service schema/database uses a service-specific PostgreSQL role with grants limited to its objects. No service gets superuser/owner rights in production. Tenant identity is authenticated at the edge and independently authorized by each owning service.

Cross-service consistency is eventual and explicit:

- Identity registration emits `identity.user_registered`; workspace creation listens and creates an initial tenant/membership.
- Campaign scheduling emits a campaign event; delivery orchestration resolves audience through an approved snapshot contract, persists a durable send plan and outbox event.
- Delivery workers request minimum necessary recipient/campaign data through internal contracts or a versioned immutable delivery payload; they do not query audience/campaign private tables.
- SMTP results and trusted bounce/complaint events update delivery state and consent/suppression through owning APIs/events.
- Analytics consumes events into its own append-only event store/aggregates; it does not query another service's OLTP tables.

No distributed transaction is the default. Sagas are reserved for customer workflows with explicit compensation; billing/payment operations require provider idempotency and reconciliation.

## Security and operations baseline

- TLS at the public edge and service-to-service identity inside the deployment network.
- Short-lived access tokens, revocable sessions/refresh-token rotation, MFA roadmap, centralized authorization decisions, least privilege and audited operator access.
- Tenant-bound access checks in every service. PostgreSQL RLS is defense-in-depth, not a replacement for service authorization.
- Webhook signatures, timestamp/replay checks and event deduplication.
- Secret-manager integration; no secret values in images, logs or docs.
- OpenTelemetry trace/context propagation across REST, NATS and database work; structured logs, RED metrics, queue lag, DB saturation, SMTP response classes and SLO/error-budget alerts.
- Per-service readiness/liveness, graceful shutdown, bounded retries, backpressure and bulkheads.
- Automated backup/restore drills and documented RPO/RTO before production.

Detailed guidance: [SECURITY_ARCHITECTURE.md](./SECURITY_ARCHITECTURE.md), [INFRASTRUCTURE_AND_DEPLOYMENT.md](./INFRASTRUCTURE_AND_DEPLOYMENT.md), [PRODUCTION_READINESS.md](./PRODUCTION_READINESS.md).

## Deployment target

Local development remains Docker Compose. Production starts with independent OCI images and isolated network/service identities. Kubernetes is a later option only when measured availability, rollout, autoscaling, and team-operability needs justify it. Managed PostgreSQL/NATS or a secured self-hosted equivalent must be selected and tested; Stalwart SMTP nodes are independent from application services and are not exposed as public admin endpoints.

Target topology is shown in [target-platform.mmd](./architecture/target-platform.mmd), [microservice-architecture.mmd](./architecture/microservice-architecture.mmd), and [deployment-architecture.mmd](./architecture/deployment-architecture.mmd); these distinguish proposed services from the current Compose stack.

## Incremental extraction strategy

1. Repair and test existing migrations; capture a baseline schema and representative anonymized fixtures.
2. Add ownership, event, outbox, inbox and migration conventions without changing existing data paths.
3. Extract one low-blast-radius capability with a clean contract and independently owned tables; validate deployment/rollback.
4. Move identity/workspace only after user, membership, token, verification/reset and API compatibility are tested end to end.
5. Extract audience/consent before campaign dispatch so recipient/suppression ownership is explicit.
6. Extract campaign planning and delivery orchestration; validate durable idempotency before enabling production sends.
7. Extract domain, notification, analytics, automation, billing, integrations, and admin by dependency and business readiness.
8. Deprecate legacy routes/table access only after dual-run reconciliation and rollback windows close.

See [DEVELOPMENT_ROADMAP.md](./DEVELOPMENT_ROADMAP.md) and [the migration strategy](./DATABASE_ARCHITECTURE.md#migration-from-the-legacy-schema).
