# Sendwise Status

## P0 — Deploy and config baseline (verified 2026-11-06)

Proof gate output (run locally against the docker compose stack):

```
$ gofmt -l cmd internal services shared
(no output — empty)

$ go vet ./... && go test ./...
VET_EXIT=0
?   	github.com/khayode/email-platform/cmd/api	[no test files]
?   	github.com/khayode/email-platform/cmd/migrate	[no test files]
?   	github.com/khayode/email-platform/cmd/worker	[no test files]
ok  	github.com/khayode/email-platform/internal/api	(cached)
ok  	github.com/khayode/email-platform/internal/auth	(cached)
ok  	github.com/khayode/email-platform/internal/campaigns	(cached)
ok  	github.com/khayode/email-platform/internal/config	(cached)
ok  	github.com/khayode/email-platform/internal/contacts	(cached)
ok  	github.com/khayode/email-platform/internal/db	(cached)
ok  	github.com/khayode/email-platform/internal/delivery	(cached)
ok  	github.com/khayode/email-platform/internal/outbox	(cached)
ok  	github.com/khayode/email-platform/internal/projections	(cached)
ok  	github.com/khayode/email-platform/internal/worker	(cached)
ok  	github.com/khayode/email-platform/services/gateway	0.819s
ok  	github.com/khayode/email-platform/services/identity	(cached)
ok  	github.com/khayode/email-platform/services/identity/cmd/devseed	(cached)
ok  	github.com/khayode/email-platform/services/workspace	(cached)
ok  	github.com/khayode/email-platform/shared/contracts/events	(cached)

$ (cd apps/web && npm run typecheck && npm run lint && npm test && npm run build)
TYPECHECK=0
LINT=0
ℹ tests 10
ℹ pass 10
ℹ fail 0
TEST=0
BUILD=0

$ docker compose -f docker-compose.yml config -q && docker compose -f docker-compose.microservices.yml config -q
(no output — both valid)

$ ./scripts/deploy.sh            # full local run
[2026-11-06 04:52:28]   every .env.example variable name is declared in .env
[2026-11-06 04:52:28] Configuration validation passed
[2026-11-06 04:52:29]   all Compose configurations are valid
[2026-11-06 04:52:29] Previous application images preserved for rollback
[2026-11-06 04:52:30] Backup complete: /Users/iam_khayode/email-platform/backups/20261106045229
[2026-11-06 04:53:25] Migrations applied
[2026-11-06 04:53:27]   ok  legacy-api-ready
[2026-11-06 04:53:27]   ok  identity-ready
[2026-11-06 04:53:27]   ok  workspace-ready
[2026-11-06 04:53:27]   ok  gateway-ready
[2026-11-06 04:53:27]   ok  worker-health
[2026-11-06 04:53:29]   ok  web
[2026-11-06 04:53:29]   ok  caddy-edge (status 200)
[2026-11-06 04:53:30]   ok  gateway-login (200)
[2026-11-06 04:53:30]   ok  gateway-lists (200)
[2026-11-06 04:53:30]   ok  gateway-contacts (200)
[2026-11-06 04:53:30]   ok  gateway-campaigns (200)
[2026-11-06 04:53:30]   ok  gateway-domains (200)
[2026-11-06 04:53:30]   ok  gateway-tags (200)
[2026-11-06 04:53:30]   ok  gateway-segments (200)
[2026-11-06 04:53:30] All smoke tests passed
[2026-11-06 04:53:30] Deploy completed successfully

$ SENDWISE_ENV_FILE=/tmp/env-no-service-token ./scripts/deploy.sh   # SERVICE_TOKEN removed
[2026-11-06 04:53:41] Comparing .env.example variable names against .env...
  missing from .env: SERVICE_TOKEN
[2026-11-06 04:53:41] ERROR: declare every variable above in .env (copy .env.example)
FAILFAST_EXIT=1
```

Notes:
- One deploy script only: `scripts/deploy.sh` (root `deploy.sh` deleted).
- `migrations/0010_forms.sql` was made idempotent (IF NOT EXISTS indexes,
  DROP TRIGGER IF EXISTS) because its objects pre-existed in the database but
  it was never recorded in `schema_migrations`; without this the deploy gate
  failed with `relation "forms_org_idx" already exists`.
- CI (`.github/workflows/ci.yml`) enforces gofmt, vet, go test, web
  typecheck/lint/test/build and `docker compose config -q`.
- Stray files removed and gitignored: `email.csv`, `"`, `test-results/`.

## Build / checks

- `go vet ./...` — pass
- `gofmt -l cmd internal services shared` — empty
- `go test ./...` — all packages pass (internal/api, auth, campaigns, contacts, etc.)
- web: `npm run typecheck` — pass
- web: `npm run lint` — pass (tsc --noEmit)
- web: `npm run build` — pass
- web: `npm test` — 10/10 pass

## Modules

- Auth: email verification, password reset, refresh rotation, logout, role checks — implemented in services/identity + internal/auth; tests `internal/auth` pass.
- Contacts CSV import: worker consumes queued imports; tests in `internal/contacts` pass; row errors reported.
- Forms: types/endpoints in `internal/api/forms.go`; frontend `apps/web/app/(dashboard)/forms`.
- Automations: frontend views present; backend partial.
- Campaigns: editor/preview/test send; backend campaigns package tests pass.
- Lists/tags/segments: CRUD + segment rules server-side; tests `internal/contacts` segments pass.
- Templates: pages present.
- Sending domains: pages present.
- Settings/billing: pages present.
- Reports: page present.
- Delivery: worker + suppression; tests pass.
- Projections backfill: `internal/projections` tests pass.

## Not working / known limitations

- Full e2e Playwright suite not yet added.
- Some module backends remain partial; see code.

## Deploy steps

1. Copy `.env.example` to `.env`, fill secrets (every variable name in
   `.env.example` must be declared in `.env`).
2. Run `scripts/check-env.sh`.
3. `scripts/deploy.sh` (backup, build, migrate, up, smoke).
4. Rollback: `scripts/deploy.sh rollback --yes` (destructive, restores DB backup).
