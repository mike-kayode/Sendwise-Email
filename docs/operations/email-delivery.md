# Email Delivery Operations

> **Status note (2026-11-17):** This page describes source-verified behavior. Worker and campaign integration tests use isolated PostgreSQL schemas; API integration tests require a dedicated test database. Stalwart production configuration and live SMTP delivery still require separate verification. No production system was changed for the deferred-job recovery work.

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
The delivery worker and PostgreSQL-backed recovery path are covered by repository tests. Live SMTP delivery and production Stalwart configuration are not verified by those tests.
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

## Stalwart outbound port 25
If campaign jobs stay **submitted forever, nothing delivered**, the worker may have successfully
handed messages to Stalwart while Stalwart's outbound connections to recipient MX servers are being
dropped. Check outbound TCP/25 from the host:

```sh
sudo tcpdump -ni eth0 'tcp dst port 25 and (tcp[tcpflags] & tcp-syn != 0)'
```

Compare the same TLS/SMTP connection from the bridge-network Stalwart container and a temporary
host-network container using the same image:

```sh
docker compose exec -T stalwart sh -c \
  'timeout 15s openssl s_client -4 -connect gmail-smtp-in.l.google.com:25 -starttls smtp -brief </dev/null'

docker run --rm --network host --entrypoint sh \
  stalwartlabs/stalwart@sha256:73c684a84c24f5b4206ec266a8deb8986fe44a9f623997e07f084388d943c8bd \
  -c 'timeout 15s openssl s_client -4 -connect gmail-smtp-in.l.google.com:25 -starttls smtp -brief </dev/null'
```

If `tcpdump` shows bridge-container SYNs leaving `eth0` without SYN-ACKs, while the
host-network connection succeeds, the hosting provider may be dropping NAT-forwarded port-25
traffic because of its packet TTL. The workaround is to set the Stalwart container's
`net.ipv4.ip_default_ttl` to `65`; Docker's bridge NAT decrements it to `64` on egress. This is
configured on the `stalwart` service in `docker-compose.yml` and is checked during deployment
smoke tests. To verify the running container setting:

```sh
docker compose exec -T stalwart cat /proc/sys/net/ipv4/ip_default_ttl
```

The outbound smoke-test host defaults to `gmail-smtp-in.l.google.com` and can be overridden with
`OUTBOUND_SMTP_CHECK_HOST`. Set `SKIP_OUTBOUND_SMTP_CHECK=true` only in environments that
intentionally have no internet access.

## Domain verification
Each sending domain must be verified before campaigns can send from it (enforced when
`REQUIRE_VERIFIED_DOMAIN=true`). Verification performs actual DNS lookups:

- SPF: `TXT` record at the domain apex starting with `v=spf1`.
- SPF must authorize the actual Stalwart sending host. Set `SPF_INCLUDE` to the
  platform SPF domain that authorizes it; the `mx` fallback only works when
  the sending domain's MX points to Stalwart.
- DKIM: `TXT` record at `<selector>._domainkey.<domain>` containing `v=DKIM1` and `p=`.
- DMARC: `TXT` record at `_dmarc.<domain>` starting with `v=DMARC1`.

Statuses: `pending` → `verified` (all required records present) → `misconfigured` (records missing
or invalid). Verification does not pretend to pass; missing DNS blocks sending.
Deleting a sending domain removes its Sendwise domain and sender-identity records;
it does not remove DNS entries from the external DNS provider.

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
- Worker startup calls `RepublishPending` with a 500-job batch. It atomically claims due deferred
  jobs and queued jobs that have not yet been successfully published.
- Startup recovery uses the same atomic claim and publish path as the scheduler.
  Every `SCHEDULER_INTERVAL` (15 seconds by default), each worker claims and republishes up to
  1,000 due jobs. Claims use `FOR UPDATE SKIP LOCKED`; only jobs for `sending` campaigns qualify,
  so multiple workers cannot claim the same job and paused/cancelled campaigns remain untouched.
- Migration `0035_delivery_job_publish_claims.sql` stores the claim and NATS-publish timestamps.
  Failed publishes return a job to `deferred` with a 15-second minimum retry delay; stale,
  unpublished claims can be reclaimed after five minutes. NATS publishes use the delivery-job ID
  as the message ID for broker deduplication.
- Warm-up-cap and domain-quota deferrals keep their next eligible time and clear publish markers.
  On resumption the job passes through normal worker checks again; changing or clearing a domain's
  warm-up override makes its deferred jobs due immediately, without bypassing quotas or safety
  controls.
- Job processing is idempotent: `max_deliveries` on the NATS consumer plus DB status checks.
- Campaign managers can cancel active campaigns. Completed and failed campaigns can be deleted;
  active campaigns must be cancelled before deletion.

## Sending limits and warm-up
- Conservative defaults: 20 emails/second per worker, max 5 attempts.
- Verified sender domains required; tenant quotas enforced.
- New sending IPs require warm-up: gradual volume increase, reputation monitoring,
  separate transactional/marketing identities.
- Per-domain concurrency controls, tenant quotas, and automatic operational pause on abuse signals
  are implemented in the worker and should still be validated against production configuration.

## Delivery reliability controls
- Migrations `0036_delivery_reliability.sql` and `0037_domain_warmup_graduation.sql` add delivery
  failure-age and processing lease fields, and per-domain warm-up graduation fields.
- `MAX_MESSAGE_AGE` applies only to repeated transient delivery failures, measured from
  `failure_started_at`. Warm-up, quota, pause, suspended-workspace and admin deferrals clear the
  delivery-age clock and never expire for age. A scheduler wake or override change restarts the
  eligible window.
- SMTP connection, greeting EOF, TLS and relay-infrastructure failures do not consume recipient
  delivery attempts or dead-letter jobs. The SMTP circuit breaker stops job intake after five
  consecutive infrastructure failures in 30 seconds, doubles the cool-down from 30 seconds to at
  most five minutes, and allows one probe to recover.
- Due deferred jobs and stale/processing/queued jobs are claimed atomically with
  `FOR UPDATE SKIP LOCKED`. Publishing is deduplicated with the delivery-job ID as the NATS
  message ID. `NATS_STALE_PUBLISH_TIMEOUT` defaults to ten minutes.
- Dead-letter replay is idempotent. Replay resets attempts and the failure-age clock and only
  requeues jobs that have not already submitted, accepted or delivered.
- Docker campaign workers must use `stalwart:587` for `SMTP_ADDR` and the public relay hostname
  for `SMTP_HOST` (TLS certificate identity). Do not point the worker at the host's public IP.

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
