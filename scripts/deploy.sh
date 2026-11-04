#!/bin/bash
set -euo pipefail

# Sendwise Deploy Script
# Usage: ./scripts/deploy.sh [environment]
#   environment: development|production (default: development)

ENVIRONMENT="${1:-development}"
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

log_error() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] ERROR: $*" >&2
    exit 1
}

log "Starting Sendwise deployment (environment: $ENVIRONMENT)"

# 1. Fail-fast config validation
log "Validating configuration..."
ENV_FILE="$PROJECT_ROOT/.env"
if [ ! -f "$ENV_FILE" ]; then
    log_error ".env file not found at $ENV_FILE. Copy .env.example and fill in secrets."
fi

set -a
. "$ENV_FILE"
set +a

# Required variables (fail if missing in any environment)
for var in JWT_SECRET POSTGRES_USER POSTGRES_PASSWORD POSTGRES_DB \
           IDENTITY_DB_USER IDENTITY_DB_PASSWORD WORKSPACE_DB_USER WORKSPACE_DB_PASSWORD \
           NATS_URL SERVICE_TOKEN; do
  if [ -z "${!var:-}" ]; then
    log_error "Required environment variable $var is not set in $ENV_FILE"
  fi
done

# Production-only required variables
if [ "$ENVIRONMENT" = "production" ]; then
  for var in IDENTITY_URL WORKSPACE_URL LEGACY_API_URL SMTP_ADDR SMTP_USERNAME SMTP_PASSWORD SMTP_FROM; do
    if [ -z "${!var:-}" ]; then
      log_error "Required production environment variable $var is not set in $ENV_FILE"
    fi
  done
fi

log "Configuration validation passed"

# 2. Build Docker images
log "Building Docker images..."
docker compose -f "$PROJECT_ROOT/docker-compose.microservices.yml" build --no-cache identity workspace gateway 2>&1 | tail -5

# 3. Run database migrations
log "Running database migrations..."
docker compose -f "$PROJECT_ROOT/docker-compose.microservices.yml" up -d --no-deps identity-db-migrate workspace-db-migrate 2>&1 | tail -5

# Wait for migrations to complete
sleep 5

# Check migration exit codes
IDENTITY_MIGRATE_STATUS=$(docker inspect -f '{{.State.ExitCode}}' email-platform-identity-db-migrate-1 2>/dev/null || echo "unknown")
WORKSPACE_MIGRATE_STATUS=$(docker inspect -f '{{.State.ExitCode}}' email-platform-workspace-db-migrate-1 2>/dev/null || echo "unknown")
if [ "$IDENTITY_MIGRATE_STATUS" != "0" ]; then
  log_error "Identity migration failed (exit code: $IDENTITY_MIGRATE_STATUS)"
fi
if [ "$WORKSPACE_MIGRATE_STATUS" != "0" ]; then
  log_error "Workspace migration failed (exit code: $WORKSPACE_MIGRATE_STATUS)"
fi
log "Migrations applied successfully"

# 4. Start services
log "Starting services..."
docker compose -f "$PROJECT_ROOT/docker-compose.microservices.yml" --env-file "$ENV_FILE" up -d --no-deps identity workspace gateway 2>&1 | tail -5

sleep 5

# 5. Smoke tests
log "Running smoke tests..."

check_health() {
    local name="$1"
    local url="$2"
    local expected="$3"
    local actual
    actual=$(docker exec email-platform-identity-1 wget -q -O - "$url" 2>/dev/null || echo "FAIL")
    if [ "$actual" != "$expected" ]; then
        log_error "$name health check failed: got '$actual', expected '$expected'"
    fi
    log "$name: $actual"
}

check_health "identity-health" "http://127.0.0.1:8082/healthz" '{"service":"identity","status":"ok"}'
check_health "identity-ready" "http://127.0.0.1:8082/readyz" '{"status":"ready"}'
check_health "workspace-health" "http://127.0.0.1:8083/healthz" '{"service":"workspace","status":"ok"}'
check_health "workspace-ready" "http://127.0.0.1:8083/readyz" '{"status":"ready"}'
check_health "gateway-health" "http://127.0.0.1:8084/healthz" '{"service":"gateway","status":"ok"}'
check_health "gateway-ready" "http://127.0.0.1:8084/readyz" '{"status":"ready"}'

log "All smoke tests passed"

# 6. Rollback procedure
log "Deploy completed successfully"
log ""
log "Rollback procedure:"
log "  docker compose -f $PROJECT_ROOT/docker-compose.microservices.yml down identity workspace gateway"
log "  # Then restart previous image versions if needed"
log ""
log "Endpoints:"
log "  Identity:  http://127.0.0.1:8082"
log "  Workspace: http://127.0.0.1:8083"
log "  Gateway:   http://127.0.0.1:8084"
