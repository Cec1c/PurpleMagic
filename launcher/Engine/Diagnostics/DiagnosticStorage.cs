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

internal sealed record DiagnosticSave(DiagnosticSummary Summary, DiagnosticLine[] Lines, string StartedUtc, string EndedUtc)
{ internal TaskCompletionSource? Completion { get; init; } internal bool CriticalTail { get; init; } }

internal sealed record DiagnosticLogIndex(DiagnosticSummary Summary, string StartedUtc, string EndedUtc, long LastSequence, double LastTime, long Bytes, int SchemaVersion = 3);
internal sealed record DiagnosticLogEnvelope(string Type, int SchemaVersion, string CollectorVersion, string ModuleVersion, DiagnosticLogIndex Log);

internal sealed class DiagnosticStorage
{
    public const int MaxLog = 10 * 1024 * 1024;
    private readonly string root;
    private readonly object gate = new();
    public DiagnosticStorage(string directory) { root = Path.GetFullPath(directory); }
    private void EnsureRoot() { SafeFiles.NoLinks(root); Directory.CreateDirectory(root); }
    private string FileFor(string id, string extension = ".log")
    {
        if (!Guid.TryParseExact(id, "N", out _)) throw new InvalidDataException("日志编号无效。");
        var path = Path.Combine(root, id + extension); SafeFiles.NoLinks(path); return path;
    }
    public DiagnosticLogIndex? ReadIndex(string id)
    {
        lock (gate) {
            var path = FileFor(id, ".json");
            if (!File.Exists(path)) return null;
            if (new FileInfo(path).Length > 65536) throw new InvalidDataException("日志索引无效。");
            var index = JsonSerializer.Deserialize(File.ReadAllBytes(path), DiagnosticJson.Default.DiagnosticLogIndex);
            if (index is null || index.Summary.SessionId != id) throw new InvalidDataException("日志索引无效。");
            return index;
        }
    }
    private DiagnosticLogIndex[] Indexes()
    {
        EnsureRoot(); var rows = new List<DiagnosticLogIndex>();
        foreach (var path in Directory.EnumerateFiles(root, "*.json")) {
            var id = Path.GetFileNameWithoutExtension(path);
            if (!Guid.TryParseExact(id, "N", out _)) continue;
            try { var item = ReadIndex(id); if (item is not null && File.Exists(FileFor(id))) rows.Add(item); }
            catch (Exception e) when (e is IOException or JsonException or UnauthorizedAccessException) { }
        }
        return [.. rows.OrderByDescending(r => r.StartedUtc).ThenByDescending(r => r.Summary.SessionId)];
    }
    internal void Cleanup()
    {
        lock (gate) foreach (var row in Indexes().Skip(5)) {
            File.Delete(FileFor(row.Summary.SessionId)); File.Delete(FileFor(row.Summary.SessionId, ".json"));
        }
    }
    public DiagnosticClip[] List()
    {
        lock (gate) {
            Cleanup();
            return [.. Indexes().Take(5).Select(r => new DiagnosticClip(r.Summary.SessionId, r.Summary.SessionId, r.StartedUtc, r.EndedUtc,
                r.Summary.EndReason, new FileInfo(FileFor(r.Summary.SessionId)).Length, r.Summary.Categories))];
        }
    }
    private static byte[] Envelope(string type, DiagnosticLogIndex index) =>
        [.. JsonSerializer.SerializeToUtf8Bytes(new DiagnosticLogEnvelope(type, index.SchemaVersion, LauncherEngine.VersionText, At5tModule.ModuleVersion, index), DiagnosticJson.Default.DiagnosticLogEnvelope), 10];
    public void Save(DiagnosticSave job)
    {
        lock (gate) {
            EnsureRoot(); var id = job.Summary.SessionId; var previous = ReadIndex(id);
            if (previous is not null && previous.SchemaVersion != 4) throw new InvalidDataException("旧格式日志已封存，请使用新日志编号。");
            var rows = Indexes();
            // A still-running old instance must not displace a newer launch.
            if (previous is null && rows.Length >= 5 && string.CompareOrdinal(job.StartedUtc, rows[4].StartedUtc) < 0) return;
            var index = previous ?? new(job.Summary, job.StartedUtc, job.EndedUtc, 0, 0, 0, 4);
            var path = FileFor(id);
            using (var file = new FileStream(path, FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.Read)) {
                // Roll back a failed append to the last durable index. No partial JSON line survives.
                if (file.Length < index.Bytes) throw new InvalidDataException("日志文件长度不匹配。");
                file.SetLength(index.Bytes); file.Position = index.Bytes;
                if (index.Bytes == 0) file.Write(Envelope("log_header", index));
                var truncated = index.Summary.Truncated; var records = index.Summary.Records;
                if (previous is null) records = 0;
                foreach (var line in job.Lines) {
                    var record = JsonSerializer.Deserialize(line.Bytes, DiagnosticJson.Default.DiagnosticRecord) ?? throw new InvalidDataException("日志记录无效。");
                    if (record.SessionId != id) throw new InvalidDataException("日志会话不匹配。");
                    if (record.RecordVersion != 3) throw new InvalidDataException("日志记录版本不匹配。");
                    if (record.Sequence <= index.LastSequence) continue;
                    // Reserve space for exception evidence and its recent operation tail,
                    // even after ordinary samples have filled the file.
                    var fatal = record.Type == "exception" && record.Fields.GetValueOrDefault("first_chance")?.Value == 0;
                    var exit = record.Reason == "game_exit";
                    var critical = fatal || exit || record.Type == "exception" || record.Evidence?.Kind == "managed_exception" || job.CriticalTail && record.Type != "sample";
                    var limit = MaxLog - (exit ? 32768 : fatal ? 65536 : critical ? 256 * 1024 : 512 * 1024);
                    if ((!critical && truncated) || file.Position + line.Bytes.Length > limit) { truncated = true; continue; }
                    file.Write(line.Bytes); records++;
                    index = index with { LastSequence = record.Sequence, LastTime = record.MonotonicMs };
                }
                file.Flush(true);
                index = index with { Summary = job.Summary with { Records = records, Truncated = truncated || job.Summary.Truncated }, EndedUtc = job.EndedUtc, Bytes = file.Length };
            }
            SafeFiles.Atomic(FileFor(id, ".json"), JsonSerializer.SerializeToUtf8Bytes(index, DiagnosticJson.Default.DiagnosticLogIndex));
            Cleanup();
        }
    }
    public DiagnosticExport Export(string sessionId, string target)
    {
        lock (gate) {
            var index = ReadIndex(sessionId) ?? throw new InvalidDataException("这份日志已清理，请选择最近的日志。");
            target = Path.GetFullPath(target);
            if (!Path.GetExtension(target).Equals(".log", StringComparison.OrdinalIgnoreCase)) throw new InvalidDataException("请保存为 .log 文件。");
            SafeFiles.NoLinks(target);
            var parent = Path.GetDirectoryName(target)!;
            var relative = Path.GetRelativePath(root, target);
            if (!Directory.Exists(parent) || !relative.StartsWith(".." + Path.DirectorySeparatorChar, StringComparison.Ordinal) && !Path.IsPathRooted(relative))
                throw new InvalidDataException("请选择日志存储区以外的保存位置。");
            var temporary = Path.Combine(parent, ".purplemagic-" + Guid.NewGuid().ToString("N") + ".writing");
            try {
                using (var source = new FileStream(FileFor(sessionId), FileMode.Open, FileAccess.Read, FileShare.Read))
                using (var dest = new FileStream(temporary, FileMode.CreateNew, FileAccess.Write, FileShare.None)) {
                    if (source.Length < index.Bytes || source.Length > MaxLog || index.Bytes > MaxLog) throw new InvalidDataException("日志文件长度不匹配。");
                    // Validate complete UTF-8 JSON lines before replacing a Save As destination.
                    using var reader = new StreamReader(source, new UTF8Encoding(false, true), false, 65536, true);
                    long bytes = 0;
                    while (reader.ReadLine() is { } line) {
                        var raw = Encoding.UTF8.GetBytes(line + "\n");
                        if (bytes + raw.Length > index.Bytes) break;
                        using var parsed = JsonDocument.Parse(line);
                        dest.Write(raw); bytes += raw.Length;
                        if (bytes == index.Bytes) break;
                    }
                    if (bytes != index.Bytes) throw new InvalidDataException("日志记录不完整。");
                    dest.Write(Envelope("log_summary", index)); dest.Flush(true);
                }
                using var input = File.OpenRead(temporary);
                var bytesWritten = input.Length; var hash = Convert.ToHexString(SHA256.HashData(input)).ToLowerInvariant(); input.Close();
                SafeFiles.NoLinks(target); File.Move(temporary, target, true);
                return new(target, bytesWritten, hash, 1);
            } finally { if (File.Exists(temporary)) File.Delete(temporary); }
        }
    }
}
