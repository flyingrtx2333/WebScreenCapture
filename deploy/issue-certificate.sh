#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "issue-certificate.sh must run as root" >&2
  exit 1
fi

cd "$(dirname "${BASH_SOURCE[0]}")"

if ! getent ahostsv4 screen.flyingrtx.com | awk '{print $1}' | grep -Fxq '1.14.58.29'; then
  echo "screen.flyingrtx.com does not resolve to 1.14.58.29 yet" >&2
  exit 2
fi

docker compose --profile cert run --rm certbot certonly \
  --webroot -w /var/www/certbot \
  --cert-name screen.flyingrtx.com \
  --domain screen.flyingrtx.com \
  --non-interactive --agree-tos --register-unsafely-without-email

install -m 644 certbot-data/live/screen.flyingrtx.com/fullchain.pem tls/fullchain.pem
install -g 65534 -m 640 certbot-data/live/screen.flyingrtx.com/privkey.pem tls/privkey.pem
nginx -t
systemctl reload nginx
docker compose --env-file .env restart coturn

echo "Trusted certificate installed for screen.flyingrtx.com"
