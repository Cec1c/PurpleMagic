using System.Security.Cryptography;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace ServerS5A3;

public sealed record PlayerProfile(string ApiUrl, string CertificateSha256, string UpdatePublicKey,
    string SettingsUrl, string GameHost, int BootstrapPort, string ExeSha256, string PvfSha256, string DefaultClientDirectory)
{
    public ChannelHistoryCompatibility? ChannelHistory { get; init; }
    public string MinimumLauncherVersion { get; init; } = "";
    public bool EnableQQBinding { get; init; } = false;
    public bool RequireQQBinding { get; init; } = false;
    public int ChannelServerId { get; init; }
    // Explicit temporary compatibility during a staged server migration.
    public int? PreviousChannelServerId { get; init; }
}

public sealed class PlayerPreferences
{
    public string ClientDirectory { get; set; } = "";
    public string Account { get; set; } = "";
    public string SecondaryAccount { get; set; } = "";
    public bool RememberAccount { get; set; } = true;
    public bool CheckUpdates { get; set; } = true;
    public static PlayerPreferences Load(string path)
    {
        try { return JsonSerializer.Deserialize(File.ReadAllText(path), CoreJson.Default.PlayerPreferences) ?? new(); }
        catch (Exception e) when (e is IOException or JsonException) { return new(); }
    }
    public void Save(string path)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        File.WriteAllText(path + ".tmp", JsonSerializer.Serialize(this, CoreJson.Default.PlayerPreferences));
        File.Move(path + ".tmp", path, true);
    }
}

public static class GameContract
{
    public static string FindClientDirectory(string launcherDirectory, string configured)
    {
        foreach (var candidate in new[] { configured, launcherDirectory, Path.Combine(launcherDirectory, "client") })
            if (!string.IsNullOrWhiteSpace(candidate) && File.Exists(Path.Combine(candidate, "DNF.exe")) && File.Exists(Path.Combine(candidate, "Script.pvf")))
                return Path.GetFullPath(candidate);
        return "";
    }

    public static string Arguments(string token, string host, int port)
    {
        if (!Regex.IsMatch(token, "^de509f65[a-f0-9]{24}$") || !System.Net.IPAddress.TryParse(host, out _) || port is < 1024 or > 65535)
            throw new InvalidDataException("登录凭据或游戏入口无效。");
        return $"99?{host}?{port}?0?{token}?01?1?0?0?0?0?1?9n2b1c8r3w7y?0?0?19847";
    }
    public static async Task VerifyClient(string directory, PlayerProfile profile)
    {
        foreach (var (file, expected) in new[] { ("DNF.exe", profile.ExeSha256), ("Script.pvf", profile.PvfSha256) })
        {
            await using var stream = File.OpenRead(Path.Combine(directory, file));
            var actual = Convert.ToHexString(await SHA256.HashDataAsync(stream));
            if (!actual.Equals(expected, StringComparison.OrdinalIgnoreCase) &&
                !(file == "DNF.exe" && NativeGuildCompatibility.AcceptsDerivative(profile, actual)))
                throw new InvalidDataException($"{file} 与测试服版本不一致，请选择匹配的客户端目录。");
        }
    }
}
