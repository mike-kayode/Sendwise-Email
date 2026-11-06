#!/usr/bin/env bash
set -euo pipefail

SITE_URL="${SITE_URL:-https://campaign.pipsleague.com}"
URL_BASE="${SITE_URL%/}"

log() { printf '%s %s\n' "[$(date '+%Y-%m-%d %H:%M:%S')]" "$*"; }
fail() { printf '%s ERROR: %s\n' "[$(date '+%Y-%m-%d %H:%M:%S')]" "$*" >&2; exit 1; }

check_url() {
  local url="$1"
  local expected_code="${2:-200}"
  local status
  status="$(curl -sS -L --max-time 20 -o /tmp/smoke-prod-body.$$ -w '%{http_code}' "$url" || true)"
  [ "$status" = "$expected_code" ] || fail "GET $url returned $status, expected $expected_code"
  log "ok  $url ($status)"
}

check_html() {
  local url="$1"
  curl -sS -L --max-time 20 "$url" | grep -qiE 'sign in|login|email|password' || fail "login page content check failed for $url"
  log "ok  page-signature $url"
}

check_api_unauthorized() {
  local url="$1"
  local status
  status="$(curl -sS -L --max-time 20 -H 'Authorization: Bearer invalid-token' -o /tmp/smoke-prod-auth.$$ -w '%{http_code}' "$url" || true)"
  [ "$status" = "401" ] || fail "unauthorized API check for $url returned $status, expected 401"
  log "ok  auth-protected $url ($status)"
}

log "Running production smoke checks against $URL_BASE"
check_url "$URL_BASE/healthz" "200"
check_html "$URL_BASE/login"
check_api_unauthorized "$URL_BASE/api/v1/auth/me"
log "Production smoke checks passed"
