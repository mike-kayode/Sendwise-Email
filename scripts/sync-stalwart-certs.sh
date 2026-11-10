#!/usr/bin/env bash
# Keep Stalwart's mounted TLS certificate in sync with the Let's Encrypt
# certificate Caddy obtains and renews for the Stalwart host
# (mail.pipsleague.com). Stalwart reads fixed filenames from
# secrets/stalwart/certs, so a Caddy renewal has to be copied across and the
# container restarted to pick it up.
#
# Intended to run daily from root's crontab on the deployment host:
#   17 3 * * * /opt/email-platform/scripts/sync-stalwart-certs.sh >> /var/log/stalwart-cert-sync.log 2>&1
set -euo pipefail

PROJECT="${STALWART_CADDY_VOLUME:-email-platform_caddy-data}"
HOSTNAME="${STALWART_HOSTNAME:-mail.pipsleague.com}"
DEST="${STALWART_CERTS_DIR:-/opt/email-platform/secrets/stalwart/certs}"
COMPOSE_DIR="${STALWART_COMPOSE_DIR:-/opt/email-platform}"

cert_root="$(docker volume inspect "$PROJECT" --format '{{ .Mountpoint }}' 2>/dev/null)/caddy/certificates"
src_dir="$(find "$cert_root" -maxdepth 2 -type d -name "$HOSTNAME" 2>/dev/null | head -n 1 || true)"
if [ -z "$src_dir" ] || [ ! -f "$src_dir/$HOSTNAME.crt" ]; then
  echo "no Caddy certificate for $HOSTNAME yet"
  exit 0
fi

mkdir -p "$DEST"
changed=0
if ! cmp -s "$src_dir/$HOSTNAME.crt" "$DEST/fullchain.pem" 2>/dev/null; then
  cp "$src_dir/$HOSTNAME.crt" "$DEST/fullchain.pem"
  changed=1
fi
if ! cmp -s "$src_dir/$HOSTNAME.key" "$DEST/privkey.pem" 2>/dev/null; then
  cp "$src_dir/$HOSTNAME.key" "$DEST/privkey.pem"
  changed=1
fi

# The Stalwart container runs as uid/gid 2000 and mounts this directory read-only.
chown 2000:2000 "$DEST/fullchain.pem" "$DEST/privkey.pem"
chmod 640 "$DEST/fullchain.pem" "$DEST/privkey.pem"

if [ "$changed" -eq 1 ]; then
  echo "certificate material changed; restarting stalwart"
  cd "$COMPOSE_DIR"
  docker compose -f docker-compose.yml -f docker-compose.microservices.yml restart stalwart
else
  echo "certificate unchanged"
fi
