using WebScreenCapture.Client;
using Xunit;

namespace WebScreenCapture.Client.Tests;

public sealed class IceCandidateTests
{
    [Theory]
    [InlineData("3426413802 1 udp 8447 1.14.58.29 49169 typ relay", "candidate:3426413802 1 udp 8447 1.14.58.29 49169 typ relay")]
    [InlineData("candidate:2242419981 1 udp 1677730047 203.0.113.10 1913 typ srflx", "candidate:2242419981 1 udp 1677730047 203.0.113.10 1913 typ srflx")]
    [InlineData("a=candidate:1 1 udp 1 127.0.0.1 5000 typ host", "candidate:1 1 udp 1 127.0.0.1 5000 typ host")]
    public void BrowserCandidateAlwaysHasRequiredPrefix(string input, string expected)
    {
        Assert.Equal(expected, NativeStreamingAgent.NormalizeIceCandidateForBrowser(input));
    }
}
