This repository is an active backend MVP foundation. It includes authentication, organizations, contacts, campaigns, scheduling, recipient snapshots, delivery jobs, tracking endpoints, domain verification, durable CSV import jobs, consent-aware subscription changes, and parameterized segment targeting. PostgreSQL/NATS/SMTP integration still requires a configured Docker environment.
# Email Platform

> **Development status:** This repository is not yet a production-ready commercial SaaS or a microservices deployment. The verified source audit, target architecture, implementation progress, and release gates are in [the system audit](docs/SYSTEM_ARCHITECTURE_AND_DEVELOPMENT_AUDIT.md), [master architecture](docs/MASTER_ARCHITECTURE.md), and [development status](docs/DEVELOPMENT_STATUS.md).

Self-hosted email marketing SaaS foundation: Next.js dashboard, Go API and delivery workers, PostgreSQL, Redis, NATS JetStream, and Stalwart as the initial SMTP transport.

This repository is an active backend MVP foundation. It includes authentication, organizations, contacts, campaigns, scheduling, recipient snapshots, delivery jobs, tracking endpoints, domain verification, durable CSV import jobs, consent-aware subscription changes, and parameterized segment targeting. PostgreSQL/NATS/SMTP integration still requires a configured database and broker environment.

## Architecture

```
apps/web          Next.js 16 dashboard (App Router, TypeScript, Tailwind CSS)
cmd/api           HTTP API
cmd/worker        Delivery consumer + campaign scheduler
cmd/migrate       SQL migration runner
internal/*        Domain packages (auth, contacts, campaigns, delivery, messaging)
migrations/       PostgreSQL schema
infrastructure/   Caddy, Prometheus, Grafana, Loki configs
```

Delivery flow:

```
API -> campaign snapshot -> delivery_jobs (Postgres) -> NATS JetStream
    -> worker -> suppression/status checks -> render -> Stalwart SMTP
    -> message_events -> analytics
```

The worker only depends on the `delivery.Transport` interface. Stalwart can be replaced with a custom Go SMTP engine without touching campaigns, contacts, or the frontend.

## Requirements

- Go 1.26+
- Node.js 20.9+
- Docker with Compose (for Postgres, Redis, NATS, Stalwart, MinIO)
- A server that permits outbound port 25 if you intend to deliver real mail

## Quick start

```bash
cp .env.example .env
make install          # frontend dependencies
make dev-up           # postgres, redis, nats, stalwart, minio
make migrate          # apply schema
make api              # http://localhost:8080
make worker           # delivery + scheduler
make web              # http://localhost:3000
```

Register the first account:

```bash
curl -s http://localhost:8080/api/v1/auth/register \
  -H 'content-type: application/json' \
  -d '{
    "email": "you@example.com",
    "password": "a-long-password",
    "name": "Your Name",
    "organization_name": "Acme"
  }'
```

Use the returned `access_token` as `Authorization: Bearer <token>`.

## Full stack (two compose files — use them combined)

There are two Compose files and **neither one alone runs the full product**:

| File | What it contains |
|---|---|
| `docker-compose.yml` | Legacy monolith: api, worker, migrate, web, caddy, postgres, redis, nats, stalwart |
| `docker-compose.microservices.yml` | Identity, workspace, gateway (+ shared infra and web/caddy) |

The Caddyfile always routes `/api/*` to `gateway:8080` and `/t/*` to the legacy
api, so the canonical stack **merges both files**:

```bash
# full stack (canonical)
docker compose --env-file .env --env-file .env.local \
  -f docker-compose.yml -f docker-compose.microservices.yml up -d

docker compose --env-file .env --env-file .env.local \
  -f docker-compose.yml -f docker-compose.microservices.yml logs -f

docker compose --env-file .env --env-file .env.local \
  -f docker-compose.yml -f docker-compose.microservices.yml down
```

Notes:

- Both files declare project name `email-platform`; Compose merges services and
  env files (later `--env-file` wins on duplicate keys).
- `.env` holds secrets/shared settings; `.env.local` (gitignored) holds
  localhost-only overrides (`APP_ENV=development`, `SITE_ADDRESS=:80`,
  `APP_BASE_URL`, `CORS_ORIGINS`, `API_BASE_URL`, `NEXT_PUBLIC_API_URL`,
  `MAIL_PROVIDER=log`).
- Running `-f docker-compose.yml` alone leaves `/api/*` pointing at a missing
  gateway; running only the microservices file leaves non-auth routes pointing
  at a missing legacy api. Both standalone modes are unsupported.
- MinIO is behind Compose profile `storage` (`--profile storage`) and is unused
  by application code.
- Observability (optional): `docker compose --profile observability up -d`
  (Prometheus :9090, Grafana :3001, Loki :3100).

Local verification commands used in the T0 audit (see
`docs/SYSTEM_STATE.md` for results):

```bash
docker compose --env-file .env --env-file .env.local \
  -f docker-compose.yml -f docker-compose.microservices.yml ps
curl -fsS http://127.0.0.1:8080/readyz   # legacy api (loopback)
curl -fsS http://127.0.0.1:8084/readyz   # gateway
curl -fsS http://127.0.0.1:3000/         # web
curl -fsS http://127.0.0.1:80/           # caddy edge
```

## API surface

```
POST   /api/v1/auth/register
POST   /api/v1/auth/login
GET    /api/v1/auth/me

GET    /api/v1/contacts
POST   /api/v1/contacts
POST   /api/v1/contacts/import
GET    /api/v1/contact-imports/{id}
GET    /api/v1/contacts/export
GET    /api/v1/contacts/{id}
PATCH  /api/v1/contacts/{id}
DELETE /api/v1/contacts/{id}

GET    /api/v1/campaigns
POST   /api/v1/campaigns
GET    /api/v1/campaigns/{id}
PATCH  /api/v1/campaigns/{id}
DELETE /api/v1/campaigns/{id}
POST   /api/v1/campaigns/{id}/validate
POST   /api/v1/campaigns/{id}/schedule
POST   /api/v1/campaigns/{id}/send
POST   /api/v1/campaigns/{id}/pause
POST   /api/v1/campaigns/{id}/resume
POST   /api/v1/campaigns/{id}/cancel

GET    /api/v1/domains
POST   /api/v1/domains
POST   /api/v1/domains/{id}/verify
GET    /api/v1/domains/{id}/dns-records

GET    /api/v1/analytics/overview
GET    /api/v1/analytics/campaigns/{id}

GET    /api/v1/api-keys
POST   /api/v1/api-keys
DELETE /api/v1/api-keys/{id}

GET    /api/v1/api-keys
POST   /api/v1/api-keys
DELETE /api/v1/api-keys/{id}

GET    /api/v1/api-keys
POST   /api/v1/api-keys
DELETE /api/v1/api-keys/{id}

POST   /api/v1/public/subscribe
GET    /api/v1/public/unsubscribe?token=...
POST   /api/v1/public/unsubscribe
GET    /t/open/{token}.gif
GET    /t/click/{token}
```

## Email rendering

Campaign `content` is structured JSON (blocks), not raw HTML. The worker renders blocks into responsive HTML and plain text. Supported blocks: `heading`, `paragraph`, `button`, `image`, `divider`, `spacer`, `footer`.

```json
{
  "version": 1,
  "blocks": [
    { "type": "heading", "content": "Hello {{first_name}}" },
    { "type": "paragraph", "content": "Here is the latest update." },
    { "type": "button", "content": "Read more", "url": "https://example.com" }
  ]
}
```

Personalization supports `{{first_name}}`, `{{last_name}}`, `{{email}}`, `{{organization}}`, and `{{custom.key}}`.

## Delivery semantics

- Recipient snapshots are immutable: changes to contacts after scheduling do not alter an in-flight campaign.
- Every delivery job has a unique id and message id. Retries check persisted state before submitting.
- SMTP acceptance is recorded as `submitted`; bounces and complaints arrive as separate events.
- Unsubscribes and complaints are written to `suppressions` and checked before every send.
- Jobs are durable in PostgreSQL. NATS JetStream is the work distribution layer, not the source of truth.
- `resume` re-publishes queued jobs so a failed NATS publish does not lose work.

## Notes and next steps

- Stalwart must be configured with a real hostname, TLS, DKIM keys, and outbound port 25. The Compose service is a local starting point only.
- Domain verification checks SPF, DMARC, and DKIM DNS records; DKIM signing is delegated to Stalwart.
- New sending IPs need warm-up, PTR records, and reputation monitoring before bulk sending.
- CSV imports are durable PostgreSQL jobs. Poll `GET /api/v1/contact-imports/{id}` for progress and bounded row errors. Imports create pending contacts and do not establish consent.
- Not yet implemented: OAuth/MFA, automation runtime, A/B testing, billing provider integration, RLS policies, event table partitioning, and object storage uploads. API keys are available through the backend but need broader scope enforcement and integration coverage.
- Database/NATS/SMTP integration and migration application must be verified against a disposable PostgreSQL environment before deployment.
# Email-Marketing
