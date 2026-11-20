#!/usr/bin/env bash
set -euo pipefail

apply=0
case "${1:-}" in
  "") ;;
  --apply) apply=1 ;;
  -h|--help)
    printf 'Usage: %s [--apply]\n' "$0"
    printf 'Dry-run is the default. --apply creates or updates the matching Stalwart webhook.\n'
    exit 0
    ;;
  *) printf 'ERROR: unknown option: %s\n' "$1" >&2; exit 2 ;;
esac

for tool in curl jq openssl; do
  command -v "$tool" >/dev/null 2>&1 || { printf 'ERROR: %s is required\n' "$tool" >&2; exit 1; }
done

: "${STALWART_MANAGEMENT_URL:?Set STALWART_MANAGEMENT_URL}"
: "${STALWART_ADMIN_USERNAME:?Set STALWART_ADMIN_USERNAME}"
: "${STALWART_ADMIN_PASSWORD:?Set STALWART_ADMIN_PASSWORD}"
: "${STALWART_EVENTS_WEBHOOK_URL:?Set STALWART_EVENTS_WEBHOOK_URL}"
: "${WEBHOOK_SECRET:?Set WEBHOOK_SECRET}"

events_json='{"delivery.attempt-start":true,"delivery.attempt-end":true,"delivery.connect-error":true,"delivery.greeting-failed":true,"delivery.ehlo-rejected":true,"delivery.mail-from-rejected":true,"delivery.rcpt-to-rejected":true,"delivery.rcpt-to-failed":true,"delivery.message-rejected":true,"delivery.start-tls-error":true,"delivery.implicit-tls-error":true,"delivery.completed":true,"delivery.delivered":true,"delivery.failed":true,"delivery.dsn-success":true,"delivery.dsn-temp-fail":true,"delivery.dsn-perm-fail":true,"queue.rescheduled":true}'

if [ "$apply" -ne 1 ]; then
  printf 'DRY RUN: configure a signed Stalwart telemetry webhook at %s\n' "$STALWART_EVENTS_WEBHOOK_URL"
  printf 'Events: delivery attempts, SMTP outcomes, DSNs, and queue rescheduling.\n'
  printf 'No Stalwart request was made. Re-run with --apply to create or update it.\n'
  exit 0
fi

umask 077
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/sendwise-stalwart-events.XXXXXX")"
trap 'rm -rf -- "$tmp_dir"' EXIT
auth_file="$tmp_dir/auth.curl"
response_file="$tmp_dir/response.json"
request_file="$tmp_dir/request.json"

curl_user="$(STALWART_CURL_USER="$STALWART_ADMIN_USERNAME:$STALWART_ADMIN_PASSWORD" jq -nr 'env.STALWART_CURL_USER | @json')"
printf 'user = %s\n' "$curl_user" >"$auth_file"
chmod 600 "$auth_file"

call_jmap() {
  local method="$1"
  curl --fail --silent --show-error --max-time 20 \
    --config "$auth_file" \
    -H 'Content-Type: application/json' \
    --data-binary @"$request_file" \
    "${STALWART_MANAGEMENT_URL%/}/jmap/" >"$response_file" || {
      printf 'ERROR: Stalwart JMAP request failed (%s)\n' "$method" >&2
      exit 1
    }
  if jq -e --arg method "$method" '
    any(.methodResponses[]?; .[0] == "error")
  ' "$response_file" >/dev/null; then
    printf 'ERROR: Stalwart rejected the %s request\n' "$method" >&2
    exit 1
  fi
}

jq -n --arg url "$STALWART_EVENTS_WEBHOOK_URL" '{
  using: ["urn:ietf:params:jmap:core", "urn:stalwart:jmap"],
  methodCalls: [["x:WebHook/get", {accountId: "0", properties: ["id", "url"]}, "hooks"]]
}' >"$request_file"
call_jmap "WebHook/get"
hook_id="$(jq -er --arg url "$STALWART_EVENTS_WEBHOOK_URL" '
  .methodResponses[]
  | select(.[0] == "x:WebHook/get" and .[2] == "hooks")
  | .[1].list[]?
  | select(.url == $url)
  | .id
' "$response_file" | head -n 1 || true)"

if [ -n "$hook_id" ]; then
  set_operation="$(jq -n --arg id "$hook_id" --arg url "$STALWART_EVENTS_WEBHOOK_URL" \
    --arg secret "$WEBHOOK_SECRET" --argjson events "$events_json" '{
      update: {
        ($id): {
          enable: true, level: "trace", lossy: false,
          signatureKey: {"@type":"Value","secret":$secret},
          url: $url,
          events: $events, eventsPolicy: "include"
        }
      }
    }')"
else
  set_operation="$(jq -n --arg url "$STALWART_EVENTS_WEBHOOK_URL" \
    --arg secret "$WEBHOOK_SECRET" --argjson events "$events_json" '{
      create: {
        sendwiseDeliveryJourney: {
          "@type": "WebHook",
          enable: true, level: "trace", lossy: false,
          signatureKey: {"@type":"Value","secret":$secret},
          url: $url,
          events: $events, eventsPolicy: "include"
        }
      }
    }')"
fi
jq -n --argjson operation "$set_operation" '{
  using: ["urn:ietf:params:jmap:core", "urn:stalwart:jmap"],
  methodCalls: [["x:WebHook/set", ($operation + {accountId:"0"}), "set"]]
}' >"$request_file"
call_jmap "WebHook/set"
printf 'Stalwart delivery-event webhook has been configured.\n'
