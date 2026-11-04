Staging/production config requires verified-domain enforcement, a JWT secret of at least 32 characters without placeholder text, and authenticated SMTP credentials; the worker requires STARTTLS outside development/test.
# Production Deployment

> **Status note (2026-11-03):** This is a deployment target, not a confirmed production deployment. Current blockers and the proposed independently deployable topology are tracked in [INFRASTRUCTURE_AND_DEPLOYMENT.md](../INFRASTRUCTURE_AND_DEPLOYMENT.md) and [PRODUCTION_READINESS.md](../PRODUCTION_READINESS.md).

## Current status
- Compose and container build definitions exist, but Docker is unavailable in the inspected environment; startup and health checks are unverified.
- Compose now defaults to `APP_ENV=development` and leaves `JWT_SECRET` empty. Development gets a code-defined development-only secret; production must provide a strong secret and SMTP credentials.
- Production config requires verified-domain enforcement, a JWT secret of at least 32 characters without placeholder text, and authenticated SMTP credentials.
- SMTP credentials and domain-verification enforcement are passed to both API/worker configuration where applicable.
- PostgreSQL/Redis/NATS/Stalwart/MinIO containers are declared. Redis and MinIO are not yet used by the Go runtime; backups are described below but no automated backup jobs/scripts are wired in Compose.
- This document is a deployment target, not a production-readiness certification.

## Components
| Component | Image | Notes |
|-----------|-------|-------|
| PostgreSQL | postgres:16-alpine | Primary DB, persistent volume, automated backups |
| Redis | redis:7-alpine | Cache + rate limiting, appendonly |
| NATS | nats:2.10-alpine | JetStream, file storage |
| Stalwart | stalwartlabs/stalwart:latest | SMTP transport, persistent data |
| MinIO | minio/minio:latest | Object storage (images, exports) |
| API | email-platform:api | Go binary, port 8080 |
| Worker | email-platform:worker | Go binary, port 9091 (metrics) |
| Web | next.js | port 3000 |
| Caddy | caddy:2-alpine | HTTPS, reverse proxy |
| Prometheus/Grafana/Loki | observability profile | metrics, dashboards, logs |

## Deployment profiles
- `docker compose up -d` — full stack including web, api, worker, caddy.
- `docker compose --profile observability up -d` — adds Prometheus, Grafana, Loki.
- `make dev-up` — starts only infra (postgres, redis, nats, stalwart, minio) for local dev.

## Environment separation
Development, staging, and production use separate `.env` files (never committed). Secrets
(JWT_SECRET, SMTP credentials, MinIO keys, DB passwords) must be provided via environment or a
secret manager and kept out of source control. `.env` and `.env.*` are gitignored.

## Required environment variables (production)
```
APP_ENV=production
DATABASE_URL=...
REDIS_URL=...
NATS_URL=...
JWT_SECRET=<long random>
JWT_ISSUER=email-platform
ACCESS_TOKEN_TTL=15m
SMTP_ADDR=stalwart:587
SMTP_HOST=stalwart
SMTP_USERNAME=<stalwart user>
SMTP_PASSWORD=<stalwart password>
SMTP_FROM=newsletter@example.com
DKIM_SELECTOR=mail
SENDING_RATE_PER_SECOND=20
MAX_DELIVERY_ATTEMPTS=5
SCHEDULER_INTERVAL=15s
MAX_MESSAGE_AGE=48h
MAX_DAILY_EMAILS=10000
MAX_BOUNCE_RATE=0.05
MAX_COMPLAINT_RATE=0.001
DOMAIN_CONCURRENCY_LIMIT=10
CORS_ORIGINS=https://app.example.com
REQUIRE_VERIFIED_DOMAIN=true
APP_BASE_URL=https://app.example.com
API_BASE_URL=https://api.example.com
TRACKING_BASE_URL=https://track.example.com
```

## Build and run
```
make build            # builds Go binaries and Next.js app
make up               # builds and starts full Docker stack
make logs             # tail stack logs
make down             # stop stack
make migrate          # apply DB migrations
```

## Health checks
- `GET /healthz` — liveness
- `GET /readyz` — readiness (database ping)
- `GET /metrics` — Prometheus
- Worker metrics on `:9091/metrics`

Docker Compose includes healthchecks for postgres and redis. NATS has no healthcheck yet. API/worker depend on `migrate`
completing successfully before starting.

## Backups and restore
- PostgreSQL: configure `pg_dump` to persistent/object storage and schedule it daily; point-in-time recovery via WAL archiving. No backup job is currently included in Compose.
- Stalwart: back up config, DKIM keys, queues, and data directory.
- MinIO: back up bucket data.
- NATS JetStream: file storage persisted on volume; rebuild streams from Postgres source of truth.
- Restore procedure is an operational requirement; automate and test restore quarterly before production.

## Prerequisites for real outbound mail
- A server/VPS that explicitly permits outbound port 25.
- Static public IP with PTR/reverse DNS.
- Valid TLS certificates.
- SPF/DKIM/DMARC for each sending domain.
- Reputation monitoring; warm-up new IPs gradually.
