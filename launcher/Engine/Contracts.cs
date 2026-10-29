using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.RegularExpressions;
using ServerS5A3;
using Dnf90.Launcher.Diagnostics;

namespace Dnf90.Launcher;

public sealed record News(string Title, string Body, string Category, string Date);
public sealed record ContentFile(string Path, long Length, string Sha256, bool Downloadable, string Compression, string BlobSha256, long BlobLength);
public sealed record ContentRelease(string Version, string SourceId, string Channel, long SourceSequence, string SourcePayloadSha256, string Endpoint, string CertificateSha256, ContentFile[] Files);
public sealed record BundleRelease(string Version, string Url, long Size, string Sha256);
public sealed record Bootstrap(string Product, int Format, long Sequence, PlayerProfile Profile, News[] News, ContentRelease? Content, BundleRelease? Launcher);
// AutoCheck is the persisted legacy key; it now controls automatic check and installation of both update types.
public sealed record Preferences(string ClientDirectory = "", string Account = "", bool RememberAccount = true, bool AutoCheck = true, bool ReducedEffects = false, DiagnosticOptions? Diagnostics = null);
public sealed record State(string Phase, string Message, double Progress, long Downloaded, long Total, bool CanLogin, bool HasUpdate, bool HasLauncherUpdate, string Source, string Version, Preferences Preferences, News[] News)
{
    public string LauncherVersion => LauncherEngine.VersionText;
    public string ClientVersion { get; init; } = "";
    public bool RequireQQBinding { get; init; }
    public DiagnosticStatus? Diagnostics { get; init; }
}
public sealed record Request(int Id, string Method, JsonElement Args);
public sealed record Response(int Id, bool Ok, string? Error = null, State? State = null, bool Restart = false, ConfigurationReset? ConfigurationReset = null, DiagnosticExport? DiagnosticExport = null, LoginReply? AccountResult = null, string? ErrorCode = null, long RetryAfter = 0);
public sealed record ConfigurationReset(bool Reset, string? BackupPath);
public sealed record Event(string Type, State State);
public sealed record Credentials(string Account, string Password);
public sealed record PasswordChange(string Account, string Password, string NewPassword);
public sealed record TransactionEntry(string Path, bool HadOriginal);
public sealed record TransactionJournal(string Phase, TransactionEntry[] Entries, string? PreviousReceipt);
public sealed record BundleJob(string InstallRoot, string Package, string Envelope, int UiPid, long UiStart, int CorePid, long CoreStart, string Work, string Phase);
public sealed record BundleOutcome(string Version, bool Succeeded, string Reason)
{
    public string Stage { get; init; } = "";
    public int NativeError { get; init; }
    public string FailedAtUtc { get; init; } = "";
}

[JsonSourceGenerationOptions(PropertyNamingPolicy = JsonKnownNamingPolicy.CamelCase, PropertyNameCaseInsensitive = true)]
[JsonSerializable(typeof(Bootstrap))]
[JsonSerializable(typeof(Preferences))]
[JsonSerializable(typeof(State))]
[JsonSerializable(typeof(Request))]
[JsonSerializable(typeof(Response))]
[JsonSerializable(typeof(Event))]
[JsonSerializable(typeof(Credentials))]
[JsonSerializable(typeof(PasswordChange))]
[JsonSerializable(typeof(LoginReply))]
[JsonSerializable(typeof(TransactionJournal))]
[JsonSerializable(typeof(BundleJob))]
[JsonSerializable(typeof(BundleOutcome))]
public partial class WireJson : JsonSerializerContext { }

public static class Trust
{
    public const int MaxMetadata = 8 * 1024 * 1024;
    public static bool Hash(string? hash) => hash is not null && Regex.IsMatch(hash, "^[0-9a-fA-F]{64}$");
    public static string Digest(byte[] raw) => Convert.ToHexString(SHA256.HashData(raw)).ToLowerInvariant();
    public static Bootstrap Verify(byte[] envelope, PlayerProfile root)
    {
        if (envelope.Length > MaxMetadata) throw new InvalidDataException("发布清单超过限制。");
        var e = JsonSerializer.Deserialize(envelope, CoreJson.Default.SignedEnvelope) ?? throw new InvalidDataException("发布清单无效。");
        var raw = Convert.FromBase64String(e.Payload);
        using var rsa = RSA.Create(); rsa.ImportFromPem(root.UpdatePublicKey);
        if (!rsa.VerifyData(raw, Convert.FromBase64String(e.Signature), HashAlgorithmName.SHA256, RSASignaturePadding.Pss)) throw new InvalidDataException("本服发布签名不匹配。");
        var b = JsonSerializer.Deserialize(raw, WireJson.Default.Bootstrap) ?? throw new InvalidDataException("发布清单无效。");
        if (b.Product != "DNF90-Flutter" || b.Format != 1 || b.Sequence < 1 || b.Profile is null || b.News is null || b.News.Length > 30)
            throw new InvalidDataException("发布产品、格式或序号无效。");
        // Download source changes never redirect account credentials or replace trust roots.
        if (b.Profile.ApiUrl.TrimEnd('/') != root.ApiUrl.TrimEnd('/') || b.Profile.UpdatePublicKey != root.UpdatePublicKey ||
            !Hash(b.Profile.CertificateSha256) || !Hash(b.Profile.ExeSha256) || !Hash(b.Profile.PvfSha256)) throw new InvalidDataException("账号入口或信任根发生不允许的变化。");
        _ = GameContract.Arguments("de509f65000000000000000000000000", b.Profile.GameHost, b.Profile.BootstrapPort);
        ChannelProbe.ValidateConfiguration(b.Profile);
        if (!string.IsNullOrEmpty(b.Profile.MinimumLauncherVersion) &&
            (!Regex.IsMatch(b.Profile.MinimumLauncherVersion, @"^\d+\.\d+\.\d+$") || !Version.TryParse(b.Profile.MinimumLauncherVersion, out _)))
            throw new InvalidDataException("最低登录器版本无效。");
        if (b.Profile.ChannelHistory is { Adapter.Length: > 0 } history &&
            !Regex.IsMatch(history.Adapter, "^[a-z0-9-]{1,64}$")) throw new InvalidDataException("频道兼容方案标识无效。");
        if (b.Content is { } c)
        {
            SignedUpdate.RequireHttps(c.Endpoint);
            var endpoint = new Uri(c.Endpoint);
            if (endpoint.AbsolutePath != "/" || endpoint.Query != "" || endpoint.Fragment != "" || (c.CertificateSha256 != "" && !Hash(c.CertificateSha256)) ||
                c.Files is null || c.Files.Length is < 2 or > 2000 || c.SourceSequence < 1 || c.Channel is not ("stable" or "preview") || !Hash(c.SourcePayloadSha256)) throw new InvalidDataException("内容源配置无效。");
            var names = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
            foreach (var file in c.Files)
            {
                SafeFiles.ValidateRelative(file.Path);
                var allowed = file.Path.Equals("DNF.exe", StringComparison.OrdinalIgnoreCase) || file.Path.Equals("Script.pvf", StringComparison.OrdinalIgnoreCase) ||
                    file.Path.Equals("dstr.dat", StringComparison.OrdinalIgnoreCase) || (file.Path.StartsWith("ImagePacks2/", StringComparison.OrdinalIgnoreCase) && file.Path.EndsWith(".npk", StringComparison.OrdinalIgnoreCase));
                if (!allowed || !names.Add(file.Path) || !Hash(file.Sha256) || file.Length is < 0 or > 8L * 1024 * 1024 * 1024 || file.Compression is not ("" or "none" or "gzip")) throw new InvalidDataException("内容文件路径、大小或格式无效。");
                if (file.Compression == "gzip" && (!Hash(file.BlobSha256) || file.BlobLength is < 1 or > 8L * 1024 * 1024 * 1024)) throw new InvalidDataException("压缩文件信息无效。");
            }
            foreach (var (name, hash) in new[] { ("DNF.exe", b.Profile.ExeSha256), ("Script.pvf", b.Profile.PvfSha256) })
                if (!c.Files.Any(f => f.Path.Equals(name, StringComparison.OrdinalIgnoreCase) && f.Sha256.Equals(hash, StringComparison.OrdinalIgnoreCase))) throw new InvalidDataException("内容与本服 EXE/PVF 兼容指纹不一致。");
        }
        if (b.Launcher is { } bundle)
        {
            SignedUpdate.RequireHttps(bundle.Url);
            if (!Version.TryParse(bundle.Version, out _) || !Hash(bundle.Sha256) || bundle.Size is < 1 or > SignedUpdate.MaxPackageBytes) throw new InvalidDataException("登录器发布信息无效。");
        }
        return b;
    }
    public static void CheckSequence(Bootstrap next, byte[] nextRaw, Bootstrap previous, byte[] previousRaw)
    {
        // Envelope signature can be randomized; compare exact signed payload bytes.
        static string Payload(byte[] raw) => JsonSerializer.Deserialize(raw, CoreJson.Default.SignedEnvelope)!.Payload;
        if (next.Sequence < previous.Sequence || next.Sequence == previous.Sequence && Payload(nextRaw) != Payload(previousRaw)) throw new InvalidDataException("发布序号倒退或同序号内容发生变化。");
        if (next.Content is { } a && previous.Content is { } b && a.SourceId == b.SourceId && a.Channel == b.Channel &&
            (a.SourceSequence < b.SourceSequence || a.SourceSequence == b.SourceSequence && a.SourcePayloadSha256 != b.SourcePayloadSha256)) throw new InvalidDataException("内容源序号倒退或内容冲突。");
    }
}
