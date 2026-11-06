# API Specification and Contract Strategy

**Current state:** REST endpoints are split between the legacy API and the extracted Identity, Workspace, and Gateway services; the OpenAPI file is `docs/api/openapi.yaml`. This catalog describes current API groups at a high level. Exact request/response definitions must be kept in the OpenAPI contract and tested against handlers.

## Current public API groups

| Group | Current route patterns | Auth / limitations |
|---|---|---|
| Health | `GET /healthz`, `/readyz`, `/metrics` | Public health; readiness checks PostgreSQL |
| Auth | `POST /api/v1/auth/register`, `/login`, `/refresh`, `/verify`, `/resend-verification`, `/forgot-password`, `/reset-password`, `/logout`; `GET /api/v1/auth/me` | Access token uses bearer JWT; refresh token is an HttpOnly cookie. Handler tests exist; database/provider end-to-end flow is unverified. |
| Public audience | `POST /api/v1/public/subscribe`; GET/POST unsubscribe | Public token/path; GET currently mutates |
| Forms | `GET/POST /api/v1/forms`, `GET/PATCH/DELETE /api/v1/forms/{id}`, publish/unpublish, paginated submissions; public definition, submit, and confirm under `/api/v1/public/forms/{slug}` | JWT and audience-manager writes; public slugs are globally unique; published forms only; submission validates consent, honeypot, rate limits, suppression, and double opt-in |
| Tracking | `GET /t/open/{token}`, `/t/click/{token}` | Signed tokens; event dedup/bot filtering incomplete |
| Contacts/imports | contacts CRUD, import, import status, export | JWT; mutation roles and tenant scoping in handlers |
| Lists/tags/segments | paginated list/tag/segment collections; list detail/update/delete, member CRUD/export, contact tag assignment, grouped-rule preview and paginated segment contacts | JWT; reads and writes are organization-scoped; segment values are parameterized |
| Templates | list/create | JWT; no update/delete/editor API |
| Campaigns | CRUD, validate, schedule, send, pause, resume, cancel | JWT and route role checks; some referenced IDs need tenant validation |
| Domains | list/create/verify/DNS records | JWT; creates a 2048-bit DKIM keypair and encrypts its private key at rest; DNS verification distinguishes missing, mismatched, and failed lookups for SPF, DKIM, DMARC, and the ownership TXT record |
| Delivery operations | DLQ list/replay, suppressions list/create/delete | JWT; DLQ replay has an ID/error-handling defect |
| Inbound mail events | `POST /api/v1/webhooks/bounce`, `/complaint` | Currently unauthenticated; must be signature-verified before production |
| Analytics | overview and campaign report | JWT; event uniqueness and reports need refinement |
| API keys | list/create/delete | Owner/admin; key auth and scopes are not wired into request authentication |

## Target API boundaries

- Gateway presents stable `/api/v1` REST resource contracts and routes to service owners.
- Service APIs are private by default, authenticated with service identity (mTLS/workload identity or short-lived signed service tokens), and authorize tenant scope independently.
- External clients receive scoped revocable API credentials; never trust a caller-provided `organization_id` without checking membership/credential scope.
- Bulk operations return `202 Accepted` with a durable operation ID, status resource, bounded result/error payload and cancellation semantics where safe.
- Mutations use idempotency keys for retryable client requests; optimistic concurrency/version fields protect edits.
- Errors use a stable machine code, safe message, request ID and optional field errors. Never return SQL/SMTP secrets.
- Pagination is cursor-based for high-volume event/contact lists; fixed offset is acceptable only for bounded views.
- Audience collection responses use `{data, pagination:{total,limit,offset}}`. Segment rules are `{groups:[{match:"AND"|"OR",conditions:[{field,operator,value?}]}]}`; groups are combined with AND. Supported fields are email, status, tag, list, created_at, and safe `custom_fields.<key>` values.

## Contract verification

1. OpenAPI generation/validation is part of CI; test every route/method against registered routes.
2. Add consumer-driven contract tests for browser/external clients and service internal APIs.
3. Breaking changes require an API version or compatibility window; avoid schema drift between docs and handlers.
4. Rate limits, payload limits, PII classification, auth scopes and audit requirements are specified per operation.
5. No direct service-to-service database access.

## Missing target routes

Workspace/membership/invitations; custom-field administration; template update/version/preview; campaign duplication/approval/test-send; automation runtime; billing/subscriptions; outbound webhook administration; platform administration; API-key authentication; session/device/MFA controls.
