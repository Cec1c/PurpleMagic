using System.IO.Compression;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace Dnf90.Launcher.Diagnostics;

internal sealed record DiagnosticLine(byte[] Bytes, double Time, bool Full, string Type)
{ public int Cost => Bytes.Length + 128; }

internal sealed class DiagnosticRing(int capacity = 3 * 1024 * 1024)
{
    private readonly Queue<DiagnosticLine> lines = [];
    public long Overwritten { get; private set; }
    public int Bytes { get; private set; }
    public int Count => lines.Count;
    public void Add(DiagnosticLine line)
    {
        if (line.Cost > capacity) { Overwritten++; return; }
        lines.Enqueue(line); Bytes += line.Cost;
        while (lines.Count > 0 && (Bytes > capacity || line.Time - lines.Peek().Time > 120000)) { Bytes -= lines.Dequeue().Cost; Overwritten++; }
        // Every retained delta chain has a retained full snapshot.
        while (lines.Count > 0 && lines.Peek().Type == "sample" && !lines.Peek().Full) { Bytes -= lines.Dequeue().Cost; Overwritten++; }
    }
    public DiagnosticLine[] Snapshot()
    {
        var result = new List<DiagnosticLine>(); var hasFull = false;
        foreach (var line in lines)
        {
            if (line.Type == "sample") { if (line.Full) hasFull = true; if (!hasFull) continue; }
            result.Add(line);
        }
        return [.. result];
    }
}

internal sealed record DiagnosticSave(DiagnosticSummary Summary, DiagnosticLine[] Lines, string StartedUtc, string EndedUtc);

internal sealed class DiagnosticStorage
{
    public const int MaxZip = 10 * 1024 * 1024;
    private readonly string root;
    private readonly object gate = new();
    public DiagnosticStorage(string directory) { root = directory; }
    private void EnsureRoot() { SafeFiles.NoLinks(root); Directory.CreateDirectory(root); }
    private string FileFor(string id)
    {
        if (!Guid.TryParseExact(id, "N", out _)) throw new InvalidDataException("诊断片段编号无效。");
        return Path.Combine(root, id + ".zip");
    }
    internal void Cleanup()
    {
        EnsureRoot();
        foreach (var partial in new DirectoryInfo(root).EnumerateFiles("*.zip.writing"))
            if (Guid.TryParseExact(partial.Name[..^12], "N", out _) && (partial.Attributes & FileAttributes.ReparsePoint) == 0) partial.Delete();
        var files = new DirectoryInfo(root).EnumerateFiles("*.zip").Where(f => Guid.TryParseExact(Path.GetFileNameWithoutExtension(f.Name), "N", out _) && (f.Attributes & FileAttributes.ReparsePoint) == 0).OrderByDescending(f => f.LastWriteTimeUtc).ToArray();
        long size = 0;
        for (var i = 0; i < files.Length; i++) { size += files[i].Length; if (i >= 10 || size > 100L * 1024 * 1024 || files[i].LastWriteTimeUtc < DateTime.UtcNow.AddDays(-7)) files[i].Delete(); }
    }
    public DiagnosticClip[] List()
    {
        lock (gate)
        {
            Cleanup(); var result = new List<DiagnosticClip>();
            foreach (var path in Directory.EnumerateFiles(root, "*.zip").Take(20))
            {
                try
                {
                    SafeFiles.NoLinks(path); using var z = ZipFile.OpenRead(path); var entry = z.GetEntry("manifest.json");
                    if (entry is null || entry.Length > 32768) continue;
                    using var source = entry.Open(); var m = JsonSerializer.Deserialize(source, DiagnosticJson.Default.DiagnosticManifest);
                    if (m is null || Path.GetFileNameWithoutExtension(path) != m.IncidentId) continue;
                    var summaryEntry = z.GetEntry("summary.json");
                    if (summaryEntry is null || summaryEntry.Length > 32768) continue;
                    using var summaryStream = summaryEntry.Open();
                    var summary = JsonSerializer.Deserialize(summaryStream, DiagnosticJson.Default.DiagnosticSummary);
                    result.Add(new(m.IncidentId, m.SessionId, m.StartedUtc, m.EndedUtc, m.EndReason, new FileInfo(path).Length, summary?.Categories ?? []));
                }
                catch (Exception e) when (e is IOException or JsonException or InvalidDataException or UnauthorizedAccessException) { }
            }
            return [.. result.OrderByDescending(x => x.StartedUtc)];
        }
    }
    public void Save(DiagnosticSave job)
    {
        lock (gate)
        {
            EnsureRoot();
            var path = FileFor(job.Summary.IncidentId); var temp = path + ".writing"; SafeFiles.NoLinks(temp);
            if (File.Exists(temp)) File.Delete(temp); // Complete retry of this GUID after an interrupted save.
            try
            {
                using (var file = new FileStream(temp, FileMode.CreateNew, FileAccess.ReadWrite, FileShare.None))
                using (var zip = new ZipArchive(file, ZipArchiveMode.Create))
                {
                    var entries = new List<DiagnosticFile>();
                    Add(zip, "summary.json", JsonSerializer.SerializeToUtf8Bytes(job.Summary, DiagnosticJson.Default.DiagnosticSummary), entries);
                    AddLines(zip, "session.jsonl", job.Lines.Where(x => x.Type == "identity"), entries);
                    var prefix = "incidents/" + job.Summary.IncidentId + "/";
                    AddLines(zip, prefix + "events.jsonl", job.Lines.Where(x => x.Type != "sample" && x.Type != "identity"), entries);
                    AddLines(zip, prefix + "samples.jsonl", job.Lines.Where(x => x.Type == "sample"), entries);
                    var manifest = new DiagnosticManifest(1, LauncherEngine.VersionText, At5tModule.ModuleVersion, job.Summary.SessionId, job.Summary.IncidentId, job.StartedUtc, job.EndedUtc, job.Summary.EndReason, [.. entries]);
                    Add(zip, "manifest.json", JsonSerializer.SerializeToUtf8Bytes(manifest, DiagnosticJson.Default.DiagnosticManifest), []);
                }
                if (new FileInfo(temp).Length > MaxZip) throw new IOException("diagnostic_zip_budget");
                File.Move(temp, path, false); Cleanup();
            }
            finally { if (File.Exists(temp)) File.Delete(temp); }
        }
    }
    private static void Add(ZipArchive z, string name, byte[] bytes, List<DiagnosticFile> files)
    { using var dest = z.CreateEntry(name, CompressionLevel.Fastest).Open(); dest.Write(bytes); files.Add(new(name, bytes.Length, Convert.ToHexString(SHA256.HashData(bytes)).ToLowerInvariant())); }
    private static void AddLines(ZipArchive z, string name, IEnumerable<DiagnosticLine> lines, List<DiagnosticFile> files)
    {
        using var dest = z.CreateEntry(name, CompressionLevel.Fastest).Open(); using var hash = IncrementalHash.CreateHash(HashAlgorithmName.SHA256); long size = 0;
        foreach (var line in lines) { dest.Write(line.Bytes); hash.AppendData(line.Bytes); size += line.Bytes.Length; }
        files.Add(new(name, size, Convert.ToHexString(hash.GetHashAndReset()).ToLowerInvariant()));
    }
    public DiagnosticExport Export(string sessionId, string[] ids, string directory)
    {
        lock (gate)
        {
            if (ids.Length is < 1 or > 10 || ids.Distinct().Count() != ids.Length) throw new InvalidDataException("请选择1至10个诊断片段。");
            var clips = List().Where(x => ids.Contains(x.Id)).ToArray();
            if (clips.Length != ids.Length || clips.Any(x => x.SessionId != sessionId)) throw new InvalidDataException("所选片段需属于同一游戏会话。");
            directory = Path.GetFullPath(directory); SafeFiles.NoLinks(directory);
            if (!Directory.Exists(directory) || directory.StartsWith(Path.GetFullPath(root), StringComparison.OrdinalIgnoreCase)) throw new InvalidDataException("请选择独立导出目录。");
            var target = Path.Combine(directory, "PurpleMagic-diagnostics-" + DateTime.Now.ToString("yyyyMMdd-HHmmss") + "-" + Guid.NewGuid().ToString("N")[..8] + ".zip");
            // Use uncompressed entry sizes for a conservative, deterministic cap.
            var keep = new List<(DiagnosticClip Clip, DiagnosticManifest Manifest)>(); var skipped = new List<string>(); long bytes = 0;
            foreach (var c in clips.OrderByDescending(x => x.StartedUtc)) {
                SafeFiles.NoLinks(FileFor(c.Id));
                using var source = ZipFile.OpenRead(FileFor(c.Id));
                var e = source.GetEntry("manifest.json") ?? throw new InvalidDataException("片段缺少清单。");
                if (e.Length > 32768) throw new InvalidDataException("诊断清单过大。");
                using var stream = e.Open();
                var m = JsonSerializer.Deserialize(stream, DiagnosticJson.Default.DiagnosticManifest) ?? throw new InvalidDataException();
                var names = new[] { "summary.json", "session.jsonl", "incidents/" + c.Id + "/events.jsonl", "incidents/" + c.Id + "/samples.jsonl" };
                if (m.SessionId != sessionId || m.IncidentId != c.Id || m.Files.Length != 4 || m.Files.Select(f => f.Path).Distinct().Count() != 4 || m.Files.Any(f => !names.Contains(f.Path) || f.Bytes < 0 || f.Bytes > 4 * 1024 * 1024)) throw new InvalidDataException("诊断清单格式无效。");
                var size = m.Files.Sum(f => f.Bytes);
                if (bytes + size <= MaxZip - 128 * 1024) { keep.Add((c, m)); bytes += size; } else skipped.Add(c.Id);
            }
            if (keep.Count == 0) throw new InvalidDataException("片段超过导出预算。");
            var temp = target + ".writing";
            try
            {
                using (var file = new FileStream(temp, FileMode.CreateNew, FileAccess.ReadWrite, FileShare.None))
                using (var zip = new ZipArchive(file, ZipArchiveMode.Create))
                {
                    var index = new List<DiagnosticFile>();
                    var summaries = new List<DiagnosticSummary>();
                    foreach (var (c, m) in keep)
                    {
                        using var source = ZipFile.OpenRead(FileFor(c.Id));
                        foreach (var entry in m.Files)
                        {
                            var sourceEntry = source.GetEntry(entry.Path) ?? throw new InvalidDataException("诊断包缺少记录。");
                            if (sourceEntry.Length != entry.Bytes) throw new InvalidDataException("诊断文件大小不匹配。");
                            var name = entry.Path == "summary.json" ? "incidents/" + c.Id + "/summary.json" : entry.Path;
                            var duplicate = name == "session.jsonl" && index.Any(f => f.Path == name);
                            using var input = sourceEntry.Open();
                            using var output = duplicate ? Stream.Null : zip.CreateEntry(name, CompressionLevel.Fastest).Open();
                            using var hash = IncrementalHash.CreateHash(HashAlgorithmName.SHA256);
                            var buffer = new byte[8192]; int read; long actual = 0;
                            while ((read = input.Read(buffer)) > 0) { actual += read; if (actual > entry.Bytes) throw new InvalidDataException("诊断文件过大。"); hash.AppendData(buffer, 0, read); output.Write(buffer, 0, read); }
                            if (actual != entry.Bytes || Convert.ToHexString(hash.GetHashAndReset()).ToLowerInvariant() != entry.Sha256) throw new InvalidDataException("诊断文件校验失败。");
                            if (!duplicate) index.Add(entry with { Path = name });
                        }
                        var se = source.GetEntry("summary.json")!;
                        if (se.Length > 32768) throw new InvalidDataException("诊断摘要过大。");
                        using var summaryStream = se.Open(); summaries.Add(JsonSerializer.Deserialize(summaryStream, DiagnosticJson.Default.DiagnosticSummary)!);
                    }
                    var summary = new DiagnosticExportSummary(sessionId, [.. summaries], [.. skipped], skipped.Count > 0 ? "export_budget" : "user_export");
                    Add(zip, "summary.json", JsonSerializer.SerializeToUtf8Bytes(summary, DiagnosticJson.Default.DiagnosticExportSummary), index);
                    var manifest = new DiagnosticManifest(1, LauncherEngine.VersionText, At5tModule.ModuleVersion, sessionId, "export", keep.Min(x => x.Clip.StartedUtc)!, keep.Max(x => x.Clip.EndedUtc)!, summary.EndReason, [.. index]);
                    Add(zip, "manifest.json", JsonSerializer.SerializeToUtf8Bytes(manifest, DiagnosticJson.Default.DiagnosticManifest), []);
                }
                if (new FileInfo(temp).Length > MaxZip) throw new IOException("diagnostic_zip_budget");
                File.Move(temp, target, false);
                using var result = File.OpenRead(target); return new(target, result.Length, Convert.ToHexString(SHA256.HashData(result)).ToLowerInvariant(), keep.Count);
            }
            finally { if (File.Exists(temp)) File.Delete(temp); }
        }
    }
}
