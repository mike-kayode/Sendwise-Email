#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
[ -f .env ] || { echo "missing .env"; exit 1; }
set -a
. .env
[ -f .env.local ] && . .env.local
set +a
required=(SERVICE_TOKEN WEBHOOK_SECRET JWT_SECRET POSTGRES_PASSWORD)
missing=0
for v in "${required[@]}"; do
  if [ -z "${!v:-}" ]; then
    echo "missing required env var: $v" >&2
    missing=1
  fi
done
[ "$missing" -eq 0 ] || exit 1
echo "env ok"
