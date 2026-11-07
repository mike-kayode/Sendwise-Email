#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

for command in docker jq stalwart-cli; do
  command -v "$command" >/dev/null 2>&1 || fail "$command is required (install stalwart-cli from https://github.com/stalwartlabs/cli)"
done

: "${STALWART_DOMAIN:?Set STALWART_DOMAIN}"
: "${STALWART_ADMIN_PASSWORD:?Set STALWART_ADMIN_PASSWORD}"
: "${STALWART_SUBMIT_PASSWORD:?Set STALWART_SUBMIT_PASSWORD}"

STALWART_URL="${STALWART_URL:-http://127.0.0.1:8081}"
STALWART_HOSTNAME="${STALWART_HOSTNAME:-mail.${STALWART_DOMAIN}}"
STALWART_ADMIN_USERNAME="${STALWART_ADMIN_USERNAME:-admin@${STALWART_DOMAIN}}"
STALWART_SUBMIT_USERNAME="${STALWART_SUBMIT_USERNAME:-noreply}"

case "$STALWART_URL" in
  http://localhost:*|http://127.0.0.1:*|https://localhost:*|https://127.0.0.1:*) ;;
  *) fail "This provisioning script is local-only; STALWART_URL must use localhost or 127.0.0.1" ;;
esac
[[ "$STALWART_DOMAIN" =~ ^[A-Za-z0-9.-]+$ ]] || fail "STALWART_DOMAIN must be a DNS domain"
[[ "$STALWART_HOSTNAME" =~ ^[A-Za-z0-9.-]+$ ]] || fail "STALWART_HOSTNAME must be a DNS hostname"
[[ "$STALWART_SUBMIT_USERNAME" =~ ^[A-Za-z0-9._+-]+$ ]] || fail "STALWART_SUBMIT_USERNAME contains unsupported characters"

umask 077
plan="$(mktemp "${TMPDIR:-/tmp}/sendwise-stalwart-plan.XXXXXX")"
output="$(mktemp "${TMPDIR:-/tmp}/sendwise-stalwart-cli.XXXXXX")"
trap 'rm -f -- "$plan" "$output"' EXIT

jq -cn \
  --arg domain "$STALWART_DOMAIN" \
  '{ "@type":"upsert", "object":"Domain", "matchOn":["name"], "value":{"sendwise-domain":{
    "name":$domain,
    "certificateManagement":{"@type":"Manual"},
    "dkimManagement":{"@type":"Automatic"},
    "dnsManagement":{"@type":"Manual"},
    "subAddressing":{"@type":"Enabled"}
  }}}' >"$plan"
jq -cn \
  --arg domain "$STALWART_DOMAIN" \
  --arg submit_name "$STALWART_SUBMIT_USERNAME" \
  --arg submit_password "$STALWART_SUBMIT_PASSWORD" \
  '{ "@type":"upsert", "object":"Account", "matchOn":["name","domainId"], "value":{"sendwise-submission":{
    "@type":"User",
    "name":$submit_name,
    "domainId":"#sendwise-domain",
    "credentials":{"0":{"@type":"Password","secret":$submit_password}}
  }}}' >>"$plan"
jq -cn \
  '{ "@type":"upsert", "object":"NetworkListener", "matchOn":["name"], "value":{"sendwise-submission-listener":{
    "name":"sendwise-submission",
    "protocol":"smtp",
    "bind":{"[::]:587":true},
    "useTls":true,
    "implicitTls":false
  }}}' >>"$plan"
jq -cn \
  --arg hostname "$STALWART_HOSTNAME" \
  '{ "@type":"update", "object":"SystemSettings", "value":{
    "defaultHostname":$hostname,
    "defaultDomainId":"#sendwise-domain"
  }}' >>"$plan"

export STALWART_URL
export STALWART_USER="$STALWART_ADMIN_USERNAME"
export STALWART_PASSWORD="$STALWART_ADMIN_PASSWORD"
if ! stalwart-cli --insecure apply --file "$plan" >"$output" 2>&1; then
  fail "Stalwart provisioning failed; command output is suppressed because it may contain submitted credentials"
fi

docker compose restart stalwart >/dev/null || fail "Could not restart the local Stalwart service"
echo "Local Stalwart domain, authenticated submission account, and STARTTLS listener configured."
echo "Verify the listener with scripts/smoke-mail.sh after configuring the identity SMTP variables."
