# Production deployment

Target host: `root@1.14.58.29`, installation path `/opt/webscreencapture`.

## Network prerequisites

1. Add `screen.flyingrtx.com A 1.14.58.29`.
2. In the Tencent Cloud security group, allow:
   - TCP/UDP 3479 (STUN/TURN)
   - TCP 5349 (TURNS)
   - UDP 49160-49200 (TURN relay)
   - Existing TCP 80/443 remain in use by nginx.

Port 3478 is intentionally not used because it is already occupied by the Tailscale DERP service.

## Install

Copy the repository to `/opt/webscreencapture`, then run:

```bash
cd /opt/webscreencapture/deploy
chmod +x deploy.sh issue-certificate.sh renew-certificate.sh
./deploy.sh
```

The first run prints the device token and viewer password exactly once. Save them securely. The `.env` file contains only the device-token digest and Argon2id password hash.

Before DNS is available, `deploy.sh` creates a seven-day self-signed bootstrap certificate so nginx and coturn can start. Once DNS resolves, replace it with a trusted certificate:

```bash
./issue-certificate.sh
```

Certificate renewal is checked daily by `webscreencapture-cert-renew.timer`.

The production TURN configuration binds the VM private interface `10.6.0.10` and advertises `1.14.58.29`, which is required for Tencent Cloud public-IP NAT.

## Operations

```bash
docker compose --env-file .env ps
docker compose --env-file .env logs --tail=100 signal coturn
curl -fsS http://127.0.0.1:18091/healthz
```

Media normally travels directly between the Windows agent and viewer. coturn only relays when ICE cannot establish a direct path.
