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
MAX_BOUNCE_RATE=0.02
MAX_COMPLAINT_RATE=0.0005
```

`RECIPIENT_DOMAIN_RATE_LIMITS` is a comma-separated list of `domain=messages_per_second`; `default` is required. A configured parent domain also applies to its subdomains. The worker applies both the global rate and the recipient-domain rate, and retains the existing per-domain concurrency limit.

When authenticated bounce or complaint feedback is ingested, it updates recipient
and delivery state. When a campaign exceeds either configured threshold,
delivery pauses and an alert appears on the campaign detail page. Hard bounces
and complaints are added to the organization suppression list; campaign
snapshots and the worker both exclude suppressed addresses. Stalwart-to-app
collection is not configured yet, so downstream DSNs and ARFs are not
automatically ingested.

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

For sustained or high-volume campaigns, do not assume a single self-hosted IP is ready for bulk delivery. Start with a low daily volume, then raise it gradually (for example 50, 100, 150, 225) only after a healthy day; the current generic schedule grows by 1.5x. Keep per-domain volume limits and stop the ramp when hard bounces, complaints, or deferrals rise. A future increase in volume should be gradual and specific to the sending domain and IP.

Before sending to external recipients:

- Confirm outbound TCP port 25 is available from the server. If it is blocked,
  arrange an authenticated relay rather than trying to bypass the provider's
  restriction.
- Set forward-confirmed reverse DNS: the sending IP's PTR should name the mail
  host, and that hostname's A/AAAA record should resolve back to the IP.
- Publish one SPF record, the DKIM key actually used by the app for each
  sending domain, and a DMARC record. Monitor DMARC reports before tightening
  policy.
- Monitor blocklists (including Spamhaus) and investigate listings before
  increasing volume.
- Register the sending IP/domain with Google Postmaster Tools and Microsoft
  SNDS/JMRP where eligible; review reputation, deferrals, and complaint signals.
- Set up recipient-provider feedback loops where available. Remove hard
  bounces and complaints immediately, honor unsubscribe requests, and do not
  re-add suppressed contacts without a valid consent event.

Add another sending IP only when the current IP is consistently at its intended
volume, its reputation and feedback metrics remain healthy, and there is a
separate warm-up and monitoring plan for the new IP. More IPs do not guarantee
inbox placement and can dilute volume or multiply operational risk.

## Throughput proof status

The opt-in local SMTP discard-sink test `TestBulkCampaignSMTPThroughput100k`
completed against the local scratch Postgres database:

```
$ RUN_BULK_THROUGHPUT=1 TEST_DATABASE_URL=****** go test ./internal/worker -run '^TestBulkCampaignSMTPThroughput100k$' -count=1 -v
=== RUN   TestBulkCampaignSMTPThroughput100k
bulk-sink: recipients=100000 elapsed=9m16.346s sends_per_second=179.74 peak_heap_bytes=33619928 heap_after_bytes=13831752 queue_depth_peak=100000 failures=0 warmup_reserved=100000
--- PASS: TestBulkCampaignSMTPThroughput100k (563.73s)
PASS
ok  	github.com/khayode/email-platform/internal/worker	564.393s
```

This proves application throughput against the test's local SMTP sink only. It
does not measure Stalwart queue depth or resource use, and does not prove
production relay throughput, inbox placement, or deliverability. A 100,000
recipient run through real Stalwart and a local remote-MX sink has not yet been
completed.
