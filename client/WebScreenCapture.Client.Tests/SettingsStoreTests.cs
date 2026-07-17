using WebScreenCapture.Client;
using Xunit;

namespace WebScreenCapture.Client.Tests;

public sealed class SettingsStoreTests
{
    [Fact]
    public void RoundTripsDpapiProtectedToken()
    {
        var directory = Path.Combine(Path.GetTempPath(), "WebScreenCapture.Tests", Guid.NewGuid().ToString("N"));
        try
        {
            var store = new SettingsStore(directory);
            var expected = new AgentSettings("https://screen.example.com", "device-token-that-must-not-appear-in-plaintext");
            store.Save(expected);

            Assert.Equal(expected, store.Load());
            Assert.DoesNotContain(expected.PairingToken, File.ReadAllText(Path.Combine(directory, "settings.json")));

            store.Delete();
            Assert.Null(store.Load());
        }
        finally
        {
            if (Directory.Exists(directory)) Directory.Delete(directory, recursive: true);
        }
    }
}
