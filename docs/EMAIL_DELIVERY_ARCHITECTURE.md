# Email Delivery Architecture

**Current path:** Campaign and worker logic share the legacy PostgreSQL schema. NATS JetStream distributes job references. A generic SMTP transport targets the configured host, intended to be Stalwart. This is not a verified production mail network.

## Target delivery sequence

1. Campaign owner validates content, consent policy, audience, sender identity and approval state.
2. Delivery orchestration accepts a campaign command and writes a durable plan and outbox record in one transaction.
3. Audience/Consent owners provide an immutable eligibility snapshot or bounded continuation contract. The orchestration service records a snapshot version and recipient count.
4. Delivery plans are sharded/batched by tenant and recipient MX/provider. Tenant fairness, quotas, IP/domain/provider policies, warm-up, suppression and backpressure determine dispatch rate.
5. Workers consume durable job events with explicit leases and inbox deduplication. They fetch only the immutable minimum payload/owner contract they need.
6. Rendering uses a versioned design and deterministic personalization. Transport selection chooses a healthy relay/node/IP consistent with domain policy.
7. SMTP transport submits using TLS/auth and records the exact SMTP reply. `submitted_to_relay` does not mean recipient-MX acceptance, inbox delivery, or inbox placement.
8. Temporary failure is retried with bounded exponential backoff+jitter and an age limit. Permanent failure is classified, correlated and sent to Consent/Analytics through trusted events.
9. Provider/DSN/FBL ingress verifies source/signature, timestamp/replay window and message identity, then deduplicates before changing suppression state.
10. Tracking events are signed, deduplicated and classified for proxies, scanners and bot activity. Reports label observed opens/clicks as estimates, not guaranteed human engagement.

## Required delivery invariants

- Consent/suppression decision is checked immediately before send; failure to verify eligibility fails closed.
- A stable delivery ID, tenant, campaign version, recipient and content version are immutable. Retry ID stays stable.
- Atomically claim DB work before SMTP. Record attempt state and an explicit ambiguous result for network uncertainty; do not assume SMTP is exactly-once.
- NATS is at-least-once. Consumer inbox and DB lease prevent concurrent processing; a transport idempotency key is advisory unless relay supports it.
- SMTP acceptance and final reply are persisted before ACK. If result persistence fails, reconcile from an attempt journal instead of retrying blindly.
- Retryable and permanent SMTP classes are explicit and tested against representative responses.
- Per-tenant, destination-provider, sending-domain and relay limits use distributed coordination and backpressure.
- DLQ stores the real job ID, original failure, attempts, replay metadata and operator identity; replay is authorized, idempotent, observable and cannot bypass suppression.
- One-click unsubscribe works through RFC 8058 POST; page-view GET is non-mutating and explicit user confirmation is required where appropriate.

## Current defects and unverified operations

Source inspection identifies a duplicate-send window, startup-only republish, fragile NAK pause behavior, broken DLQ identifier mapping, unauthenticated bounce/complaint endpoints, broken message-ID association, GET unsubscribe side effects, and non-deduplicated tracking. See [audit section 10](./SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md#10-email-delivery-infrastructure).

Compose contains a Stalwart image and volume, but no production Stalwart configuration, authenticated DSN/FBL bridge, key provisioning, PTR/port-25 setup, warm-up controller, reputation integration, multi-node relay selection, or live test result. Platform transactional mail may use an explicitly configured optional fallback; campaigns use Stalwart only.

## Delivery state vocabulary

Keep distinct statuses for queued, claimed, attempting, submitted-to-relay, deferred, relay-rejected, bounced, complained, suppressed, expired and failed. Store raw bounded SMTP status/reply and classified code separately. Do not report relay submission as “delivered.”

## Commercial readiness gates

Controlled SMTP sink; real STARTTLS/auth test; DKIM signature verification; SPF/DMARC alignment test; provider bounce and complaint ingestion; replay/duplicate/failure-injection tests; rate-limit and tenant fairness load tests; reputation/abuse runbooks; verified unsubscribe compliance; measured queue lag and service SLOs.
