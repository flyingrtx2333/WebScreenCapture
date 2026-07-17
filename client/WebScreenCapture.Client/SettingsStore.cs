using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.IO;

namespace WebScreenCapture.Client;

public sealed record AgentSettings(string ServerUrl, string AccessToken);

public sealed class SettingsStore
{
    private readonly string _settingsPath;

    public SettingsStore(string? settingsDirectory = null)
    {
        var directory = settingsDirectory ?? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "WebScreenCapture");
        _settingsPath = Path.Combine(directory, "settings.json");
    }

    public AgentSettings? Load()
    {
        if (!File.Exists(_settingsPath)) return null;
        var stored = JsonSerializer.Deserialize<StoredSettings>(File.ReadAllText(_settingsPath))
            ?? throw new InvalidDataException("连接设置为空。");
        var encrypted = Convert.FromBase64String(stored.ProtectedToken);
        var plaintext = ProtectedData.Unprotect(encrypted, optionalEntropy: null, DataProtectionScope.CurrentUser);
        var token = Encoding.UTF8.GetString(plaintext);
        CryptographicOperations.ZeroMemory(plaintext);
        return new AgentSettings(stored.ServerUrl, token);
    }

    public void Save(AgentSettings settings)
    {
        var plaintext = Encoding.UTF8.GetBytes(settings.AccessToken);
        try
        {
            var encrypted = ProtectedData.Protect(plaintext, optionalEntropy: null, DataProtectionScope.CurrentUser);
            var stored = new StoredSettings(settings.ServerUrl, Convert.ToBase64String(encrypted));
            var directory = Path.GetDirectoryName(_settingsPath)!;
            Directory.CreateDirectory(directory);
            var temporaryPath = _settingsPath + ".tmp";
            File.WriteAllText(temporaryPath, JsonSerializer.Serialize(stored));
            File.Move(temporaryPath, _settingsPath, overwrite: true);
        }
        finally
        {
            CryptographicOperations.ZeroMemory(plaintext);
        }
    }

    public void Delete()
    {
        if (File.Exists(_settingsPath)) File.Delete(_settingsPath);
    }

    private sealed record StoredSettings(string ServerUrl, string ProtectedToken);
}
