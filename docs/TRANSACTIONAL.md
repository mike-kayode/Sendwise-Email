---
title: "Transactional sending and quotas"
description: "Transactional API and SMTP sending, per-domain quota behavior, and configuration."
---

Transactional sending is available through the Sendwise REST API and, when
enabled, the SMTP submission relay. Both paths use the same per-sending-domain
quota policy. Campaign deliveries use the marketing stream; transactional API
and SMTP recipients use the transactional stream.

## Send a message through the REST API

Create a transactional API key with the `send` scope, then submit to
`POST /api/v1/transactional/messages` with that key as a bearer token. The key
must belong to a workspace with transactional sending enabled, and the sender
domain must be verified, SPF/DKIM authenticated, enabled for transactional
sending, and not disabled by a platform admin.

When a domain quota is exhausted, the API responds with HTTP `429`, an error
code of `quota_exceeded`, the exhausted window, and its UTC reset time. The
`Retry-After` header gives the number of seconds until the limit can be retried.
Clients should wait at least that long before retrying.

## Domain quota model

Each sending domain has independent `marketing` and `transactional` streams.
Each stream has daily, monthly, and per-second limits:

- A domain override takes precedence over its platform default.
- A `null` domain override inherits the corresponding platform default.
- `unlimited` removes the daily and monthly caps only. It never removes the
  per-second hard safety ceiling or other sending safeguards.
- Daily and monthly accounting uses UTC windows. The reset timestamps are
  returned with the customer-facing domain quota snapshot.
- The per-second limit cannot exceed `PLATFORM_MAX_DOMAIN_RATE`.

The transactional API reserves quota for each recipient when it accepts a
message. Reservations are atomic across concurrent requests. A quota rejection
does not enqueue the message. If a delivery fails before acceptance by the
mail transfer agent, its reservation is released; once accepted by the MTA, its
quota remains counted.

The SMTP relay returns a temporary `452 4.5.3` response when a domain quota is
exhausted so the submitting client can retry later. Other SMTP validation and
authentication outcomes keep their own response codes.

Campaign workers reserve marketing quota per delivery. When a daily, monthly,
or rate quota is reached, remaining deliveries are deferred to the relevant
reset time and retained for worker resumption rather than failed or discarded.

Unlimited quota settings do not bypass workspace suspension, sending-domain
verification or disablement, suppression checks, abuse auto-pauses, or the
per-second hard safety ceiling.

The marketing campaign worker also applies the sending domain's warm-up
profile. A temporary super-admin warm-up override replaces that profile curve
only while its expiry is in the future, for at most 14 days. The effective
override cap is limited by the domain's daily marketing quota unless that
quota is unlimited. This setting does not change successful-day history or
`sending_domain_daily_stats`, and it does not bypass monthly or per-second
quotas, recipient-domain rate limits, delivery-age checks, DNS verification,
suspension, suppression, or reputation auto-pauses. Expiry is evaluated at
read/send time; deferred campaign deliveries remain eligible for the next
daily window. Transactional API and SMTP deliveries continue to use their
transactional quotas and do not use the marketing warm-up curve.

## Customer quota endpoints

- `GET /api/v1/domains/{id}/quotas` returns effective limits, current daily,
  monthly, and per-second usage, and UTC reset times for both streams.
- `POST /api/v1/domains/{id}/quota-requests` records a requested limit increase
  and reason for a stream. The request is available to platform admins from
  that domain's admin detail page; it does not send an email notification.

## Admin controls

Super admins can inspect and change per-domain quotas and platform defaults in
the admin console. Writes require a typed reason and MFA re-verification within
five minutes. The Identity service stores an immutable audit record with the
before and after values. Support admins can read quota data and defaults but
cannot change them.

See the [platform admin guide](./ADMIN.md) for role permissions and the
administrative routes.

## Environment variables

Quota-related environment settings are seed defaults. Once seeded, database
values are authoritative and can be changed through the super-admin console.

| Variable | Purpose |
| --- | --- |
| `SENDING_RATE_PER_SECOND` | Initial marketing per-second quota default. |
| `MAX_DAILY_EMAILS` | Initial marketing daily quota default; the separate warm-up logic also uses its warm-up settings. |
| `TRANSACTIONAL_REQUESTS_PER_SECOND` | Initial transactional per-second quota default. |
| `TRANSACTIONAL_DAILY_LIMIT` | Initial transactional daily quota default. |
| `TRANSACTIONAL_MONTHLY_LIMIT` | Initial transactional monthly quota default. |
| `PLATFORM_MAX_DOMAIN_RATE` | Hard per-domain per-second ceiling. No domain override can exceed it. |

The migration adding the quota tables is `0033_domain_quotas.sql`. Apply
migrations using the normal deployment process before starting services that
use quota enforcement. Migration `0034_domain_warmup_overrides.sql` adds the
temporary warm-up override fields and validation constraints. This change adds
no environment variables. No production migration or runtime verification is
claimed by this repository change; operators must verify deployment behavior
against their own staging environment before rollout.
