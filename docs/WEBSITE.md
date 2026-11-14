---
title: Public website
description: Marketing pages, contact form, and public authentication shell
---

The public marketing site is implemented in `apps/web` using the App Router.
Routes in the `(marketing)` group share the marketing header, footer, responsive
navigation, persistent light/dark theme toggle, and skip link. The dashboard
remains under `/dashboard`; authenticated visitors to `/` are sent there by the
home-page client redirect.

## Routes

- `/` — home
- `/platform`, `/developers`, `/pricing`, `/about`, `/contact`
- `/login`, `/register`, `/forgot-password`, `/auth/verify`,
  `/auth/reset-password` — existing authentication flows with the public site
  shell and styling

Legal routes are intentionally not published until approved policy text is
available. They return 404 rather than displaying generated or placeholder
terms.

## Editing content

- Shared design tokens, responsive styles, and components are under
  `apps/web/components/marketing/`.
- The login and registration pages use the supplied local Sendwise dashboard
  illustration at `apps/web/public/images/sendwise-dashboard.png`.
- Marketing page copy and page layouts are in
  `apps/web/components/marketing/pages.tsx`.
- Change published monthly prices and email volumes in
  `apps/web/lib/marketing/pricing.ts`: Free (3,000/month, up to 100/day),
  Starter (10,000/month, ₦5,000), Business (50,000/month, ₦15,000), and
  Enterprise (200,000/month, ₦50,000). Paid-plan payment and activation are not
  automated in the app; the pricing page directs those questions to Contact.
  Annual prices are not published.
- Update the public canonical origin using `NEXT_PUBLIC_SITE_URL`. It defaults
  to `https://campaign.pipsleague.com`, matching the configured public site
  origin in `.env.example`.
- Contact notification delivery is disabled when `CONTACT_NOTIFY_EMAIL` is
  empty. Valid submissions are stored; set the address to enable notification
  email through the API's configured platform SMTP relay. The public form does
  not promise a response time.

## Public-content boundaries

- The About page describes product behavior verified in the repository; it
  does not invent a founder story, location, or team roster.
- Contact and abuse reports use the validated public contact form. No direct
  contact email or response-time commitment is published.
- The Developers page documents registered routes only. Its REST examples use
  `POST /api/v1/transactional/messages`; no batch or suppression route is
  claimed. The public API base follows `NEXT_PUBLIC_SITE_URL`.
- SMTP submission is enabled only where configured. The public page does not
  invent a public SMTP hostname.
- Provide counsel-approved legal policies and a confirmed billing/activation
  process before publishing those details. The site does not fabricate either.

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
