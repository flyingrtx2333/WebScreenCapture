using WebScreenCapture.Client;
using Xunit;

namespace WebScreenCapture.Client.Tests;

public sealed class AdaptiveStreamControllerTests
{
    [Fact]
    public void DowngradesAfterFiveSecondsOfHighLoss()
    {
        var controller = new AdaptiveStreamController();
        var start = DateTimeOffset.UnixEpoch;
        controller.UpdateLoss(0.08, start);
        controller.UpdateAvailableBitrate(15_000_000, start);

        Assert.Null(controller.Evaluate(start));
        Assert.Null(controller.Evaluate(start.AddSeconds(4)));
        Assert.Equal(CaptureProfile.Medium, controller.Evaluate(start.AddSeconds(5)));
    }

    [Fact]
    public void UpgradesOnlyAfterTwentySecondsWithHeadroom()
    {
        var controller = new AdaptiveStreamController();
        controller.Reset(CaptureProfile.Low);
        var start = DateTimeOffset.UnixEpoch;

        for (var second = 0; second <= 19; second++)
        {
            var now = start.AddSeconds(second);
            controller.UpdateLoss(0.005, now);
            controller.UpdateAvailableBitrate(9_000_000, now);
            Assert.Null(controller.Evaluate(now));
        }

        var upgradedAt = start.AddSeconds(20);
        controller.UpdateLoss(0.005, upgradedAt);
        controller.UpdateAvailableBitrate(9_000_000, upgradedAt);
        Assert.Equal(CaptureProfile.Medium, controller.Evaluate(upgradedAt));
    }

    [Fact]
    public void DoesNotFlapOnShortNetworkSpike()
    {
        var controller = new AdaptiveStreamController();
        var start = DateTimeOffset.UnixEpoch;
        controller.UpdateLoss(0.1, start);
        controller.UpdateAvailableBitrate(3_000_000, start);
        Assert.Null(controller.Evaluate(start));
        Assert.Null(controller.Evaluate(start.AddSeconds(3)));

        controller.UpdateLoss(0.01, start.AddSeconds(4));
        controller.UpdateAvailableBitrate(15_000_000, start.AddSeconds(4));
        Assert.Null(controller.Evaluate(start.AddSeconds(4)));
        Assert.Equal(CaptureProfile.High, controller.CurrentProfile);
    }
}
