using WebScreenCapture.Client;
using Xunit;

namespace WebScreenCapture.Client.Tests;

public sealed class NvencDesktopCaptureTests
{
    [Fact]
    public void BuildsDxgiNvencUltraLowLatencyPipeline()
    {
        var arguments = NvencDesktopCapture.BuildArguments(CaptureProfile.High);
        var command = string.Join(' ', arguments);

        Assert.Contains("ddagrab=output_idx=0:framerate=60:draw_mouse=1", command);
        Assert.Contains("h264_nvenc", command);
        Assert.Contains("-tune ull", command);
        Assert.Contains("-zerolatency 1", command);
        Assert.Contains("-aud 1", command);
        Assert.Contains("-bf 0", command);
        Assert.DoesNotContain("gdigrab", command);
        Assert.DoesNotContain("getDisplayMedia", command);
    }

    [Theory]
    [InlineData(0, 60, 12_000_000)]
    [InlineData(1, 45, 7_000_000)]
    [InlineData(2, 30, 3_500_000)]
    public void BuildsAllAdaptiveProfiles(int tier, int expectedFps, int expectedBitrate)
    {
        var profile = new[] { CaptureProfile.High, CaptureProfile.Medium, CaptureProfile.Low }[tier];
        var arguments = NvencDesktopCapture.BuildArguments(profile);

        Assert.Equal(expectedFps, profile.FramesPerSecond);
        Assert.Equal(expectedBitrate, profile.Bitrate);
        Assert.Contains($"ddagrab=output_idx=0:framerate={expectedFps}:draw_mouse=1", arguments);
        Assert.Equal(expectedBitrate.ToString(), arguments[Array.IndexOf(arguments.ToArray(), "-b:v") + 1]);
    }
}
