# Database Schema

> **Status note (2026-11-03):** The repository's shared PostgreSQL schema remains the legacy source of truth; service-per-database ownership is a target, not implemented. This page's historical simplified SQL examples are illustrative, not the exact current migration DDL. See [DATABASE_ARCHITECTURE.md](../DATABASE_ARCHITECTURE.md), [DATA_OWNERSHIP_MATRIX.md](../DATA_OWNERSHIP_MATRIX.md), and [SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md](../SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md) for verified table relationships and current migration caveats.

Source of truth: PostgreSQL 16. Migrations live in `/migrations` and are applied in lexical order, recorded in `schema_migrations`. Each migration runs in a transaction. The current migration runner only applies migrations; it does not implement rollback. The current environment has no Docker/PostgreSQL runtime, so database application is unverified.

## Core tables

| Table | Purpose |
|-------|---------|
| `organizations` | Workspaces/tenants. `plan`, `status` (active/suspended/closed). |
| `users` | User accounts. Argon2id password hash, email verification timestamp. |
| `organization_members` | User ↔ organization membership + role. |
| `api_keys` | Hashed API keys per organization with scopes and expiration. |
| `contacts` | Subscriber profiles. `status`, `custom_fields` JSONB, `source`. |
| `contact_imports` | Durable, tenant-scoped CSV import payload, mapping, lease/retry state, progress counters, and bounded row errors. |
| `contact_lists` | Audience lists per organization. |
| `contact_list_members` | Contact ↔ list membership with unsubscribe timestamp. |
| `tags` | Contact tags per organization. |
| `contact_tags` | Contact ↔ tag relationships. |
| `segments` | Saved dynamic segment rule definitions (JSONB rules). |
| `consent_events` | Consent/subscription history (event_type, source, purpose, evidence). |
| `suppressions` | Unsubscribed/bounced/complained addresses checked before every send. |
| `campaigns` | Campaign configuration + lifecycle status. |
| `campaign_recipients` | Immutable recipient snapshot per campaign. |
| `templates` | Versioned structured JSON email designs per organization. |
| `delivery_jobs` | Per-recipient durable send jobs, message_id, attempts, next_attempt_at. |
| `message_events` | Delivery and engagement events with dedup keys. |
| `automations` | Workflow definitions (triggers, steps). |
| `automation_executions` | Durable per-contact workflow execution state. |
| `automation_steps` | Step definitions within a workflow. |
| `sending_domains` | Verified sending domains (SPF/DKIM/DMARC status). |
| `webhooks` | Tenant webhook configuration (URL, secret, events). |
| `usage_records` | Auditable usage ledger (metric, quantity, metadata). |
| `audit_logs` | Security/account activity log. |

## Key design rules
- **UUID primary keys**, `timestamptz` timestamps, `created_at`/`updated_at` via trigger.
- **Every tenant-owned table carries `organization_id`** and is queried scoped to the caller's organization.
- **Foreign keys** with `ON DELETE CASCADE` where ownership implies lifetime.
- **Unique constraints**: `(organization_id, lower(email))` on contacts; `key_prefix` on api_keys; `(campaign_id, contact_id)` on campaign_recipients/delivery_jobs; `(contact_id, list_id)` on list members; `(contact_id, tag_id)` on contact_tags.
- **JSONB** for `custom_fields`, segment rules, template design, usage metadata, audit metadata.
- **Indexes**: tenant-aware indexes on `organization_id` + status/created_at; GIN index on `custom_fields`; partial indexes for due automation executions and pending delivery jobs.
- **High-volume event tables** (`message_events`, `delivery_jobs`, `audit_logs`, `usage_records`) are candidates for time-based partitioning/retention in production.

## Simplified examples

```sql
CREATE TABLE contacts (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email           text NOT NULL,
    first_name      text,
    last_name       text,
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','subscribed','unsubscribed','bounced','complained','suppressed')),
    custom_fields   jsonb NOT NULL DEFAULT '{}'::jsonb,
    source          text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX contacts_org_email_lower_idx
    ON contacts (organization_id, lower(email));
CREATE INDEX contacts_org_status_idx ON contacts (organization_id, status);
CREATE INDEX contacts_custom_fields_idx ON contacts USING gin (custom_fields);
```

```sql
CREATE TABLE delivery_jobs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    campaign_id     uuid NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    contact_id      uuid NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
    message_id      text UNIQUE NOT NULL,
    status          text NOT NULL DEFAULT 'queued'
                    CHECK (status IN ('queued','processing','submitted','accepted','deferred','bounced','complained','unsubscribed','failed','cancelled')),
    attempts        integer NOT NULL DEFAULT 0,
    max_attempts    integer NOT NULL DEFAULT 5,
    next_attempt_at timestamptz,
    last_error      text,
    accepted_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX delivery_jobs_campaign_contact_idx
    ON delivery_jobs (campaign_id, contact_id);
CREATE INDEX delivery_jobs_due_idx
    ON delivery_jobs (status, next_attempt_at)
    WHERE status IN ('queued','deferred');
```

## Consent and suppression states
- `pending` — imported or invited, no valid marketing consent yet.
- `subscribed` — valid consent recorded; eligible for marketing sends.
- `unsubscribed` — opted out; suppressed immediately and permanently for that org.
- `bounced` — hard bounce; suppressed.
- `complained` — spam complaint; suppressed immediately.
- `suppressed` — administratively suppressed.

A contact who is `unsubscribed` or `complained` must not receive another applicable marketing email, including through automations in progress. Suppression is checked immediately before submission.

## Migration status
- `0001_init.sql` — initial schema is present; application status is unknown until a database is available.
- `0002_contact_imports.sql` — durable CSV import job storage; application status is unknown until a database is available.
- `0003_api_keys_updated_at.sql` — adds update timestamps required for API-key revocation; application status is unknown until a database is available.
- The migration includes core identity, audience, campaign, delivery, automation, domain, usage, and audit tables.
- The schema does not yet include invitations, invoices/subscription records, role-permission tables, or RLS policies.
- There is no down-migration mechanism. Future destructive/schema changes must use forward-only migrations with an explicit recovery plan.
