using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;

namespace WebScreenCapture.Client;

public sealed record CaptureProfile(string Name, int FramesPerSecond, int Bitrate, int GopFrames)
{
    public static CaptureProfile High { get; } = new("原生桌面 / 60fps", 60, 12_000_000, 60);
}

public sealed record CaptureSnapshot(long Frames, long Bytes, double FramesPerSecond, double BitsPerSecond);

public sealed class NvencDesktopCapture : IAsyncDisposable
{
    private readonly CaptureProfile _profile;
    private readonly AnnexBAccessUnitReader _reader = new();
    private readonly Stopwatch _statsClock = new();
    private readonly TaskCompletionSource _firstFrame = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private Process? _process;
    private CancellationTokenSource? _captureCts;
    private Task? _outputTask;
    private Task? _errorTask;
    private string _lastError = string.Empty;
    private long _frames;
    private long _bytes;

    public NvencDesktopCapture(CaptureProfile? profile = null)
    {
        _profile = profile ?? CaptureProfile.High;
    }

    public event Action<byte[]>? EncodedFrame;
    public event Action<string>? Diagnostic;

    public bool IsRunning => _process is { HasExited: false };

    public async Task StartAsync(CancellationToken cancellationToken)
    {
        if (IsRunning) return;
        ValidateNvencDriver();
        var ffmpegPath = ResolveFfmpegPath();
        _captureCts = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        var startInfo = BuildStartInfo(ffmpegPath, _profile);
        _process = Process.Start(startInfo) ?? throw new InvalidOperationException("无法启动原生桌面捕获进程。");
        _statsClock.Restart();
        _outputTask = ReadOutputAsync(_process.StandardOutput.BaseStream, _captureCts.Token);
        _errorTask = ReadErrorsAsync(_process.StandardError, _captureCts.Token);

        var exited = _process.WaitForExitAsync(cancellationToken);
        var completed = await Task.WhenAny(_firstFrame.Task, exited, Task.Delay(TimeSpan.FromSeconds(12), cancellationToken));
        if (completed == _firstFrame.Task)
        {
            await _firstFrame.Task;
            return;
        }
        if (completed == exited)
        {
            throw new InvalidOperationException(BuildStartupError("原生桌面捕获进程提前退出。"));
        }
        throw new TimeoutException(BuildStartupError("等待 DXGI/NVENC 首帧超时。"));
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
        _captureCts?.Cancel();
        if (_process is { HasExited: false } process)
        {
            try { process.Kill(entireProcessTree: true); } catch (InvalidOperationException) { }
        }
        if (_outputTask is not null)
        {
            try { await _outputTask; } catch (OperationCanceledException) { }
        }
        if (_errorTask is not null)
        {
            try { await _errorTask; } catch (OperationCanceledException) { }
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

    private async Task ReadOutputAsync(Stream stream, CancellationToken cancellationToken)
    {
        var buffer = new byte[128 * 1024];
        while (!cancellationToken.IsCancellationRequested)
        {
            var count = await stream.ReadAsync(buffer, cancellationToken);
            if (count == 0) break;
            foreach (var frame in _reader.Push(buffer.AsSpan(0, count))) PublishFrame(frame);
        }
        var finalFrame = _reader.Flush();
        if (finalFrame is not null) PublishFrame(finalFrame);
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

    private void PublishFrame(byte[] frame)
    {
        Interlocked.Increment(ref _frames);
        Interlocked.Add(ref _bytes, frame.Length);
        _firstFrame.TrySetResult();
        EncodedFrame?.Invoke(frame);
    }

    private string BuildStartupError(string prefix)
    {
        return string.IsNullOrWhiteSpace(_lastError) ? prefix : $"{prefix} {_lastError}";
    }
}
