# WebScreenCapture

Low-latency Windows screen sharing over peer-to-peer WebRTC, with a small Go signaling service and coturn fallback.

## Quick start

1. Open `https://screen.flyingrtx.com`, enter any non-empty pairing token, and click **进入画面**.
2. Run `artifacts/release/WebScreenCapture.exe`, keep the default server URL, and enter exactly the same token.
3. Click **开始捕获**, then choose a monitor in the Windows system picker.

The signaling server hashes the token only to select an isolated in-memory room. It does not pre-register, validate, or persist pairing tokens.

Closing or minimizing the agent window keeps it running in the system tray. Signaling reconnects preserve the selected screen whenever the browser capture track remains valid.

## Layout

- `client/` — .NET 8 WPF + WebView2 single-file Windows agent.
- `server/` — Go authentication, signaling, and embedded viewer/agent web UI.
- `deploy/` — Docker Compose, coturn, nginx, and certificate renewal assets.

See `deploy/README.md` for production deployment and `client/README.md` for building the Windows executable.
