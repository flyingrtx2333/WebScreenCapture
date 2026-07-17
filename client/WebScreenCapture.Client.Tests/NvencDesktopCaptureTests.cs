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
}
