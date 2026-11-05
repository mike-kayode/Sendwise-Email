#!/usr/bin/env bash
set -euo pipefail

# Sendwise deploy script for the canonical combined stack
# (docker-compose.yml + docker-compose.microservices.yml as one project).
#
# Usage:
#   ./scripts/deploy.sh                  # full deploy (backup, build, migrate, up, smoke)
#   ./scripts/deploy.sh deploy           # same as above
#   ./scripts/deploy.sh smoke            # run smoke tests only
#   ./scripts/deploy.sh backup           # take a DB backup only
#   ./scripts/deploy.sh rollback --yes   # DESTRUCTIVE: restore the latest DB backup
#   ./scripts/deploy.sh production       # deploy with production env validation
#
# Deploy order: .env.example-vs-.env variable check -> config validation ->
# docker compose config -q -> fresh DB backup -> build -> migrations (legacy +
# identity + workspace) -> up --no-deps -> seven-endpoint smoke test.
# Every step refuses to continue on failure.
#
# Rollback restores data only; images are not versioned. To roll back code,
# check out the previous git tag and deploy again.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

COMMAND="${1:-deploy}"
case "$COMMAND" in
  deploy|smoke|rollback|backup) ;;
  development|production)
    ENVIRONMENT_ARG="$COMMAND"
    COMMAND="deploy"
    ;;
  *)
    echo "Unknown command: $COMMAND (use deploy|smoke|backup|rollback)" >&2
    exit 2
    ;;
esac

log()  { echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"; }
fail() { echo "[$(date '+%Y-%m-%d %H:%M:%S')] ERROR: $*" >&2; exit 1; }

ENV_FILE="$ROOT/.env"
[ -f "$ENV_FILE" ] || fail ".env not found at $ENV_FILE. Copy .env.example and fill in required secrets."

# Later --env-file wins on duplicate keys; .env.local holds localhost overrides.
COMPOSE=(docker compose --env-file "$ENV_FILE")
if [ -f "$ROOT/.env.local" ]; then
  COMPOSE+=(--env-file "$ROOT/.env.local")
fi
COMPOSE+=(-f "$ROOT/docker-compose.yml" -f "$ROOT/docker-compose.microservices.yml")

[ -f "$ROOT/.env.example" ] || fail ".env.example not found at $ROOT/.env.example"

command -v curl >/dev/null 2>&1 || fail "curl is required for smoke tests"
command -v docker >/dev/null 2>&1 || fail "docker is required"

# Load env files for validation only (values are never echoed). Precedence
# mirrors compose: .env.local, when present, wins on duplicate keys.
set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
if [ -f "$ROOT/.env.local" ]; then
  # shellcheck disable=SC1090
  . "$ROOT/.env.local"
fi
set +a

APP_ENV_VALUE="${APP_ENV:-development}"
ENVIRONMENT="${ENVIRONMENT_ARG:-$APP_ENV_VALUE}"

# Every variable name documented in .env.example must exist in .env (or the
# .env.local override). A new required variable that reaches compose and
# .env.example but never lands in .env is exactly the gap that caused the
# last outage: containers start, fail config validation, dashboard down.
validate_env_keys() {
  log "Comparing .env.example variable names against .env..."
  local missing
  missing="$(
    comm -23 \
      <(grep -E '^[A-Za-z_][A-Za-z0-9_]*=' "$ROOT/.env.example" | cut -d= -f1 | sort -u) \
      <(cat \
          <(grep -E '^[A-Za-z_][A-Za-z0-9_]*=' "$ENV_FILE" | cut -d= -f1) \
          <(grep -E '^[A-Za-z_][A-Za-z0-9_]*=' "$ROOT/.env.local" 2>/dev/null | cut -d= -f1) \
        | sort -u)
  )"
  if [ -n "$missing" ]; then
    printf '%s\n' "$missing" | sed 's/^/  missing from .env: /' >&2
    fail "add every variable above to .env (copy values from .env.example)"
  fi
  log "  every .env.example variable name is present"
}

validate_config() {
  log "Validating configuration (environment: $ENVIRONMENT)..."
  local var
  for var in JWT_SECRET SERVICE_TOKEN POSTGRES_USER POSTGRES_PASSWORD POSTGRES_DB \
            IDENTITY_DB_USER IDENTITY_DB_PASSWORD WORKSPACE_DB_USER WORKSPACE_DB_PASSWORD \
            WEBHOOK_SECRET; do
    [ -n "${!var:-}" ] || fail "required variable $var is not set in .env"
  done
  if [ "$ENVIRONMENT" = "production" ]; then
    for var in IDENTITY_URL WORKSPACE_URL LEGACY_API_URL \
               SMTP_HOST SMTP_ADDR SMTP_FROM; do
      [ -n "${!var:-}" ] || fail "production requires $var in .env"
    done
    [ "${APP_ENV_VALUE}" = "production" ] || fail "APP_ENV must be 'production' when deploying production (got '$APP_ENV_VALUE')"
    # Mirror the gateway fail-fast rules so deploy fails before containers do.
    [ -n "${IDENTITY_URL:-}" ] && [ -n "${WORKSPACE_URL:-}" ] && [ -n "${LEGACY_API_URL:-}" ] \
      || fail "gateway upstream URLs must all be set in production"
  fi
  log "Configuration validation passed"
}

# Catches interpolation errors (e.g. ${WEBHOOK_SECRET:?...} with an empty
# value) before any container is built or started.
check_compose_config() {
  log "Validating compose configuration (docker compose config -q)..."
  "${COMPOSE[@]}" config -q || fail "docker compose config is invalid"
  log "  compose config OK"
}

take_backup() {
  local stamp dir
  stamp="$(date +%Y%m%d%H%M%S)"
  dir="$ROOT/backups/$stamp"
  mkdir -p "$dir"
  log "Backing up databases to backups/$stamp ..."
  "${COMPOSE[@]}" up -d --no-deps postgres >/dev/null
  local i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    "${COMPOSE[@]}" exec -T postgres pg_isready -U "${POSTGRES_USER:-email}" >/dev/null 2>&1 && break
    sleep 2
  done
  local db
  for db in "${POSTGRES_DB:-email}" identity workspace; do
    "${COMPOSE[@]}" exec -T postgres pg_dump -U "${POSTGRES_USER:-email}" -d "$db" | gzip > "$dir/$db.sql.gz"
    # pipefail already aborts on pg_dump failure; this also guards a silent
    # zero-byte dump. Deploy refuses to continue without a real backup.
    [ -s "$dir/$db.sql.gz" ] || fail "backup for $db is empty: $dir/$db.sql.gz"
    log "  dumped $db"
  done
  echo "$dir" > "$ROOT/backups/latest"
  log "Backup complete: $dir"
}

build_images() {
  log "Building images (migrate services, api, worker, identity, workspace, gateway, web)..."
  "${COMPOSE[@]}" build migrate identity-db-migrate workspace-db-migrate api worker identity workspace gateway web
}

run_migrations() {
  log "Running database migrations..."
  "${COMPOSE[@]}" run --rm migrate
  "${COMPOSE[@]}" run --rm identity-db-migrate
  "${COMPOSE[@]}" run --rm workspace-db-migrate
  log "Migrations applied"
}

start_stack() {
  # Infrastructure first (no application dependencies), then the application
  # services with --no-deps: migrations already ran explicitly above, so the
  # one-shot migrate jobs are not re-triggered here.
  log "Starting infrastructure (postgres, redis, nats, stalwart)..."
  "${COMPOSE[@]}" up -d postgres redis nats stalwart
  log "Starting application services (--no-deps)..."
  "${COMPOSE[@]}" up -d --no-deps api worker identity workspace gateway web caddy
  log "Stack started"
}

# http_check <name> <url> <expected_substring|-> [retries]
http_check() {
  local name="$1" url="$2" expect="$3" retries="${4:-15}" body status i
  for ((i = 1; i <= retries; i++)); do
    body="$(curl -fsS -m 3 "$url" 2>/dev/null || true)"
    status="$(curl -s -o /dev/null -w '%{http_code}' -m 3 "$url" 2>/dev/null || echo 000)"
    if [ "$status" = "200" ]; then
      if [ "$expect" = "-" ] || printf '%s' "$body" | grep -qF "$expect"; then
        log "  ok  $name"
        return 0
      fi
    fi
    sleep 2
  done
  fail "smoke check failed: $name (status=$status url=$url body=${body:0:200})"
}

smoke_tests() {
  log "Running smoke tests against the combined stack..."
  http_check "legacy-api-ready"  "http://127.0.0.1:8080/readyz" "-"
  http_check "identity-ready"    "http://127.0.0.1:8082/readyz" "-"
  http_check "workspace-ready"   "http://127.0.0.1:8083/readyz" "-"
  # Gateway readiness must prove every upstream, including legacy, is reachable.
  http_check "gateway-ready"     "http://127.0.0.1:8084/readyz" '"legacy":"up"'
  http_check "worker-health"     "http://127.0.0.1:9091/healthz" "-"
  http_check "web"               "http://127.0.0.1:3000" "-"
  local caddy_status
  caddy_status="$(curl -s -o /dev/null -w '%{http_code}' -m 5 http://127.0.0.1:80/ || echo 000)"
  case "$caddy_status" in
    2*|3*) log "  ok  caddy-edge (status $caddy_status)" ;;
    *) fail "smoke check failed: caddy-edge (status $caddy_status)" ;;
  esac
  # End-to-end auth path through the gateway: unauthenticated protected route
  # must reach the legacy API and return 401 (not 5xx/upstream error).
  local auth_status
  auth_status="$(curl -s -o /dev/null -w '%{http_code}' -m 5 http://127.0.0.1:8084/api/v1/contacts || echo 000)"
  [ "$auth_status" = "401" ] || fail "gateway auth check: expected 401 from /api/v1/contacts, got $auth_status"
  log "  ok  gateway-auth (401 unauthenticated)"
  log "All smoke tests passed"
}

do_rollback() {
  local yes="" arg
  for arg in "$@"; do
    if [ "$arg" = "--yes" ]; then yes=1; fi
  done
  if [ -z "$yes" ]; then
    fail "rollback restores database backups and is DESTRUCTIVE. Re-run with: ./scripts/deploy.sh rollback --yes"
  fi
  local latest
  latest="$(cat "$ROOT/backups/latest" 2>/dev/null || true)"
  [ -n "$latest" ] && [ -d "$latest" ] || fail "no backup found (backups/latest missing)"
  log "Rolling back databases from $latest ..."
  local db
  for db in "${POSTGRES_DB:-email}" identity workspace; do
    [ -f "$latest/$db.sql.gz" ] || fail "backup file missing: $latest/$db.sql.gz"
    log "  restoring $db ..."
    gunzip -c "$latest/$db.sql.gz" | "${COMPOSE[@]}" exec -T postgres psql -U "${POSTGRES_USER:-email}" -d "$db" -v ON_ERROR_STOP=1 -c "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;" >/dev/null
    gunzip -c "$latest/$db.sql.gz" | "${COMPOSE[@]}" exec -T postgres psql -U "${POSTGRES_USER:-email}" -d "$db" -v ON_ERROR_STOP=1 >/dev/null
  done
  start_stack
  smoke_tests
  log "Rollback complete (data restored from $latest; images unchanged — redeploy a previous git tag to roll back code)"
}

case "$COMMAND" in
  backup)
    validate_env_keys
    validate_config
    take_backup
    ;;
  smoke)
    smoke_tests
    ;;
  rollback)
    do_rollback "$@"
    ;;
  deploy)
    validate_env_keys
    validate_config
    check_compose_config
    take_backup
    build_images
    run_migrations
    start_stack
    smoke_tests
    log "Deploy completed successfully"
    log "Rollback data with: ./scripts/deploy.sh rollback --yes"
    ;;
esac
