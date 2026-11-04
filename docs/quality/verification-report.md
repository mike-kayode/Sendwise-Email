# Verification Report

**Assessment:** NOT READY for production
**T0 run:** 2026-11-04, local Docker (Compose canonical two-file invocation), evidence in [SYSTEM_STATE](../SYSTEM_STATE.md).

## Evidence (T0 run, 2026-11-04)

| Area | Status | Evidence |
|---|---|---|
| Go build / vet / unit tests, current tree | **PASS** | `go build ./...`, `go vet ./...`, `go test ./...` after restoring `internal/api/contact_imports.go` to `package api` |
| Compose config (combined) | **PASS** | `docker compose -f docker-compose.yml -f docker-compose.microservices.yml config -q` |
| Compose stack start (combined, fresh volume) | **PASS (with committed T0 fixes)** | Init script exec bit, postgres DB env vars, gateway `LEGACY_API_URL`, MinIO profiled out. All migration containers exited 0; `api`/`identity`/`workspace`/`gateway` `/healthz`+`/readyz` 200; `web` 200; Caddy `:80` proxies `/api/*` and `/t/*` |
| Base-only / microservices-only compose | **NOT STANDALONE** | Caddy hard-routes `/api/*` to `gateway:8080`; base-only lacks gateway, micro-only lacks legacy api. Combined invocation is the documented stack |
| Identity auth journeys (via gateway) | **PASS** | register 201 → verify 200 → login 200 → me 200; refresh rotation 200 + old-cookie replay 401; logout revokes (refresh 401); forgot→reset→login 200; reset-token reuse 400 |
| Workspace journeys (via gateway) | **PASS** | orgs list/get/patch, members, invitations create/list/accept/revoke — 200/201/204 as designed; internal endpoints 200 with service token, 401 with wrong token |
| Legacy API authenticated routes | **PASS (most), see failures** | Contacts CRUD+export+consent, lists, segments, templates, domains, campaigns lifecycle (send→409 state guard), suppressions, analytics overview/report, dead-letter, public subscribe — all exercised live with results in SYSTEM_STATE §B |
| Contact import end-to-end | **PASS (backend)** | multipart CSV → 202 → **worker completed**: 3 rows, 2 imported, 1 invalid (row 3); API tenant-scope test PASS. **No UI**; storage is PostgreSQL `bytea` |
| `DELETE /api/v1/tags/{id}` | **FAIL** | 404 live; route not registered |
| `POST /api/v1/api-keys` | **FAIL** | panic in `auth.randomHex` (base64 slice `[:64]` on 43 chars) → 500 |
| `POST /campaigns/{id}/resume` (domain unverified) | **FAIL** | 500 `internal_error`; queue error not mapped |
| `POST /campaigns` with unknown `list_id` | **FAIL** | 500 FK violation instead of 400 |
| `GET /analytics/campaigns/{invalid-uuid}` | **FAIL** | 500 instead of 400/404 |
| Bounce/complaint webhooks | **FAIL (security)** | Unauthenticated forged bounce → 204 **and created a suppression row** |
| `GET /public/unsubscribe` with valid token | **FAIL (safety)** | Contact flipped `subscribed` → `unsubscribed` on GET |
| Gateway readiness with Legacy API stopped | **FAIL** | `/readyz` still 200 (re-tested in T0) |
| Contact import worker integration test | **FAIL** | `TestContactImportWorkerPersistsPendingContactsAndResults`: row 3 reported, test expects 4 |
| Legacy API integration group | **FAIL (6/6)** | Fresh DB: tests expect register to return `access_token`/`user`/`organization`; handler returns `{message,email}`. API-key test would additionally hit D1 |
| Frontend typecheck | **PASS** | `npm run typecheck` exit 0 |
| Frontend unit tests | **FAIL (3/4)** | `npm test` exit 1. PASS: `apiUrl uses same-origin paths…`, `login location removes all query values…`, `login submits credentials only in a POST JSON body…`. FAIL: `network fetch failures become a clear login message…` — test expects “Cannot reach the server. Please try again.” but the code returns “…Please check your connection and try again.” (stale expectation, D5) |
| Frontend production build | **PASS** | Next.js production build inside the `web` image build |
| Mail delivery (verification/reset) | **FAIL** | Stalwart bootstrap mode (no SMTP listener; identity dial refused → 503); legacy Resend key absent. Journeys completed only via `MAIL_PROVIDER=log` links / DB-read legacy tokens |
| System from-address | **PASS (changed)** | `noreply@pipsleague.com` → `hello@pipsleague.com` in `.env`, compose defaults, `.env.example`; confirmed inside restarted containers |
| `auth_audit` writes | **PASS (fresh schema)** | 21 rows written live (register/login/verify/reset/refresh/reuse); forward migration for pre-default schemas still owed |
| Web unit/browser API URL helpers | PASS (prior unit runs) | Same-origin construction tests; not re-run beyond full `npm test` above |
| PostgreSQL provisioning (fresh) | **PASS (fresh volume)** | Init script creates identity/workspace roles+dbs; three migration sets exit 0 |
| NATS event delivery/recovery | NOT VERIFIED | Containers up; publish/consume/recovery checks not run |
| TLS / SMTP / provider delivery | NOT VERIFIED | Local Caddy HTTP only; Stalwart unusable in bootstrap mode |
| CI workflow | NOT EXECUTED | `.github/workflows/ci.yml` present, not run |
| Production server parity | NOT INSPECTED | No server access in T0 |

## Source observations (not runtime passes)

- Gateway production fail-fast requires the three upstream URLs (`services/gateway/config.go`); `SERVICE_TOKEN` is passed by compose but not validated by gateway config.
- Identity login/refresh issue refresh tokens only in `HttpOnly; SameSite=Lax` cookie scoped to `/api/v1/auth`; production forces `Secure`.
- Legacy auth routes are not gateway-reachable (gateway maps `/api/v1/auth/*` to identity); they remain on the loopback-bound legacy port as a rollback path.
- `List-Unsubscribe` and one-click `List-Unsubscribe-Post` headers are set in `internal/worker/worker.go`; not observed in a delivered message (no delivery).
- The log mailer now prints the message link (development only; `MAIL_PROVIDER=log` must never be set in production).
- Compose files still supply default PostgreSQL/MinIO passwords when unset (e.g. `POSTGRES_PASSWORD:-email`); no secret scan or production-secret review was run in T0.

## Login error interpretation

The Identity handler checks the password before checking email verification. An
existing account with a wrong password returns the same `401 invalid_credentials`
as an unknown email. Only a correct password on an unverified account returns
`403 email_not_verified` (live-verified in T0), after which the login UI links to
the verification/resend page. Registration returns `409 email_in_use`, which
reveals account existence. No additional pre-password verification disclosure was
introduced.

## Development seed command (local only)

A development-only Identity seed command is guarded by `APP_ENV=development` and
accepts credentials only through environment variables (it does not print the
password or email). To use it: apply identity migrations, then set
`APP_ENV=development`, `DATABASE_URL`, `DEV_SEED_EMAIL`, `DEV_SEED_PASSWORD`,
`DEV_SEED_NAME`, `DEV_SEED_ORGANIZATION` and run
`go run ./services/identity/cmd/devseed` with a unique local-only password; the
command refuses to run under any other `APP_ENV`.

## T0 release blockers → carried into T1+

- Gateway `/readyz` must fail when Legacy API (and other upstreams) are unreachable; validate `SERVICE_TOKEN`; verify fail-fast at runtime.
- `deploy.sh` currently builds only identity/workspace/gateway — it must operate on the combined stack with backup/migrate/smoke/rollback.
- Fix or remove: D1 api-key panic, D2 resume mapping, D3 FK validation, D4 analytics uuid, D5 web test, D6 integration-test contract, D7 row assertion, D11 tag DELETE, D9 GET unsubscribe, D10 webhook auth (T3), D12 mail (T5).
- Browser-driven e2e (T6): register, verify, login, refresh-after-expiry, logout, reset, import, campaign, test send, tracking.
- Production server inspection, TLS issuance, backups/restore, CI execution, secret scan remain open.

No skipped or unavailable integration check is counted as a pass.
