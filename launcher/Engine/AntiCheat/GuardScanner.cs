using System.ComponentModel;
using System.Diagnostics;
using System.Security.Cryptography;
using ServerS5A3;

namespace Dnf90.Launcher.AntiCheat;

internal sealed class GuardScanner(AntiCheatPolicy policy, string exeHash, Action<GuardEvent> emit)
{
    private readonly Dictionary<string, (long Size, long Stamp, long Checked, string Hash)> hashes = new(StringComparer.OrdinalIgnoreCase);
    private readonly HashSet<string> reported = new(StringComparer.Ordinal);
    private int processCursor;
    private int moduleCursor;
    private long lastMemoryScan;
    internal bool Blocked { get; private set; }
    private void Observe(GuardEvent value)
    {
        var key = value.Kind + ":" + value.Rule + ":" + value.Sha256;
        if (reported.Count < 512 && reported.Add(key)) emit(value);
    }
    private void Match(string scope, string hash)
    {
        foreach (var rule in policy.Rules.Where(r => r.Scope == scope && r.Sha256.Equals(hash, StringComparison.OrdinalIgnoreCase))) {
            Observe(new("rule_match", rule.Id, hash));
            if (policy.Mode == "enforce") Blocked = true;
        }
    }
    private string HashFile(string path, CancellationToken token)
    {
        var info = new FileInfo(path);
        if (info.Length > 512L * 1024 * 1024) throw new IOException();
        // Bound stale entries even when a file's timestamp/length was preserved.
        if (hashes.TryGetValue(path, out var prior) && prior.Size == info.Length && prior.Stamp == info.LastWriteTimeUtc.Ticks && Environment.TickCount64 - prior.Checked < 30000) return prior.Hash;
        using var file = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read | FileShare.Delete, 65536);
        using var hash = IncrementalHash.CreateHash(HashAlgorithmName.SHA256); var buffer = new byte[65536]; int read;
        while ((read = file.Read(buffer)) != 0) { token.ThrowIfCancellationRequested(); hash.AppendData(buffer, 0, read); }
        var value = Convert.ToHexString(hash.GetHashAndReset()).ToLowerInvariant();
        if (hashes.Count >= 1024) hashes.Clear();
        hashes[path] = (info.Length, info.LastWriteTimeUtc.Ticks, Environment.TickCount64, value); return value;
    }
    internal void Processes(int sessionId, CancellationToken token)
    {
        if (!policy.Rules.Any(r => r.Scope == "process")) return;
        var processes = Process.GetProcesses().OrderBy(p => p.Id).ToArray();
        using var budget = CancellationTokenSource.CreateLinkedTokenSource(token); budget.CancelAfter(1500);
        try {
            if (processes.Length == 0) return;
            for (var n = 0; n < processes.Length; n++) {
                if (budget.IsCancellationRequested) { Observe(new("coverage_limited", "process-budget")); break; }
                processCursor %= processes.Length;
                var p = processes[processCursor++];
                try { if (p.SessionId == sessionId) Match("process", HashFile(GuardNative.Image(p), budget.Token)); }
                catch (Exception e) when (e is Win32Exception or InvalidOperationException or IOException or UnauthorizedAccessException or OperationCanceledException) { Observe(new("coverage_limited", "process-read")); }
                if (Blocked) break;
            }
        } finally { foreach (var p in processes) p.Dispose(); }
    }
    internal void Game(Process game, CancellationToken token)
    {
        using var budget = CancellationTokenSource.CreateLinkedTokenSource(token); budget.CancelAfter(1500);
        try {
            game.Refresh();
            var modules = game.Modules;
            for (var n = 0; n < modules.Count; n++) {
                if (budget.IsCancellationRequested) { Observe(new("coverage_limited", "module-budget")); break; }
                moduleCursor %= modules.Count;
                var module = modules[moduleCursor++];
                try {
                    var hash = HashFile(module.FileName, budget.Token); Match("module", hash);
                    if (!hash.Equals(exeHash, StringComparison.OrdinalIgnoreCase) && !policy.AllowedModuleHashes.Contains(hash, StringComparer.OrdinalIgnoreCase) &&
                        !policy.RequiredFiles.Any(f => f.Sha256.Equals(hash, StringComparison.OrdinalIgnoreCase))) Observe(new("module_observed", Sha256: hash));
                } catch (Exception e) when (e is Win32Exception or IOException or UnauthorizedAccessException or OperationCanceledException) { Observe(new("coverage_limited", "module-read")); }
                if (Blocked) break;
            }
            var main = game.MainModule;
            if (main is not null) foreach (var region in policy.CodeRegions.Where(r => r.ExeSha256.Equals(exeHash, StringComparison.OrdinalIgnoreCase))) {
                if (region.Rva + region.Length > main.ModuleMemorySize) { Observe(new("coverage_limited", region.Id)); continue; }
                var actual = Convert.ToHexString(SHA256.HashData(GuardNative.Read(game, main.BaseAddress.ToInt64() + region.Rva, region.Length))).ToLowerInvariant();
                if (!actual.Equals(region.Sha256, StringComparison.OrdinalIgnoreCase)) Observe(new("code_region_changed", region.Id, actual));
            }
            if (Environment.TickCount64 - lastMemoryScan >= 20000) {
                lastMemoryScan = Environment.TickCount64;
                var count = GuardNative.PrivateExecutableRegions(game);
                if (count > 0) Observe(new("private_executable_regions", Count: count));
            }
        } catch (Exception e) when (e is Win32Exception or InvalidOperationException or IOException or UnauthorizedAccessException or OperationCanceledException) { Observe(new("coverage_limited", "game-read")); }
    }
}
