namespace WebScreenCapture.Client;

public sealed class AdaptiveStreamController
{
    private static readonly TimeSpan FeedbackFreshness = TimeSpan.FromSeconds(10);
    private static readonly TimeSpan DowngradeDelay = TimeSpan.FromSeconds(5);
    private static readonly TimeSpan UpgradeDelay = TimeSpan.FromSeconds(20);

    private DateTimeOffset? _lossUpdatedAt;
    private DateTimeOffset? _bandwidthUpdatedAt;
    private DateTimeOffset? _badSince;
    private DateTimeOffset? _goodSince;
    private double _lossRatio;
    private int _availableBitrate;

    public CaptureProfile CurrentProfile { get; private set; } = CaptureProfile.High;
    public double LossRatio => _lossRatio;
    public int AvailableBitrate => _availableBitrate;

    public void UpdateLoss(double lossRatio, DateTimeOffset now)
    {
        _lossRatio = Math.Clamp(lossRatio, 0, 1);
        _lossUpdatedAt = now;
    }

    public void UpdateAvailableBitrate(int bitrate, DateTimeOffset now)
    {
        if (bitrate <= 0) return;
        _availableBitrate = bitrate;
        _bandwidthUpdatedAt = now;
    }

    public CaptureProfile? Evaluate(DateTimeOffset now)
    {
        var lossFresh = _lossUpdatedAt is not null && now - _lossUpdatedAt <= FeedbackFreshness;
        var bandwidthFresh = _bandwidthUpdatedAt is not null && now - _bandwidthUpdatedAt <= FeedbackFreshness;
        var bandwidthLow = bandwidthFresh && _availableBitrate < CurrentProfile.Bitrate * 0.9;
        var unhealthy = (lossFresh && _lossRatio > 0.05) || bandwidthLow;

        if (unhealthy && CurrentProfile != CaptureProfile.Low)
        {
            _badSince ??= now;
            _goodSince = null;
            if (now - _badSince >= DowngradeDelay)
            {
                CurrentProfile = CaptureProfile.NextLower(CurrentProfile);
                _badSince = null;
                return CurrentProfile;
            }
            return null;
        }

        _badSince = null;
        var nextHigher = CaptureProfile.NextHigher(CurrentProfile);
        var canUpgrade = nextHigher != CurrentProfile
            && lossFresh
            && bandwidthFresh
            && _lossRatio < 0.02
            && _availableBitrate >= nextHigher.Bitrate * 1.2;
        if (!canUpgrade)
        {
            _goodSince = null;
            return null;
        }

        _goodSince ??= now;
        if (now - _goodSince < UpgradeDelay) return null;
        CurrentProfile = nextHigher;
        _goodSince = null;
        return CurrentProfile;
    }

    public void Reset(CaptureProfile profile)
    {
        CurrentProfile = profile;
        _badSince = null;
        _goodSince = null;
    }
}
