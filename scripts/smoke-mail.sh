#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

[ "$#" -le 1 ] || fail "usage: DEPLOY_SMOKE_MAIL_TO=<recipient> scripts/smoke-mail.sh [recipient]"
recipient="${1:-${DEPLOY_SMOKE_MAIL_TO:-}}"
[ -n "$recipient" ] || fail "set DEPLOY_SMOKE_MAIL_TO or pass a recipient"
[ -n "${PLATFORM_SMTP_ADDR:-}" ] || fail "PLATFORM_SMTP_ADDR is required"
[ -n "${PLATFORM_FROM_EMAIL:-}" ] || fail "PLATFORM_FROM_EMAIL is required"
case "$recipient$PLATFORM_FROM_EMAIL" in
  *$'\r'*|*$'\n'*) fail "email addresses cannot contain line breaks" ;;
esac
command -v curl >/dev/null 2>&1 || fail "curl is required"

tls_mode="${PLATFORM_SMTP_TLS_MODE:-starttls}"
case "$tls_mode" in
  starttls)
    protocol="smtp"
    ;;
  tls)
    protocol="smtps"
    ;;
  none)
    [ "${APP_ENV:-development}" != "production" ] || fail "TLS is required in production"
    protocol="smtp"
    ;;
  *) fail "PLATFORM_SMTP_TLS_MODE must be none, starttls, or tls" ;;
esac

message="$(mktemp "${TMPDIR:-/tmp}/sendwise-mail-smoke.XXXXXX")"
trap 'rm -f -- "$message"' EXIT
printf 'From: %s\r\nTo: %s\r\nSubject: Sendwise deployment mail smoke test\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nSendwise transactional SMTP handoff check.\r\n' \
  "$PLATFORM_FROM_EMAIL" "$recipient" >"$message"

curl_args=(
  --silent --show-error
  --connect-timeout 5 --max-time 30
  --url "$protocol://$PLATFORM_SMTP_ADDR"
  --mail-from "$PLATFORM_FROM_EMAIL"
  --mail-rcpt "$recipient"
  --upload-file "$message"
)
if [ -n "${PLATFORM_SMTP_USERNAME:-}" ]; then
  [ -n "${PLATFORM_SMTP_PASSWORD:-}" ] || fail "PLATFORM_SMTP_PASSWORD is required with PLATFORM_SMTP_USERNAME"
  curl_args+=(--user "$PLATFORM_SMTP_USERNAME:$PLATFORM_SMTP_PASSWORD")
fi
if [ "$tls_mode" = starttls ]; then
  curl_args+=(--ssl-reqd)
fi

if ! curl "${curl_args[@]}"; then
  fail "SMTP relay did not accept a message for $recipient"
fi

echo "SMTP smoke passed: the platform relay accepted a message for $recipient."
echo "This confirms SMTP handoff only; it does not prove inbox delivery."
