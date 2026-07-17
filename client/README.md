# Windows capture agent

## Requirements

- Windows 10/11 x64
- NVIDIA GPU with a current driver and NVENC support
- .NET 8 SDK for building only

## Build

```powershell
dotnet restore .\WebScreenCapture.Client\WebScreenCapture.Client.csproj
dotnet publish .\WebScreenCapture.Client\WebScreenCapture.Client.csproj -c Release -r win-x64 -o ..\artifacts\win-x64
```

The output is a self-contained `WebScreenCapture.exe` with the native FFmpeg capture runtime embedded. It does not use WebView2. Enter any non-empty pairing token in the viewer, enter the same token in the Windows agent, and click **开始捕获整个桌面**. The agent captures the entire primary desktop through DXGI, encodes H.264 through NVIDIA NVENC, and sends it through native WebRTC. The token is encrypted with DPAPI for the current Windows user.

After authentication, click **开始捕获** and select a monitor in the Windows picker. The agent then waits for the viewer automatically; there is no command line or background service to configure.
