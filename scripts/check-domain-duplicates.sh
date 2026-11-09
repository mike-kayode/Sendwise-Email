#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${SENDWISE_ENV_FILE:-$ROOT/.env}"
[ -f "$ENV_FILE" ] || {
  echo "missing $ENV_FILE" >&2
  exit 1
}

COMPOSE=(docker compose --env-file "$ENV_FILE")
if [ -z "${SENDWISE_ENV_FILE:-}" ] && [ -f "$ROOT/.env.local" ]; then
  COMPOSE+=(--env-file "$ROOT/.env.local")
fi
COMPOSE+=(-f "$ROOT/docker-compose.yml" -f "$ROOT/docker-compose.microservices.yml")

query="
WITH duplicate_domains AS (
    SELECT lower(domain) AS domain
    FROM sending_domains
    GROUP BY lower(domain)
    HAVING count(*) > 1
)
SELECT lower(d.domain) AS domain, d.organization_id, d.status
FROM sending_domains d
JOIN duplicate_domains duplicates ON duplicates.domain = lower(d.domain)
ORDER BY lower(d.domain), d.created_at, d.organization_id;
"

if [[ -n "${DOMAIN_CHECK_DATABASE_URL:-}" ]]; then
  rows=$("${COMPOSE[@]}" exec -T -e "DOMAIN_CHECK_DATABASE_URL=$DOMAIN_CHECK_DATABASE_URL" postgres \
    sh -c 'psql "$DOMAIN_CHECK_DATABASE_URL" -X -v ON_ERROR_STOP=1 -A -t -F "|" -c "$1"' sh "$query")
else
  rows=$("${COMPOSE[@]}" exec -T postgres sh -c \
    'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -X -v ON_ERROR_STOP=1 -A -t -F "|" -c "$1"' sh "$query")
fi

echo "Duplicate sending domains (domain, organization_id, status):"
if [[ -z "$rows" ]]; then
  echo "No duplicate sending domains found."
else
  printf '%s\n' "$rows"
fi
