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
# Deploy order: environment validation -> both Compose-file checks -> image
# snapshot -> database backup -> build -> migrations -> up --no-deps -> smoke.
# A failed deploy restores the previous application images automatically.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

ROLLBACK_SERVICES=(api worker identity workspace gateway web)
ROLLBACK_IMAGE_REFS=()
ROLLBACK_IMAGE_IDS=()
ROLLBACK_TAGS=()
ROLLBACK_STAMP=""
DEPLOY_IN_PROGRESS=0
SMOKE_TEMP_DIR=""

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

ENV_FILE="${SENDWISE_ENV_FILE:-$ROOT/.env}"
[ -f "$ENV_FILE" ] || fail ".env not found at $ENV_FILE. Copy .env.example and fill in required secrets."

# Later --env-file wins on duplicate keys; .env.local is a local override.
COMPOSE=(docker compose --env-file "$ENV_FILE")
USE_ENV_LOCAL=1
if [ -n "${SENDWISE_ENV_FILE:-}" ]; then
  USE_ENV_LOCAL=0
fi
if [ "$USE_ENV_LOCAL" -eq 1 ] && [ -f "$ROOT/.env.local" ]; then
  COMPOSE+=(--env-file "$ROOT/.env.local")
fi
COMPOSE+=(-f "$ROOT/docker-compose.yml" -f "$ROOT/docker-compose.microservices.yml")

[ -f "$ROOT/.env.example" ] || fail ".env.example not found at $ROOT/.env.example"

command -v curl >/dev/null 2>&1 || fail "curl is required for smoke tests"
command -v docker >/dev/null 2>&1 || fail "docker is required"

# Load env files for validation only (values are never echoed). All names in
# .env.example must be declared in .env; .env.local may only override values.
set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
if [ "$USE_ENV_LOCAL" -eq 1 ] && [ -f "$ROOT/.env.local" ]; then
  # shellcheck disable=SC1090
  . "$ROOT/.env.local"
fi
set +a

APP_ENV_VALUE="${APP_ENV:-development}"
ENVIRONMENT="${ENVIRONMENT_ARG:-$APP_ENV_VALUE}"

validate_env_keys() {
  log "Comparing .env.example variable names against .env..."
  local missing
  missing="$(
    comm -23 \
      <(grep -E '^[A-Za-z_][A-Za-z0-9_]*=' "$ROOT/.env.example" | cut -d= -f1 | sort -u) \
      <(grep -E '^[A-Za-z_][A-Za-z0-9_]*=' "$ENV_FILE" | cut -d= -f1 | sort -u)
  )"
  if [ -n "$missing" ]; then
    printf '%s\n' "$missing" | sed 's/^/  missing from .env: /' >&2
    fail "declare every variable above in .env (copy .env.example)"
  fi
  log "  every .env.example variable name is declared in .env"
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

validate_smoke_credentials() {
  [ -n "${DEPLOY_SMOKE_EMAIL:-}" ] || fail "DEPLOY_SMOKE_EMAIL is required for authenticated smoke tests"
  [ -n "${DEPLOY_SMOKE_PASSWORD:-}" ] || fail "DEPLOY_SMOKE_PASSWORD is required for authenticated smoke tests"
}

# Catches interpolation errors (e.g. ${WEBHOOK_SECRET:?...} with an empty
# value) before any container is built or started.
check_compose_config() {
  local base=(docker compose --env-file "$ENV_FILE")
  if [ "$USE_ENV_LOCAL" -eq 1 ] && [ -f "$ROOT/.env.local" ]; then
    base+=(--env-file "$ROOT/.env.local")
  fi
  log "Validating docker-compose.yml..."
  "${base[@]}" -f "$ROOT/docker-compose.yml" config -q || fail "docker-compose.yml is invalid"
  log "Validating docker-compose.microservices.yml..."
  "${base[@]}" -f "$ROOT/docker-compose.microservices.yml" config -q || fail "docker-compose.microservices.yml is invalid"
  log "Validating the combined Compose configuration..."
  "${COMPOSE[@]}" config -q || fail "docker compose config is invalid"
  log "  all Compose configurations are valid"
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
    "${COMPOSE[@]}" exec -T postgres pg_dump -U "${POSTGRES_USER:-email}" -d "$db" > "$dir/$db.sql"
    [ -s "$dir/$db.sql" ] || fail "backup for $db is empty: $dir/$db.sql"
    gzip "$dir/$db.sql"
    log "  dumped $db"
  done
  echo "$dir" > "$ROOT/backups/latest"
  log "Backup complete: $dir"
}

build_images() {
  log "Building images (migrate services, api, worker, identity, workspace, gateway, web)..."
  "${COMPOSE[@]}" build migrate identity-db-migrate workspace-db-migrate api worker identity workspace gateway web
}

capture_previous_images() {
  local service container image_ref image_id tag
  ROLLBACK_STAMP="$(date +%Y%m%d%H%M%S)"
  for service in "${ROLLBACK_SERVICES[@]}"; do
    container="$("${COMPOSE[@]}" ps -q "$service" 2>/dev/null || true)"
    if [ -z "$container" ]; then
      # First deploy: nothing is running yet, so there is no image to roll back to.
      log "  $service: no running container; nothing to preserve (first deploy)"
      ROLLBACK_IMAGE_REFS+=("")
      ROLLBACK_IMAGE_IDS+=("")
      ROLLBACK_TAGS+=("")
      continue
    fi
    image_ref="$(docker inspect --format '{{.Config.Image}}' "$container")"
    image_id="$(docker inspect --format '{{.Image}}' "$container")"
    if [ -z "$image_ref" ] || [ -z "$image_id" ]; then
      fail "cannot identify the previous image for $service"
    fi
    tag="sendwise-rollback:${ROLLBACK_STAMP}-${service}"
    if docker image tag "$image_id" "$tag" 2>/dev/null; then
      :
    elif docker commit --pause=false "$container" "$tag" >/dev/null 2>&1; then
      # The image layer was pruned while the container kept running: snapshot
      # the container itself so rollback still has a restore point.
      log "  $service: image $image_id missing locally; snapshotted running container instead"
    else
      log "  WARNING: could not preserve the previous image for $service; rollback will skip it"
      ROLLBACK_IMAGE_REFS+=("")
      ROLLBACK_IMAGE_IDS+=("")
      ROLLBACK_TAGS+=("")
      continue
    fi
    ROLLBACK_IMAGE_REFS+=("$image_ref")
    ROLLBACK_IMAGE_IDS+=("$image_id")
    ROLLBACK_TAGS+=("$tag")
  done
  log "Previous application images preserved for rollback"
}

cleanup_rollback_tags() {
  local tag
  for tag in "${ROLLBACK_TAGS[@]}"; do
    [ -n "$tag" ] || continue
    docker image rm "$tag" >/dev/null 2>&1 || log "WARNING: could not remove temporary rollback tag $tag"
  done
}

rollback_images() {
  local i failed=0
  local restore_services=()
  log "Restoring previous application images..."
  for ((i = 0; i < ${#ROLLBACK_SERVICES[@]}; i++)); do
    if [ -z "${ROLLBACK_TAGS[$i]}" ]; then
      log "  ${ROLLBACK_SERVICES[$i]}: no preserved image (first deploy); skipping"
      continue
    fi
    if ! docker image tag "${ROLLBACK_TAGS[$i]}" "${ROLLBACK_IMAGE_REFS[$i]}"; then
      log "ERROR: could not restore image for ${ROLLBACK_SERVICES[$i]}"
      failed=1
      continue
    fi
    restore_services+=("${ROLLBACK_SERVICES[$i]}")
  done
  if [ "${#restore_services[@]}" -eq 0 ]; then
    log "No previous images were preserved; nothing to restore"
    cleanup_rollback_tags
    return 0
  fi
  if [ "$failed" -eq 0 ]; then
    if ! "${COMPOSE[@]}" up -d --no-deps --force-recreate --no-build "${restore_services[@]}"; then
      log "ERROR: previous images were restored, but the application containers could not be restarted"
      failed=1
    fi
  fi
  cleanup_rollback_tags
  if [ "$failed" -eq 0 ]; then
    log "Previous application images restored"
    return 0
  fi
  return 1
}

on_exit() {
  local status="$1"
  trap - EXIT
  if [ "$DEPLOY_IN_PROGRESS" -eq 1 ] && [ "$status" -ne 0 ]; then
    log "Deployment failed; attempting automatic image rollback"
    rollback_images || log "ERROR: automatic image rollback was incomplete"
  fi
  if [ -n "$SMOKE_TEMP_DIR" ]; then
    rm -rf -- "$SMOKE_TEMP_DIR" || log "WARNING: could not remove temporary smoke files"
  fi
  exit "$status"
}

run_migrations() {
  log "Running database migrations..."
  "${COMPOSE[@]}" run --rm migrate
  "${COMPOSE[@]}" run --rm identity-db-migrate
  "${COMPOSE[@]}" run --rm workspace-db-migrate
  log "Migrations applied"
}

start_stack() {
  log "Starting infrastructure (postgres, redis, nats, stalwart)..."
  "${COMPOSE[@]}" up -d --no-deps postgres redis nats stalwart
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
  validate_smoke_credentials
  command -v jq >/dev/null 2>&1 || fail "jq is required for authenticated smoke tests"
  SMOKE_TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sendwise-smoke.XXXXXX")"
  jq -n '{email: env.DEPLOY_SMOKE_EMAIL, password: env.DEPLOY_SMOKE_PASSWORD}' > "$SMOKE_TEMP_DIR/login.json"
  local auth_status access_token endpoint status
  auth_status="$(curl -sS -m 10 -o "$SMOKE_TEMP_DIR/login-response.json" -w '%{http_code}' \
    -H 'Content-Type: application/json' --data-binary @"$SMOKE_TEMP_DIR/login.json" \
    http://127.0.0.1:8084/api/v1/auth/login 2>/dev/null || true)"
  [ "$auth_status" = "200" ] || fail "gateway login smoke test expected 200, got $auth_status"
  log "  ok  gateway-login (200)"
  access_token="$(jq -er '.access_token | select(type == "string" and length > 0)' "$SMOKE_TEMP_DIR/login-response.json" 2>/dev/null)" \
    || fail "gateway login smoke test returned no access token"
  printf 'header = "Authorization: Bearer %s"\n' "$access_token" > "$SMOKE_TEMP_DIR/auth.curl"
  chmod 600 "$SMOKE_TEMP_DIR/auth.curl"
  for endpoint in lists contacts campaigns domains tags segments; do
    status="$(curl -sS -m 10 -o /dev/null -w '%{http_code}' --config "$SMOKE_TEMP_DIR/auth.curl" \
      "http://127.0.0.1:8084/api/v1/$endpoint" 2>/dev/null || true)"
    [ "$status" = "200" ] || fail "gateway $endpoint smoke test expected 200, got $status"
    log "  ok  gateway-$endpoint (200)"
  done
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
    validate_smoke_credentials
    smoke_tests
    ;;
  rollback)
    do_rollback "$@"
    ;;
  deploy)
    validate_env_keys
    validate_config
    validate_smoke_credentials
    check_compose_config
    capture_previous_images
    DEPLOY_IN_PROGRESS=1
    trap 'on_exit $?' EXIT
    take_backup
    build_images
    run_migrations
    start_stack
    smoke_tests
    DEPLOY_IN_PROGRESS=0
    cleanup_rollback_tags
    log "Deploy completed successfully"
    log "Previous application images are restored automatically if deployment smoke tests fail"
    ;;
esac
