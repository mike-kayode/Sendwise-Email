#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

for tool in docker curl jq openssl; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done

: "${STALWART_DOMAIN:?Set STALWART_DOMAIN}"
: "${STALWART_SUBMIT_PASSWORD:?Set STALWART_SUBMIT_PASSWORD}"
: "${STALWART_ADMIN_PASSWORD:?Set STALWART_ADMIN_PASSWORD}"

STALWART_URL="${STALWART_URL:-http://127.0.0.1:8081}"
STALWART_HOSTNAME="${STALWART_HOSTNAME:-mail.${STALWART_DOMAIN}}"
STALWART_ADMIN_USERNAME="${STALWART_ADMIN_USERNAME:-admin}"
STALWART_SUBMIT_USERNAME="${STALWART_SUBMIT_USERNAME:-noreply}"
STALWART_RECOVERY_ADMIN="${STALWART_RECOVERY_ADMIN:-${STALWART_ADMIN_USERNAME}:${STALWART_ADMIN_PASSWORD}}"

case "$STALWART_URL" in
  http://localhost:*|http://127.0.0.1:*|https://localhost:*|https://127.0.0.1:*) ;;
  *) fail "STALWART_URL must use localhost or 127.0.0.1; provisioning is local-only" ;;
esac
[[ "$STALWART_DOMAIN" =~ ^[A-Za-z0-9.-]+$ ]] || fail "STALWART_DOMAIN must be a DNS domain"
[[ "$STALWART_HOSTNAME" =~ ^[A-Za-z0-9.-]+$ ]] || fail "STALWART_HOSTNAME must be a DNS hostname"
[[ "$STALWART_SUBMIT_USERNAME" =~ ^[A-Za-z0-9._+-]+$ ]] || fail "STALWART_SUBMIT_USERNAME is invalid"
password_is_strong() {
  local password="$1"
  [[ ${#password} -ge 16 ]] || return 1
  [[ "$password" =~ [A-Z] ]] || return 1
  [[ "$password" =~ [a-z] ]] || return 1
  [[ "$password" =~ [0-9] ]] || return 1
  [[ "$password" =~ [^A-Za-z0-9] ]] || return 1
  return 0
}
password_is_strong "$STALWART_ADMIN_PASSWORD" || fail "STALWART_ADMIN_PASSWORD must be at least 16 characters and include upper/lowercase, a number and a symbol"
password_is_strong "$STALWART_SUBMIT_PASSWORD" || fail "STALWART_SUBMIT_PASSWORD must be at least 16 characters and include upper/lowercase, a number and a symbol"

umask 077
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/sendwise-stalwart.XXXXXX")"
trap 'rm -rf -- "$tmp_dir"' EXIT

auth_file="$tmp_dir/auth.curl"
request_file="$tmp_dir/request.json"
response_file="$tmp_dir/response.json"
session_file="$tmp_dir/session.json"

write_auth() {
  local user="$1"
  local password="$2"
  printf 'user = %s\n' "$(jq -nr --arg credentials "$user:$password" '$credentials | @json')" >"$auth_file"
  chmod 600 "$auth_file"
}

request() {
  local method="$1" args="$2"
  jq -cn \
    --arg method "$method" \
    --argjson args "$args" \
    --arg account "$account_id" \
    '{using:["urn:ietf:params:jmap:core","urn:stalwart:jmap"],
      methodCalls:[[$method, ($args + {accountId:$account}), "sendwise"]]}' >"$request_file"
  curl --max-time 15 -fsS -K "$auth_file" \
    -H 'Content-Type: application/json' \
    --data-binary "@$request_file" "$STALWART_URL/jmap/" -o "$response_file" ||
    fail "Stalwart JMAP request failed ($method)"
  local response_name
  response_name="$(jq -r '.methodResponses[0][0] // empty' "$response_file")"
  if [[ "$response_name" == "error" ]]; then
    local error_type
    error_type="$(jq -r '.methodResponses[0][1].type // "unknown"' "$response_file")"
    local error_description
    error_description="$(jq -r '.methodResponses[0][1].description // ""' "$response_file")"
    if [[ "$error_description" == *"bootstrap mode"* ]]; then
      return 3
    fi
    fail "Stalwart rejected $method ($error_type)"
  fi
}

method_result() {
  local method="$1"
  jq -ce --arg method "$method" \
    '.methodResponses[] | select(.[0] == $method) | .[1]' "$response_file"
}

load_session() {
  local ready=0
  for _ in $(seq 1 60); do
    if curl --max-time 2 -fsS -K "$auth_file" "$STALWART_URL/jmap/session" \
      -o "$session_file" 2>/dev/null; then
      ready=1
      break
    fi
    sleep 1
  done
  [[ "$ready" -eq 1 ]] || fail "could not authenticate to Stalwart management API"
  account_id="$(jq -r '.primaryAccounts["urn:stalwart:jmap"] // empty' "$session_file")"
  [[ -n "$account_id" ]] || fail "Stalwart session did not expose its configuration account"
}

export STALWART_RECOVERY_ADMIN
docker compose up -d stalwart >/dev/null || fail "could not start the local Stalwart service"
write_auth "$STALWART_ADMIN_USERNAME" "$STALWART_ADMIN_PASSWORD"

ready=0
for _ in $(seq 1 60); do
  if curl --max-time 2 -fsS -K "$auth_file" "$STALWART_URL/jmap/session" -o "$session_file" 2>/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
[[ "$ready" -eq 1 ]] || fail "Stalwart management API did not become ready"

account_id="$(jq -r '.primaryAccounts["urn:stalwart:jmap"] // empty' "$session_file")"
[[ -n "$account_id" ]] || fail "Stalwart session did not expose its configuration account"

if request "x:Domain/get" '{"ids":null}'; then
  :
else
  request_status=$?
  [[ "$request_status" -eq 3 ]] || fail "could not inspect Stalwart's configured domains"
  request "x:Bootstrap/set" "$(jq -cn --arg domain "$STALWART_DOMAIN" --arg hostname "$STALWART_HOSTNAME" '{
    update:{singleton:{
      defaultDomain:$domain,
      defaultHostname:$hostname,
      generateDkimKeys:true,
      requestTlsCertificate:false
    }}
  }')"
  bootstrap_result="$(method_result "x:Bootstrap/set")"
  if [[ "$(jq '(.notUpdated // {}) | length' <<<"$bootstrap_result")" -gt 0 ]]; then
    fail "Stalwart first-run configuration was rejected"
  fi
  docker compose restart stalwart >/dev/null || fail "could not restart local Stalwart after bootstrap"
  sleep 2
  write_auth "$STALWART_ADMIN_USERNAME" "$STALWART_ADMIN_PASSWORD"
  load_session
  request "x:Domain/get" '{"ids":null}' || fail "Stalwart remained in bootstrap mode after setup"
fi
domain_response="$(method_result "x:Domain/get")"
domain_id="$(jq -r --arg domain "$STALWART_DOMAIN" \
  '.list[]? | select(.name == $domain) | .id' <<<"$domain_response" | head -n 1)"

if [[ -z "$domain_id" ]]; then
  request "x:Domain/set" "$(jq -cn --arg domain "$STALWART_DOMAIN" '{
    create:{"#sendwise-domain":{
      name:$domain,
      certificateManagement:{"@type":"Manual"},
      dkimManagement:{"@type":"Automatic"},
      dnsManagement:{"@type":"Manual"},
      subAddressing:{"@type":"Enabled"}
    }}
  }')"
  domain_result="$(method_result "x:Domain/set")"
  if [[ "$(jq '.notCreated // {} | length' <<<"$domain_result")" -gt 0 ]]; then
    fail "Stalwart could not create the configured mail domain"
  fi
  request "x:Domain/get" '{"ids":null}'
  domain_response="$(method_result "x:Domain/get")"
  domain_id="$(jq -r --arg domain "$STALWART_DOMAIN" \
    '.list[]? | select(.name == $domain) | .id' <<<"$domain_response" | head -n 1)"
fi
[[ -n "$domain_id" ]] || fail "Stalwart did not return the configured mail domain"

request "x:Account/get" '{"ids":null}'
account_response="$(method_result "x:Account/get")"
submit_id="$(jq -r --arg name "$STALWART_SUBMIT_USERNAME" --arg domain "$domain_id" \
  '.list[]? | select(.name == $name and .domainId == $domain and .["@type"] == "User") | .id' \
  <<<"$account_response" | head -n 1)"
account_value="$(jq -cn \
  --arg name "$STALWART_SUBMIT_USERNAME" \
  --arg domain "$domain_id" \
  --arg password "$STALWART_SUBMIT_PASSWORD" \
  '{("@type"):"User",name:$name,domainId:$domain,credentials:{"0":{("@type"):"Password",secret:$password}}}')"
if [[ -n "$submit_id" ]]; then
  request "x:Account/set" "$(jq -cn --arg id "$submit_id" --argjson value "$account_value" '{update:{($id):$value}}')"
else
  request "x:Account/set" "$(jq -cn --argjson value "$account_value" '{create:{"#sendwise-submission":$value}}')"
fi
account_result="$(method_result "x:Account/set")"
if [[ "$(jq '(.notCreated // {}) + (.notUpdated // {}) | length' <<<"$account_result")" -gt 0 ]]; then
  fail "Stalwart could not provision the submission account"
fi

request "x:NetworkListener/get" '{"ids":null}'
listener_response="$(method_result "x:NetworkListener/get")"
listener_id="$(jq -r '.list[]? | select(.name == "sendwise-submission") | .id' \
  <<<"$listener_response" | head -n 1)"
listener_value='{"name":"sendwise-submission","protocol":"smtp","bind":{"[::]:587":true},"useTls":true,"implicitTls":false}'
if [[ -n "$listener_id" ]]; then
  request "x:NetworkListener/set" "$(jq -cn --arg id "$listener_id" \
    '{update:{($id):{protocol:"smtp",bind:{"[::]:587":true},useTls:true,implicitTls:false}}}')"
else
  request "x:NetworkListener/set" "$(jq -cn --argjson value "$listener_value" '{create:{"#sendwise-submission-listener":$value}}')"
fi
listener_result="$(method_result "x:NetworkListener/set")"
if [[ "$(jq '(.notCreated // {}) + (.notUpdated // {}) | length' <<<"$listener_result")" -gt 0 ]]; then
  fail "Stalwart could not configure authenticated STARTTLS submission on port 587"
fi

request "x:SystemSettings/set" "$(jq -cn --arg hostname "$STALWART_HOSTNAME" --arg domain "$domain_id" \
  '{update:{singleton:{defaultHostname:$hostname,defaultDomainId:$domain}}}')"
settings_result="$(method_result "x:SystemSettings/set")"
if [[ "$(jq '(.notCreated // {}) + (.notUpdated // {}) | length' <<<"$settings_result")" -gt 0 ]]; then
  fail "Stalwart could not set its default hostname and mail domain"
fi

docker compose restart stalwart >/dev/null || fail "could not reload the local Stalwart listener configuration"
load_session
tls_ready=0
for _ in $(seq 1 30); do
  openssl s_client -starttls smtp -connect 127.0.0.1:587 \
    -servername "$STALWART_HOSTNAME" </dev/null >"$tmp_dir/tls.log" 2>&1 || true
  if grep -Eq 'Cipher is [^ (]' "$tmp_dir/tls.log"; then
    tls_ready=1
    break
  fi
  sleep 1
done
if [[ "$tls_ready" -ne 1 ]]; then
  fail "Stalwart port 587 did not complete a STARTTLS handshake"
fi

printf 'Stalwart provisioned locally: domain=%s submission=%s@%s port=587 STARTTLS\n' \
  "$STALWART_DOMAIN" "$STALWART_SUBMIT_USERNAME" "$STALWART_DOMAIN"
