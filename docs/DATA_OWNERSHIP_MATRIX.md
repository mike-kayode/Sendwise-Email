# Data Ownership Matrix

**Legend:** “Legacy” means the current code accesses the shared database; “target owner” is proposed. Target consumers must use owner APIs/events, never the owner's tables. Existing data remains in place until an approved extraction/backfill/cutover.

| Entity/table (legacy) | Target owner | Current legacy writers/readers | Target consumers and access method | Extraction notes |
|---|---|---|---|---|
| `users` | Identity | API auth handlers; auth/me and API-key creator references | Workspace consumes `identity.user_registered`; services consume signed identity claims | Preserve UUIDs/email uniqueness; password reset/verification must be tested |
| `verification_tokens` | Identity | Auth handlers | Notification sends only tokenized command; no direct read | Store only token hashes in target |
| Authentication sessions/refresh tokens (not present) | Identity | None | Gateway introspection/JWKS or signed claims | New target data; rotation/revocation required |
| `organizations` | Workspace | Auth registration and many tenant-scoped handlers | Other services consume `workspace.organization_*` and use org APIs | Legacy registration currently couples user/org transaction |
| `organization_members` | Workspace | Auth, middleware/token claims | Gateway/workspace authorization contract | Membership and role changes must invalidate/revoke relevant access |
| `organization_settings` | Workspace (quota policy) or Delivery (send runtime policy) | Worker delivery checks and delivery APIs | Delivery fetches versioned quota policy; ownership decision required before cutover | Avoid duplicate authoritative quota values |
| `contacts` | Audience | API contacts/public handlers, campaign, worker | Campaign via audience snapshot API; Consent via owner API/events; Delivery via immutable job payload/API | Large backfill; preserve IDs and tenant/email uniqueness |
| `custom_fields` | Audience | Schema; application usage varies | Audience only; other domains consume projected contact fields | Move with contact schema |
| `contact_imports` | Audience | API and worker | Audience worker/import API | Duplicate migration must be repaired first; binary payload retention policy required |
| `contact_lists`, `contact_list_members` | Audience | API/public subscribe/campaign | Campaign uses audience query/snapshot contract | Same-tenant list/contact constraints required |
| `tags`, `contact_tags`, `segments` | Audience | API/campaign | Campaign uses validated segment/snapshot API | Segment rules require versioned supported DSL |
| `consent_events` | Consent | Contact/public/worker handlers | Audience and campaign consume consent facts and eligibility decision | Existing mixed writers must be removed before DB ownership transition |
| `suppressions` | Consent | Public, contacts, worker, delivery handlers | Delivery queries consent decision API/cache backed by events | Global vs tenant suppression semantics require product/legal decision |
| `sending_domains` | Domain & Deliverability | API domain handlers, worker checks | Campaign validation and SMTP selection consume verification/status events | Keep private keys/credentials out of shared table |
| `campaigns` | Campaign | Campaign handlers/service, worker | Design and delivery consume campaign version/snapshot events | Replace worker direct query with immutable job contract |
| `campaign_recipients` | Campaign initially; Delivery if delivery plan owns snapshots | Campaign service, analytics and delivery | Analytics gets delivery facts; other services consume reports/contracts | Decide owner before moving, avoid two sources of truth |
| `delivery_jobs` | Delivery Orchestration | Campaign service, worker, API DLQ/suppression handlers | Worker gets durable event/payload; API asks delivery service | Must establish leases, outbox/inbox and idempotency |
| `dead_letter_queue` | Delivery Orchestration | Worker and API delivery handlers | Operator API through delivery service | Replay must carry true delivery job identifier |
| `message_events` | Tracking & Analytics | Worker and public tracking/bounce handlers | Campaign dashboard via analytics APIs | Partition, dedupe, retention, privacy and aggregation plan |
| `templates` | Design/Template | Template API and campaign/worker reads | Campaign references version ID; worker gets rendered/immutable version | Design versioning and immutable content snapshot |
| `automations`, `automation_steps`, `automation_executions` | Automation | No full runtime found | Audience/campaign/consent publish events; actions call owner APIs | Tables currently live in legacy migration only |
| `webhooks` | Integrations/Webhooks | Schema only | Subscribers use signed event deliveries | Add endpoint verification, secrets handling and delivery attempts |
| `api_keys` | Identity (client credentials) or Gateway (key policy) | API-key admin and auth package | Gateway calls identity credential validation / verifies keyed JWT | Split credential verification from authorization scopes explicitly |
| `usage_records` | Billing & Entitlements | Worker/analytics uses limited quota path | Services publish metering events; billing owns ledger | Reconcile usage and idempotency keys |
| `audit_logs` | Each service owns local audit outbox; central audit service is a consumer | Writes limited/not comprehensive | Operator UI consumes audit events | Keep security audit durable and tamper-evident |

## Enforcement requirements

1. Each database/schema has a service-specific owner and restricted app role; other services have no `SELECT`, `INSERT`, `UPDATE`, or `DELETE` grants.
2. The service API authorizes organization access on each operation. Tenant IDs from headers are never accepted as authorization by themselves.
3. Use composite tenant-aware FKs in a service's own schema where relationships are tenant-scoped. Preserve deliberate historical snapshots with explicit lifecycle semantics.
4. Use PostgreSQL RLS as defense-in-depth with transaction-scoped tenant context and tests executed under the actual non-owner app role.
5. Cross-service references use IDs and contracts, not cross-database FKs. Owners validate reference existence and tenant membership.
6. Before cutover, inventory every old reader/writer, implement a compatibility contract, backfill, compare row counts/checksums, dual-run or shadow-read, switch consumers, and retain a bounded rollback window.

