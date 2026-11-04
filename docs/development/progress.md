# Backend Progress

> **Status note (2026-11-03):** The entries below are a historical backend checkpoint and may no longer match current source. Use [DEVELOPMENT_PROGRESS.md](../DEVELOPMENT_PROGRESS.md) and [DEVELOPMENT_STATUS.md](../DEVELOPMENT_STATUS.md) as the current status record.

Last inspected: 2026-11-02. Active scope: Go backend engine only; frontend work is out of scope.

## Repository findings
- Existing Go module targets Go 1.26 and contains `cmd/api`, `cmd/worker`, and `cmd/migrate`.
- Existing internal modules cover config, auth, API handlers, campaigns, contacts, delivery, NATS messaging, PostgreSQL, observability, and worker logic.
- `services/` and `shared/` are empty; deployment currently uses one modular API and one worker.
- Migrations 0001-0003 contain core tables plus durable contact-import state and API-key update timestamps. Application to PostgreSQL has not been verified.
- Go unit tests cover config, SMTP policy/classification, Argon2 password verification, roles/JWTs, consent validation, segment SQL compilation, CSV mapping, and worker behavior.
- Optional PostgreSQL integration tests cover API import enqueue/status/tenant scoping and worker import persistence/recovery when `TEST_DATABASE_URL` is configured.
- Optional PostgreSQL integration tests cover API import enqueue/status/tenant scoping, worker import persistence/recovery, API-key lifecycle, and existing API workflows when a disposable database URL is configured.
- Compose, Dockerfiles, Makefile, `.env.example`, and all seven requested documentation files are present.
- Existing progress/roadmap text mixed frontend tasks with backend work and marked unverified work complete; the roadmap is being converted to backend-only scope.
- Roadmap and progress are now backend-only; integration tests are recorded separately from unit/static verification.

## Environment and verification
- Node.js 26.13.0, npm 11.2.0, and Go 1.27.3 are available. Go is installed at `/usr/local/go/bin/go` but is not on PATH.
- Docker and Homebrew are unavailable. Git status remains unavailable because Xcode command-line tools are missing; worktree state is unknown.
- CGO-enabled builds invoke Apple clang and fail without Xcode command-line tools.
- `CGO_ENABLED=0 go test ./...`, `go vet ./...`, and `go test -race ./...` pass using `/usr/local/go/bin/go`.
- PostgreSQL integration tests compile but skip because neither `TEST_DATABASE_URL` nor `INTEGRATION_DATABASE_URL` is configured; Docker is unavailable to start a local database.
- PostgreSQL integration tests compile but skip because neither `TEST_DATABASE_URL` nor `INTEGRATION_DATABASE_URL` is configured.
- Phase 2 durable imports are implemented: explicit mapping, a 16 MiB/100k-row limit, pending-only import, organization-scoped status polling, row-level errors, batch progress, lease recovery, retry, and payload cleanup.
- PostgreSQL-backed import tests are implemented but not run because no test database is configured.
- The focused API tests pass after the bounced-contact resubscription guard.
- Compose and OpenAPI YAML parse successfully; OpenAPI covers all 45 route-method pairs registered by the Go API.
- Compose and OpenAPI YAML parse successfully; OpenAPI covers all 49 route-method pairs registered by the Go API.
- Migrations 0001-0003 contain core tables plus contact-import job state and API-key update timestamps. Application to PostgreSQL has not been verified.
- PostgreSQL migrations and PostgreSQL/NATS/SMTP integration remain unverified because Docker is unavailable.

## Current milestone
- Phase 0 inspection and documentation reconciliation: complete.
- Phase 1 backend foundation and unit/static verification: complete under Go 1.27.3 with `CGO_ENABLED=0`; CGO-enabled compilation and infrastructure integration remain unverified.
- Phase 1 changes include strict config validation, staging/production safeguards, STARTTLS enforcement, bounded Argon2 verification, route-level role authorization, and regression tests.
- Phase 2 consent and segmentation slice: pending-by-default contacts/imports, explicit consent evidence, transactional consent events, suppression checks, and parameterized segment compilation/snapshot targeting are implemented.
- Phase 2 remains active: current segments support whitelisted contact fields, `custom_fields.<key>`, bounded boolean conditions, and parameterized campaign snapshots. Tag/list/event-driven segment predicates are not implemented.
- CSV imports are durable jobs with explicit mapping, a 16 MiB/100k-row limit, pending-only contact creation, organization-scoped status polling, bounded row errors, batch progress, lease recovery, retries, and payload cleanup.
- OpenAPI now parses and covers all 45 routes registered by `internal/api/server.go`.
- OpenAPI now parses and covers all 49 routes registered by `internal/api/server.go`.
- Do not claim production readiness based only on source inspection.

## Next actions
1. Provide Xcode command-line tools to restore CGO-enabled builds and run `go test ./...` without CGO disabled.
2. Provide Docker/PostgreSQL/NATS/Stalwart, then verify migrations, consent transactions, tenant boundaries, segment snapshots, scheduler recovery, and SMTP STARTTLS behavior.
3. Configure `TEST_DATABASE_URL` and `INTEGRATION_DATABASE_URL` and run the real-schema API/worker import integration tests.
4. Add campaign-state, suppression/bounce, idempotency, and retry integration tests; audit tenant ownership across data queries.
