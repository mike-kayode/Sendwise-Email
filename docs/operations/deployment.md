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
| Stalwart | pinned Stalwart image | SMTP transport, persistent data |
| Stalwart admin proxy | email-platform:stalwart-admin-proxy | Host-gateway-only path from bridge containers to loopback JMAP |
| MinIO | minio/minio:latest | Object storage (images, exports) |
| API | email-platform:api | Go binary, container port 8080; host loopback 18080 with host-network override |
| Worker | email-platform:worker | Go binary, port 9091 (metrics) |
| Web | next.js | port 3000 |
| Caddy | caddy:2-alpine | HTTPS, reverse proxy |
| Egress probe | email-platform:egress-probe | Outbound SMTP port-25 egress health on the selected Stalwart network path |
| Prometheus/Grafana/Loki | observability profile | metrics, dashboards, logs |

## Deployment profiles
- `docker compose up -d` — full stack including web, api, worker, caddy.
- Linux hosts that need SMTP to bypass Docker NAT can opt in to
  `docker-compose.host-network.yml`; the default Compose topology remains
  bridge-networked for development.
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
# Identity verification, password-reset, and invitation mail use a dedicated
# relay; they never route through campaign SMTP or Stalwart.
PLATFORM_SMTP_ADDR=smtp-relay.example.com:587
PLATFORM_SMTP_HOST=smtp-relay.example.com
PLATFORM_SMTP_TLS_MODE=starttls
PLATFORM_SMTP_USERNAME=<relay user>
PLATFORM_SMTP_PASSWORD=<relay password>
PLATFORM_VERIFIED_SENDER_DOMAINS=example.com
PLATFORM_FROM_EMAIL=noreply@example.com
PLATFORM_MAIL_API_KEY=<optional API fallback>
# Campaign delivery settings remain separate:
SMTP_ADDR=stalwart:587
SMTP_HOST=stalwart
SMTP_USERNAME=<stalwart user>
SMTP_PASSWORD=<stalwart password>
SMTP_FROM=newsletter@example.com
SMTP_POOL_SIZE=10
BOUNCE_DOMAIN=bounces.example.com
DKIM_SELECTOR=mail
SENDING_RATE_PER_SECOND=20
MAX_DELIVERY_ATTEMPTS=5
SCHEDULER_INTERVAL=15s
MAX_MESSAGE_AGE=48h
MAX_DAILY_EMAILS=10000
MAX_BOUNCE_RATE=0.02
MAX_COMPLAINT_RATE=0.0005
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
- `GET /readyz` — database readiness plus `mail_status`; sender or relay
  problems report `mail_status: degraded` without preventing account
  registration or taking the database-ready service out of rotation.
- `GET /metrics` — Prometheus
- Worker metrics on `:9091/metrics`

Docker Compose includes healthchecks for postgres and redis. NATS has no healthcheck yet. API/worker depend on `migrate`
completing successfully before starting.

Before applying migrations, `scripts/deploy.sh` checks for duplicate sending
domains. The check is non-blocking on a fresh database where the
`sending_domains` table does not exist, and exits before migrations if it finds
duplicate ownership that would prevent the unique domain index from applying.

### Host-network Stalwart mode (Linux server only)

The host-network override moves Stalwart's SMTP listeners (25/465/587) and the
egress probe onto the host network. The Stalwart HTTP/JMAP management listener
must bind only to `127.0.0.1:8080`. The API's existing loopback publication is
moved from 8080 to `127.0.0.1:18080` for the Stalwart event webhook, while
bridge-network API/worker SMTP traffic uses `host.docker.internal:587`. A small
admin proxy binds only to the Docker host-gateway address on port 18081 and
forwards to `127.0.0.1:8080`; API and worker management calls use this proxy.
The host-gateway value defaults to `172.17.0.1`; verify it matches Docker's
`host-gateway` on the target server and set `DOCKER_HOST_GATEWAY_IP` if it does
not. The proxy rejects non-private, loopback, and wildcard bind addresses.
The override uses Compose `!reset`/`!override` merge tags and requires Docker
Compose v2.24.4 or newer.

Before enabling, check the Linux host's listeners. Ports 25, 465, and 587 must
be available for Stalwart. Port 8080 is Stalwart's loopback-only admin port;
the application API moves to loopback port 18080. Port 18081 on the Docker
host-gateway address is used by the private management proxy. Ports 80/443
(Caddy), 8082 (Identity), 8083 (Workspace), and 8084 (Gateway) retain their
existing bindings; transactional SMTP host ports 2465/2587 are also distinct.
The host API listener formerly on 8080 moves to 18080 to avoid colliding with
Stalwart admin. IMAP ports 143/993 are retained only when a Stalwart listener
already uses them; the provisioner removes other non-HTTP listener bindings.
Check the host before enabling with `sudo ss -ltnp`.

This repository cannot verify the target host's actual listeners; the deploy
smoke check confirms that Stalwart admin port 8080 is bound exactly to
`127.0.0.1`. Before switching, inspect the ports involved with:

```sh
sudo ss -ltnp | grep -E ':(25|465|587|8080|143|993|18080|18081)\b'
```

Enable the mode in the server's protected `.env`:

```dotenv
STALWART_HOST_NETWORK=true
DOCKER_HOST_GATEWAY_IP=172.17.0.1
```

Confirm the gateway address (normally `172.17.0.1`) with
`docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}'`,
then apply the override to both existing Compose files:

```sh
docker compose -f docker-compose.yml -f docker-compose.microservices.yml \
  -f docker-compose.host-network.yml config
```

For first activation, back up Stalwart's data and stop the API container
currently occupying loopback port 8080, the worker, and the egress probe. The
provisioner uses the current bridge-mode admin endpoint, writes the loopback-only
listener setting as its final JMAP operation, then recreates Stalwart and the
probe with the host override. This prevents starting host-network Stalwart with
an unreviewed wildcard admin binding:

```sh
docker compose -f docker-compose.yml -f docker-compose.microservices.yml \
  -f docker-compose.host-network.yml stop api worker egress-probe
set -a; . ./.env; set +a
STALWART_HOST_NETWORK=true STALWART_URL=http://127.0.0.1:8081 \
  ./scripts/provision-stalwart.sh --live
STALWART_HOST_NETWORK=true ./scripts/configure-stalwart-events.sh --apply
STALWART_HOST_NETWORK=true ./scripts/deploy.sh
```

The event webhook URL defaults to
`http://127.0.0.1:18080/api/v1/webhooks/stalwart-events` in host-network mode
and retains the HMAC signature key. `scripts/deploy.sh` automatically includes
the override when `STALWART_HOST_NETWORK=true`, builds the admin proxy, checks
the admin listener is loopback-only, and runs its normal smoke checks. Do not
expose port 8080 or 18081 on public interfaces.

Rollback networking by setting `STALWART_HOST_NETWORK=false` (or removing the
setting), then remove the proxy and recreate the original bridge-network
services without the override. Stalwart must be recreated, not merely
restarted, to leave host networking:

```sh
# Set STALWART_HOST_NETWORK=false in .env before running these commands.
docker compose -f docker-compose.yml -f docker-compose.microservices.yml \
  -f docker-compose.host-network.yml rm -sf stalwart-admin-proxy
docker compose -f docker-compose.yml -f docker-compose.microservices.yml \
  up -d --force-recreate stalwart api worker egress-probe
```

The next normal `scripts/deploy.sh` run uses the default bridge configuration.

The host-network override is Linux-specific; Docker Desktop does not provide
the same host-network semantics as a Linux deployment host.

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

## Transactional mail smoke check

After deploying Identity with its dedicated `PLATFORM_SMTP_*` settings and a
verified sender domain, source the deployment environment and send a real
message to an operator-controlled inbox:

```sh
set -a
. ./.env
set +a
DEPLOY_SMOKE_MAIL_TO=ops@example.com ./scripts/smoke-mail.sh
```

The script sends directly through the platform SMTP relay and reports only
successful SMTP handoff; check the recipient inbox separately to confirm
delivery. Do not point Identity's platform relay settings at campaign Stalwart.
