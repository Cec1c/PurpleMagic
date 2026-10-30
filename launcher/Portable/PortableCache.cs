using System.IO.Compression;
using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Serialization;
using System.Text.RegularExpressions;
using Dnf90.Launcher;

namespace Dnf90.Portable;

public sealed record FileDigest(long Size, string Sha256);
public sealed record Payload(string Version, long Size, string Sha256, Dictionary<string, FileDigest> Files)
{
    public string BuildFlavor { get; set; } = "standard";
}
public sealed record Seed(string Product, string PayloadSha256, string Version);
public sealed record Prepared(string Directory, bool Reused);

[JsonSourceGenerationOptions(PropertyNamingPolicy = JsonKnownNamingPolicy.CamelCase)]
[JsonSerializable(typeof(Payload))]
[JsonSerializable(typeof(Seed))]
[JsonSerializable(typeof(Prepared))]
public partial class PortableJson : JsonSerializerContext { }

public static class PortableCache
{
    public const string Marker = "DNF90-Flutter-bundle-v1";
    public const string Product = "DNF90-Portable-v1";
    private const long MaxSize = 500L << 20;

    private static bool Hash(string value) => Regex.IsMatch(value, "^[a-f0-9]{64}$");

    private static void ValidatePayload(Payload payload)
    {
        if (payload.BuildFlavor is not ("standard" or "anticheat") || !Hash(payload.Sha256) || payload.Size is <= 0 or > MaxSize ||
            !Regex.IsMatch(payload.Version, @"^\d+\.\d+\.\d+$") || payload.Files.Count is < 8 or > 2000)
            throw new InvalidDataException("便携包清单无效。");
        var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        long total = 0;
        foreach (var (name, file) in payload.Files)
        {
            SafeFiles.ValidateRelative(name);
            if (!seen.Add(name) || !Hash(file.Sha256) || file.Size < 0 || file.Size > MaxSize ||
                (total += file.Size) > MaxSize || name.StartsWith('.') ||
                !(name.StartsWith("app/", StringComparison.Ordinal) || name is "DNF90Launcher.exe" or "launcher.id" or "SHA256SUMS.txt" or "使用说明.txt"))
                throw new InvalidDataException("便携包包含不支持的文件。");
        }
        foreach (var name in new[] { "DNF90Launcher.exe", "launcher.id", "app/DNF90.exe", "app/DNF90.Core.exe", "app/flutter_windows.dll", "app/data/app.so", "app/data/icudtl.dat", "SHA256SUMS.txt" })
            if (!payload.Files.ContainsKey(name)) throw new InvalidDataException("便携包缺少运行文件。");
    }

    private static FileStream Gate(string path)
    {
        var deadline = Environment.TickCount64 + 30000;
        while (true)
        {
            SafeFiles.NoLinks(path);
            try { return new FileStream(path, FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None); }
            catch (IOException) when (Environment.TickCount64 < deadline) { Thread.Sleep(100); }
        }
    }

    private static void VerifyFile(string file, FileDigest expected)
    {
        SafeFiles.NoLinks(file);
        using var stream = File.OpenRead(file);
        if (stream.Length != expected.Size || !Convert.ToHexString(SHA256.HashData(stream)).Equals(expected.Sha256, StringComparison.OrdinalIgnoreCase))
            throw new InvalidDataException("便携入口文件损坏，请重新下载完整便携包。");
    }

    private static void ValidateInstalled(string directory, Payload payload)
    {
        SafeFiles.NoLinks(directory);
        var seedFile = Path.Combine(directory, ".portable.json");
        SafeFiles.NoLinks(seedFile);
        var seed = JsonSerializer.Deserialize(File.ReadAllBytes(seedFile), PortableJson.Default.Seed);
        if (seed?.Product != Product || seed.PayloadSha256 != payload.Sha256)
            throw new InvalidDataException("该目录不是此便携包创建的，已保留原文件。");
        using var gate = Gate(Path.Combine(directory, ".launcher-install.lock"));
        VerifyFile(Path.Combine(directory, "launcher.id"), payload.Files["launcher.id"]);
        VerifyFile(Path.Combine(directory, "DNF90Launcher.exe"), payload.Files["DNF90Launcher.exe"]);
        // app is intentionally mutable: signed self-updates own it. Never compare
        // it with the original seed or overwrite a newer app on subsequent runs.
        var journal = Path.Combine(directory, ".launcher-update.json");
        SafeFiles.NoLinks(journal);
        if (!File.Exists(journal))
            foreach (var name in new[] { "app/DNF90.exe", "app/DNF90.Core.exe" })
                if (!File.Exists(SafeFiles.Target(directory, name)))
                    throw new InvalidDataException("已释放的运行文件不完整，已停止启动并保留现有更新数据。");
    }

    public static Prepared Prepare(Stream archive, Payload payload, string cacheRoot)
    {
        ValidatePayload(payload);
        cacheRoot = Path.GetFullPath(cacheRoot);
        SafeFiles.NoLinks(cacheRoot);
        Directory.CreateDirectory(cacheRoot);
        using var gate = Gate(Path.Combine(cacheRoot, "." + payload.Sha256 + ".prepare.lock"));
        var destination = Path.Combine(cacheRoot, payload.Sha256);
        SafeFiles.NoLinks(destination);
        if (Directory.Exists(destination))
        {
            ValidateInstalled(destination, payload);
            return new(destination, true);
        }
        if (File.Exists(destination)) throw new InvalidDataException("便携目录被其他文件占用。");
        if (!archive.CanSeek || archive.Length != payload.Size)
            throw new InvalidDataException("便携包长度不符。");
        archive.Position = 0;
        if (!Convert.ToHexString(SHA256.HashData(archive)).Equals(payload.Sha256, StringComparison.OrdinalIgnoreCase))
            throw new InvalidDataException("便携包校验失败，请重新下载。");
        archive.Position = 0;
        using var zip = new ZipArchive(archive, ZipArchiveMode.Read, true);
        if (zip.Entries.Count != payload.Files.Count) throw new InvalidDataException("便携包文件清单不符。");
        var stage = destination + ".stage-" + Guid.NewGuid().ToString("N");
        SafeFiles.NoLinks(stage);
        Directory.CreateDirectory(stage);
        var names = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        foreach (var entry in zip.Entries)
        {
            var target = SafeFiles.Target(stage, entry.FullName);
            if (!names.Add(entry.FullName) || !payload.Files.TryGetValue(entry.FullName, out var expected) ||
                entry.Length != expected.Size || (entry.ExternalAttributes >> 16 & 0xf000) == 0xa000 || (entry.ExternalAttributes & 0x400) != 0)
                throw new InvalidDataException("便携包文件条目无效。");
            Directory.CreateDirectory(Path.GetDirectoryName(target)!);
            using var input = entry.Open();
            using (var output = new FileStream(target, FileMode.CreateNew, FileAccess.Write, FileShare.None))
            {
                var buffer = new byte[65536]; long written = 0; int count;
                while ((count = input.Read(buffer)) != 0)
                {
                    if ((written += count) > expected.Size) throw new InvalidDataException("便携包文件大小超出清单。");
                    output.Write(buffer, 0, count);
                }
                output.Flush(true);
            }
            VerifyFile(target, expected);
        }
        if (File.ReadAllText(Path.Combine(stage, "launcher.id")).Trim() != Marker)
            throw new InvalidDataException("便携包的安装标识无效。");
        var seed = new Seed(Product, payload.Sha256, payload.Version);
        SafeFiles.Atomic(Path.Combine(stage, ".portable.json"), JsonSerializer.SerializeToUtf8Bytes(seed, PortableJson.Default.Seed));
        // Same-volume rename publishes a complete installation. Incomplete stages
        // from interruption are never used, overwritten or recursively deleted.
        Directory.Move(stage, destination);
        return new(destination, false);
    }
}
