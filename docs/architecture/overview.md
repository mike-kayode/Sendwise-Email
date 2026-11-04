# Architecture Overview

> **Status note (2026-11-03):** This overview describes the legacy implementation and contains aspirational statements below. It is not the microservices target or a production-readiness claim. The current-vs-target boundary and audited gaps are maintained in [MASTER_ARCHITECTURE.md](../MASTER_ARCHITECTURE.md), [MICROSERVICE_CATALOG.md](../MICROSERVICE_CATALOG.md), and [SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md](../SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md). The target services are not yet independently deployed.

## Product
Sendwise — a self-hosted, multi-tenant email marketing SaaS comparable to Mailchimp/Brevo/Listmonk. The platform owns its campaign engine, delivery orchestration, tracking, analytics, and administration. It does **not** depend on Amazon SES, Mailgun, Postmark, SendGrid, or any commercial delivery API as a required provider. Self-hosted Stalwart SMTP is the initial transport; the transport is abstracted behind a Go `delivery.Transport` interface so it can be replaced later.

## Repository layout (monorepo)
```
email-platform/
├── apps/web/                 Next.js 16 dashboard (App Router, TS, Tailwind, shadcn/ui)
├── cmd/api/                  Go HTTP API entrypoint
├── cmd/worker/               Go delivery worker + scheduler entrypoint
├── cmd/migrate/              SQL migration runner entrypoint
├── internal/                 Go domain packages (auth, campaigns, contacts, delivery, messaging, worker, observability, config, db, httpx, api)
├── services/                 Reserved for independently deployable service modules (extraction targets)
├── shared/                   Reserved for shared Go libraries
├── migrations/               PostgreSQL migrations (applied/rolled back via schema_migrations)
├── infrastructure/           Docker, Postgres, Redis, NATS, Stalwart, Caddy, Prometheus, Grafana, Loki
├── docs/                     Architecture, API, database, operations, deployment
├── docker-compose.yml
├── Dockerfile
├── Makefile
└── go.mod / go.sum
```

## Logical data flow
```
Next.js frontend -> API Gateway (Go) -> Postgres (source of truth)
                                                -> Redis (cache/rate limits)
                                                -> NATS JetStream (durable jobs)
                                                    -> Delivery Worker
                                                        -> suppression/quota checks
                                                        -> render (MJML/structured blocks)
                                                        -> Stalwart SMTP (transport)
                                                            -> recipient MX
                                                                -> bounce/complaint feedback
                                                                    -> message_events -> analytics
```

## Service boundaries (modular monorepo)
Kept as Go packages/modules inside a single deployable API + worker for Phase 1, with clear contracts so each can be extracted into its own service later:

| Module | Responsibility |
|--------|---------------|
| `auth` | Argon2id passwords, JWT access tokens, session/rotation, MFA hooks, API keys |
| `contacts` | Contact CRUD, CSV import/export, dedup, consent events, suppressions |
| `audiences` | Lists, tags, custom fields, dynamic segment rule engine |
| `campaigns` | Campaign lifecycle, recipient snapshots, delivery job generation |
| `delivery` | `Transport` interface, SMTP/Stalwart adapter, rendering, tracking links |
| `worker` | Job consumption, retries, dead-letter, scheduler loop, campaign completion |
| `messaging` | NATS JetStream connection, streams, consumers |
| `api` | Versioned REST handlers, validation, authz, tenant scoping, consistent errors |
| `config` | Environment loading, validation, defaults |
| `db` | pgx pool, migration runner |
| `observability` | Structured logger, Prometheus metrics, HTTP middleware |
| `httpx` | JSON helpers, error envelope, middleware chain (request ID, CORS, recover, access log) |

## Core design principles
- **Tenant isolation**: every tenant-owned row carries `organization_id`; queries are scoped by the organization in the access token; RLS is applied where practical.
- **No fake data**: every dashboard number comes from the database; no localStorage/substitute persistence.
- **At-least-once delivery with idempotency**: every delivery job has a unique id + message id; retries check persisted state before submitting; SMTP acceptance ≠ inbox delivery.
- **Suppression before send**: unsubscribed, bounced, complained contacts are checked immediately before every submission, not only at campaign creation.
- **Secure by default**: Argon2id passwords, short-lived JWTs, secure cookies where applicable, CSRF where applicable, HTML sanitization, signed expiring unsubscribe/preference tokens, secrets never logged.
- **Observable**: correlation IDs, structured JSON logs, Prometheus metrics, health checks.

## Transport interface (replaceable)
```go
type Transport interface {
    Send(ctx context.Context, message Message) (*Result, error)
}
```
The Stalwart SMTP adapter implements this over authenticated SMTP with STARTTLS, DKIM delegation to Stalwart, and permanent/temporary error classification. A custom Go SMTP engine can implement the same interface without touching campaigns, contacts, or frontend.

## Environment note
The inspected environment has Node.js 26.13.0, npm 11.2.0, and Go 1.27.3 at `/usr/local/go/bin/go` (not on the shell PATH). Docker and Homebrew are unavailable. Git status is unavailable because the macOS Xcode command-line tools are missing. Go unit tests, vet, and race checks pass with `CGO_ENABLED=0`; database and infrastructure integration remain unverified.

## Current backend status
- Existing executables: `cmd/api`, `cmd/worker`, and `cmd/migrate`.
- Existing modules include auth, contacts, campaigns, delivery, NATS messaging, API handlers, PostgreSQL migrations, and observability.
- Mutating API routes now apply workspace role checks: audience writes are manager-scoped, campaign edits include authors, sending controls require managers, and domain administration requires owners/admins.
- The initial schema is present in `migrations/0001_init.sql`; application to a database has not been verified in this environment.
- Config, SMTP, Argon2 password, JWT, and role-middleware unit tests now exist, but are unrun because Go is unavailable.
- Config, SMTP, Argon2 password, JWT, role-middleware, consent-validation, and segment-compiler unit tests exist and pass with `CGO_ENABLED=0`.
- Redis, MinIO, and several service boundaries are present in Compose/docs but are not yet integrated into the Go runtime.
- `services/` and `shared/` are intentionally empty; the current deployment is a modular API and worker, not a set of independently deployed microservices.
- The backend roadmap is tracked in `docs/development/roadmap.md`; only verified work is marked complete.
- The backend roadmap is tracked in `docs/development/roadmap.md`; source presence, unit verification, and infrastructure verification are tracked separately.
