using System.Diagnostics;
using System.ComponentModel;
using System.IO.Compression;
using System.Text.Json;
using ServerS5A3;

namespace Dnf90.Launcher;

public static class BundleInstaller
{
    public const string Marker = "DNF90-Flutter-bundle-v1";
    internal static string OutcomeFile(string root) => Path.Combine(root, ".launcher-update-result.json");
    internal static string? PreviousFailure(string root)
    {
        var outcome = ReadOutcome(root);
        if (outcome is null || outcome.Succeeded) return null;
        var detail = outcome.Reason switch
        {
            "sharing-violation" => "运行文件被其他进程占用",
            "access-denied" => "运行目录访问被拒绝",
            "disk-full" => "磁盘空间不足",
            "files-in-use" => "旧版未记录具体原因的文件操作失败",
            "io-error" => "文件操作失败",
            _ => "新登录器启动检查失败"
        };
        var code = outcome.NativeError == 0 ? "" : $"（Windows 错误 {outcome.NativeError}）";
        return $"上次登录器更新失败：{detail}{code}，已保留旧版。点击“更新登录器”重试。";
    }
    internal static BundleOutcome? ReadOutcome(string root)
    {
        var file = OutcomeFile(root);
        SafeFiles.NoLinks(file);
        if (!File.Exists(file)) return null;
        return JsonSerializer.Deserialize(File.ReadAllBytes(file), WireJson.Default.BundleOutcome);
    }
    internal static bool SuppressAutomaticRetry(BundleOutcome? prior, string targetVersion) =>
        prior is { Succeeded: false } && prior.Reason == "startup-failed" && prior.Version == targetVersion;
    internal static int NativeError(Exception error) => error is Win32Exception native ? native.NativeErrorCode : error.HResult & 0xffff;
    internal static string FailureReason(Exception error) => NativeError(error) switch
    {
        32 or 33 when error is IOException or Win32Exception => "sharing-violation",
        5 when error is IOException or UnauthorizedAccessException or Win32Exception => "access-denied",
        39 or 112 when error is IOException or Win32Exception => "disk-full",
        _ when error is UnauthorizedAccessException => "access-denied",
        _ when error is IOException => "io-error",
        _ => "startup-failed"
    };
    private static void Outcome(BundleJob job, string version, Exception? error, string stage) =>
        SafeFiles.Atomic(OutcomeFile(job.InstallRoot), JsonSerializer.SerializeToUtf8Bytes(new BundleOutcome(version, error is null, error is null ? "" : FailureReason(error))
        { Stage = stage, NativeError = error is null ? 0 : NativeError(error), FailedAtUtc = error is null ? "" : DateTimeOffset.UtcNow.ToString("O") }, WireJson.Default.BundleOutcome));
    public static void ValidateRoot(string root)
    {
        SafeFiles.NoLinks(root);
        SafeFiles.NoLinks(Path.Combine(root, "launcher.id"));
        if (!Path.IsPathFullyQualified(root) || !File.Exists(Path.Combine(root, "launcher.id")) || File.ReadAllText(Path.Combine(root, "launcher.id")).Trim() != Marker)
            throw new InvalidDataException("当前不是支持整体更新的登录器安装目录。");
    }
    public static void Extract(string package, string destination)
    {
        SafeFiles.NoLinks(destination); Directory.CreateDirectory(destination);
        using var zip = ZipFile.OpenRead(package);
        if (zip.Entries.Count is < 5 or > 2000) throw new InvalidDataException("登录器更新包文件数无效。");
        var names = new HashSet<string>(StringComparer.OrdinalIgnoreCase); long total = 0;
        foreach (var entry in zip.Entries)
        {
            if (entry.FullName.EndsWith('/')) continue;
            var path = SafeFiles.Target(destination, entry.FullName);
            if (!names.Add(entry.FullName) || (entry.ExternalAttributes >> 16 & 0xf000) == 0xa000 || (entry.ExternalAttributes & 0x400) != 0 || entry.Length < 0 || (total += entry.Length) > 500L * 1024 * 1024)
                throw new InvalidDataException("登录器包包含重复路径、链接或超大文件。");
            Directory.CreateDirectory(Path.GetDirectoryName(path)!); entry.ExtractToFile(path, false);
        }
        foreach (var name in new[] { "DNF90.exe", "DNF90.Core.exe", "flutter_windows.dll", "data/app.so", "data/icudtl.dat" })
            if (!names.Contains(name)) throw new InvalidDataException("登录器包缺少必要文件。");
        foreach (var name in new[] { "DNF90.exe", "DNF90.Core.exe", "flutter_windows.dll" })
        { using var stream = File.OpenRead(Path.Combine(destination, name)); if (stream.ReadByte() != 'M' || stream.ReadByte() != 'Z') throw new InvalidDataException("更新包程序格式无效。"); }
    }
    private static void MoveDirectory(string from, string to)
    {
        var timer = Stopwatch.StartNew();
        while (true)
        {
            SafeFiles.NoLinks(from); SafeFiles.NoLinks(to);
            try { Directory.Move(from, to); return; }
            catch (IOException error) when ((error.HResult & 0xffff) is 32 or 33 && timer.Elapsed < TimeSpan.FromSeconds(20)) { Thread.Sleep(100); }
        }
    }
    private static void Save(BundleJob job) => SafeFiles.Atomic(Path.Combine(job.InstallRoot, ".launcher-update.json"), JsonSerializer.SerializeToUtf8Bytes(job, WireJson.Default.BundleJob));
    private static void ValidateJob(BundleJob job)
    {
        ValidateRoot(job.InstallRoot);
        var parent = Path.Combine(job.InstallRoot, ".updates") + Path.DirectorySeparatorChar;
        if (!Path.GetFullPath(job.Work).StartsWith(parent, StringComparison.OrdinalIgnoreCase) || Path.GetDirectoryName(job.Work) != parent.TrimEnd(Path.DirectorySeparatorChar)) throw new InvalidDataException("更新事务目录无效。");
        SafeFiles.NoLinks(job.Work);
    }
    public static void Recover(string root)
    {
        ValidateRoot(root); var journal = Path.Combine(root, ".launcher-update.json"); if (!File.Exists(journal)) return;
        var job = JsonSerializer.Deserialize(File.ReadAllBytes(journal), WireJson.Default.BundleJob) ?? throw new InvalidDataException(); ValidateJob(job);
        var app = Path.Combine(root, "app"); var backup = Path.Combine(job.Work, "previous");
        if (job.Phase != "healthy" && Directory.Exists(backup))
        {
            if (Directory.Exists(app)) MoveDirectory(app, Path.Combine(job.Work, "failed-" + Guid.NewGuid().ToString("N")));
            MoveDirectory(backup, app);
        }
        File.Delete(journal); // Keep prior/candidate artifacts; never delete user files recursively.
    }
    public static async Task<int> Apply(BundleJob job, PlayerProfile root)
    {
        ValidateJob(job);
        var raw = Convert.FromBase64String(job.Envelope); var release = Trust.Verify(raw, root).Launcher ?? throw new InvalidDataException("缺少登录器发布信息。");
        LauncherBuild.RequireFlavor(release.BuildFlavor);
        if (!await SafeFiles.Matches(job.Package, release.Size, release.Sha256)) throw new InvalidDataException("登录器包摘要不符。");
        var app = Path.Combine(job.InstallRoot, "app");
        await BundleProcesses.WaitForOwner(app, job.UiPid, job.UiStart);
        await BundleProcesses.WaitForOwner(app, job.CorePid, job.CoreStart);
        using var gate = new FileStream(Path.Combine(job.InstallRoot, ".launcher-install.lock"), FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None);
        if (File.Exists(Path.Combine(job.InstallRoot, ".launcher-update.json"))) Recover(job.InstallRoot);
        Process? launched = null;
        var stage = "exclusive-check";
        try
        {
            await BundleProcesses.StopInstalled(app);
            BundleProcesses.RequireExclusive(Path.Combine(job.InstallRoot, "app"));
            stage = "extract";
            var next = Path.Combine(job.Work, "next"); Extract(job.Package, next);
            RequireBuildFlavor(next, release.BuildFlavor);
            job = job with { Phase = "prepared" }; Save(job);
            stage = "move-old-app";
            MoveDirectory(Path.Combine(job.InstallRoot, "app"), Path.Combine(job.Work, "previous")); job = job with { Phase = "old-moved" }; Save(job);
            stage = "install-new-app";
            MoveDirectory(next, Path.Combine(job.InstallRoot, "app")); job = job with { Phase = "candidate-installed" }; Save(job);
            SafeFiles.NoLinks(OutcomeFile(job.InstallRoot)); File.Delete(OutcomeFile(job.InstallRoot));
            var health = Path.Combine(job.Work, "ready");
            var start = new ProcessStartInfo(Path.Combine(job.InstallRoot, "app", "DNF90.exe")) { UseShellExecute = false, WorkingDirectory = Path.Combine(job.InstallRoot, "app") };
            start.ArgumentList.Add("--update-health"); start.ArgumentList.Add(health);
            stage = "start-new-app";
            launched = Process.Start(start);
            stage = "wait-for-health";
            for (var i = 0; i < 600; i++)
            {
                if (File.Exists(health)) { Save(job with { Phase = "healthy" }); Outcome(job, release.Version, null, "healthy"); File.Delete(Path.Combine(job.InstallRoot, ".launcher-update.json")); return 0; }
                if (launched is null || launched.HasExited) break;
                await Task.Delay(100);
            }
            throw new InvalidOperationException("新登录器未返回就绪回执。");
        }
        catch (Exception error)
        {
            // Only this replacement UI may be terminated. No game processes are touched.
            if (launched is not null) await BundleProcesses.StopLaunched(launched);
            Recover(job.InstallRoot);
            Outcome(job, release.Version, error, stage);
            var fallback = new ProcessStartInfo(Path.Combine(job.InstallRoot, "app", "DNF90.exe")) { UseShellExecute = false, WorkingDirectory = Path.Combine(job.InstallRoot, "app") };
            // Only this fallback launch suppresses automatic retry. A later fresh
            // launch can retry a transient file failure without an endless loop.
            fallback.ArgumentList.Add("--update-retry-required");
            Process.Start(fallback);
            return 5;
        }
        finally { launched?.Dispose(); }
    }
    internal static void RequireBuildFlavor(string directory, string expected)
    {
        var file = Path.Combine(directory, "build-flavor.json"); SafeFiles.NoLinks(file);
        if (!File.Exists(file)) { if (expected != "standard") throw new InvalidDataException("保护更新包缺少构建标识。"); return; }
        if (new FileInfo(file).Length > 1024) throw new InvalidDataException("构建标识过大。");
        using var json = JsonDocument.Parse(File.ReadAllBytes(file));
        if (json.RootElement.GetProperty("format").GetInt32() != 1 || json.RootElement.GetProperty("flavor").GetString() != expected)
            throw new InvalidDataException("更新包构建标识不匹配。");
    }
}
