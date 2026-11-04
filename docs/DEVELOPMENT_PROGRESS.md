# Development Progress

**Last updated:** 2026-11-04

## Completed foundation work

- [x] Re-read the complete repository audit and existing architecture/roadmap documentation.
- [x] Added standalone identity, workspace, and gateway service implementations, service-owned migration sets, and a microservices Compose stack. Runtime parity and deployment remain unverified.
- [x] Verified the service/database/operational blockers called out by the audit against current source.
- [x] Added target master architecture, service catalog, data ownership matrix, event catalog, dependency graph, DB/API/delivery/security/infra/scalability/testing/roadmap/production documentation.
- [x] Added required target architecture diagrams for platform/services/communications/data/delivery/auth/automation/deployment (existing audit diagrams retained).
- [x] Added a typed, validated versioned event envelope contract and unit tests as the first distributed-systems code foundation.
- [x] Added an idempotency inbox/outbox migration, typed transactional enqueue, leased retrying relay with JetStream message-ID deduplication, and a registration event written in the account transaction.
- [x] Added event-envelope/subject and retry unit tests, plus a PostgreSQL integration test for transactional rollback, claiming, publish acknowledgement, and inbox deduplication (integration test is skipped unless `TEST_DATABASE_URL` is configured).
- [x] Repaired duplicate contact-import table creation using additive migration changes and added a forward repair; added static migration regression tests.
- [x] Fixed local Compose port collisions/exposure, made the web API URL a Next.js build argument, aligned the Go build image with `go.mod`, and made Caddy's site address configurable for hostname-based HTTPS.
- [x] Ran `PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./...`, `PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go vet ./...`, `npm --prefix apps/web run typecheck`, and `npm --prefix apps/web run build`.

## Not complete

- [~] Identity and workspace have separate databases and runtime roles configured in the microservices stack; least-privilege behavior and provisioning have not been validated against PostgreSQL.
- [ ] No full product-domain service has completed parity, cutover, rollback, and independent production deployment.
- [~] Initial registration event uses a transactional outbox and JetStream. Other event producers and all durable business consumers are absent; inbox has only a shared helper.
- [~] Identity registration outbox and workspace projection are implemented, but PostgreSQL/JetStream execution and recovery were not verified.
- [ ] No live migration, Docker, NATS, SMTP, domain, TLS, backup/restore or production topology was verified.
- [ ] No service-scale performance, chaos, or end-to-end test has been executed.
- [ ] Product features in `DEVELOPMENT_STATUS.md` remain partial/missing as documented.

## Current execution order

1. [x] Architecture and data/event ownership design.
2. [x] Repair the duplicate contact-import migration and verification-token trigger with additive/idempotent SQL. [ ] Validate all migrations against PostgreSQL when available.
3. [~] Fix local deployment blockers and establish repeatable integration environment. Compose syntax parses, but the unmodified combined stack failed startup. Baseline migrations and health checks only passed with temporary local test overrides; see [verification report](./quality/verification-report.md).
4. [~] Implement transactional outbox for registration in the monolith; independently deployable service extraction still requires PostgreSQL-backed verification and restricted owner DB credentials.
5. [ ] Continue service extraction only after parity, contract, and rollback criteria pass.

## Verification record

- `PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./...`: passed in the current audited session.
- Current follow-up: `PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go test ./...` and `PATH=/usr/local/go/bin:$PATH CGO_ENABLED=0 go vet ./...` passed after outbox changes. Outbox PostgreSQL integration test was skipped because `TEST_DATABASE_URL` is not configured.
- Latest auth/deployment follow-up: `CGO_ENABLED=0 go test ./...`, `CGO_ENABLED=0 go vet ./...`, and `cd apps/web && npm run typecheck` passed. Identity handler tests cover refresh-cookie issuance/rotation. Compose YAML parses and `sh -n infrastructure/postgres/init-databases.sh` passes; Docker/PostgreSQL runtime validation remains blocked.
- Uncached API/worker run: unit tests passed; Postgres-backed tests skipped because no DB URL/database was available.
- `apps/web`: typecheck and production build passed in the prior audit work.
- T0 on 2026-11-04: current working-tree Go tests and Docker build fail on the pre-existing mixed package declaration in `internal/api/contact_imports.go`. A clean snapshot at `2066258` passed `go test ./...`; DB-backed API tests failed stale response assertions, the contact-import API tenant test passed, and the worker integration test failed its row-number assertion. The combined Compose stack required temporary local-only fixes and did not verify Caddy routing or all authenticated routes. See [verification report](./quality/verification-report.md).
