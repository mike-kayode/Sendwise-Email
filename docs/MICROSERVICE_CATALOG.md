# Microservice Catalog

**Current repository has separately runnable Gateway, Identity, and Workspace services and a corresponding Compose stack, but that stack has not been built or started.** Most product capabilities remain in the legacy API/worker. The status column distinguishes code/configuration from verified deployment. A service is production-implemented only after private data ownership, contracts, health checks, independent deployment, tests, observability, and failure isolation are verified.

| Service | Business ownership | Primary interface | Owned data (target) | Current repository status |
|---|---|---|---|---|
| Edge/API Gateway | Public routing, auth enforcement, rate limiting, API versioning | External REST; internal routing | Gateway configuration, rate-limit policy (no customer OLTP) | Partial: standalone gateway routes auth/workspace to owners and remaining APIs to legacy; Compose/runtime is unverified |
| Identity | Credentials, verification/reset, sessions, MFA, auth events | REST; `identity.*` events | users, credentials, sessions, verification tokens, auth audit | Partial: standalone service, owned migrations, outbox event, and refresh-cookie lifecycle exist; database/provider end-to-end validation remains |
| Workspace | Organizations, memberships, roles, settings, invitations | REST; `workspace.*` events | organizations, memberships, invitations, org settings | Partial: standalone service and owned migrations provision registration events; management routes and live event validation remain |
| Audience | Contacts, profile, imports/exports, lists, tags, fields, segments | REST; audience events | contacts, imports, fields, lists/members, tags, segments | Partial: contact/audience handlers; no independent process |
| Consent | Consent evidence, preference changes, suppression decisions | REST decision API; `consent.*` events | consent events, preferences, suppression registry | Partial: mixed into public/contact/delivery handlers and shared DB |
| Campaign | Draft/lifecycle/approval/schedule intent, recipient snapshot request | REST; `campaign.*` events | campaigns, approvals, snapshot manifests | Partial: `internal/api/campaigns.go`, `internal/campaigns`; delivery job creation coupled |
| Design/Template | Structured designs, versions, rendering and previews | REST; design events | templates, design versions, assets metadata | Partial: template create/list and renderer exist; no editor/deployment |
| Domain & Deliverability | Sender identities, DNS evidence, reputation and policy | REST; `domain.*` events | domains, DNS observations, reputation/policy | Partial: DNS checks and domain handlers; SMTP key provisioning/reputation absent |
| Delivery Orchestration | Schedule, recipient plans, quotas, fair queueing, reconciliation | Internal REST/events; `delivery.requested` | delivery plans, jobs, outbox, attempts | Partial: campaign service/worker share DB; no isolated service |
| Delivery Worker | Consume jobs and execute policy-approved sends | NATS durable consumer; internal contracts | Local inbox/lease state only; no foreign table writes | Partial: `cmd/worker` handles scheduling and send processing in one binary |
| SMTP Transport | Relay selection, connection/TLS/auth, SMTP outcomes | Internal gRPC/REST or worker library boundary; health | Relay config/health/transport logs | Partial: SMTP is a package, not independent deployable service |
| Bounce/Complaint Ingress | Verify DSNs/FBL, correlate, deduplicate, emit facts | Signed provider webhook; NATS events | Ingress dedupe and quarantine records | Unsafe partial: unauthenticated handlers in API |
| Tracking & Analytics | Collect events, classify bots/proxies, aggregate reports | Public pixel/redirect; query REST; NATS | Event store, dedupe keys, aggregates | Partial: tracking handlers insert into shared `message_events`; UI partly demo |
| Automation | Workflow definitions, durable execution and delays | REST; NATS triggers and commands | workflows, versions, execution/inbox/outbox | Missing runtime: tables and demo pages only |
| Forms | Hosted/embed forms, validation and analytics | Public HTTPS; form events | forms, versions, submission records | Partial: public subscribe exists; no form management API |
| Notifications | Account, security, billing and operational messages | Internal command/events | Notification preferences, dispatch/outbox | Partial: Resend calls embedded in auth handlers |
| Billing & Entitlements | Subscription, invoice, usage, limits, payment reconciliation | REST; billing events | plans, subscriptions, invoices, usage ledger | Missing business service: schema/quota fragments only |
| Integrations/Webhooks | Outbound signed webhooks, retries/replay, connectors | REST; events; signed outbound HTTP | endpoint config, delivery attempts, replay cursor | Missing: schema exists, runtime absent |
| Platform Administration | Operator access, support/abuse actions and audit | Separate operator UI/API | operator identities, privileged audit | Missing |

## Service release gate

For every row, production status requires all of:

- Separate executable/image and independently deployable Compose/dev environment.
- Independent config validation, liveness/readiness and graceful shutdown.
- Dedicated PostgreSQL database/schema and restricted service role.
- Migration set with compatibility and rollback plan.
- OpenAPI/protobuf/event schemas and compatibility checks.
- Unit, integration, contract, authorization and failure-recovery tests.
- Structured logs, metrics, traces, SLO/alerts and runbook.
- No direct access to another service's private tables.
- Data migration/reconciliation and cutover/rollback evidence.

## Deployment sequence

The next extraction should be selected after measuring remaining monolith coupling and validating service authentication, schema ownership, event publication, and deployment tooling. The existing Identity and Workspace extractions are not yet production-validated; a further migration must not assume their parity or rollback gates have passed.
