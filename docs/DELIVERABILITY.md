# Deliverability and bulk sending

This guide covers operational controls, not a promise of inbox placement. Recipient providers make independent filtering decisions, and a local throughput test cannot establish deliverability.

## Warm up a sending domain and IP

The worker reserves sending-domain capacity in PostgreSQL before delivery. The initial daily cap is `WARMUP_START_PER_DAY` (default `50`). After each prior calendar day with at least one successful submission, the next cap grows by `WARMUP_GROWTH_FACTOR` (default `1.5`), rounded down, and never exceeds `MAX_DAILY_EMAILS`. Jobs beyond the current cap remain durable and are eligible for the next day. A successful SMTP submission is not evidence that a message reached the inbox.

Start at a conservative volume and increase only while bounce, complaint, and provider feedback remain healthy. Monitor every recipient domain separately. For example:

```env
WARMUP_START_PER_DAY=50
WARMUP_GROWTH_FACTOR=1.5
MAX_DAILY_EMAILS=10000
RECIPIENT_DOMAIN_RATE_LIMITS=default=10,gmail.com=5,yahoo.com=5,outlook.com=5
MAX_BOUNCE_RATE=0.05
MAX_COMPLAINT_RATE=0.001
```

`RECIPIENT_DOMAIN_RATE_LIMITS` is a comma-separated list of `domain=messages_per_second`; `default` is required. A configured parent domain also applies to its subdomains. The worker applies both the global rate and the recipient-domain rate, and retains the existing per-domain concurrency limit.

Campaign bounce or complaint feedback updates recipient and delivery state. When a campaign exceeds either configured threshold, delivery pauses and an alert appears on the campaign detail page. Hard bounces and complaints are added to the organization suppression list; campaign snapshots and the worker both exclude suppressed addresses.

Campaigns require a sender postal address before sending. The address and visible unsubscribe link are added to the HTML and text footers. Messages include `List-Unsubscribe` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click`.

## DNS and mail infrastructure checklist

- Publish an MX record for inbound mail on the domain or configured bounce/feedback domain.
- Publish one SPF record that authorizes the actual outbound relay. Merge mechanisms into the existing record; do not publish multiple SPF records.
- Publish the DKIM public key for the selector configured by the sending domain. Confirm the signer used on the outbound path matches this key.
- Publish a DMARC record, initially with reporting enabled and a policy appropriate to the domain's rollout. Review aggregate reports before tightening enforcement.
- Configure reverse DNS (PTR) for a dedicated sending IP and make its forward DNS resolve back to the same IP/hostname.
- Confirm outbound TCP port 25 is available from the sending host/provider. Many cloud providers block it; use an authenticated submission relay if it is unavailable.
- Keep HELO/EHLO, PTR, forward DNS, and the sender domain aligned where the relay supports it.

## Volume and feedback-loop operations

For sustained or high-volume campaigns, use a managed relay such as Amazon SES or Resend rather than assuming a single self-hosted IP is ready for bulk delivery. Configure provider event webhooks for bounces and complaints, verify their signatures, and test that each event suppresses the recipient and pauses campaigns at the configured thresholds.

Use provider feedback loops where available. Remove hard bounces and complaints immediately, honor unsubscribe requests, and do not re-add suppressed contacts without a valid consent event. Review complaint and bounce rates by recipient domain and pause sending if they rise unexpectedly.

## Throughput proof status

No 100,000-recipient campaign run against a discard-only SMTP sink is recorded here yet. Until that run reports wall-clock time, sends per second, peak memory, queue depth, and failures, bulk throughput remains **unverified**. Any future sink result proves only application/relay throughput under that test setup, not inbox placement or deliverability.
