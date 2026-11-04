# Verification Report

**Assessment:** NOT READY for production  
**Scope:** Authentication/browser API routing, login URL handling, extracted identity/workspace/gateway services, and `docker-compose.microservices.yml`.

## Evidence

| Area | Status | Evidence |
|---|---|---|
| Go unit tests | PASS | `CGO_ENABLED=0 go test ./...` |
| Go static analysis | PASS | `CGO_ENABLED=0 go vet ./...` |
| Web TypeScript | PASS | `cd apps/web && npm run typecheck` |
| Web unit tests | PASS | `cd apps/web && npm test` |
| Web production build | PASS | `cd apps/web && npm run build` |
| API URL and login request helpers | PASS | Tests confirm same-origin URL construction, POST JSON credentials with `credentials: include`, and safe URL cleanup. |
| Network error handling | PASS | Tests confirm fetch `TypeError` maps to “Cannot reach the server. Please try again.” and HTTP errors retain their server message. |
| Production browser API URL | PARTIALLY VERIFIED | Dockerfile and Compose defaults use an empty API base, producing same-origin `/api/v1/...` paths in source; production web image was not rebuilt. |
| Credential query handling | PARTIALLY VERIFIED | Login reads only `callbackUrl` and replaces the current URL with its pathname. Unit tests cover stripping all query data; browser/history/referrer/log audit was not run. |
| Login verification distinction | PASS | Identity handler source and unit test distinguish wrong credentials (`401 invalid_credentials`) from correct-password unverified accounts (`403 email_not_verified`). |
| PostgreSQL init script syntax | PASS | `sh -n infrastructure/postgres/init-databases.sh` |
| Compose YAML syntax | PASS | Ruby Psych parsed `docker-compose.microservices.yml` |
| Identity refresh behavior | PARTIALLY VERIFIED | Handler tests cover HttpOnly refresh-cookie issuance, rotation, and response token omission. No live browser/API/database journey was run. |
| Frontend refresh behavior | PARTIALLY VERIFIED | Typecheck passed; concurrent requests share one refresh attempt in source. No browser-to-service test was run. |
| Compose deployment and container health | BLOCKED | Docker/Compose is unavailable in this environment; images and health checks were not built or started. |
| PostgreSQL provisioning and migrations | BLOCKED | PostgreSQL is unavailable. SQL/role behavior, fresh-volume setup, and upgrade of an existing volume were not executed. |
| NATS event delivery/recovery | BLOCKED | No live JetStream was available; outbox relay and workspace consumer were not integration-tested. |
| TLS, SMTP, and email-provider delivery | BLOCKED | DNS, certificates, SMTP credentials, and controlled inbox checks were not available. |

## Changes verified in source

- Identity login and refresh issue refresh tokens only in a `HttpOnly`, `SameSite=Lax` cookie scoped to `/api/v1/auth`; production configuration forces `Secure`.
- Refresh tokens rotate in the identity store; frontend requests include credentials and retry a 401 once after a shared refresh request.
- Logout sends the refresh cookie for revocation and clears both the server cookie and client session.
- The microservices Compose stack now routes Caddy through the gateway, gates services on healthy dependencies, and provisions identity/workspace databases and roles even when the named Postgres volume already exists.
- Identity verification mail is configured to use the local Stalwart SMTP service or configured Resend credentials; failed registration mail is reported to the caller so the account can request a resend.
- Runtime database and MinIO credentials no longer have committed insecure defaults in the microservices Compose file. Generate distinct hex secrets before starting that stack.
- Caddy routes `/api/*` and `/t/*` to the configured API upstream and other paths to the web service. Same-origin browser API requests do not require CORS; no live proxy request was made in this task.
- A development-only Identity seed command is guarded by `APP_ENV=development`; it accepts credentials only through environment variables and does not print the password or email.

To run it locally, first apply the identity migrations, then set `APP_ENV=development`, `DATABASE_URL`, `DEV_SEED_EMAIL`, `DEV_SEED_PASSWORD`, `DEV_SEED_NAME`, and `DEV_SEED_ORGANIZATION` in the environment and run `go run ./services/identity/cmd/devseed`. Use a unique local-only password; the command refuses to run under any other `APP_ENV`.

## Login error interpretation

The current Identity handler checks the password before checking email verification. An existing account with a wrong password returns the same `401 invalid_credentials` as an unknown email. Only a correct password on an unverified account returns `403 email_not_verified`, after which the login UI links to the verification/resend page. Registration already returns `409 email_in_use`, which reveals account existence. No additional pre-password verification disclosure was introduced.

## Release blockers

- Run Compose config/build/start and all health checks with Docker.
- Rebuild and restart the production web image, then inspect browser DevTools to verify login uses `https://campaign.pipsleague.com/api/v1/auth/login` with credentials only in the POST JSON body.
- Validate browser history/referrer and edge/application logging behavior for any legacy credential-bearing login URL; client cleanup cannot undo logs from its original request.
- Apply all migration sets to fresh and supported upgrade databases; test the runtime roles with least privilege.
- Run browser-driven registration, email verification, login, refresh-after-expiry, concurrent refresh, logout, and password reset against PostgreSQL and JetStream.
- Verify event delivery after NATS restart and confirm the workspace projection is idempotent.
- Complete tenant-isolation, bounce/complaint authentication, delivery recovery, backups/restore, TLS, SMTP, abuse/security, and capacity testing as listed in [production readiness](../PRODUCTION_READINESS.md).

No skipped or unavailable integration check is counted as a pass.
