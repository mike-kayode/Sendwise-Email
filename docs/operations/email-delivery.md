# Email Delivery Operations

> **Status note (2026-11-03):** This page includes intended operating policy and historical implementation claims. For source-verified delivery behavior and known defects, use [EMAIL_DELIVERY_ARCHITECTURE.md](../EMAIL_DELIVERY_ARCHITECTURE.md) and [SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md](../SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md). Stalwart production configuration and real SMTP integration have not been verified.

## Transport architecture
The platform owns campaign orchestration and delivery scheduling. Stalwart SMTP is the initial
transport, accessed through the Go `delivery.Transport` interface:

```go
type Transport interface {
    Send(ctx context.Context, message Message) (*Result, error)
}
```

The Stalwart adapter submits messages over authenticated SMTP (STARTTLS). DKIM signing is
delegated to Stalwart. The transport is replaceable — a custom Go SMTP engine can implement the
same interface without changing campaigns, contacts, automation, or frontend.

## Current implementation status
Source inspection only; Go and Docker are unavailable in the current environment, so none of this is integration-tested here.
- The worker records successful Stalwart submission as `submitted`; it does not claim inbox delivery.
- Production SMTP now requires STARTTLS and configured SMTP credentials. Development may explicitly allow an insecure local transport.
- Temporary SMTP errors are deferred with exponential backoff; exhausted or permanent SMTP errors become `failed`.
- **Hard-bounce classification → automatic contact suppression:** Permanent (5xx) SMTP failures are classified as hard bounces. The contact is added to the `suppressions` table (reason `hard_bounce`), marked `suppressed` on the `contacts` record, and the delivery job is marked `bounced` so it is never retried.
- **Inbound DSN / FBL processing:** `POST /api/v1/webhooks/bounce` and `POST /api/v1/webhooks/complaint` endpoints accept inbound bounce/DSN and feedback-loop notifications from Stalwart (or another SMTP relay). The affected contact is suppressed immediately (reason `complaint` or `hard_bounce`) and any in-flight delivery jobs are marked bounced.
- **Dead-letter queue and manual replay:** Exhausted or aged-out jobs are moved to the `dead_letter_queue` table. `GET /api/v1/delivery/dead-letter` lists unreplayed dead-lettered jobs; `POST /api/v1/delivery/dead-letter/{id}/replay` re-queues a job for retry.
- **Per-domain concurrency limits:** The worker limits simultaneous outbound SMTP connections per recipient domain (default 10, configurable via `DOMAIN_CONCURRENCY_LIMIT`).
- **Tenant quotas and abuse pause:** `organization_settings` stores `max_daily_emails`, `max_bounce_rate`, `max_complaint_rate`, and `paused_until`. The worker checks daily email usage against the quota and refuses sends for paused organizations.
- **Maximum message age:** Jobs older than `MAX_MESSAGE_AGE` (default 48h) are dead-lettered rather than retried.
- Inbound DSN/feedback-loop processing, guaranteed remote-MX acceptance, per-domain concurrency, tenant quotas, and maximum message age were previously not implemented; they are now implemented but still require end-to-end integration testing against a live Stalwart/SMTP relay.
- A permanent SMTP rejection now fails the job and automatically suppresses the contact (hard bounce) or dead-letters it (max attempts exceeded).
- The current unsubscribe footer has a link and RFC 8058 headers; a configurable physical mailing address is not yet included.

The remaining sections describe the intended operating policy and required production work, not a claim that every control is implemented.

## Delivery semantics (do not confuse these)
- `queued` — job accepted into the platform, awaiting a worker.
- `submitted` — Stalwart (SMTP) accepted the message for relay. This is **not** inbox delivery.
- `accepted` — receiving MX confirmed acceptance (when a positive SMTP final response is observed).
- `deferred` — temporary rejection (4xx); retried with exponential backoff.
- `bounced` — permanent rejection (5xx hard bounce); contact suppressed.
- `complained` — spam complaint; contact suppressed immediately.
- `unsubscribed` — recipient opted out; suppressed immediately.
- `failed` — delivery could not be completed after max attempts.

SMTP acceptance alone never equals successful inbox delivery. Bounce/complaint events are
processed as separate events and update `message_events` and `suppressions`.

## Stalwart configuration (development starting point)
`infrastructure/docker-compose.yml` runs `stalwartlabs/stalwart:latest` on hostname
`mail.localhost`, ports 25/465/587/8080. This is a local starting point only. Production requires:

- A static public IP.
- Confirmed outbound port 25 access (verify with the hosting provider).
- PTR / reverse DNS matching the sending hostname.
- Valid forward DNS (A/AAAA) for the sending hostname.
- TLS certificates for SMTP.
- SPF, DKIM, DMARC DNS records for each sending domain.
- Firewall restricting SMTP submission/admin ports.
- DKIM private keys stored securely (secret manager / encrypted at rest), never logged.

## Domain verification
Each sending domain must be verified before campaigns can send from it (enforced when
`REQUIRE_VERIFIED_DOMAIN=true`). Verification performs actual DNS lookups:

- SPF: `TXT` record at the domain apex starting with `v=spf1`.
- DKIM: `TXT` record at `<selector>._domainkey.<domain>` containing `v=DKIM1` and `p=`.
- DMARC: `TXT` record at `_dmarc.<domain>` starting with `v=DMARC1`.

Statuses: `pending` → `verified` (all required records present) → `misconfigured` (records missing
or invalid). Verification does not pretend to pass; missing DNS blocks sending.

## Target bounce classification
| Type | SMTP signal | Action | Status |
|------|-----------|--------|--------|
| Hard bounce | 5xx permanent rejection | Suppress address, mark job bounced | ✅ Implemented |
| Soft bounce / deferral | 4xx temporary failure | Retry with exponential backoff | ✅ Implemented |
| Permanent rejection | 5xx after max attempts | Dead-letter, mark failed | ✅ Implemented |
| Complaint | Feedback loop / FBL event | Suppress immediately | ✅ Implemented via webhook |
| Blocked | Reputation/blocklist rejection | Mark bounced, investigate | ✅ Implemented |

## Retry policy
Implemented exponential backoff: `2^(attempt-1)` minutes, capped at 6 hours by the current
worker implementation. Max attempts default to 5 and are configurable via
`MAX_DELIVERY_ATTEMPTS`. **Maximum message age** is enforced via `MAX_MESSAGE_AGE`
(default 48h): jobs older than this are dead-lettered rather than retried.
Exhausted jobs are moved to the dead-letter queue (`dead_letter_queue` table) and can be
manually replayed via `POST /api/v1/delivery/dead-letter/{id}/replay`. Per-domain
concurrency limits are enforced at runtime (configurable via `DOMAIN_CONCURRENCY_LIMIT`).
Automatic operational pause on abuse signals is supported via
`organization_settings.paused_until`.

## Idempotency and crash recovery
- Every delivery job has a unique `id` and `message_id`.
- Before submitting, the worker checks persisted job status; a job already in a terminal state
  is not re-submitted.
- After a successful SMTP submission but before DB update (uncertain outcome), the job is
  reconciled on the next poll: `submitted` jobs are not duplicated.
- Worker restart re-publishes `queued`/`deferred` jobs whose `next_attempt_at <= now()` and
  have no in-flight NATS message (`RepublishPending`).
- Job processing is idempotent: `max_deliveries` on the NATS consumer plus DB status checks.

## Sending limits and warm-up
- Conservative defaults: 20 emails/second per worker, max 5 attempts.
- Verified sender domains required; tenant quotas enforced.
- New sending IPs require warm-up: gradual volume increase, reputation monitoring,
  separate transactional/marketing identities.
- Per-domain concurrency controls, tenant quotas, and automatic operational pause on abuse signals remain future work.

## Compliance
- RFC 8058 one-click unsubscribe: `List-Unsubscribe` and `List-Unsubscribe-Post` headers on
  marketing messages; clear unsubscribe link in every email.
- Signed, expiring unsubscribe/preference tokens (365-day TTL).
- Open tracking treated as approximate (privacy protections, image blocking).
- Include a physical mailing address in marketing emails where required by law; the current footer does not yet provide a configurable address.
- No purchased-list blasting, no sender-identity concealment, no spam-enabling features.

## Monitoring
Track: queue backlog, worker throughput, SMTP connection failures, deferred messages,
hard bounce rate, complaint rate, unsubscribe rate, authentication failures, disk space.
Alert on: queue growth, worker failure, Stalwart unavailable, database unavailable, high
bounce/complaint rates, domain authentication failures.
