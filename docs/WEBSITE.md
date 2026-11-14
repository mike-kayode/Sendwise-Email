---
title: Public website
description: Marketing pages, contact form, legal pages, and public auth shell
---

# Public website

The public marketing site is implemented in `apps/web` using the App Router.
Routes in the `(marketing)` group share the marketing header, footer, responsive
navigation, persistent light/dark theme toggle, and skip link. The dashboard
remains under `/dashboard`; authenticated visitors to `/` are sent there by the
home-page client redirect.

## Routes

- `/` — home
- `/platform`, `/developers`, `/pricing`, `/about`, `/contact`
- `/legal` and `/legal/privacy`, `/legal/terms`,
  `/legal/acceptable-use`, `/legal/data-protection`
- `/login`, `/register`, `/forgot-password`, `/auth/verify`,
  `/auth/reset-password` — existing authentication flows with the public site
  shell and styling

## Editing content

- Shared design tokens, responsive styles, and components are under
  `apps/web/components/marketing/`.
- The login and registration pages use the supplied local Sendwise dashboard
  illustration at `apps/web/public/images/sendwise-dashboard.png`.
- Marketing page copy and page layouts are in
  `apps/web/components/marketing/pages.tsx`.
- Change plan prices, limits, and features in
  `apps/web/lib/marketing/pricing.ts`. The current monthly prices and email
  volumes are Free (3,000/month, 100/day), Starter (10,000/month, ₦5,000),
  Business (50,000/month, ₦15,000), and Enterprise (200,000/month, ₦50,000).
  Monthly/yearly selection is a display toggle and does not change billing.
- Replace legal section text and metadata in
  `apps/web/content/legal/documents.ts`. Legal copy is intentionally a
  placeholder and must be reviewed by counsel before launch.
- Update the public canonical origin using `NEXT_PUBLIC_SITE_URL`. It defaults
  to `https://sendwise.ng`; set it to the actual site origin for builds and
  deployments.
- Contact notification delivery is disabled when `CONTACT_NOTIFY_EMAIL` is
  empty. Submissions are still stored. Set the address to enable notification
  email through the API's configured platform SMTP relay.

## Owner TODOs before public launch

- Confirm yearly billing prices and any discount; no annual discount is
  currently applied or advertised. The yearly selector asks users to contact
  Sendwise instead of assuming an annual rate.
- Confirm daily caps for paid tiers and any sending-domain, sender-address, and
  log-retention limits before publishing them.
- Confirm plan audiences and included features before treating them as contract
  terms.
- Set the pay-as-you-go rate and usage terms. The current callout directs
  customers to contact Sendwise and does not promise a rate.
- Confirm operational policies and payment methods referenced in the pricing
  FAQs before offering definitive answers.
- Replace the About page `[YOUR STORY]`, `[YOUR MISSION]`, `[CITY, COUNTRY]`,
  `[NAME]`, and `[ROLE]` placeholders with approved company and team details.
- Replace `[EMAIL ADDRESS]`, `[SUPPORT EMAIL]`, `[RESPONSE TIME]`, and
  `[ABUSE EMAIL]` on Contact with monitored addresses and an accurate response
  commitment.
- Replace each legal section's `[LEGAL TEXT: written or reviewed by your
  lawyer]` and `[DATE]` with approved text and the real effective date.
- Review the sample API hostnames, SMTP hostnames, response example, and
  integration credentials in Developers against the production service.

## Contact endpoint and migration

`POST /api/v1/public/contact` accepts a validated name, email, topic and
message. The hidden `website` honeypot is accepted without storing a message.
Requests are limited per client IP. Valid submissions are stored in
`contact_messages` by migration `0032_public_contact_messages.sql`; no public
read endpoint is provided. Notification delivery can be enabled with
`CONTACT_NOTIFY_EMAIL`. The existing Caddy setup already sends `/api/*` to the
gateway and other paths to the web app. No production Caddyfile change is
required; verify equivalent routing if using a different proxy.

## Build and test

From `apps/web`, run `npm run typecheck`, `npm run lint`, `npm test`,
`npm run build`, and `npm run test:e2e`. The marketing Playwright suite is
`apps/web/e2e/marketing.spec.ts`; it produces viewport screenshots in the test
output directory. API validation tests are in `internal/api/public_contact_test.go`;
the storage/notification integration test requires `TEST_DATABASE_URL` or
`INTEGRATION_DATABASE_URL`.
