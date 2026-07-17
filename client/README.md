# Windows capture agent

## Requirements

- Windows 10/11 x64
- Microsoft Edge WebView2 Runtime
- .NET 8 SDK for building only

## Build

```powershell
dotnet restore .\WebScreenCapture.Client\WebScreenCapture.Client.csproj
dotnet publish .\WebScreenCapture.Client\WebScreenCapture.Client.csproj -c Release -r win-x64 -o ..\artifacts\win-x64
```

The output is a self-contained `WebScreenCapture.exe`. WebView2 Runtime remains a system prerequisite. Enter any non-empty pairing token in the web viewer, then enter exactly the same token in the Windows agent. The token is encrypted with DPAPI for the current Windows user.

After authentication, click **开始捕获** and select a monitor in the Windows picker. The agent then waits for the viewer automatically; there is no command line or background service to configure.
