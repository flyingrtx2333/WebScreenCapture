using WebScreenCapture.Client;
using Xunit;

namespace WebScreenCapture.Client.Tests;

public sealed class ServerAddressTests
{
    [Theory]
    [InlineData(" https://screen.example.com/path/ ", "https://screen.example.com")]
    [InlineData("http://localhost:18091/", "http://localhost:18091")]
    public void NormalizesSupportedAddresses(string value, string expected)
    {
        Assert.Equal(expected, ServerAddress.Normalize(value));
    }

    [Theory]
    [InlineData("http://screen.example.com")]
    [InlineData("not-a-url")]
    public void RejectsUnsafeOrInvalidAddresses(string value)
    {
        Assert.Throws<InvalidOperationException>(() => ServerAddress.Normalize(value));
    }
}
