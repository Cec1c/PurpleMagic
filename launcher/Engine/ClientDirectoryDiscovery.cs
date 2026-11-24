namespace Dnf90.Launcher;

internal enum ClientDirectoryResolutionKind
{
    Empty,
    Valid,
    AutoResolvedSubdirectory,
    NotFoundOrInvalid,
    AmbiguousSubdirectories,
}

internal sealed record ClientDirectoryResolution(
    ClientDirectoryResolutionKind Kind,
    string Path,
    int ScannedSubdirectories = 0,
    bool ScanTruncated = false,
    int MatchedSubdirectories = 0)
{
    internal bool IsValid => Kind is ClientDirectoryResolutionKind.Valid or ClientDirectoryResolutionKind.AutoResolvedSubdirectory;
}

internal static class ClientDirectoryDiscovery
{
    internal const string LaunchDirectoryVariable = "DNF90_LAUNCH_DIRECTORY";
    internal const int MaxSubdirectoryScan = 64;

    internal static string Find(string saved, string configured, string appDirectory, string? entryDirectory, string workingDirectory)
    {
        // An existing valid selection wins; an outer wrapper directory saved previously
        // auto-resolves to its unique valid 1-level client subdirectory.
        if (!string.IsNullOrWhiteSpace(saved))
        {
            var resolvedSaved = Resolve(saved);
            if (resolvedSaved.IsValid) return resolvedSaved.Path;
        }
        var app = Path.TrimEndingDirectorySeparator(appDirectory);
        var install = Path.GetFileName(app).Equals("app", StringComparison.OrdinalIgnoreCase) ? Path.GetDirectoryName(app) : null;
        foreach (var root in new[] { entryDirectory, install, appDirectory, workingDirectory, configured })
        {
            var selected = Inspect(root);
            if (selected != "") return selected;
            if (string.IsNullOrWhiteSpace(root)) continue;
            foreach (var child in new[] { ".client", "client" })
            {
                selected = Inspect(Path.Combine(root, child));
                if (selected != "") return selected;
            }
            var unique = FindUniqueChild(root, requireRunnableFiles: true, MaxSubdirectoryScan);
            if (unique.Kind == ClientDirectoryResolutionKind.AutoResolvedSubdirectory) return unique.Path;
        }
        return "";
    }

    internal static bool IsValidStructure(string? candidate)
    {
        var directory = NormalizeAccessibleDirectory(candidate);
        if (directory == "") return false;
        // Require real client resource layout that incremental patch downloads alone
        // never recreate: ImagePacks2 + SoundPacks + (Music or core native runtime DLLs).
        if (!HasRealDirectory(directory, "ImagePacks2") || !HasRealDirectory(directory, "SoundPacks")) return false;
        return HasRealDirectory(directory, "Music") ||
               (HasNonEmptyFile(directory, "fmodex.dll") && HasNonEmptyFile(directory, "ijl15.dll"));
    }

    internal static ClientDirectoryResolution Resolve(string? candidate, int maxSubdirectoryScan = MaxSubdirectoryScan)
    {
        if (string.IsNullOrWhiteSpace(candidate))
            return new(ClientDirectoryResolutionKind.Empty, "");
        var directory = NormalizeAccessibleDirectory(candidate);
        if (directory == "")
            return new(ClientDirectoryResolutionKind.NotFoundOrInvalid, "");
        if (IsValidStructure(directory))
            return new(ClientDirectoryResolutionKind.Valid, directory);
        return FindUniqueChild(directory, requireRunnableFiles: false, maxSubdirectoryScan);
    }

    internal static string DescribeInvalid(ClientDirectoryResolution resolution) => resolution.Kind switch
    {
        ClientDirectoryResolutionKind.Empty =>
            "请先点击「更改」选择包含完整游戏资源（如 ImagePacks2、SoundPacks）的客户端目录。",
        ClientDirectoryResolutionKind.AmbiguousSubdirectories =>
            "当前目录下发现多个游戏客户端子目录，无法自动确定目标；请点击「更改」进入具体的游戏客户端目录。",
        ClientDirectoryResolutionKind.NotFoundOrInvalid when resolution.ScanTruncated && resolution.MatchedSubdirectories > 0 =>
            $"当前目录下子目录数量超过自动探测上限（{resolution.ScannedSubdirectories} 个），无法确认目标唯一；请点击「更改」手动进入具体的游戏客户端目录。",
        ClientDirectoryResolutionKind.NotFoundOrInvalid when resolution.ScanTruncated =>
            $"所选目录不是完整的游戏客户端目录（缺少 ImagePacks2、SoundPacks 或 Music 等核心资源），已扫描前 {resolution.ScannedSubdirectories} 个子目录仍未找到；请点击「更改」手动选择正确的客户端目录。",
        _ =>
            "所选目录不是完整的游戏客户端目录（缺少 ImagePacks2、SoundPacks 或 Music 等核心资源），已拦截更新以防写入错误目录；请点击「更改」选择正确的客户端目录。"
    };

    private static ClientDirectoryResolution FindUniqueChild(string? parentCandidate, bool requireRunnableFiles, int maxSubdirectoryScan)
    {
        var parent = NormalizeAccessibleDirectory(parentCandidate);
        if (parent == "") return new(ClientDirectoryResolutionKind.NotFoundOrInvalid, "");
        var limit = Math.Max(0, maxSubdirectoryScan);
        var scanned = 0;
        var matchedCount = 0;
        var matchedPath = "";
        var truncated = false;
        try
        {
            var options = new EnumerationOptions
            {
                IgnoreInaccessible = true,
                AttributesToSkip = FileAttributes.ReparsePoint,
                RecurseSubdirectories = false,
            };
            foreach (var sub in Directory.EnumerateDirectories(parent, "*", options))
            {
                if (IsReparsePoint(sub)) continue;
                if (scanned >= limit)
                {
                    truncated = true;
                    break;
                }
                scanned++;
                var ok = requireRunnableFiles ? Inspect(sub) != "" : IsValidStructure(sub);
                if (!ok) continue;
                matchedCount++;
                if (matchedCount == 1) matchedPath = Path.TrimEndingDirectorySeparator(Path.GetFullPath(sub));
                else return new(ClientDirectoryResolutionKind.AmbiguousSubdirectories, "", scanned, false, matchedCount);
            }
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or ArgumentException or NotSupportedException or System.Security.SecurityException or InvalidDataException)
        {
            return new(ClientDirectoryResolutionKind.NotFoundOrInvalid, "", scanned, truncated, matchedCount);
        }
        return matchedCount == 1 && !truncated
            ? new(ClientDirectoryResolutionKind.AutoResolvedSubdirectory, matchedPath, scanned, false, 1)
            : new(ClientDirectoryResolutionKind.NotFoundOrInvalid, "", scanned, truncated, matchedCount);
    }

    private static string Inspect(string? candidate)
    {
        var directory = NormalizeAccessibleDirectory(candidate);
        if (directory == "" || !IsValidStructure(directory)) return "";
        // Automatic startup discovery requires non-empty baseline client files as well,
        // while still accepting older versions so they can be updated.
        return HasNonEmptyFile(directory, "DNF.exe") && HasNonEmptyFile(directory, "Script.pvf") ? directory : "";
    }

    private static string NormalizeAccessibleDirectory(string? candidate)
    {
        if (string.IsNullOrWhiteSpace(candidate) || !Path.IsPathFullyQualified(candidate) || candidate.StartsWith(@"\\", StringComparison.Ordinal))
            return "";
        try
        {
            var directory = Path.TrimEndingDirectorySeparator(Path.GetFullPath(candidate));
            SafeFiles.NoLinks(directory);
            return Directory.Exists(directory) ? directory : "";
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or ArgumentException or NotSupportedException or System.Security.SecurityException or InvalidDataException)
        {
            return "";
        }
    }

    private static bool IsReparsePoint(string path)
    {
        try { return (File.GetAttributes(path) & FileAttributes.ReparsePoint) != 0; }
        catch { return true; }
    }

    private static bool HasRealDirectory(string root, string child)
    {
        try
        {
            var path = Path.Combine(root, child);
            return Directory.Exists(path) && (File.GetAttributes(path) & FileAttributes.ReparsePoint) == 0;
        }
        catch { return false; }
    }

    private static bool HasNonEmptyFile(string root, string child)
    {
        try
        {
            var path = Path.Combine(root, child);
            return File.Exists(path) && (File.GetAttributes(path) & FileAttributes.ReparsePoint) == 0 && new FileInfo(path).Length > 0;
        }
        catch { return false; }
    }
}
