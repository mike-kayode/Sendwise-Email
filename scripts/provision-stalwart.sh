#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

mode=local
dry_run=0
for arg in "$@"; do
  case "$arg" in
    --local) mode=local ;;
    --live) mode=live ;;
    --dry-run) dry_run=1 ;;
    -h|--help)
      printf 'Usage: %s [--local|--live] [--dry-run]\n' "$0"
      exit 0
      ;;
    *) printf 'ERROR: unknown option: %s\n' "$arg" >&2; exit 2 ;;
  esac
done

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

for tool in docker curl jq openssl; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done

if [[ "$mode" == "live" ]]; then
  : "${STALWART_URL:?Set STALWART_URL}"
  : "${STALWART_ADMIN_USERNAME:?Set STALWART_ADMIN_USERNAME}"
  : "${STALWART_ADMIN_PASSWORD:?Set STALWART_ADMIN_PASSWORD}"
  : "${STALWART_HOSTNAME:?Set STALWART_HOSTNAME}"
  : "${STALWART_DOMAIN:?Set STALWART_DOMAIN}"
  : "${STALWART_SUBMIT_PASSWORD:?Set STALWART_SUBMIT_PASSWORD}"
  STALWART_SUBMIT_USERNAME=noreply
  STALWART_CERTS_DIR="${STALWART_CERTS_DIR:-$ROOT/secrets/stalwart/certs}"
  [[ -r "$STALWART_CERTS_DIR/fullchain.pem" ]] || fail "mounted TLS certificate is missing: $STALWART_CERTS_DIR/fullchain.pem"
  [[ -r "$STALWART_CERTS_DIR/privkey.pem" ]] || fail "mounted TLS private key is missing: $STALWART_CERTS_DIR/privkey.pem"
else
  : "${STALWART_DOMAIN:?Set STALWART_DOMAIN}"
  : "${STALWART_SUBMIT_PASSWORD:?Set STALWART_SUBMIT_PASSWORD}"
  : "${STALWART_ADMIN_PASSWORD:?Set STALWART_ADMIN_PASSWORD}"
  STALWART_URL="${STALWART_URL:-http://127.0.0.1:8081}"
  STALWART_HOSTNAME="${STALWART_HOSTNAME:-mail.${STALWART_DOMAIN}}"
  STALWART_ADMIN_USERNAME="${STALWART_ADMIN_USERNAME:-admin}"
  STALWART_SUBMIT_USERNAME="${STALWART_SUBMIT_USERNAME:-noreply}"
fi
STALWART_PORT_587="${STALWART_PORT_587:-587}"
STALWART_PORT_465="${STALWART_PORT_465:-465}"

case "$STALWART_URL" in
  http://localhost:*|http://127.0.0.1:*|https://localhost:*|https://127.0.0.1:*) ;;
  *) fail "STALWART_URL must use localhost or 127.0.0.1; keep the management API private" ;;
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

if [[ "$dry_run" -eq 1 ]]; then
  printf 'DRY RUN: mode=%s Stalwart URL=%s\n' "$mode" "$STALWART_URL"
  printf 'Would set hostname=%s, platform domain=%s, and submission account=noreply@%s.\n' \
    "$STALWART_HOSTNAME" "$STALWART_DOMAIN" "$STALWART_DOMAIN"
  printf 'Would configure authenticated STARTTLS on %s, implicit TLS on %s, a 10-thread outbound queue, and 5 delivery attempts.\n' \
    "$STALWART_PORT_587" "$STALWART_PORT_465"
  if [[ "$mode" == "live" ]]; then
    printf 'Would install the mounted certificate from %s and restart the running Stalwart service.\n' "$STALWART_CERTS_DIR"
  else
    printf 'Would provision the local Compose Stalwart service.\n'
  fi
  printf 'DNS to publish after provisioning:\n'
  printf '  %s MX 10 %s.\n' "$STALWART_DOMAIN" "$STALWART_HOSTNAME"
  printf '  %s A <SERVER_PUBLIC_IP>\n' "$STALWART_HOSTNAME"
  printf '  %s TXT "v=spf1 mx -all"\n' "$STALWART_DOMAIN"
  printf '  <ACTIVE_SELECTOR>._domainkey.%s TXT <DKIM_RECORD_FROM_PROVISION_OUTPUT>\n' "$STALWART_DOMAIN"
  printf '  _dmarc.%s TXT "v=DMARC1; p=none; rua=mailto:dmarc@%s"\n' "$STALWART_DOMAIN" "$STALWART_DOMAIN"
  printf '  Request PTR: <SERVER_PUBLIC_IP> -> %s.\n' "$STALWART_HOSTNAME"
  printf 'Smoke: swaks --server %s --port 587 --tls --auth LOGIN --auth-user noreply@%s --auth-password "$STALWART_SUBMIT_PASSWORD" --from noreply@%s --to <TEST_RECIPIENT>\n' \
    "$STALWART_HOSTNAME" "$STALWART_DOMAIN" "$STALWART_DOMAIN"
  exit 0
fi

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
  local encoded
  encoded="$(STALWART_CURL_USER="$user:$password" jq -nr 'env.STALWART_CURL_USER | @json')"
  printf 'user = %s\n' "$encoded" >"$auth_file"
  chmod 600 "$auth_file"
}

compose() {
  if [[ -n "${STALWART_COMPOSE_PROJECT:-}" ]]; then
    docker compose --project-name "$STALWART_COMPOSE_PROJECT" "$@"
  else
    docker compose "$@"
  fi
}

request() {
  local method="$1" args="$2"
  local args_file="$tmp_dir/args.json"
  printf '%s\n' "$args" >"$args_file"
  jq -cn \
    --arg method "$method" \
    --slurpfile args "$args_file" \
    --arg account "$account_id" \
    '{using:["urn:ietf:params:jmap:core","urn:stalwart:jmap"],
      methodCalls:[[$method, ($args[0] + {accountId:$account}), "sendwise"]]}' >"$request_file"
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

restart_stalwart() {
  compose restart stalwart >/dev/null || fail "could not restart the Stalwart service"
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

if [[ "$mode" == "local" ]]; then
  STALWART_RECOVERY_ADMIN="${STALWART_RECOVERY_ADMIN:-${STALWART_ADMIN_USERNAME}:${STALWART_ADMIN_PASSWORD}}"
  export STALWART_RECOVERY_ADMIN
  compose up -d stalwart >/dev/null || fail "could not start the local Stalwart service"
fi
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
      generateDkimKeys:false,
      requestTlsCertificate:false
    }}
  }')"
  bootstrap_result="$(method_result "x:Bootstrap/set")"
  if [[ "$(jq '(.notUpdated // {}) | length' <<<"$bootstrap_result")" -gt 0 ]]; then
    fail "Stalwart first-run configuration was rejected"
  fi
  restart_stalwart
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
      dkimManagement:{"@type":"Automatic","algorithms":{"Dkim1RsaSha256":true}},
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
other_domain_ids=()
while IFS= read -r other_domain_id; do
  other_domain_ids+=("$other_domain_id")
done < <(jq -r --arg domain "$STALWART_DOMAIN" '.list[]? | select(.name != $domain) | .id' <<<"$domain_response")
if ((${#other_domain_ids[@]} > 0)); then
  other_ids_json="$(printf '%s\n' "${other_domain_ids[@]}" | jq -R . | jq -s .)"
  request "x:Domain/set" "$(jq -cn --argjson ids "$other_ids_json" '{delete:$ids}')"
fi
request "x:DkimSignature/get" '{"ids":null}'
dkim_response="$(method_result "x:DkimSignature/get")"
stale_dkim_ids=()
while IFS= read -r stale_id; do
  stale_dkim_ids+=("$stale_id")
done < <(jq -r '.list[]? | .id' <<<"$dkim_response")
if ((${#stale_dkim_ids[@]} > 0)); then
  stale_ids_json="$(printf '%s\n' "${stale_dkim_ids[@]}" | jq -R . | jq -s .)"
  request "x:DkimSignature/set" "$(jq -cn --argjson ids "$stale_ids_json" '{delete:$ids}')"
fi
request "x:Domain/set" "$(jq -cn --arg id "$domain_id" '{
  update:{($id):{
    certificateManagement:{"@type":"Manual"},
    dkimManagement:{"@type":"Automatic","algorithms":{"Dkim1RsaSha256":true}},
    dnsManagement:{"@type":"Manual"}
  }}
}')"
domain_result="$(method_result "x:Domain/set")"
if [[ "$(jq '(.notUpdated // {}) | length' <<<"$domain_result")" -gt 0 ]]; then
  fail "Stalwart could not enable platform-domain DKIM management"
fi

request "x:Account/get" '{"ids":null}'
account_response="$(method_result "x:Account/get")"
submit_id="$(jq -r --arg name "$STALWART_SUBMIT_USERNAME" --arg domain "$domain_id" \
  '.list[]? | select(.name == $name and .domainId == $domain and .["@type"] == "User") | .id' \
  <<<"$account_response" | head -n 1)"
account_value="$(STALWART_SUBMIT_PASSWORD="$STALWART_SUBMIT_PASSWORD" jq -cn \
  --arg name "$STALWART_SUBMIT_USERNAME" \
  --arg domain "$domain_id" \
  '{("@type"):"User",name:$name,domainId:$domain,credentials:{"0":{("@type"):"Password",secret:env.STALWART_SUBMIT_PASSWORD}}}')"
printf '%s\n' "$account_value" >"$tmp_dir/account.json"
if [[ -n "$submit_id" ]]; then
  request "x:Account/set" "$(jq -cn --arg id "$submit_id" --slurpfile value "$tmp_dir/account.json" '{update:{($id):$value[0]}}')"
else
  request "x:Account/set" "$(jq -cn --slurpfile value "$tmp_dir/account.json" '{create:{"#sendwise-submission":$value[0]}}')"
fi
account_result="$(method_result "x:Account/set")"
if [[ "$(jq '(.notCreated // {}) + (.notUpdated // {}) | length' <<<"$account_result")" -gt 0 ]]; then
  fail "Stalwart could not provision the submission account"
fi

request "x:MtaStageAuth/get" '{"ids":["singleton"]}'
auth_result="$(method_result "x:MtaStageAuth/get")"
[[ "$(jq -r '.list[0].id // empty' <<<"$auth_result")" == "singleton" ]] ||
  fail "Stalwart has no singleton SMTP authentication policy"
submission_address="$STALWART_SUBMIT_USERNAME@$STALWART_DOMAIN"
request "x:MtaStageAuth/set" "$(jq -cn --arg sender "$submission_address" '
  {update:{singleton:{
    mustMatchSender:{
      match:{"0":{
        "if":("authenticated_as == " + ($sender | tojson)),
        "then":"false"
      }},
      "else":"true"
    }
  }}}
')"
auth_result="$(method_result "x:MtaStageAuth/set")"
if [[ "$(jq '(.notCreated // {}) + (.notUpdated // {}) | length' <<<"$auth_result")" -gt 0 ]]; then
  auth_error="$(jq -c '(.notCreated // {}) + (.notUpdated // {})' <<<"$auth_result")"
  fail "Stalwart could not scope sender authorization to the submission account ($auth_error)"
fi

configure_listener() {
  local name="$1" port="$2" implicit_tls="$3"
  request "x:NetworkListener/get" '{"ids":null}'
  local listener_response listener_id listener_value listener_result
  listener_response="$(method_result "x:NetworkListener/get")"
  listener_id="$(jq -r --arg name "$name" '.list[]? | select(.name == $name) | .id' \
    <<<"$listener_response" | head -n 1)"
  listener_value="$(jq -cn --arg name "$name" --arg port "$port" --argjson implicit "$implicit_tls" \
    '{name:$name,protocol:"smtp",bind:{("[::]:" + $port):true},useTls:true,implicitTls:$implicit}')"
  if [[ -n "$listener_id" ]]; then
    request "x:NetworkListener/set" "$(jq -cn --arg id "$listener_id" --arg port "$port" --argjson implicit "$implicit_tls" \
      '{update:{($id):{protocol:"smtp",bind:{("[::]:" + $port):true},useTls:true,implicitTls:$implicit}}}')"
  else
    printf '%s\n' "$listener_value" >"$tmp_dir/listener.json"
    request "x:NetworkListener/set" "$(jq -cn --slurpfile value "$tmp_dir/listener.json" \
      '{create:{"#sendwise-listener":$value[0]}}')"
  fi
  listener_result="$(method_result "x:NetworkListener/set")"
  if [[ "$(jq '(.notCreated // {}) + (.notUpdated // {}) | length' <<<"$listener_result")" -gt 0 ]]; then
    fail "Stalwart could not configure authenticated SMTP submission on port $port"
  fi
}

configure_listener "sendwise-submission" "587" false
configure_listener "sendwise-implicit" "465" true

if [[ "$mode" == "live" ]]; then
  openssl x509 -in "$STALWART_CERTS_DIR/fullchain.pem" -noout -text 2>/dev/null |
    grep -Fqi "DNS:$STALWART_HOSTNAME" ||
    fail "mounted TLS certificate does not cover STALWART_HOSTNAME"
  openssl x509 -in "$STALWART_CERTS_DIR/fullchain.pem" -noout -checkend 0 >/dev/null 2>&1 ||
    fail "mounted TLS certificate is expired"
  cert_public_key="$(openssl x509 -in "$STALWART_CERTS_DIR/fullchain.pem" -pubkey -noout | openssl pkey -pubin -outform DER 2>/dev/null | openssl dgst -sha256)"
  key_public_key="$(openssl pkey -in "$STALWART_CERTS_DIR/privkey.pem" -pubout -outform DER 2>/dev/null | openssl dgst -sha256)" ||
    fail "could not read mounted TLS private key"
  [[ "$cert_public_key" == "$key_public_key" ]] || fail "mounted TLS certificate and private key do not match"

  request "x:SystemSettings/get" '{"ids":["singleton"]}'
  settings_response="$(method_result "x:SystemSettings/get")"
  certificate_id="$(jq -r '.list[0].defaultCertificateId // empty' <<<"$settings_response")"
  certificate_value="$(jq -cn '{
    certificate:{"@type":"File",filePath:"/etc/stalwart/certs/fullchain.pem"},
    privateKey:{"@type":"File",filePath:"/etc/stalwart/certs/privkey.pem"}
  }')"
  printf '%s\n' "$certificate_value" >"$tmp_dir/certificate.json"
  if [[ -n "$certificate_id" ]]; then
    request "x:Certificate/set" "$(jq -cn --arg id "$certificate_id" --slurpfile value "$tmp_dir/certificate.json" \
      '{update:{($id):$value[0]}}')"
  else
    request "x:Certificate/set" "$(jq -cn --slurpfile value "$tmp_dir/certificate.json" \
      '{create:{"#sendwise-certificate":$value[0]}}')"
    certificate_result="$(method_result "x:Certificate/set")"
    certificate_id="$(jq -r '.created["#sendwise-certificate"].id // empty' <<<"$certificate_result")"
    if [[ -z "$certificate_id" ]]; then
      certificate_error="$(jq -r '.notCreated["#sendwise-certificate"].description // .notCreated["#sendwise-certificate"].type // "unknown error"' <<<"$certificate_result")"
      fail "Stalwart rejected the mounted TLS certificate ($certificate_error)"
    fi
  fi
  certificate_result="$(method_result "x:Certificate/set")"
  if [[ "$(jq '(.notCreated // {}) + (.notUpdated // {}) | length' <<<"$certificate_result")" -gt 0 ]]; then
    fail "Stalwart could not install the mounted TLS certificate"
  fi
else
  certificate_id=""
fi

request "x:MtaDeliverySchedule/get" '{"ids":null}'
schedule_response="$(method_result "x:MtaDeliverySchedule/get")"
schedule_id="$(jq -r '.list[]? | select(.name == "remote") | .id' <<<"$schedule_response" | head -n 1)"
queue_id="$(jq -r '.list[]? | select(.name == "remote") | .queueId' <<<"$schedule_response" | head -n 1)"
[[ -n "$schedule_id" && -n "$queue_id" ]] || fail "Stalwart has no remote delivery schedule and queue"
request "x:MtaVirtualQueue/set" "$(jq -cn --arg id "$queue_id" \
  '{update:{($id):{threadsPerNode:10}}}')"
queue_result="$(method_result "x:MtaVirtualQueue/set")"
if [[ "$(jq '(.notUpdated // {}) | length' <<<"$queue_result")" -gt 0 ]]; then
  queue_error="$(jq -r '.notUpdated[]?.description // .notUpdated[]?.type // "unknown error"' <<<"$queue_result")"
  fail "Stalwart could not set outbound queue concurrency ($queue_error)"
fi
request "x:MtaDeliverySchedule/set" "$(jq -cn --arg id "$schedule_id" '{
  update:{($id):{
    expiry:{"@type":"Attempts","maxAttempts":5},
    retry:{"@type":"Custom","intervals":{
      "0":{duration:300000},"1":{duration:900000},"2":{duration:3600000},
      "3":{duration:21600000},"4":{duration:86400000}
    }}
  }}
}')"
schedule_result="$(method_result "x:MtaDeliverySchedule/set")"
if [[ "$(jq '(.notUpdated // {}) | length' <<<"$schedule_result")" -gt 0 ]]; then
  schedule_error="$(jq -c '.notUpdated' <<<"$schedule_result")"
  fail "Stalwart could not set outbound retry and expiry limits ($schedule_error)"
fi

request "x:SystemSettings/set" "$(jq -cn --arg hostname "$STALWART_HOSTNAME" --arg domain "$domain_id" \
  --arg certificate "$certificate_id" \
  '{update:{singleton:({defaultHostname:$hostname,defaultDomainId:$domain} + (if $certificate == "" then {} else {defaultCertificateId:$certificate} end))}}')"
settings_result="$(method_result "x:SystemSettings/set")"
if [[ "$(jq '(.notCreated // {}) + (.notUpdated // {}) | length' <<<"$settings_result")" -gt 0 ]]; then
  fail "Stalwart could not set its default hostname and mail domain"
fi

restart_stalwart
load_session
tls_ready=0
for _ in $(seq 1 30); do
  openssl s_client -starttls smtp -connect "127.0.0.1:$STALWART_PORT_587" \
    -servername "$STALWART_HOSTNAME" </dev/null >"$tmp_dir/tls.log" 2>&1 || true
  if grep -Eq 'Cipher is [^ (]|Ciphersuite:' "$tmp_dir/tls.log"; then
    tls_ready=1
    break
  fi
  sleep 1
done
if [[ "$tls_ready" -ne 1 ]]; then
  fail "Stalwart port 587 did not complete a STARTTLS handshake"
fi

tls_ready=0
for _ in $(seq 1 30); do
  openssl s_client -connect "127.0.0.1:$STALWART_PORT_465" -servername "$STALWART_HOSTNAME" \
    </dev/null >"$tmp_dir/implicit-tls.log" 2>&1 || true
  if grep -Eq 'Cipher is [^ (]|Ciphersuite:' "$tmp_dir/implicit-tls.log"; then
    tls_ready=1
    break
  fi
  sleep 1
done
[[ "$tls_ready" -eq 1 ]] || fail "Stalwart port 465 did not complete an implicit TLS handshake"

request "x:DkimSignature/get" '{"ids":null}'
dkim_response="$(method_result "x:DkimSignature/get")"
dkim_count="$(jq --arg domain "$domain_id" \
  '[.list[]? | select(.domainId == $domain and .stage == "active" and .["@type"] == "Dkim1RsaSha256")] | length' \
  <<<"$dkim_response")"
[[ "$dkim_count" -gt 0 ]] || fail "Stalwart has no active RSA DKIM key for the platform domain"

printf 'Stalwart v1.0.3 provisioned: domain=%s submission=noreply@%s; 587 STARTTLS; 465 implicit TLS\n' \
  "$STALWART_DOMAIN" "$STALWART_DOMAIN"
printf 'DNS records to publish:\n'
printf '  %s MX 10 %s.\n' "$STALWART_DOMAIN" "$STALWART_HOSTNAME"
printf '  %s A <SERVER_PUBLIC_IP>\n' "$STALWART_HOSTNAME"
printf '  %s TXT "v=spf1 mx -all"\n' "$STALWART_DOMAIN"
jq -r --arg domain "$domain_id" --arg name "$STALWART_DOMAIN" \
  '.list[]? | select(.domainId == $domain and .stage == "active" and .["@type"] == "Dkim1RsaSha256") |
   "  \(.selector)._domainkey.\($name) TXT \"v=DKIM1; k=rsa; p=\(.publicKey)\""' \
  <<<"$dkim_response"
printf '  _dmarc.%s TXT "v=DMARC1; p=none; rua=mailto:dmarc@%s"\n' "$STALWART_DOMAIN" "$STALWART_DOMAIN"
printf '  Request PTR: <SERVER_PUBLIC_IP> -> %s.\n' "$STALWART_HOSTNAME"
printf 'Smoke: swaks --server %s --port 587 --tls --auth LOGIN --auth-user noreply@%s --auth-password "$STALWART_SUBMIT_PASSWORD" --from noreply@%s --to <TEST_RECIPIENT>\n' \
  "$STALWART_HOSTNAME" "$STALWART_DOMAIN" "$STALWART_DOMAIN"
