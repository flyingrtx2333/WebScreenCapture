# WebScreenCapture

Low-latency Windows screen sharing over peer-to-peer WebRTC, with a small Go signaling service and coturn fallback.

## Quick start

1. Open `https://screen.flyingrtx.com` and sign in with the initial access token printed during deployment.
2. Use **生成 Token** whenever you need a new token, then copy the displayed value.
3. Run `artifacts/release/WebScreenCapture.exe`, keep the default server URL, and enter that same access token.
4. Click **开始捕获**, then choose a monitor in the Windows system picker. Future web logins use the same token.

Closing or minimizing the agent window keeps it running in the system tray. Signaling reconnects preserve the selected screen whenever the browser capture track remains valid.

## Layout

- `client/` — .NET 8 WPF + WebView2 single-file Windows agent.
- `server/` — Go authentication, signaling, and embedded viewer/agent web UI.
- `deploy/` — Docker Compose, coturn, nginx, and certificate renewal assets.

See `deploy/README.md` for production deployment and `client/README.md` for building the Windows executable.
