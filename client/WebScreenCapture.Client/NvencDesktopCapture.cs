using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;

namespace WebScreenCapture.Client;

public sealed record CaptureProfile(string Name, int FramesPerSecond, int Bitrate, int GopFrames, int Tier)
{
    public static CaptureProfile High { get; } = new("高清 / 60fps / 12Mbps", 60, 12_000_000, 60, 0);
    public static CaptureProfile Medium { get; } = new("均衡 / 45fps / 7Mbps", 45, 7_000_000, 45, 1);
    public static CaptureProfile Low { get; } = new("流畅 / 30fps / 3.5Mbps", 30, 3_500_000, 30, 2);

    public static CaptureProfile NextLower(CaptureProfile profile) => profile.Tier switch
    {
        0 => Medium,
        1 => Low,
        _ => Low,
    };

    public static CaptureProfile NextHigher(CaptureProfile profile) => profile.Tier switch
    {
        2 => Medium,
        1 => High,
        _ => High,
    };
}

public sealed record CaptureSnapshot(long Frames, long Bytes, double FramesPerSecond, double BitsPerSecond);

public sealed class NvencDesktopCapture : IAsyncDisposable
{
    private readonly AnnexBAccessUnitReader _reader = new();
    private readonly Stopwatch _statsClock = new();
    private readonly SemaphoreSlim _lifecycleGate = new(1, 1);
    private CaptureProfile _profile;
    private Process? _process;
    private CancellationTokenSource? _captureCts;
    private Task? _outputTask;
    private Task? _errorTask;
    private string _lastError = string.Empty;
    private long _frames;
    private long _bytes;
    private CancellationToken _runToken;
    private bool _started;

    public NvencDesktopCapture(CaptureProfile? profile = null)
    {
        _profile = profile ?? CaptureProfile.High;
    }

    public event Action<byte[]>? EncodedFrame;
    public event Action<string>? Diagnostic;

    public bool IsRunning => _process is { HasExited: false };
    public CaptureProfile CurrentProfile => _profile;

    public async Task StartAsync(CancellationToken cancellationToken)
    {
        await _lifecycleGate.WaitAsync(cancellationToken);
        try
        {
            if (_started) return;
            ValidateNvencDriver();
            _runToken = cancellationToken;
            _statsClock.Restart();
            Interlocked.Exchange(ref _frames, 0);
            Interlocked.Exchange(ref _bytes, 0);
            _started = true;
            try
            {
                await StartProcessAsync(_profile, cancellationToken);
            }
            catch
            {
                _started = false;
                await StopProcessAsync();
                throw;
            }
        }
        finally
        {
            _lifecycleGate.Release();
        }
    }

    public async Task<bool> ChangeProfileAsync(CaptureProfile profile, CancellationToken cancellationToken)
    {
        await _lifecycleGate.WaitAsync(cancellationToken);
        try
        {
            if (!_started || profile == _profile) return false;
            await StopProcessAsync();
            _profile = profile;
            await StartProcessAsync(profile, _runToken);
            return true;
        }
        finally
        {
            _lifecycleGate.Release();
        }
    }

    public async Task RequestKeyFrameAsync(CancellationToken cancellationToken)
    {
        await _lifecycleGate.WaitAsync(cancellationToken);
        try
        {
            if (!_started) return;
            await StopProcessAsync();
            await StartProcessAsync(_profile, _runToken);
        }
        finally
        {
            _lifecycleGate.Release();
        }
    }

    public CaptureSnapshot GetSnapshot()
    {
        var seconds = Math.Max(_statsClock.Elapsed.TotalSeconds, 0.001);
        var frames = Interlocked.Read(ref _frames);
        var bytes = Interlocked.Read(ref _bytes);
        return new CaptureSnapshot(frames, bytes, frames / seconds, bytes * 8d / seconds);
    }

    public async Task StopAsync()
    {
        await _lifecycleGate.WaitAsync();
        try
        {
            _started = false;
            await StopProcessAsync();
        }
        finally
        {
            _lifecycleGate.Release();
        }
    }

    private async Task StartProcessAsync(CaptureProfile profile, CancellationToken cancellationToken)
    {
        var firstFrame = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        _lastError = string.Empty;
        _reader.Reset();
        _captureCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        var startInfo = BuildStartInfo(ResolveFfmpegPath(), profile);
        _process = Process.Start(startInfo) ?? throw new InvalidOperationException("无法启动原生桌面捕获进程。");
        _outputTask = ReadOutputAsync(_process.StandardOutput.BaseStream, firstFrame, _captureCts.Token);
        _errorTask = ReadErrorsAsync(_process.StandardError, _captureCts.Token);

        var exited = _process.WaitForExitAsync(cancellationToken);
        var completed = await Task.WhenAny(firstFrame.Task, exited, Task.Delay(TimeSpan.FromSeconds(12), cancellationToken));
        if (completed == firstFrame.Task)
        {
            await firstFrame.Task;
            return;
        }
        if (completed == exited)
        {
            throw new InvalidOperationException(BuildStartupError("原生桌面捕获进程提前退出。"));
        }
        throw new TimeoutException(BuildStartupError("等待 DXGI/NVENC 首帧超时。"));
    }

    private async Task StopProcessAsync()
    {
        _captureCts?.Cancel();
        if (_process is { HasExited: false } process)
        {
            try { process.Kill(entireProcessTree: true); } catch (InvalidOperationException) { }
        }
        if (_outputTask is not null)
        {
            try { await _outputTask; } catch (OperationCanceledException) { } catch (IOException) { }
        }
        if (_errorTask is not null)
        {
            try { await _errorTask; } catch (OperationCanceledException) { } catch (IOException) { }
        }
        _process?.Dispose();
        _process = null;
        _captureCts?.Dispose();
        _captureCts = null;
        _reader.Reset();
    }

    public async ValueTask DisposeAsync() => await StopAsync();

    internal static ProcessStartInfo BuildStartInfo(string ffmpegPath, CaptureProfile profile)
    {
        var info = new ProcessStartInfo
        {
            FileName = ffmpegPath,
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        foreach (var argument in BuildArguments(profile)) info.ArgumentList.Add(argument);
        return info;
    }

    internal static IReadOnlyList<string> BuildArguments(CaptureProfile profile)
    {
        return
        [
            "-hide_banner",
            "-loglevel", "warning",
            "-filter_complex", $"ddagrab=output_idx=0:framerate={profile.FramesPerSecond}:draw_mouse=1",
            "-an",
            "-c:v", "h264_nvenc",
            "-profile:v", "baseline",
            "-preset", "p1",
            "-tune", "ull",
            "-zerolatency", "1",
            "-rc", "cbr",
            "-b:v", profile.Bitrate.ToString(System.Globalization.CultureInfo.InvariantCulture),
            "-maxrate", profile.Bitrate.ToString(System.Globalization.CultureInfo.InvariantCulture),
            "-bufsize", Math.Max(profile.Bitrate / 12, 500_000).ToString(System.Globalization.CultureInfo.InvariantCulture),
            "-g", profile.GopFrames.ToString(System.Globalization.CultureInfo.InvariantCulture),
            "-bf", "0",
            "-forced-idr", "1",
            "-aud", "1",
            "-f", "h264",
            "pipe:1",
        ];
    }

    internal static string ResolveFfmpegPath()
    {
        var bundled = Path.Combine(AppContext.BaseDirectory, "ffmpeg.exe");
        if (File.Exists(bundled)) return bundled;

        var path = Environment.GetEnvironmentVariable("PATH") ?? string.Empty;
        foreach (var directory in path.Split(Path.PathSeparator, StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries))
        {
            var candidate = Path.Combine(directory, "ffmpeg.exe");
            if (File.Exists(candidate)) return candidate;
        }
        throw new FileNotFoundException("未找到内置 ffmpeg.exe，无法启动 DXGI/NVENC 捕获。请重新下载完整发布版。", bundled);
    }

    private static void ValidateNvencDriver()
    {
        if (!NativeLibrary.TryLoad("nvEncodeAPI64.dll", out var handle))
        {
            throw new DllNotFoundException("未找到 NVIDIA NVENC 驱动 DLL (nvEncodeAPI64.dll)。请安装最新 NVIDIA 显卡驱动。");
        }
        NativeLibrary.Free(handle);
    }

    private async Task ReadOutputAsync(Stream stream, TaskCompletionSource firstFrame, CancellationToken cancellationToken)
    {
        var buffer = new byte[128 * 1024];
        while (!cancellationToken.IsCancellationRequested)
        {
            var count = await stream.ReadAsync(buffer, cancellationToken);
            if (count == 0) break;
            foreach (var frame in _reader.Push(buffer.AsSpan(0, count))) PublishFrame(frame, firstFrame);
        }
        var finalFrame = _reader.Flush();
        if (finalFrame is not null) PublishFrame(finalFrame, firstFrame);
    }

    private async Task ReadErrorsAsync(StreamReader reader, CancellationToken cancellationToken)
    {
        while (!cancellationToken.IsCancellationRequested)
        {
            var line = await reader.ReadLineAsync(cancellationToken);
            if (line is null) return;
            _lastError = line;
            Diagnostic?.Invoke(line);
        }
    }

    private void PublishFrame(byte[] frame, TaskCompletionSource firstFrame)
    {
        Interlocked.Increment(ref _frames);
        Interlocked.Add(ref _bytes, frame.Length);
        firstFrame.TrySetResult();
        EncodedFrame?.Invoke(frame);
    }

    private string BuildStartupError(string prefix)
    {
        return string.IsNullOrWhiteSpace(_lastError) ? prefix : $"{prefix} {_lastError}";
    }
}
