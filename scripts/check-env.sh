#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Validates the effective environment (values are never printed, only names).
#
# Usage:
#   scripts/check-env.sh              # .env plus local overrides from .env.local
#   scripts/check-env.sh --no-local   # .env only (what a server sees)
#
# SENDWISE_ENV_FILE overrides the .env path, matching scripts/deploy.sh.
STRICT=0
case "${1:-}" in
  "") ;;
  --no-local|--production) STRICT=1 ;;
  *) echo "usage: $0 [--no-local]" >&2; exit 2 ;;
esac

ENV_FILE="${SENDWISE_ENV_FILE:-$ROOT/.env}"
[ -f "$ENV_FILE" ] || { echo "missing $ENV_FILE" >&2; exit 1; }

set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
if [ "$STRICT" -eq 0 ] && [ -f "$ROOT/.env.local" ]; then
  # shellcheck disable=SC1090
  . "$ROOT/.env.local"
fi
set +a

ENV_NAME="${APP_ENV:-development}"
if [ -z "${SMTP_TLS_MODE:-}" ]; then
  case "$ENV_NAME" in
    production|staging) SMTP_TLS_MODE=starttls ;;
    *) SMTP_TLS_MODE=none ;;
  esac
fi

case "$SMTP_TLS_MODE" in
  none|starttls|tls) ;;
  *)
    echo "SMTP_TLS_MODE must be none, starttls, or tls" >&2
    exit 1
    ;;
esac

# --- Required names (values are never echoed) --------------------------------
# SERVICE_TOKEN      service-to-service auth (gateway, api, identity, workspace)
# DKIM_ENCRYPTION_KEY encrypts each generated DKIM private key at rest
# IDENTITY_URL       session revocation checks in staging/production
# API_BASE_URL       public origin the browser calls
required=(
  APP_ENV
  SERVICE_TOKEN
  WEBHOOK_SECRET
  JWT_SECRET
  POSTGRES_PASSWORD
  DKIM_ENCRYPTION_KEY
  IDENTITY_URL
  API_BASE_URL
  APP_BASE_URL
  SMTP_ADDR
  SMTP_HOST
  SMTP_TLS_MODE
  SMTP_FROM
)
missing=0
for v in "${required[@]}"; do
  if [ -z "${!v:-}" ]; then
    echo "missing required env var: $v" >&2
    missing=1
  fi
done
[ "$missing" -eq 0 ] || exit 1

# --- API_BASE_URL must be a real http(s) origin ------------------------------
case "$API_BASE_URL" in
  http://*|https://*) ;;
  *)
    echo "API_BASE_URL must be an http(s) URL (got a non-URL value)" >&2
    exit 1
    ;;
esac
case "$API_BASE_URL" in
  http://localhost*|http://127.0.0.1*|https://localhost*)
    if [ "$ENV_NAME" = "production" ] || [ "$ENV_NAME" = "staging" ]; then
      echo "API_BASE_URL must be the public URL in APP_ENV=$ENV_NAME, not a localhost address" >&2
      exit 1
    fi
    ;;
esac

# --- Secure environments: no development mail path ---------------------------
if [ "$ENV_NAME" = "production" ] || [ "$ENV_NAME" = "staging" ]; then
  if [ "$SMTP_TLS_MODE" = "none" ]; then
    echo "SMTP_TLS_MODE must be starttls or tls in APP_ENV=$ENV_NAME" >&2
    exit 1
  fi
  case "${MAIL_PROVIDER:-auto}" in
    log)
      echo "MAIL_PROVIDER=log is not allowed in APP_ENV=$ENV_NAME: it prints messages instead of delivering them" >&2
      exit 1
      ;;
    auto|smtp) ;;
    *)
      echo "MAIL_PROVIDER must be auto, smtp, or log" >&2
      exit 1
      ;;
  esac

  # Campaign delivery always uses SMTP/Stalwart, so the relay must be real.
  addr_host="${SMTP_ADDR%%:*}"
  for candidate in "$SMTP_HOST" "$addr_host"; do
    normalized="$(printf '%s' "$candidate" | tr '[:upper:]' '[:lower:]')"
    case "$normalized" in
      mailpit|localhost|127.0.0.1|::1|0.0.0.0)
        echo "SMTP target '$candidate' is a development-only relay; set SMTP_ADDR and SMTP_HOST to a real relay in APP_ENV=$ENV_NAME" >&2
        exit 1
        ;;
    esac
  done
  if [ -z "${SMTP_USERNAME:-}" ] || [ -z "${SMTP_PASSWORD:-}" ]; then
    echo "missing required env var: SMTP_USERNAME/SMTP_PASSWORD (campaign delivery authenticates in $ENV_NAME)" >&2
    exit 1
  fi
fi

echo "env ok (APP_ENV=$ENV_NAME, file=$ENV_FILE, required names: ${#required[@]} declared, secure-mail checks: $([ "$STRICT" -eq 1 ] && echo strict || echo with-local-overrides))"
