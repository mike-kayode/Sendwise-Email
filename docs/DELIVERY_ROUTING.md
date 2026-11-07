# Workspace delivery routing

Sendwise routes platform mail through Stalwart with Resend as a configured
fallback. Campaign mail defaults to Stalwart and can use the workspace's
encrypted Resend API key. Provider requests use fake Resend endpoints in tests;
the application does not make live Resend calls during test runs.

## Routing table

| Message kind | Workspace setting | Ordered providers | Cross-provider fallback |
|---|---|---|---|
| Verification, password reset, invitation | Platform | Stalwart, then Resend when `PLATFORM_RESEND_API_KEY` is configured | Yes; the Stalwart circuit breaker opens after five failures in 60 seconds for two minutes |
| Campaign | Unconfigured workspace | Stalwart | No |
| Campaign | `provider=stalwart` | Stalwart | Only when an explicit fallback is configured and the sender domain is authenticated by both providers |
| Campaign | `provider=resend` | Resend | No implicit fallback; an explicit fallback still requires both domain-authentication flags |

The resolver enforces the two-provider authentication check before returning
an optional campaign fallback. The workspace settings response never includes
an API key; it only reports whether one is set and its last four characters.
Keys are encrypted at rest using the configured `DKIM_ENCRYPTION_KEY` scheme.

**Current webhook secret choice:** `RESEND_WEBHOOK_SECRET` is one platform-wide
Svix signing secret. Per-workspace webhook secrets are not supported. Resend
events are attributed only through the `workspace_id`, `campaign_id`, and
`message_id` tags on the email and are applied only to a matching delivery job
inside that workspace.

## Key handling

Use separate Resend API keys for platform transactional mail and each client
workspace. Set `PLATFORM_RESEND_API_KEY` for platform failover; a workspace
owner configures the workspace key in Settings. Do not copy client keys into
the platform environment or commit keys to the repository. The shared limiter
keys its account-wide budget by a one-way digest of the API key, so platform
mail receives the next available request slot before queued campaign requests
when both use the same key. API, worker, and Identity processes coordinate
through Redis; a configured Redis instance is required for cross-process
account-wide scheduling.

Relevant environment variables:

```env
PLATFORM_FROM_EMAIL=notifications@example.com
PLATFORM_RESEND_API_KEY=
RESEND_WEBHOOK_SECRET=
RESEND_REQUESTS_PER_SECOND=8
```

`RESEND_WEBHOOK_SECRET` is a Svix `whsec_...` secret. Configure Resend to send
its events to `/api/v1/webhooks/resend`. Requests with an invalid signature or
a timestamp outside the five-minute acceptance window are rejected. Svix IDs
are recorded transactionally to prevent replayed events from changing delivery
state more than once. Hard bounces and complaints create workspace-scoped
suppressions; soft bounces are recorded without suppression.

## Domain DNS

For Stalwart, use the app-generated SPF and DKIM values shown for the sending
domain, publish a DMARC policy, and verify the records through the domain
verification action. For Resend, publish the SPF/DKIM records returned by
Resend's Domains API; those values are displayed as returned by the provider,
not synthesized from the app's Stalwart DKIM key. Do not publish multiple SPF
records; merge authorized senders into the existing record. Set a DMARC policy
for the From domain and ensure the actual signing provider aligns with it.

For self-hosted Stalwart, also configure MX as needed for inbound/bounce
handling, reverse DNS (PTR) for the sending IP, matching forward DNS, and
outbound TCP port 25. If the host's provider blocks port 25, use an authenticated
relay. For Resend, follow the DNS and domain-status guidance in its Metrics
page and wait for its domain verification state before sending.

The API currently stores one provider's Resend domain ID and DNS records per
sending-domain row. Although the resolver refuses cross-provider fallback
unless both authentication flags are true, an end-to-end UI flow for
authenticating one domain with both providers is **not yet implemented**.
Do not configure a campaign fallback until both provider-specific flags have
been verified.

## Resend campaign safety

The batch API sends at most 100 messages per call. Each message has an
idempotency key and workspace, campaign, and message tags. Requests share a
per-API-key rate limiter; platform messages have priority over campaign mail.
429 responses honor `Retry-After`/rate-limit reset headers and transient 5xx
responses retry with bounded backoff.

The requested fixed warm-up ramp of 200, 500, 1,000, 2,500, 5,000, 10,000,
20,000, 40,000, and 100,000 per day, advancing only after a day below 2%
bounces, is **not yet implemented**. Current generic worker warm-up settings
are documented in [DELIVERABILITY.md](./DELIVERABILITY.md) and must not be
mistaken for this Resend-specific schedule. Rolling Resend feedback pause
threshold logic is also not yet verified over a production-like campaign.

Monitor Resend's Metrics page for sending volume, delivery delays, bounces,
complaints, opens, and clicks. Pause sending and investigate list quality,
domain authentication, and provider feedback when rates rise. Application
throughput or successful provider acceptance is not evidence of inbox
placement.

## Verification status

Postgres-backed fake-webhook tests cover signature acceptance/rejection,
stale timestamps, replay deduplication, all supported event types, suppression
behavior, and cross-workspace isolation. The complete Playwright routing
journey and 100,000-recipient fake-Resend throughput run are **not yet
verified**; see [STATUS.md](./STATUS.md).
