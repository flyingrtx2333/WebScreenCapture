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

mkdir -p tls acme-webroot certbot-data
chown root:65534 tls
chmod 750 tls
chmod 700 certbot-data

if [[ "${SKIP_IMAGE_BUILD:-0}" == "1" ]]; then
  docker image inspect webscreencapture-signal:1.0.0 >/dev/null || {
    echo "webscreencapture-signal:1.0.0 is not loaded" >&2
    exit 1
  }
else
  docker build -t webscreencapture-signal:1.0.0 "${BASE_DIR}/server"
fi

if [[ ! -f .env ]]; then
  DEVICE_TOKEN="$(openssl rand -hex 32)"
  VIEWER_PASSWORD="$(openssl rand -hex 12)"
  DEVICE_TOKEN_SHA256="$(printf '%s' "${DEVICE_TOKEN}" | sha256sum | awk '{print $1}')"
  SESSION_SECRET="$(openssl rand -base64 48 | tr -d '\n')"
  TURN_SHARED_SECRET="$(openssl rand -hex 32)"
  VIEWER_PASSWORD_HASH="$(printf '%s\n' "${VIEWER_PASSWORD}" | docker run --rm -i --entrypoint /wscctl webscreencapture-signal:1.0.0 hash-password)"
  VIEWER_PASSWORD_HASH_B64="$(printf '%s' "${VIEWER_PASSWORD_HASH}" | openssl base64 -A)"

  {
    printf 'APP_ADDR=:8080\n'
    printf 'PUBLIC_URL=https://screen.flyingrtx.com\n'
    printf 'SECURE_COOKIES=true\n'
    printf 'DEVICE_TOKEN_SHA256=%s\n' "${DEVICE_TOKEN_SHA256}"
    printf 'VIEWER_PASSWORD_HASH_B64=%s\n' "${VIEWER_PASSWORD_HASH_B64}"
    printf 'SESSION_SECRET=%s\n' "${SESSION_SECRET}"
    printf 'TURN_SHARED_SECRET=%s\n' "${TURN_SHARED_SECRET}"
    printf 'TURN_HOST=screen.flyingrtx.com\n'
    printf 'TURN_PORT=3479\n'
    printf 'TURNS_PORT=5349\n'
  } > .env
  chmod 600 .env

  echo "INITIAL_CREDENTIALS_BEGIN"
  echo "DEVICE_TOKEN=${DEVICE_TOKEN}"
  echo "VIEWER_PASSWORD=${VIEWER_PASSWORD}"
  echo "INITIAL_CREDENTIALS_END"
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
