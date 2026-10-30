using System.Text.RegularExpressions;

namespace ServerS5A3;

// Wire data is shared with ordinary builds. The Windows implementation is not.
public sealed class AntiCheatPolicy
{
    // Setters preserve absent-field defaults in .NET 8's source-generated reader.
    public bool Enabled { get; set; }
    public string Mode { get; set; } = "audit";
    public ProtectedClientFile[] RequiredFiles { get; set; } = [];
    public AntiCheatRule[] Rules { get; set; } = [];
    public string[] AllowedModuleHashes { get; set; } = [];
    public AntiCheatRegion[] CodeRegions { get; set; } = [];

    public void Validate()
    {
        if (Mode is not ("audit" or "enforce") || RequiredFiles is null || Rules is null || AllowedModuleHashes is null || CodeRegions is null ||
            RequiredFiles.Length > 64 || Rules.Length > 256 || AllowedModuleHashes.Length > 512 || CodeRegions.Length > 32)
            throw new InvalidDataException("反作弊策略格式或数量无效。");
        var paths = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (var file in RequiredFiles) {
            if (file is null || !SafeLeaf(file.Name) || !file.Name.EndsWith(".dll", StringComparison.OrdinalIgnoreCase) || !Hash(file.Sha256) || !paths.Add(file.Name))
                throw new InvalidDataException("反作弊 DLL 策略无效。");
        }
        var ids = new HashSet<string>(StringComparer.Ordinal);
        foreach (var rule in Rules) {
            if (rule is null || !Id(rule.Id) || !ids.Add(rule.Id) || !Hash(rule.Sha256) || rule.Scope is not ("module" or "process"))
                throw new InvalidDataException("反作弊规则无效。");
        }
        if (AllowedModuleHashes.Any(h => !Hash(h))) throw new InvalidDataException("反作弊模块摘要无效。");
        foreach (var region in CodeRegions) {
            if (region is null || !Id(region.Id) || !ids.Add(region.Id) || !Hash(region.ExeSha256) || !Hash(region.Sha256) ||
                region.Rva < 0 || region.Rva > 0x7fffffff || region.Length is < 1 or > 32768 || region.Rva + region.Length > 0x80000000L)
                throw new InvalidDataException("反作弊代码区域无效。");
        }
    }
    private static bool Id(string? value) => value is not null && Regex.IsMatch(value, "^[a-z0-9][a-z0-9-]{0,47}$");
    private static bool Hash(string? value) => value is not null && Regex.IsMatch(value, "^[a-fA-F0-9]{64}$");
    private static bool SafeLeaf(string? value) => value is not null && Regex.IsMatch(value, "^[a-zA-Z0-9_-][a-zA-Z0-9_.-]{0,119}$") &&
        !value.EndsWith('.') && !Regex.IsMatch(value.Split('.')[0], "^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])$", RegexOptions.IgnoreCase);
}
public sealed record ProtectedClientFile(string Name, string Sha256);
public sealed record AntiCheatRule(string Id, string Scope, string Sha256);
public sealed record AntiCheatRegion(string Id, string ExeSha256, long Rva, int Length, string Sha256);

public static class LauncherBuild
{
#if ANTICHEAT
    public const bool AntiCheat = true;
    public const string Flavor = "anticheat";
    public const string ChannelPath = "/launcher/anticheat/v2/";
#else
    public const bool AntiCheat = false;
    public const string Flavor = "standard";
    public const string ChannelPath = "/launcher/v2/";
#endif
    public static void RequireFlavor(string flavor)
    {
        if (flavor != Flavor) throw new InvalidOperationException("更新包与当前登录器的保护类型不同，请使用同类型发行包。");
    }
}
