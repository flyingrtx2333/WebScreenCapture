#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "deploy.sh must run as root" >&2
  exit 1
fi

BASE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY_DIR="${BASE_DIR}/deploy"
cd "${DEPLOY_DIR}"

for command in docker openssl nginx sha256sum; do
  command -v "${command}" >/dev/null || { echo "missing command: ${command}" >&2; exit 1; }
done
docker compose version >/dev/null

mkdir -p tls acme-webroot certbot-data data
chown root:65534 tls
chown 65532:65532 data
chmod 750 tls
chmod 700 data
chmod 700 certbot-data

if [[ "${SKIP_IMAGE_BUILD:-0}" == "1" ]]; then
  docker image inspect webscreencapture-signal:1.1.0 >/dev/null || {
    echo "webscreencapture-signal:1.1.0 is not loaded" >&2
    exit 1
  }
else
  docker build -t webscreencapture-signal:1.1.0 "${BASE_DIR}/server"
fi

if [[ ! -f .env ]]; then
  ACCESS_TOKEN="$(openssl rand -hex 32)"
  ACCESS_TOKEN_SHA256="$(printf '%s' "${ACCESS_TOKEN}" | sha256sum | awk '{print $1}')"
  SESSION_SECRET="$(openssl rand -base64 48 | tr -d '\n')"
  TURN_SHARED_SECRET="$(openssl rand -hex 32)"

  {
    printf 'APP_ADDR=:8080\n'
    printf 'PUBLIC_URL=https://screen.flyingrtx.com\n'
    printf 'SECURE_COOKIES=true\n'
    printf 'ACCESS_TOKEN_SHA256=%s\n' "${ACCESS_TOKEN_SHA256}"
    printf 'ACCESS_TOKEN_FILE=/data/access-token.sha256\n'
    printf 'SESSION_SECRET=%s\n' "${SESSION_SECRET}"
    printf 'TURN_SHARED_SECRET=%s\n' "${TURN_SHARED_SECRET}"
    printf 'TURN_HOST=screen.flyingrtx.com\n'
    printf 'TURN_PORT=3479\n'
    printf 'TURNS_PORT=5349\n'
  } > .env
  chmod 600 .env

  echo "INITIAL_ACCESS_TOKEN_BEGIN"
  echo "ACCESS_TOKEN=${ACCESS_TOKEN}"
  echo "INITIAL_ACCESS_TOKEN_END"
else
  if ! grep -q '^ACCESS_TOKEN_SHA256=' .env; then
    LEGACY_TOKEN_SHA256="$(sed -n 's/^DEVICE_TOKEN_SHA256=//p' .env | head -n 1)"
    if [[ ! "${LEGACY_TOKEN_SHA256}" =~ ^[0-9a-fA-F]{64}$ ]]; then
      echo "existing .env has no valid DEVICE_TOKEN_SHA256 to migrate" >&2
      exit 1
    fi
    grep -Ev '^(DEVICE_TOKEN_SHA256|VIEWER_PASSWORD_HASH|VIEWER_PASSWORD_HASH_B64|ACCESS_TOKEN_FILE)=' .env > .env.migrated
    printf 'ACCESS_TOKEN_SHA256=%s\n' "${LEGACY_TOKEN_SHA256}" >> .env.migrated
    printf 'ACCESS_TOKEN_FILE=/data/access-token.sha256\n' >> .env.migrated
    mv .env.migrated .env
    echo "Migrated the existing device token to the shared access token."
  elif ! grep -q '^ACCESS_TOKEN_FILE=' .env; then
    printf 'ACCESS_TOKEN_FILE=/data/access-token.sha256\n' >> .env
  fi
  chmod 600 .env
fi

if [[ ! -s tls/fullchain.pem || ! -s tls/privkey.pem ]]; then
  openssl req -x509 -nodes -newkey rsa:2048 -days 7 \
    -keyout tls/privkey.pem \
    -out tls/fullchain.pem \
    -subj "/CN=screen.flyingrtx.com" \
    -addext "subjectAltName=DNS:screen.flyingrtx.com"
  chmod 600 tls/privkey.pem
  chmod 644 tls/fullchain.pem
fi
chown root:65534 tls/privkey.pem
chmod 640 tls/privkey.pem

install -m 644 nginx/screen.flyingrtx.com.conf /www/server/panel/vhost/nginx/screen.flyingrtx.com.conf
nginx -t
systemctl reload nginx

docker compose --env-file .env up -d signal coturn

install -m 644 systemd/webscreencapture-cert-renew.service /etc/systemd/system/webscreencapture-cert-renew.service
install -m 644 systemd/webscreencapture-cert-renew.timer /etc/systemd/system/webscreencapture-cert-renew.timer
systemctl daemon-reload
systemctl enable --now webscreencapture-cert-renew.timer

for attempt in {1..20}; do
  if curl -fsS http://127.0.0.1:18091/healthz >/dev/null; then
    break
  fi
  sleep 1
done
curl -fsS http://127.0.0.1:18091/healthz
docker compose --env-file .env ps
