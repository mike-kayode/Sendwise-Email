#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

log() { printf '%s\n' "$*"; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: scripts/bootstrap-stalwart-live.sh [--dry-run]

Bootstraps a fresh Stalwart instance on localhost and configures the default
mail domain plus a submission account used by Sendwise.

Required env vars:
  STALWART_URL               http://127.0.0.1:8081
  STALWART_DOMAIN            example.com
  STALWART_HOSTNAME          mail.example.com
  STALWART_ADMIN_USERNAME    admin@example.com
  STALWART_ADMIN_PASSWORD    <permanent admin password>
  STALWART_SUBMIT_PASSWORD   <submission account password>

Optional env vars:
  STALWART_BOOTSTRAP_USER    admin (temporary first-run admin username)
  STALWART_BOOTSTRAP_PASSWORD <temporary bootstrap password from docker logs>
  STALWART_SUBMIT_USERNAME   noreply (default)
  STALWART_WAIT_SECONDS      60

The script is intentionally loopback-only. Do not run it against a remote host
unless you have explicitly validated the host and its environment.
EOF
}

DRY_RUN=0
if [[ ${1:-} == "--dry-run" ]]; then
  DRY_RUN=1
elif [[ ${1:-} != "" ]]; then
  usage
  exit 2
fi

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required. Install it and retry."
}

install_stalwart_cli() {
  local base="$HOME/.local/bin"
  mkdir -p "$base"
  local os arch url
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) arch="x86_64" ;;
    arm64|aarch64) arch="aarch64" ;;
    *) fail "Unsupported architecture: $arch" ;;
  esac
  case "$os" in
    darwin) os="apple-darwin" ;;
    linux) os="unknown-linux-gnu" ;;
    *) fail "Unsupported OS: $os" ;;
  esac
  local archive="stalwart-cli-${arch}-${os}.tar.xz"
  local extracted="stalwart-cli-${arch}-${os}"
  url="https://github.com/stalwartlabs/cli/releases/latest/download/${archive}"

  log "Installing Stalwart CLI to $base"
  curl --proto '=https' --tlsv1.2 -fsSL "$url" -o "/tmp/${archive}"
  tar -xf "/tmp/${archive}" -C /tmp
  install -m 0755 "/tmp/${extracted}/stalwart-cli" "$base/stalwart-cli"
  export PATH="$base:$PATH"
}

if ! command -v stalwart-cli >/dev/null 2>&1; then
  install_stalwart_cli
fi

if [[ ${1:-} == "--help" || ${1:-} == "-h" ]]; then
  usage
  exit 0
fi

: "${STALWART_URL:?Set STALWART_URL (for example http://127.0.0.1:8081)}"
: "${STALWART_DOMAIN:?Set STALWART_DOMAIN (for example example.com)}"
: "${STALWART_HOSTNAME:?Set STALWART_HOSTNAME (for example mail.example.com)}"
: "${STALWART_ADMIN_USERNAME:?Set STALWART_ADMIN_USERNAME (for example admin@example.com)}"
: "${STALWART_ADMIN_PASSWORD:?Set STALWART_ADMIN_PASSWORD}"
: "${STALWART_SUBMIT_PASSWORD:?Set STALWART_SUBMIT_PASSWORD}"

case "$STALWART_URL" in
  http://localhost:*|http://127.0.0.1:*|https://localhost:*|https://127.0.0.1:*) ;;
  *) fail "STALWART_URL must use localhost or 127.0.0.1; this script is loopback-only by design." ;;
 esac

need_cmd docker
need_cmd curl
need_cmd jq

STALWART_BOOTSTRAP_USER="${STALWART_BOOTSTRAP_USER:-admin}"
STALWART_SUBMIT_USERNAME="${STALWART_SUBMIT_USERNAME:-noreply}"
STALWART_WAIT_SECONDS="${STALWART_WAIT_SECONDS:-60}"

if [[ "$DRY_RUN" -eq 1 ]]; then
  cat <<EOF
DRY RUN
- Ensure docker compose stack is running: docker compose -f docker-compose.yml -f docker-compose.microservices.yml up -d stalwart
- Wait for Stalwart at $STALWART_URL
- If the bootstrap admin is still active, use $STALWART_BOOTSTRAP_USER with STALWART_BOOTSTRAP_PASSWORD to run:
  stalwart-cli --insecure update Bootstrap \
    --field defaultDomain=$STALWART_DOMAIN \
    --field defaultHostname=$STALWART_HOSTNAME \
    --field generateDkimKeys=true \
    --field requestTlsCertificate=false
- Then provision domain, account, and listener:
  Domain: $STALWART_DOMAIN
  Account: $STALWART_SUBMIT_USERNAME@$STALWART_DOMAIN
  Listener: smtp STARTTLS on 587
- Final admin account used for follow-up commands: $STALWART_ADMIN_USERNAME
EOF
  exit 0
fi

log "Ensuring the Stalwart service is up"
docker compose -f docker-compose.yml -f docker-compose.microservices.yml up -d stalwart

log "Waiting for Stalwart admin interface on $STALWART_URL"
for _ in $(seq 1 "$STALWART_WAIT_SECONDS"); do
  if curl -fsS "$STALWART_URL" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! curl -fsS "$STALWART_URL" >/dev/null 2>&1; then
  fail "Stalwart did not become reachable on $STALWART_URL within ${STALWART_WAIT_SECONDS}s. Check docker logs and the server state."
fi

STALWART_BOOTSTRAP_PASSWORD="${STALWART_BOOTSTRAP_PASSWORD:-}"
if [[ -z "$STALWART_BOOTSTRAP_PASSWORD" ]]; then
  log "No STALWART_BOOTSTRAP_PASSWORD set; attempting to read the temporary bootstrap password from the container logs."
  STALWART_BOOTSTRAP_PASSWORD="$(docker logs --tail 80 email-platform-stalwart-1 2>/dev/null | sed -n 's/.*password: \([^[:space:]]*\).*/\1/p' | tail -n 1 || true)"
fi

if [[ -n "$STALWART_BOOTSTRAP_PASSWORD" ]]; then
  log "Bootstrap password detected; updating the Bootstrap singleton."
  STALWART_USER="$STALWART_BOOTSTRAP_USER" STALWART_PASSWORD="$STALWART_BOOTSTRAP_PASSWORD" stalwart-cli --insecure update Bootstrap \
    --field defaultDomain="$STALWART_DOMAIN" \
    --field defaultHostname="$STALWART_HOSTNAME" \
    --field generateDkimKeys=true \
    --field requestTlsCertificate=false >/tmp/stalwart-bootstrap.log 2>&1 || {
      cat /tmp/stalwart-bootstrap.log >&2
      fail "Bootstrap failed. Check the temporary admin password and the Stalwart logs."
  }
  log "Bootstrap complete."
else
  log "No bootstrap password was available; assuming the instance is already bootstrapped."
fi

# Verify the configured admin login works before provisioning the domain.
if ! STALWART_USER="$STALWART_ADMIN_USERNAME" STALWART_PASSWORD="$STALWART_ADMIN_PASSWORD" stalwart-cli --insecure query Account >/tmp/stalwart-admin-check.log 2>&1; then
  cat /tmp/stalwart-admin-check.log >&2 || true
  fail "The configured admin username/password could not authenticate. Confirm the permanent admin account, then retry."
fi

plan="$(mktemp "${TMPDIR:-/tmp}/sendwise-stalwart-plan.XXXXXX")"
trap 'rm -f -- "$plan"' EXIT

jq -cn \
  --arg domain "$STALWART_DOMAIN" \
  --arg submit_name "$STALWART_SUBMIT_USERNAME" \
  --arg submit_password "$STALWART_SUBMIT_PASSWORD" \
  '{
    "@type":"upsert",
    "object":"Domain",
    "matchOn":["name"],
    "value":{"sendwise-domain":{
      "name":$domain,
      "certificateManagement":{"@type":"Manual"},
      "dkimManagement":{"@type":"Automatic"},
      "dnsManagement":{"@type":"Manual"},
      "subAddressing":{"@type":"Enabled"}
    }}
  }' >"$plan"

jq -cn \
  --arg domain "$STALWART_DOMAIN" \
  --arg submit_name "$STALWART_SUBMIT_USERNAME" \
  --arg submit_password "$STALWART_SUBMIT_PASSWORD" \
  '{
    "@type":"upsert",
    "object":"Account",
    "matchOn":["name","domainId"],
    "value":{"sendwise-submission":{
      "@type":"User",
      "name":$submit_name,
      "domainId": "#sendwise-domain",
      "credentials":{"0":{"@type":"Password","secret":$submit_password}}
    }}
  }' >>"$plan"

jq -cn \
  --arg hostname "$STALWART_HOSTNAME" \
  '{
    "@type":"upsert",
    "object":"NetworkListener",
    "matchOn":["name"],
    "value":{"sendwise-submission-listener":{
      "name":"sendwise-submission",
      "protocol":"smtp",
      "bind":{"[::]:587":true},
      "useTls":true,
      "implicitTls":false
    }}
  }' >>"$plan"

jq -cn \
  --arg hostname "$STALWART_HOSTNAME" \
  --arg domain "$STALWART_DOMAIN" \
  '{
    "@type":"update",
    "object":"SystemSettings",
    "value":{"defaultHostname":$hostname, "defaultDomainId":"#sendwise-domain"}
  }' >>"$plan"

log "Applying the Stalwart plan for $STALWART_DOMAIN"
STALWART_USER="$STALWART_ADMIN_USERNAME" STALWART_PASSWORD="$STALWART_ADMIN_PASSWORD" stalwart-cli --insecure apply --file "$plan" >/tmp/stalwart-provision.log 2>&1 || {
  cat /tmp/stalwart-provision.log >&2
  fail "Provisioning failed. Review the domain/account/listener plan and the Stalwart logs."
}

docker compose -f docker-compose.yml -f docker-compose.microservices.yml restart stalwart >/dev/null

log "Stalwart bootstrap + provisioning complete."
log "Configured domain: $STALWART_DOMAIN"
log "Submission account: ${STALWART_SUBMIT_USERNAME}@${STALWART_DOMAIN}"
log "SMTP listener: 587 STARTTLS"
log "Credentials remain on the host and should be stored in your secret manager or .env file."
