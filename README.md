# WebScreenCapture

Low-latency Windows screen sharing over peer-to-peer WebRTC, with a small Go signaling service and coturn fallback.

## Quick start

1. Run `artifacts/release/WebScreenCapture.exe` on the Windows machine to share.
2. On first launch, keep the server URL as `https://screen.flyingrtx.com` and enter the deployed device token.
3. Click **开始捕获**, then choose a monitor in the Windows system picker.
4. Open `https://screen.flyingrtx.com` on the viewing machine and enter the viewer password.

Closing or minimizing the agent window keeps it running in the system tray. Signaling reconnects preserve the selected screen whenever the browser capture track remains valid.

## Layout

- `client/` — .NET 8 WPF + WebView2 single-file Windows agent.
- `server/` — Go authentication, signaling, and embedded viewer/agent web UI.
- `deploy/` — Docker Compose, coturn, nginx, and certificate renewal assets.

See `deploy/README.md` for production deployment and `client/README.md` for building the Windows executable.
