# WebScreenCapture

Low-latency native Windows desktop sharing over peer-to-peer WebRTC, with a small Go signaling service and coturn fallback.

## Quick start

1. Open `https://screen.flyingrtx.com`, enter any non-empty pairing token, and click **进入画面**.
2. Run `artifacts/release/WebScreenCapture.exe`, keep the default server URL, and enter exactly the same token.
3. Click **开始捕获整个桌面**. The native agent immediately captures the full primary desktop without a browser or screen picker.

The signaling server hashes the token only to select an isolated in-memory room. It does not pre-register, validate, or persist pairing tokens.

The Windows agent uses DXGI Desktop Duplication, NVIDIA NVENC H.264, and a native WebRTC stack. It does not embed WebView2 or call browser screen-capture APIs. Closing or minimizing the window keeps capture running in the system tray.

## Layout

- `client/` — .NET 8 native WPF single-file Windows agent.
- `server/` — Go authentication, signaling, and embedded browser viewer.
- `deploy/` — Docker Compose, coturn, nginx, and certificate renewal assets.

See `deploy/README.md` for production deployment and `client/README.md` for building the Windows executable.
