using System.IO;
using System.Text;

namespace WebScreenCapture.Client;

internal static class AgentLog
{
    private const long MaxBytes = 2 * 1024 * 1024;
    private static readonly object Gate = new();
    private static readonly string DirectoryPath = Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),
        "WebScreenCapture");
    internal static readonly string FilePath = Path.Combine(DirectoryPath, "agent.log");

    public static void Write(string message)
    {
        try
        {
            lock (Gate)
            {
                Directory.CreateDirectory(DirectoryPath);
                if (File.Exists(FilePath) && new FileInfo(FilePath).Length >= MaxBytes)
                {
                    var previous = Path.Combine(DirectoryPath, "agent.previous.log");
                    File.Move(FilePath, previous, true);
                }
                File.AppendAllText(
                    FilePath,
                    $"{DateTimeOffset.Now:O} {message}{Environment.NewLine}",
                    Encoding.UTF8);
            }
        }
        catch
        {
            // Logging must never interrupt capture or signaling.
        }
    }
}
