# Sendwise Status

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

1. Copy `.env.example` to `.env`, fill secrets.
2. Run `scripts/check-env.sh`.
3. `./deploy.sh` (backup, build, migrate, up, smoke).
4. Rollback: `./deploy.sh rollback --yes`.
"}