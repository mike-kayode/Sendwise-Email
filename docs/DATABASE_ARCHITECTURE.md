# Database Architecture

**Current state:** A shared PostgreSQL database and `pgxpool` are used by API and worker. The migrations in `migrations/` declare a legacy shared schema; they are not yet service-owned databases. Migration 0007 defines shared outbox/inbox tables; the registration event and identity data still share the monolith database. Migration execution remains unverified because PostgreSQL is unavailable. See [the full audit](./SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md#6-database-architecture-and-schema) and [ownership matrix](./DATA_OWNERSHIP_MATRIX.md).

## Target ownership

Each service owns its schema/database, credentials, migrations, constraints, backup policy, and data lifecycle. Use separate PostgreSQL databases when isolation, independent restore, or operational ownership warrants it; otherwise use separate schemas plus distinct non-owner roles and grants. A schema is not an adequate boundary if every service credential can query all schemas.

| Target owner | Candidate data |
|---|---|
| Identity | users, credential hashes, verification/reset token hashes, sessions, auth audit |
| Workspace | organizations, memberships, invitations, settings, entitlement snapshot |
| Audience | contacts, custom fields, imports, lists, memberships, tags, segments |
| Consent | consent evidence, preference state, tenant/global suppressions |
| Campaign | campaigns, approvals, immutable audience snapshot manifest |
| Design | structured designs, versions, content assets metadata |
| Domain & Deliverability | sender domains, DNS observations, reputation and policy state |
| Delivery Orchestration | delivery plans/jobs, claims, attempts, outbox/inbox, DLQ |
| Tracking & Analytics | deduplicated raw event partitions and derived aggregates |
| Automation | workflow definitions/versions, durable execution, delayed work |
| Billing | subscriptions, invoices, payment references, idempotent usage ledger |
| Integrations | webhook endpoints, signing metadata, delivery attempts/replay |

Entity-to-service mapping and legacy writers/readers: [DATA_OWNERSHIP_MATRIX.md](./DATA_OWNERSHIP_MATRIX.md).

## PostgreSQL standards

- UUID IDs for entities; `timestamptz` UTC for timestamps; explicit status CHECKs or reference tables.
- Parameterized SQL. Every service has a least-privilege app role, separate migration role, pool limits, statement/query timeouts and TLS policy.
- Local transactions only. Outbox row and business update commit atomically. Consumers use inbox/idempotency constraints. Current `event_outbox`/`event_inbox` tables are global legacy-schema tables, not yet owned by isolated services.
- Tenant-scoped uniqueness includes tenant ID. Same-service tenant references use composite `(tenant_id,id)` unique keys and composite FKs where appropriate.
- Use RLS as defense-in-depth with transaction-scoped context, test as the real non-owner service role, and always perform business authorization in service code.
- Index actual predicates/orderings; validate with `EXPLAIN (ANALYZE, BUFFERS)` on representative anonymized data. Avoid speculative indexes.
- Do not cascade delete legal, financial, or delivery audit history without retention approval. Prefer explicit anonymization/retention policies.
- No migration may be marked complete without fresh-install and previous-version upgrade tests, forward compatibility, failure recovery, and a rollback/forward-fix plan.

## High-volume data

- Delivery plans/jobs: index claim key/status/next attempt and tenant; use bounded batches and row leases/`SKIP LOCKED` when appropriate. Keep payloads small.
- Tracking events, message events, audit and usage: time partitioning only after retention/reporting requirements and PostgreSQL operational procedure are established. Include stable dedupe keys, partition-aware uniqueness, and archival.
- Analytics reads derived aggregates or a dedicated warehouse/OLAP store. Do not execute expensive full-history scans against transactional service databases.
- Define retention by data class, tenant export/deletion semantics, legal hold and backup expiration before enabling event collection.

## Migration from the legacy schema

1. Stop before changing ownership: record schema version, table/column/index/constraint inventory, row counts, tenant distributions, and representative checksums. Take and restore-test a backup.
2. The current migration conflict is repaired in source: migration 0002 now adds import lease/retry fields and indexes idempotently; migration 0006 repeats the repair for environments that recorded earlier versions.
3. Migration 0005 now includes `verification_tokens.updated_at`; migration 0006 adds it idempotently and reinstalls the trigger for databases that already recorded migration 0005. Run both fresh and upgrade tests before release.
4. Introduce service-owned schema and roles; service code initially reads/writes existing data only through its current monolith boundary.
5. Backfill with stable IDs and resumable checkpoints. Dual-write through a single owner/outbox, not uncontrolled writes from both services.
6. Shadow-read and compare normalized records/authorization results. Test inserts, updates, deletes, tenant isolation, and event replay.
7. Cut consumers over behind a reversible flag. Observe reconciliation and error budgets. Keep old data read-only for a bounded rollback window.
8. Revoke legacy cross-schema grants after proven cutover; then archive/drop old structures only in a separately reviewed, recoverable operation.

Current deployment history is unknown and no live database was available, so this document does not prescribe destructive DDL against an existing installation.

## Backups and recovery

Automate encrypted backups, WAL/PITR where supported, retention, off-site copies, restore tests, and service-specific recovery runbooks. Establish numeric RPO/RTO only after a measured restore drill. A named Docker volume is persistence, not a backup.
