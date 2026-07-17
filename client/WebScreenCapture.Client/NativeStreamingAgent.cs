using SIPSorcery.Net;
using SIPSorceryMedia.Abstractions;
using System.IO;
using System.Net;
using System.Net.Http;
using System.Net.Http.Json;
using System.Net.WebSockets;
using System.Text;
using System.Text.Json;

namespace WebScreenCapture.Client;

public sealed record AgentRuntimeStatus(
    string State,
    string Detail,
    bool IsCapturing,
    bool ViewerConnected,
    string Codec = "H.264 NVENC");

public sealed class NativeStreamingAgent : IAsyncDisposable
{
    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        PropertyNameCaseInsensitive = true,
    };

    private readonly Uri _serverUri;
    private readonly string _pairingToken;
    private readonly CookieContainer _cookies = new();
    private readonly SemaphoreSlim _sendGate = new(1, 1);
    private readonly SemaphoreSlim _peerGate = new(1, 1);
    private readonly List<RTCIceCandidateInit> _pendingCandidates = [];
    private CancellationTokenSource? _runCts;
    private HttpClient? _http;
    private ClientWebSocket? _webSocket;
    private NvencDesktopCapture? _capture;
    private RTCPeerConnection? _peer;
    private Task? _signalingTask;
    private Task? _statsTask;
    private TaskCompletionSource? _firstConnection;
    private IReadOnlyList<IceServerDto> _iceServers = [];
    private string _sessionId = string.Empty;
    private bool _viewerConnected;

    public NativeStreamingAgent(AgentSettings settings)
    {
        _serverUri = new Uri(settings.ServerUrl.TrimEnd('/') + "/", UriKind.Absolute);
        _pairingToken = settings.PairingToken;
    }

    public event Action<AgentRuntimeStatus>? StatusChanged;
    public event Action<CaptureSnapshot>? CaptureStatsChanged;

    public bool IsRunning => _runCts is { IsCancellationRequested: false };

    public async Task StartAsync(CancellationToken cancellationToken = default)
    {
        if (IsRunning) return;
        _runCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        _firstConnection = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);

        var handler = new HttpClientHandler
        {
            CookieContainer = _cookies,
            UseCookies = true,
        };
        _http = new HttpClient(handler, disposeHandler: true)
        {
            BaseAddress = _serverUri,
            Timeout = TimeSpan.FromSeconds(15),
        };

        PublishStatus("正在连接", "正在建立原生信令连接。", false, false);
        _signalingTask = Task.Run(() => SignalingLoopAsync(_runCts.Token), CancellationToken.None);
        try
        {
            await _firstConnection.Task.WaitAsync(TimeSpan.FromSeconds(20), cancellationToken);
            _capture = new NvencDesktopCapture();
            _capture.EncodedFrame += OnEncodedFrame;
            await _capture.StartAsync(_runCts.Token);
            _statsTask = Task.Run(() => StatsLoopAsync(_runCts.Token), CancellationToken.None);
            PublishStatus("捕获已启动", "DXGI 整桌面捕获与 NVIDIA NVENC 编码正在运行。", true, _viewerConnected);
            await SendSignalAsync("status", new
            {
                captureActive = true,
                profile = CaptureProfile.High.Name,
                codec = "H264/NVENC",
                capture = "DXGI Desktop Duplication",
            }, _runCts.Token);
        }
        catch
        {
            await StopAsync();
            throw;
        }
    }

    public async Task StopAsync()
    {
        var cts = _runCts;
        if (cts is null) return;
        try
        {
            if (!string.IsNullOrEmpty(_sessionId))
            {
                await SendSignalAsync("peer.stop", new { reason = "capture stopped" }, CancellationToken.None);
            }
        }
        catch { }

        cts.Cancel();
        await ClosePeerAsync();
        if (_webSocket is not null)
        {
            try
            {
                if (_webSocket.State is WebSocketState.Open or WebSocketState.CloseReceived)
                {
                    await _webSocket.CloseAsync(WebSocketCloseStatus.NormalClosure, "capture stopped", CancellationToken.None);
                }
            }
            catch (WebSocketException) { }
            _webSocket.Dispose();
            _webSocket = null;
        }
        if (_capture is not null)
        {
            _capture.EncodedFrame -= OnEncodedFrame;
            await _capture.DisposeAsync();
            _capture = null;
        }
        if (_signalingTask is not null)
        {
            try { await _signalingTask; } catch (OperationCanceledException) { }
        }
        if (_statsTask is not null)
        {
            try { await _statsTask; } catch (OperationCanceledException) { }
        }
        _http?.Dispose();
        _http = null;
        _runCts = null;
        cts.Dispose();
        _sessionId = string.Empty;
        _viewerConnected = false;
        PublishStatus("已停止", "点击开始后会直接捕获整个主桌面。", false, false);
    }

    public async ValueTask DisposeAsync() => await StopAsync();

    private async Task SignalingLoopAsync(CancellationToken cancellationToken)
    {
        var delays = new[] { 1, 2, 5, 10, 30 };
        var attempt = 0;
        while (!cancellationToken.IsCancellationRequested)
        {
            try
            {
                await AuthenticateAsync(cancellationToken);
                _iceServers = (await _http!.GetFromJsonAsync<IceConfigurationDto>("api/ice", JsonOptions, cancellationToken))?.IceServers
                    ?? throw new InvalidOperationException("服务器没有返回 ICE 配置。");
                using var socket = new ClientWebSocket();
                socket.Options.Cookies = _cookies;
                _webSocket = socket;
                await socket.ConnectAsync(BuildWebSocketUri(), cancellationToken);
                attempt = 0;
                _firstConnection?.TrySetResult();
                PublishStatus(
                    _capture?.IsRunning == true ? "捕获已启动" : "信令已连接",
                    _capture?.IsRunning == true ? "等待观看端或建立媒体链路。" : "正在初始化原生桌面捕获。",
                    _capture?.IsRunning == true,
                    _viewerConnected);
                await ReceiveLoopAsync(socket, cancellationToken);
                if (socket.CloseStatus == WebSocketCloseStatus.PolicyViolation)
                {
                    throw new InvalidOperationException("同一 Token 的另一个捕获端已接管连接。");
                }
            }
            catch (OperationCanceledException) when (cancellationToken.IsCancellationRequested)
            {
                return;
            }
            catch (Exception ex)
            {
                _firstConnection?.TrySetException(ex);
                await ClosePeerAsync();
                if (cancellationToken.IsCancellationRequested) return;
                var delay = delays[Math.Min(attempt++, delays.Length - 1)];
                PublishStatus("正在重连", $"{ex.Message} {delay} 秒后重试。", _capture?.IsRunning == true, false);
                await Task.Delay(TimeSpan.FromSeconds(delay), cancellationToken);
            }
            finally
            {
                if (ReferenceEquals(_webSocket, null) || _webSocket?.State != WebSocketState.Open)
                {
                    _webSocket = null;
                }
            }
        }
    }

    private async Task AuthenticateAsync(CancellationToken cancellationToken)
    {
        var response = await _http!.PostAsJsonAsync("api/agent/session", new { token = _pairingToken }, JsonOptions, cancellationToken);
        if (!response.IsSuccessStatusCode)
        {
            var error = await ReadApiErrorAsync(response, cancellationToken);
            throw new InvalidOperationException(error);
        }
    }

    private async Task ReceiveLoopAsync(ClientWebSocket socket, CancellationToken cancellationToken)
    {
        var buffer = new byte[64 * 1024];
        while (socket.State == WebSocketState.Open && !cancellationToken.IsCancellationRequested)
        {
            using var message = new MemoryStream();
            WebSocketReceiveResult result;
            do
            {
                result = await socket.ReceiveAsync(buffer, cancellationToken);
                if (result.MessageType == WebSocketMessageType.Close) return;
                if (message.Length + result.Count > 256 * 1024) throw new InvalidDataException("信令消息过大。");
                message.Write(buffer, 0, result.Count);
            } while (!result.EndOfMessage);

            if (result.MessageType != WebSocketMessageType.Text) continue;
            var envelope = JsonSerializer.Deserialize<SignalEnvelope>(message.ToArray(), JsonOptions);
            if (envelope is not null) await HandleSignalAsync(envelope, cancellationToken);
        }
    }

    private async Task HandleSignalAsync(SignalEnvelope message, CancellationToken cancellationToken)
    {
        switch (message.Type)
        {
            case "hello":
                _sessionId = message.SessionId ?? string.Empty;
                if (!string.IsNullOrEmpty(_sessionId)) await CreateOfferAsync(cancellationToken);
                break;
            case "peer.start":
                _sessionId = message.SessionId ?? string.Empty;
                _viewerConnected = true;
                PublishStatus("观看端已连接", "正在协商原生 WebRTC 媒体链路。", _capture?.IsRunning == true, true);
                await CreateOfferAsync(cancellationToken);
                break;
            case "sdp.answer":
                if (message.SessionId != _sessionId || message.Payload.ValueKind != JsonValueKind.Object) return;
                await ApplyAnswerAsync(message.Payload, cancellationToken);
                break;
            case "ice.candidate":
                if (message.SessionId != _sessionId || message.Payload.ValueKind != JsonValueKind.Object) return;
                await AddRemoteCandidateAsync(message.Payload, cancellationToken);
                break;
            case "peer.stop":
                if (message.SessionId != _sessionId) return;
                _viewerConnected = false;
                _sessionId = string.Empty;
                await ClosePeerAsync();
                PublishStatus("捕获已启动", "整桌面持续捕获中，等待观看端。", _capture?.IsRunning == true, false);
                break;
        }
    }

    private async Task CreateOfferAsync(CancellationToken cancellationToken)
    {
        if (string.IsNullOrEmpty(_sessionId)) return;
        await _peerGate.WaitAsync(cancellationToken);
        try
        {
            ClosePeerUnsafe();
            var configuration = new RTCConfiguration
            {
                iceServers = BuildRtcIceServers(_iceServers),
                bundlePolicy = RTCBundlePolicy.max_bundle,
                rtcpMuxPolicy = RTCRtcpMuxPolicy.require,
                X_UseRtpFeedbackProfile = true,
            };
            var peer = new RTCPeerConnection(configuration);
            _peer = peer;
            peer.onicecandidate += candidate =>
            {
                if (string.IsNullOrWhiteSpace(candidate.candidate)) return;
                _ = SendSignalAsync("ice.candidate", new
                {
                    candidate = candidate.candidate,
                    sdpMid = candidate.sdpMid,
                    sdpMLineIndex = candidate.sdpMLineIndex,
                    usernameFragment = candidate.usernameFragment,
                }, _runCts?.Token ?? CancellationToken.None);
            };
            peer.onconnectionstatechange += state =>
            {
                if (!ReferenceEquals(_peer, peer)) return;
                if (state == RTCPeerConnectionState.connected)
                {
                    PublishStatus("实时传输中", "DXGI → NVENC → 原生 WebRTC 已连接。", true, true);
                }
                else if (state is RTCPeerConnectionState.failed or RTCPeerConnectionState.disconnected)
                {
                    PublishStatus("媒体链路恢复中", $"WebRTC 状态：{state}", _capture?.IsRunning == true, _viewerConnected);
                }
            };

            var h264 = new VideoFormat(
                VideoCodecsEnum.H264,
                96,
                90_000,
                "packetization-mode=1;level-asymmetry-allowed=1;profile-level-id=42e033");
            peer.addTrack(new MediaStreamTrack(h264, MediaStreamStatusEnum.SendOnly));
            var offer = peer.createOffer(null);
            await peer.setLocalDescription(offer);
            await SendSignalAsync("sdp.offer", new
            {
                type = "offer",
                sdp = peer.localDescription.sdp.ToString(),
            }, cancellationToken);
        }
        finally
        {
            _peerGate.Release();
        }
    }

    private async Task ApplyAnswerAsync(JsonElement payload, CancellationToken cancellationToken)
    {
        var answer = payload.Deserialize<SessionDescriptionDto>(JsonOptions);
        if (answer?.Sdp is null) return;
        await _peerGate.WaitAsync(cancellationToken);
        try
        {
            if (_peer is null) return;
            var result = _peer.setRemoteDescription(new RTCSessionDescriptionInit
            {
                type = RTCSdpType.answer,
                sdp = answer.Sdp,
            });
            if (result != SetDescriptionResultEnum.OK)
            {
                throw new InvalidOperationException($"观看端 SDP 应答无效：{result}");
            }
            foreach (var candidate in _pendingCandidates) _peer.addIceCandidate(candidate);
            _pendingCandidates.Clear();
        }
        finally
        {
            _peerGate.Release();
        }
    }

    private async Task AddRemoteCandidateAsync(JsonElement payload, CancellationToken cancellationToken)
    {
        var candidate = payload.Deserialize<IceCandidateDto>(JsonOptions);
        if (candidate?.Candidate is null) return;
        var init = new RTCIceCandidateInit
        {
            candidate = candidate.Candidate,
            sdpMid = candidate.SdpMid,
            sdpMLineIndex = candidate.SdpMLineIndex,
            usernameFragment = candidate.UsernameFragment,
        };
        await _peerGate.WaitAsync(cancellationToken);
        try
        {
            if (_peer?.remoteDescription is not null) _peer.addIceCandidate(init);
            else _pendingCandidates.Add(init);
        }
        finally
        {
            _peerGate.Release();
        }
    }

    private async Task SendSignalAsync(string type, object payload, CancellationToken cancellationToken)
    {
        var socket = _webSocket;
        var sessionId = _sessionId;
        if (socket?.State != WebSocketState.Open || string.IsNullOrEmpty(sessionId)) return;
        var bytes = JsonSerializer.SerializeToUtf8Bytes(new { type, sessionId, payload }, JsonOptions);
        await _sendGate.WaitAsync(cancellationToken);
        try
        {
            if (socket.State == WebSocketState.Open)
            {
                await socket.SendAsync(bytes, WebSocketMessageType.Text, true, cancellationToken);
            }
        }
        finally
        {
            _sendGate.Release();
        }
    }

    private void OnEncodedFrame(byte[] frame)
    {
        var peer = _peer;
        if (peer?.connectionState != RTCPeerConnectionState.connected) return;
        try { peer.SendVideo(90_000u / (uint)CaptureProfile.High.FramesPerSecond, frame); }
        catch (Exception ex) { PublishStatus("发送异常", ex.Message, true, _viewerConnected); }
    }

    private async Task StatsLoopAsync(CancellationToken cancellationToken)
    {
        while (!cancellationToken.IsCancellationRequested)
        {
            await Task.Delay(TimeSpan.FromSeconds(1), cancellationToken);
            if (_capture is not null) CaptureStatsChanged?.Invoke(_capture.GetSnapshot());
        }
    }

    private async Task ClosePeerAsync()
    {
        await _peerGate.WaitAsync();
        try { ClosePeerUnsafe(); }
        finally { _peerGate.Release(); }
    }

    private void ClosePeerUnsafe()
    {
        _pendingCandidates.Clear();
        var peer = _peer;
        _peer = null;
        if (peer is null) return;
        try { peer.Close("peer reset"); } catch { }
        peer.Dispose();
    }

    private void PublishStatus(string state, string detail, bool capturing, bool viewerConnected)
    {
        StatusChanged?.Invoke(new AgentRuntimeStatus(state, detail, capturing, viewerConnected));
    }

    private Uri BuildWebSocketUri()
    {
        var builder = new UriBuilder(new Uri(_serverUri, "ws"))
        {
            Scheme = _serverUri.Scheme == Uri.UriSchemeHttps ? "wss" : "ws",
            Port = _serverUri.IsDefaultPort ? -1 : _serverUri.Port,
        };
        return builder.Uri;
    }

    private static List<RTCIceServer> BuildRtcIceServers(IReadOnlyList<IceServerDto> servers)
    {
        var result = new List<RTCIceServer>();
        foreach (var server in servers)
        {
            foreach (var url in server.Urls)
            {
                result.Add(new RTCIceServer
                {
                    urls = url,
                    username = server.Username,
                    credential = server.Credential,
                });
            }
        }
        return result;
    }

    private static async Task<string> ReadApiErrorAsync(HttpResponseMessage response, CancellationToken cancellationToken)
    {
        try
        {
            var error = await response.Content.ReadFromJsonAsync<ApiErrorDto>(JsonOptions, cancellationToken);
            return error?.Error ?? $"服务器返回 {response.StatusCode}";
        }
        catch
        {
            return $"服务器返回 {response.StatusCode}";
        }
    }

    private sealed record SignalEnvelope(string Type, string? SessionId, JsonElement Payload);
    private sealed record SessionDescriptionDto(string Type, string? Sdp);
    private sealed record IceCandidateDto(string? Candidate, string? SdpMid, ushort SdpMLineIndex, string? UsernameFragment);
    private sealed record ApiErrorDto(string Error);
    private sealed record IceConfigurationDto(IReadOnlyList<IceServerDto> IceServers);
    private sealed record IceServerDto(IReadOnlyList<string> Urls, string Username = "", string Credential = "");
}
