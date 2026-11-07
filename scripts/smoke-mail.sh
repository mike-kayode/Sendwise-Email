#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

[ "$#" -eq 1 ] || fail "usage: scripts/smoke-mail.sh <recipient>"
recipient="$1"
command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v jq >/dev/null 2>&1 || fail "jq is required"
command -v openssl >/dev/null 2>&1 || fail "openssl is required"

identity_url="${IDENTITY_SMOKE_URL:-http://127.0.0.1:8082}"
case "$identity_url" in
  http://localhost:*|http://127.0.0.1:*) ;;
  *) fail "IDENTITY_SMOKE_URL must point to a local Identity listener" ;;
esac

password="Smoke-$(openssl rand -hex 24)"
stamp="$(date +%s)-${RANDOM}"
payload="$(mktemp "${TMPDIR:-/tmp}/sendwise-mail-smoke.XXXXXX")"
response="$(mktemp "${TMPDIR:-/tmp}/sendwise-mail-response.XXXXXX")"
trap 'rm -f -- "$payload" "$response"' EXIT

jq -n \
  --arg email "$recipient" \
  --arg password "$password" \
  --arg name "Sendwise Mail Smoke" \
  --arg organization_name "Sendwise Mail Smoke $stamp" \
  '{email:$email,password:$password,name:$name,organization_name:$organization_name}' >"$payload"

if ! status="$(curl -sS --connect-timeout 5 --max-time 20 \
  -o "$response" -w '%{http_code}' \
  -H 'Content-Type: application/json' \
  --data-binary @"$payload" \
  "${identity_url%/}/auth/register")"; then
  fail "could not reach the local Identity registration endpoint at $identity_url"
fi

if [ "$status" != "201" ]; then
  message="$(jq -r '.error.message // .message // "no error detail returned"' "$response" 2>/dev/null || true)"
  fail "Identity registration failed (HTTP $status): $message"
fi

delivery_status="$(jq -r '.delivery_status // "accepted"' "$response")"
if [ "$delivery_status" != "accepted" ]; then
  fail "Identity created the account but verification delivery is $delivery_status; check Identity SMTP logs"
fi

echo "SMTP smoke passed: the configured relay accepted a verification email for $recipient."
echo "This confirms SMTP handoff only; it does not prove inbox delivery."
