# Infrastructure and Deployment

## Current verified configuration

`docker-compose.yml` declares Postgres, Redis, NATS JetStream, Stalwart, MinIO, migration, API, worker, Next.js, Caddy, and optional Prometheus/Grafana/Loki. Current binaries are not separately deployable business-domain microservices. A Docker or live deployment was not available during the audit.

The Compose configurations keep dependency/admin ports bound to loopback, use a non-conflicting loopback port for Stalwart administration, pass the browser API URL at Next.js build time, and align the Go build image with the module version. Caddy's `SITE_ADDRESS` is configurable; `API_UPSTREAM` selects the monolith API or microservices gateway. A public hostname enables Caddy-managed HTTPS when DNS and inbound ports 80/443 are correctly configured. Compose YAML and the Postgres init script pass syntax checks, but Docker builds/startup, database provisioning, and TLS issuance have not been exercised. The microservices Compose file requires distinct generated values for `POSTGRES_PASSWORD`, `IDENTITY_DB_PASSWORD`, `WORKSPACE_DB_PASSWORD`, `JWT_SECRET`, `SERVICE_TOKEN`, and MinIO credentials; use URL-safe hex for database passwords. Do not use the development Compose files as a production deployment.

## Local development target

- Compose-only private service network; publish only web/edge and explicitly needed local development endpoints.
- Named volumes for stateful dependencies; deterministic health/readiness and startup dependencies.
- Local/test secrets must be non-production and generated for developers; no committed credential values.
- Each service has a separate image, command, config, health endpoint, DB credential and migration command.
- Compose profiles distinguish local mail sandbox, optional observability and production-like integration.

## Production target

1. TLS edge proxy (Caddy/Nginx or managed ingress) routes public web/API/tracking only; configure a public `SITE_ADDRESS` and validate certificate issuance and renewal.
2. App services/workers on private network with workload identity and outbound restrictions.
3. PostgreSQL HA/managed service with private endpoint, service-owned DB/schema/roles, pool sizing and PITR.
4. NATS JetStream secured cluster with stream replication, account/subject permissions, backups and lag alerts.
5. Stalwart SMTP nodes separated from app nodes, secured admin interface, TLS, DKIM secret management, multiple egress/IPs only after deliverability controls.
6. Object storage uses MinIO/S3-compatible private bucket, access policy, lifecycle and malware/content policy for uploads.
7. Prometheus, Grafana and Loki plus OpenTelemetry collector; alert delivery and retention defined.
8. Secret manager, CI build/sign/scan, immutable image promotion and staged rollout/rollback.

Kubernetes is a possible production scheduler, not a default requirement. Choose it only with demonstrated need and an operations owner. Compose production is possible for a small controlled deployment but does not itself provide HA/rollout guarantees.

## Reliability/security controls

- Independent liveness/readiness; readiness includes dependencies needed for that service only.
- Graceful shutdown drains HTTP and broker leases; bounded startup retries and explicit failure modes.
- Resource requests/limits, autoscaling based on queue lag/CPU/latency, tenant fairness and backpressure.
- Restrict network ingress/egress; Postgres/NATS/SMTP admin not exposed publicly.
- Automated encrypted backup and scheduled restore test; record numeric RPO/RTO only after exercise.
- Blue/green or rolling compatibility for APIs/events; migration expand/contract sequence.
- Deployment logs include release SHA, schema version, config fingerprint without secret values.

## CI/CD required checks

Go formatting, unit tests, race tests where supported, static analysis, frontend lint/typecheck/build, SQL migration tests from empty and prior schema, OpenAPI and event compatibility, container build, SBOM/vulnerability/secret scan, contract/integration tests, signed artifact promotion, and rollback smoke test.
