# Verification Report

**Assessment:** NOT READY for production  
**Scope:** Authentication/browser API routing, login URL handling, extracted identity/workspace/gateway services, and `docker-compose.microservices.yml`.

## Evidence

| Area | Status | Evidence |
|---|---|---|
| Go tests, current working tree | FAIL | `go test ./...` fails with mixed `api`/`main` packages in the pre-existing uncommitted `internal/api/contact_imports.go`. |
| Go tests, committed baseline `2066258` | PASS | `go test ./...`; DB-backed tests were not enabled in this run. |
| Go static analysis | PASS, PRIOR RUN | `CGO_ENABLED=0 go vet ./...`; not rerun in T0. |
| Web TypeScript | PASS, PRIOR RUN | `cd apps/web && npm run typecheck`; not rerun in T0. |
| Web unit tests | PASS, PRIOR RUN | `cd apps/web && npm test`; not rerun in T0. |
| Web production build | PASS, PRIOR RUN | `cd apps/web && npm run build`; not rerun in T0. |
| API URL and login request helpers | PASS | Tests confirm same-origin URL construction, POST JSON credentials with `credentials: include`, and safe URL cleanup. |
| Network error handling | PASS | Tests confirm fetch `TypeError` maps to “Cannot reach the server. Please try again.” and HTTP errors retain their server message. |
| Production browser API URL | PARTIALLY VERIFIED | Dockerfile and Compose defaults use an empty API base, producing same-origin `/api/v1/...` paths in source; production web image was not rebuilt. |
| Credential query handling | PARTIALLY VERIFIED | Login reads only `callbackUrl` and replaces the current URL with its pathname. Unit tests cover stripping all query data; browser/history/referrer/log audit was not run. |
| Login verification distinction | PASS | Identity handler source and unit test distinguish wrong credentials (`401 invalid_credentials`) from correct-password unverified accounts (`403 email_not_verified`). |
| PostgreSQL init script syntax | PASS, PRIOR RUN | `sh -n infrastructure/postgres/init-databases.sh`; runtime execution failed in T0. |
| Compose YAML syntax | PASS | `docker compose -f docker-compose.yml -f docker-compose.microservices.yml config -q` |
| Identity refresh behavior | PARTIALLY VERIFIED | Handler tests cover HttpOnly refresh-cookie issuance, rotation, and response token omission. No live browser/API/database journey was run. |
| Frontend refresh behavior | PARTIALLY VERIFIED | Typecheck passed; concurrent requests share one refresh attempt in source. No browser-to-service test was run. |
| Compose deployment as checked in | BLOCKED | Compose parses, but the full stack did not start: MinIO pull was denied; PostgreSQL init failed on script permissions and missing DB variables; Gateway lacks `LEGACY_API_URL`. |
| Baseline containers with test-only overrides | PARTIAL | The non-MinIO services started after a temporary Compose override and a permission change in the isolated snapshot. All three migration containers exited 0; this is not a pass for checked-in Compose. |
| Health/readiness endpoints | PARTIAL | `/healthz` and `/readyz` returned 200 on API, Identity, Workspace, and Gateway in the overridden baseline stack. Caddy port 80 returned 308; redirect was not followed. |
| Gateway Legacy API readiness | FAIL | Gateway `/readyz` returned 200 while the Legacy API container was stopped. |
| Authenticated route behavior | NOT VERIFIED | Unauthenticated route sweep confirmed dispatch/auth boundaries only; most protected paths returned 401. `DELETE /api/v1/tags/{id}` returned 404 and has no handler registration. |
| Contact import API | PASS, PARTIAL SCOPE | `TestContactImportAPIQueuesAndScopesProgress` passed against an isolated PostgreSQL schema. This does not verify the UI or full worker path. |
| Contact import worker | FAIL | `TestContactImportWorkerPersistsPendingContactsAndResults` failed because the reported invalid row is 3 while the test expects 4. |
| Legacy API DB integration group | FAIL | Registration returned only message/email while tests expect an access token; following protected requests used an empty token and returned 401. |
| PostgreSQL provisioning and migrations | PARTIAL | Fresh migrations exited 0 only after test-only Compose fixes; unmodified init, supported upgrades, and production roles remain unverified. |
| NATS event delivery/recovery | NOT VERIFIED | NATS container started, but publish/consume/recovery checks were not run. |
| TLS, SMTP, and email-provider delivery | NOT VERIFIED | Stalwart container started, but no controlled verification/reset message or DNS/TLS check was completed. |

## Source observations (not runtime passes)

- Identity login and refresh issue refresh tokens only in a `HttpOnly`, `SameSite=Lax` cookie scoped to `/api/v1/auth`; production configuration forces `Secure`.
- Refresh tokens rotate in the identity store; frontend requests include credentials and retry a 401 once after a shared refresh request.
- Logout sends the refresh cookie for revocation and clears both the server cookie and client session.
- Compose source routes Caddy `/api/*` to Gateway, but the local port-80 request returned 308 and proxy routing was not confirmed. PostgreSQL init did not run as checked in because the mounted script lacked execute permission and its required DB variables were not passed to the container.
- Gateway source validates presence of the three upstream URLs in production, but does not validate `SERVICE_TOKEN`; the Compose gateway environment omits `LEGACY_API_URL` and `SERVICE_TOKEN`.
- Identity verification mail source supports SMTP/Resend configuration, but no verification or reset delivery was established. The legacy API integration log reported that no Resend API key was configured.
- The microservices Compose file still supplies a default PostgreSQL password when unset. No secret scan or production-secret review was run in T0.
- Caddy source routes `/api/*` to Gateway, `/t/*` to the Legacy API, and other paths to Web. The local Caddy request redirected; no API response through Caddy was verified.
- A development-only Identity seed command is guarded by `APP_ENV=development`; it accepts credentials only through environment variables and does not print the password or email.

To run it locally, first apply the identity migrations, then set `APP_ENV=development`, `DATABASE_URL`, `DEV_SEED_EMAIL`, `DEV_SEED_PASSWORD`, `DEV_SEED_NAME`, and `DEV_SEED_ORGANIZATION` in the environment and run `go run ./services/identity/cmd/devseed`. Use a unique local-only password; the command refuses to run under any other `APP_ENV`.

## Login error interpretation

The current Identity handler checks the password before checking email verification. An existing account with a wrong password returns the same `401 invalid_credentials` as an unknown email. Only a correct password on an unverified account returns `403 email_not_verified`, after which the login UI links to the verification/resend page. Registration already returns `409 email_in_use`, which reveals account existence. No additional pre-password verification disclosure was introduced.

## T0 release blockers

- Fix the current working-tree package mismatch and make the unmodified combined Compose stack start; then repeat checks without a test-only override.
- Exercise every authenticated API route with valid tenant-scoped credentials. Current no-auth probes and a subset of integration tests are not sufficient.
- Fix or explicitly remove the missing `DELETE /api/v1/tags/{id}` route and rerun the route sweep.
- Make Gateway readiness fail when Legacy API is unavailable and validate required `SERVICE_TOKEN`.
- Rebuild and restart the production web image, then inspect browser DevTools to verify login uses `https://campaign.pipsleague.com/api/v1/auth/login` with credentials only in the POST JSON body.
- Validate browser history/referrer and edge/application logging behavior for any legacy credential-bearing login URL; client cleanup cannot undo logs from its original request.
- Apply all migration sets to fresh and supported upgrade databases; test the runtime roles with least privilege.
- Run browser-driven registration, email verification, login, refresh-after-expiry, concurrent refresh, logout, and password reset against PostgreSQL and JetStream.
- Verify event delivery after NATS restart and confirm the workspace projection is idempotent.
- Complete tenant-isolation, bounce/complaint authentication, delivery recovery, backups/restore, TLS, SMTP, abuse/security, and capacity testing as listed in [production readiness](../PRODUCTION_READINESS.md).

No skipped or unavailable integration check is counted as a pass.
