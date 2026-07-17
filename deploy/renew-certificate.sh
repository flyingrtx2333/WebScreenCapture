#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

if [[ ! -d certbot-data/renewal ]]; then
  exit 0
fi

docker compose --profile cert run --rm certbot renew \
  --webroot -w /var/www/certbot \
  --non-interactive

install -m 644 certbot-data/live/screen.flyingrtx.com/fullchain.pem tls/fullchain.pem
install -g 65534 -m 640 certbot-data/live/screen.flyingrtx.com/privkey.pem tls/privkey.pem
nginx -t
systemctl reload nginx
docker compose --env-file .env restart coturn
