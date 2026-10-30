using System.Text.Json;

namespace Dnf90.Launcher.AntiCheat;

internal static class LegacyPreferences
{
    internal static void Import(string stateDirectory)
    {
        var targetRoot = Path.GetFullPath(stateDirectory).TrimEnd(Path.DirectorySeparatorChar);
        if (!Path.GetFileName(targetRoot).Equals("DNF90Launcher-anticheat", StringComparison.OrdinalIgnoreCase)) return;
        var target = Path.Combine(targetRoot, "preferences.json");
        if (File.Exists(target)) return;
        var source = Path.Combine(Path.GetDirectoryName(targetRoot)!, "DNF90Launcher", "preferences.json");
        try
        {
            SafeFiles.NoLinks(source);
            if (!File.Exists(source) || new FileInfo(source).Length > 65536) return;
            var previous = JsonSerializer.Deserialize(File.ReadAllBytes(source), WireJson.Default.Preferences);
            if (previous is null || previous.Account is null || previous.ClientDirectory is null ||
                previous.Account.Length > 64 || previous.ClientDirectory.Length > 1000) return;
            // Only preferences cross channels. Signed metadata, sequence history,
            // download caches and protection consent belong to the new channel.
            var preferences = previous with { AntiCheatConsentVersion = 0 };
            SafeFiles.Atomic(target, JsonSerializer.SerializeToUtf8Bytes(preferences, WireJson.Default.Preferences));
        }
        catch (Exception error) when (error is JsonException or IOException or UnauthorizedAccessException)
        {
            // An unreadable legacy configuration must not prevent a fresh start.
        }
    }
}
