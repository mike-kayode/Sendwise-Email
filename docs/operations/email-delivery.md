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

## Recipient delivery journey

Migration `0039_delivery_journey.sql` adds tenant-scoped current journey state and an
append-only, event-hash-deduplicated timeline per campaign recipient. The API exposes
`GET /api/v1/campaigns/{id}/recipients/{recipientId}/journey`; campaign reports expose
stage counts and the stale-submission count, and recipient CSV exports include the last
stage, SMTP code, reason, attempt count, and remote MX host.
The migration is forward-only. For an application rollback, revert the application
image while leaving these additive columns, tables, and status constraints in place;
do not delete journey events or restore the ambiguous legacy `accepted` label.

The customer UI calls the final positive SMTP result **Accepted by recipient server**.
That means only that the recipient's mail server accepted the message; it never means
inbox placement. `submitted_to_mail_server` means Stalwart accepted the SMTP submission.
Events are correlated by the signed VERP return path, Stalwart span ID, or transport
Message-ID, and all persisted records are organization scoped.

The pinned Stalwart image emits delivery telemetry as signed JSON batches. Its
`X-Signature` is Base64 HMAC-SHA256 over the raw request body; this differs from the
timestamped signature on the inbound DSN endpoint. For the pinned `v1.0.3` build, the
configured event set includes `delivery.attempt-start`, `delivery.attempt-end`,
connection/TLS/SMTP failures, `delivery.completed`, `delivery.delivered`,
`delivery.failed`, `delivery.dsn-success`, `delivery.dsn-temp-fail`,
`delivery.dsn-perm-fail`, and `queue.rescheduled`.

Configure the Stalwart webhook using the dry-run-by-default script. Do not put
credentials on the command line or in shell history; supply them as environment
variables through the operator's secret manager:

```sh
export STALWART_MANAGEMENT_URL='http://127.0.0.1:8081'
export STALWART_ADMIN_USERNAME='<operator>'
export STALWART_ADMIN_PASSWORD='<secret-manager value>'
export STALWART_EVENTS_WEBHOOK_URL='https://<sendwise-host>/api/v1/webhooks/stalwart-events'
export WEBHOOK_SECRET='<same secret configured for Sendwise>'
./scripts/configure-stalwart-events.sh
./scripts/configure-stalwart-events.sh --apply
```

The default invocation makes no network request. `--apply` creates or updates the
matching webhook on the Stalwart JMAP API. Never enable invalid TLS certificates for a
production endpoint. The Sendwise deploy smoke test verifies that the telemetry route
rejects an unsigned body and accepts a signed empty test batch without creating data.

The worker reconciles stale submitted/relaying recipients against Stalwart's JMAP
queue with one queue snapshot per tick: it pages `x:QueuedMessage/query` and
`x:QueuedMessage/get`, respects the JMAP session `maxObjectsInGet`, and indexes
messages by lower-cased VERP `returnPath`. DSN queue messages (`flags.dsn`) are
counted but ignored for campaign-recipient matching.

Recipient `status` is decoded as either the legacy string form or the tagged object
form. `@type` mapping is case-insensitive: `Scheduled` -> `relaying`,
`TemporaryFailure` -> `deferred_by_recipient`, `PermanentFailure` -> `bounced`,
`Completed` -> `accepted_by_recipient_server` (4xx/5xx SMTP response codes still take
precedence). Unknown `@type` values or unknown shapes never fail the decode; they
are recorded as `accepted_final_state_unknown` with the raw `@type`/reason included.
Field mapping uses `responseHostname`, `responseCode`, `responseEnhanced`, and
`responseMessage` falling back to `errorMessage`/`errorType`; attempt is
`retryCount + 1`; next retry is `retryDue`, else message `nextRetry`.

Per-job reconciliation errors are isolated: the worker logs a WARN with job id and a
short reason, updates `journey_last_reconciled_at` and `journey_attempts` so the job
goes to the back of the line, and continues the batch. The tick aborts only after 5
consecutive infrastructure failures (auth/connection/TLS/session/EOF). If the queue
snapshot is incomplete or errors midway, no job is marked not found. When a complete
snapshot does not contain the candidate, the job is recorded as
`accepted_final_state_unknown` with reason
`"left Stalwart queue; final outcome not reported"`.

Configure reconciliation with `DELIVERY_JOURNEY_RECONCILE_INTERVAL` (default `1m`),
`DELIVERY_JOURNEY_RECONCILE_BATCH` (default `100`), `DELIVERY_JOURNEY_STUCK_AFTER`
(default `30m`), and optional `DELIVERY_JOURNEY_LEGACY_BOUNCE_DOMAINS` (comma-
separated old bounce domains to match after `BOUNCE_DOMAIN` rotation, e.g.
`bounces.pipsleague.com`).

Each tick emits an INFO log `delivery journey reconciliation tick` with
`candidates`, `queue_size`, `dsn_in_queue`, `found`, `not_found`, `errors`,
`stages`, and `duration`. The `DeliveryJourneyStuck` Prometheus warning fires while
`delivery_journey_stuck_messages` is nonzero. The gauge is exported on the worker
metrics address (`WORKER_METRICS_ADDR`, default `:9091`):

```sh
curl -fsS http://127.0.0.1:9091/metrics | grep delivery_journey_stuck_messages
```

### Delivery timeline and exports

`delivery_journey_events` is append-only. Each event carries a UTC timestamp and a
source (`sendwise`, `stalwart_webhook`, `reconciliation`, `dsn`, `backfill`, or
`tracking`). Timeline event types include `queued`, `picked_up`, `processing`,
`smtp_attempt`, `deferred_by_sendwise`, `submitted_to_mail_server`, `relaying`,
`deferred_by_recipient`, `accepted_by_recipient_server`, `bounced`, `expired`,
`failed`, and `accepted_final_state_unknown`. SMTP attempt/deferral events include
remote MX host, SMTP code, enhanced code, reason, attempt number, and next retry
time when available from Sendwise or Stalwart telemetry.

The campaign report recipient panel shows the full timeline with full UTC timestamps
and local time on hover. Campaign-level delivery events can be exported as CSV from:

```sh
curl -fsS -H "Authorization: Bearer <token>" \
  "https://<host>/api/v1/campaigns/<campaign-id>/journey/events/export" -o journey-events.csv
```

### Delayed messages

Recipients in `submitted_to_mail_server` or `relaying` with no recent Stalwart event
older than `DELIVERY_JOURNEY_STUCK_AFTER` (default `30m`) are shown as **Delayed**
in the campaign report with the latest Stalwart reason. Delayed is a visibility
signal; it does not fabricate a terminal outcome.

### Outbound SMTP egress health

The `egress-probe` service checks outbound TCP port 25 connectivity every
`DELIVERY_EGRESS_CHECK_INTERVAL` (default `5m`, timeout
`DELIVERY_EGRESS_CHECK_TIMEOUT`, default `5s`) to the hosts in
`DELIVERY_EGRESS_CHECK_HOSTS` (comma-separated `host:port`, default
`gmail-smtp-in.l.google.com:25,outlook-com.olc.protection.outlook.com:25,mta5.am0.yahoodns.net:25`).
The probe runs with `network_mode: "service:stalwart"` so it shares Stalwart's
network namespace and measures Stalwart's own outbound path, including the same
sysctls/NAT path. The worker never probes or pauses sending based on its own
container network; it only reads `delivery_egress_health` for metrics and the
optional sending gate.

Hysteresis prevents flapping: status becomes `blocked` only after 2 consecutive
confirmed failed cycles, becomes `ok` after 1 successful cycle, and is `blocked`
only when all configured probe hosts fail. Because the probe shares Stalwart's
network namespace, it can lose connectivity when Stalwart restarts. After any
failed cycle the probe exits non-zero so Docker restarts it into Stalwart's
current namespace; a failure counts toward `blocked` only when it is seen again
after that fresh start. The confirmation counter is persisted in
`delivery_egress_health` (`consecutive_failures`, `unconfirmed_failure`).
`scripts/sync-stalwart-certs.sh` restarts `egress-probe` after restarting
Stalwart so the probe rejoins the current namespace after certificate syncs.

Staleness is fail-open: if the last reading is older than
`3 * DELIVERY_EGRESS_CHECK_INTERVAL`, the worker gate, the
`GET /api/v1/delivery/egress-health` API, the dashboard, and the worker metrics
treat the state as `unknown` with a visible "probe not reporting" state. Stale
readings never pause sends.

Results are stored in `delivery_egress_health`, exposed via
`GET /api/v1/delivery/egress-health`, and included in analytics overview as
`egress_health`.

`DELIVERY_EGRESS_GATE_ENABLED` (default `true`) is a master switch for the send
pause. When enabled and status is `blocked`, the scheduler pauses new campaign
queueing and the worker defers picked jobs with `deferred_by_sendwise`; the
dashboard shows a visible BLOCKED banner with the last check time. Sends resume
automatically when checks recover. When disabled, the dashboard and API still
report egress status, but nothing is paused.

Metrics on the worker metrics address (`:9091`), refreshed by the worker from the
database:

```sh
curl -fsS http://127.0.0.1:9091/metrics | grep delivery_egress
# or, if curl is unavailable in the image:
wget -qO- http://127.0.0.1:9091/metrics | grep delivery_egress
```

`delivery_egress_blocked` is `1` when egress is blocked and `0` otherwise.
`delivery_egress_last_check_timestamp_seconds` records the last check time.
Prometheus alert `DeliveryEgressBlocked` fires at critical severity after 5 minutes
blocked.

### Safe sending speed defaults

Do not raise sending limits blindly; respect warm-up and recipient-domain/provider
limits.

- `SENDING_RATE_PER_SECOND` (default `20`): global Sendwise worker send rate. Keep
  low on new or recovering IPs/domains; increase only after warm-up metrics are
  healthy.
- `DOMAIN_CONCURRENCY_LIMIT` (default `10`): per-recipient-domain worker
  concurrency. Conservative default to avoid provider throttling.
- `RECIPIENT_DOMAIN_RATE_LIMITS`: use tighter limits for large providers where
  needed; keep `default` conservative.

Stalwart queue/retry settings should favor quick initial recovery without aggressive
flooding. Recommended safe starting point: first retries after 1-5 minutes, then
back off (for example 15m, 30m, 1h, 2h...) up to the configured max message age.
Do not configure hour-long initial retry gaps for transient 4xx/connection failures.
Review Stalwart's queue concurrency and retry schedule in Stalwart configuration
and keep them aligned with Sendwise warm-up and domain limits.

The campaign report shows per-campaign stage summary counts (queued, submitted,
relaying, deferred, accepted by recipient server, bounced, expired) and refreshes
automatically.

The super-admin Campaigns console provides a cursor-resumable historical backfill.
First run **Preview next batch** to see counts without writes, then **Apply next
batch** to record those reconciled states. Both operations require fresh MFA and a
typed reason and are written to the immutable admin audit log. Continue batches using
the returned cursor; jobs are never republished or resent by this operation. Jobs
without tracking rows are counted as `untracked`; missing Stalwart queue records
become `accepted_final_state_unknown`.

The campaign's **Delivery jobs** panel shows the oldest unresolved messages, their
last known journey stage/error, and the count older than the configured stuck
threshold. Investigate these with the configured JMAP poller and signed event stream;
do not use manual SQL updates to fabricate recipient outcomes.
