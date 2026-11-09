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

run_query() {
  local query="$1"
  if [[ -n "${DOMAIN_CHECK_DATABASE_URL:-}" ]]; then
    "${COMPOSE[@]}" exec -T -e "DOMAIN_CHECK_DATABASE_URL=$DOMAIN_CHECK_DATABASE_URL" postgres \
      sh -c 'psql "$DOMAIN_CHECK_DATABASE_URL" -X -v ON_ERROR_STOP=1 -A -t -F "|" -c "$1"' sh "$query"
  else
    "${COMPOSE[@]}" exec -T postgres sh -c \
      'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -X -v ON_ERROR_STOP=1 -A -t -F "|" -c "$1"' sh "$query"
  fi
}

table_exists="$(run_query "SELECT to_regclass('public.sending_domains') IS NOT NULL" | tr -d '[:space:]')"
if [[ "$table_exists" != "t" ]]; then
  echo "Sending-domain table does not exist yet; no duplicates to check."
  exit 0
fi

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

rows="$(run_query "$query")"

echo "Duplicate sending domains (domain, organization_id, status):"
if [[ -z "$rows" ]]; then
  echo "No duplicate sending domains found."
else
  printf '%s\n' "$rows"
  echo "ERROR: resolve duplicate sending domains before running migrations." >&2
  exit 1
fi
