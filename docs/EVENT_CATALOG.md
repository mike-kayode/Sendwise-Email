# Event Catalog and Contracts

This is the target event catalog and the initial implementation record. The API now writes `identity.user_registered.v1` to a PostgreSQL outbox in the same transaction as account/workspace registration, and a relay publishes it to JetStream with the stable event ID. The shared inbox helper and schema exist, but no production consumer uses them yet; most listed contracts remain proposals.

## Envelope

Every event uses a versioned envelope:

```json
{
  "id": "uuid",
  "type": "audience.contact_subscribed",
  "version": 1,
  "occurred_at": "RFC3339 timestamp",
  "producer": "consent",
  "tenant_id": "uuid or null for global identity events",
  "aggregate": {"type": "contact", "id": "uuid", "version": 12},
  "correlation_id": "uuid",
  "causation_id": "uuid or null",
  "traceparent": "W3C trace context",
  "data": {}
}
```

Rules: immutable fact; event, aggregate, tenant, correlation and causation IDs are UUIDs; `type` excludes the version suffix and the NATS subject is `<type>.v<version>`; additive optional fields only within a version; breaking changes create a new major event version/subject. Do not include passwords, raw credentials, verification/reset secrets, SMTP credentials, or unnecessary personal data. The initial registration event carries UUID references only. The Go envelope validator enforces required identifiers and metadata; automated schema compatibility enforcement is not implemented.

## Event ownership

| Event | Producer (owner) | Consumers | Current state / purpose |
|---|---|---|---|
| `identity.user_registered.v1` | Identity | Workspace, Notifications, Analytics | Implemented producer: atomically inserted with registration and published by the API outbox relay; no consumer is deployed |
| `identity.user_verified.v1` | Identity | Workspace, Notifications, Analytics | Proposed; activation |
| `identity.password_reset.v1` | Identity | Notifications, Audit | Proposed; never publish password/token |
| `workspace.organization_created.v1` | Workspace | Campaign, Audience, Billing, Audit | Proposed |
| `workspace.membership_changed.v1` | Workspace | Gateway, all services | Proposed; invalidate cached policy/session |
| `audience.contact_created.v1` | Audience | Consent, Automation, Analytics | Proposed |
| `audience.contact_updated.v1` | Audience | Consent, Automation, Analytics | Proposed |
| `consent.contact_subscribed.v1` | Consent | Audience, Campaign, Automation, Analytics | Proposed; include evidence reference, not excess PII |
| `consent.contact_unsubscribed.v1` | Consent | Audience, Delivery, Automation, Analytics | Proposed; delivery must fail closed |
| `consent.contact_suppressed.v1` | Consent | Delivery, Campaign, Analytics | Proposed; reason and scope |
| `campaign.created.v1` | Campaign | Analytics, Audit | Proposed |
| `campaign.scheduled.v1` | Campaign | Delivery Orchestration, Notifications | Proposed |
| `campaign.started.v1` | Campaign | Delivery, Analytics, Notifications | Proposed |
| `campaign.paused.v1` | Campaign | Delivery Orchestration | Proposed |
| `campaign.completed.v1` | Delivery Orchestration | Campaign, Analytics, Notifications | Proposed |
| `delivery.requested.v1` | Delivery Orchestration | Delivery Workers | Current analogue: `delivery.jobs` JetStream payload; not a versioned public contract yet |
| `delivery.smtp_submitted.v1` | SMTP Transport/Worker | Delivery, Analytics | Proposed; means relay accepted submission only |
| `delivery.deferred.v1` | SMTP Transport/Worker | Delivery, Analytics | Proposed |
| `delivery.bounced.v1` | Bounce Ingress | Consent, Delivery, Analytics | Proposed; trusted/deduped ingestion required |
| `delivery.complained.v1` | Bounce Ingress | Consent, Delivery, Analytics | Proposed; trusted FBL required |
| `tracking.open_observed.v1` | Tracking | Analytics | Proposed; observed event, not guaranteed human open |
| `tracking.click_observed.v1` | Tracking | Analytics | Proposed; bot/link-scanner classification required |
| `automation.triggered.v1` | Automation | Automation workers | Proposed |
| `billing.subscription_updated.v1` | Billing | Workspace, Gateway, Notifications | Proposed |
| `billing.payment_received.v1` | Billing | Entitlements, Notifications, Audit | Proposed; provider webhook verified/idempotent |

## Reliability and delivery

- Registration business rows and outbox row commit in one transaction against the current shared database; service-owned database boundaries are not yet implemented.
- The API relay claims rows using expiring leases, publishes with stable event ID as NATS message ID, and marks published after broker acknowledgement. A crash after publish can still redeliver; consumers must deduplicate with the inbox.
- `MarkProcessed` must be called in the same local transaction as consumer side effects; no production consumer currently demonstrates this.
- Publish failures retry with bounded exponential delay (1 second to 2,048 seconds); jitter, max-attempt quarantine, event/inbox retention and operator replay tooling remain unimplemented.
- The business-event stream is file-backed with seven-day retention and a ten-minute broker deduplication window. It is not replicated/HA-configured here.
- Correlation IDs are present in the envelope, but trace propagation, event lag and outbox-age metrics are not wired.
- If NATS is unavailable at API startup, writes remain durable in PostgreSQL; the relay starts only after the API reconnects on restart.
