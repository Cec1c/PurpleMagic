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
    private const int RollTrigger = MaxLog - 256 * 1024;
    private const int RollTarget = 6 * 1024 * 1024;
    private readonly string root;
    private readonly object gate = new();
    public DiagnosticStorage(string directory) { root = Path.GetFullPath(directory); }
    private void EnsureRoot() { SafeFiles.NoLinks(root); Directory.CreateDirectory(root); }
    private string FileFor(string id, string extension = ".log")
    {
        if (!Guid.TryParseExact(id, "N", out _)) throw new InvalidDataException("日志编号无效。");
        var path = Path.Combine(root, id + extension); SafeFiles.NoLinks(path); return path;
    }
    private void RecoverPendingRoll(string id)
    {
        var logPath = FileFor(id);
        var jsonPath = FileFor(id, ".json");
        var logRoll = FileFor(id, ".log.roll");
        var jsonRoll = FileFor(id, ".json.roll");
        if (File.Exists(jsonRoll)) {
            if (File.Exists(logRoll)) File.Move(logRoll, logPath, true);
            File.Move(jsonRoll, jsonPath, true);
        } else if (File.Exists(logRoll)) {
            File.Delete(logRoll);
        }
    }
    public DiagnosticLogIndex? ReadIndex(string id)
    {
        lock (gate) {
            RecoverPendingRoll(id);
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
            var sid = row.Summary.SessionId;
            File.Delete(FileFor(sid)); File.Delete(FileFor(sid, ".json"));
            File.Delete(FileFor(sid, ".log.roll")); File.Delete(FileFor(sid, ".json.roll"));
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
            if (previous is not null && previous.SchemaVersion != 5) throw new InvalidDataException("旧格式日志已封存，请使用新日志编号。");
            var rows = previous is null ? Indexes() : [];
            // A still-running old instance must not displace a newer launch.
            if (previous is null && rows.Length >= 5 && string.CompareOrdinal(job.StartedUtc, rows[4].StartedUtc) < 0) return;
            var index = previous ?? new(job.Summary, job.StartedUtc, job.EndedUtc, 0, 0, 0, 5);
            var path = FileFor(id);
            var flushToDisk = job.CriticalTail || job.Summary.EndReason != "recording";
            var truncated = index.Summary.Truncated; var records = previous is null ? 0 : index.Summary.Records;
            var needRoll = false;
            var pending = new List<(byte[] Bytes, long Sequence, double MonotonicMs)>(job.Lines.Length);
            long pendingBytes = 0;
            var lastSeq = index.LastSequence;
            foreach (var line in job.Lines) {
                var record = JsonSerializer.Deserialize(line.Bytes, DiagnosticJson.Default.DiagnosticRecord) ?? throw new InvalidDataException("日志记录无效。");
                if (record.SessionId != id) throw new InvalidDataException("日志会话不匹配。");
                if (record.RecordVersion != 4) throw new InvalidDataException("日志记录版本不匹配。");
                if (record.Sequence <= lastSeq) continue;
                lastSeq = record.Sequence;
                pending.Add((line.Bytes, record.Sequence, record.MonotonicMs));
                pendingBytes += line.Bytes.Length;
            }
            var estimatedSize = (index.Bytes == 0 ? Envelope("log_header", index).Length : index.Bytes) + pendingBytes;
            if (estimatedSize > RollTrigger) needRoll = true;
            if (!needRoll) {
                using var file = new FileStream(path, FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.Read);
                // Roll back a failed append to the last durable index. No partial JSON line survives.
                if (file.Length < index.Bytes) throw new InvalidDataException("日志文件长度不匹配。");
                file.SetLength(index.Bytes); file.Position = index.Bytes;
                if (index.Bytes == 0) file.Write(Envelope("log_header", index));
                foreach (var item in pending) {
                    file.Write(item.Bytes); records++;
                    index = index with { LastSequence = item.Sequence, LastTime = item.MonotonicMs };
                }
                if (flushToDisk) file.Flush(true);
                index = index with { Summary = job.Summary with { Records = records, Truncated = truncated || job.Summary.Truncated }, EndedUtc = job.EndedUtc, Bytes = file.Length };
            } else {
                // Tail-rolling compaction: keep log_header + newest ~6 MiB tail lines so late disconnects/crashes are never lost.
                var existing = new List<byte[]>();
                byte[]? header = null;
                if (index.Bytes > 0 && File.Exists(path)) {
                    using var source = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read);
                    if (source.Length < index.Bytes) throw new InvalidDataException("日志文件长度不匹配。");
                    var raw = new byte[index.Bytes];
                    source.ReadExactly(raw, 0, raw.Length);
                    var start = 0;
                    for (var i = 0; i < raw.Length; i++) {
                        if (raw[i] != 10) continue;
                        var slice = raw.AsSpan(start, i - start + 1).ToArray();
                        start = i + 1;
                        if (header is null) header = slice;
                        else existing.Add(slice);
                    }
                }
                header ??= Envelope("log_header", index);
                var combined = new List<byte[]>(existing.Count + pending.Count);
                combined.AddRange(existing);
                foreach (var item in pending) {
                    combined.Add(item.Bytes); records++;
                    index = index with { LastSequence = item.Sequence, LastTime = item.MonotonicMs };
                }
                var maxTailBytes = Math.Max(0, RollTarget - header.Length);
                var keepFrom = combined.Count;
                long keptBytes = 0;
                for (var i = combined.Count - 1; i >= 0; i--) {
                    var len = combined[i].Length;
                    if (keptBytes + len > maxTailBytes && keepFrom < combined.Count) break;
                    keptBytes += len;
                    keepFrom = i;
                }
                if (keepFrom > 0) truncated = true;
                // Ensure the rolled file always fits strictly below MaxLog even if a single batch was huge.
                var hardTailBudget = Math.Max(0, MaxLog - 32768 - header.Length);
                while (keepFrom < combined.Count - 1 && keptBytes > hardTailBudget) {
                    keptBytes -= combined[keepFrom].Length;
                    keepFrom++;
                    truncated = true;
                }
                using var ms = new MemoryStream(header.Length + (int)Math.Min(keptBytes, MaxLog));
                ms.Write(header);
                for (var i = keepFrom; i < combined.Count; i++) ms.Write(combined[i]);
                var rolled = ms.ToArray();
                index = index with { Summary = job.Summary with { Records = records, Truncated = truncated || job.Summary.Truncated }, EndedUtc = job.EndedUtc, Bytes = rolled.Length };
                // Two-phase paired roll commit: stage both .log.roll and .json.roll before replacing live files;
                // if interrupted before .json.roll exists, RecoverPendingRoll discards .log.roll leaving old pair intact;
                // if interrupted after .json.roll exists, RecoverPendingRoll completes both moves cleanly.
                var logRoll = FileFor(id, ".log.roll");
                var jsonRoll = FileFor(id, ".json.roll");
                SafeFiles.Atomic(logRoll, rolled, flushToDisk);
                SafeFiles.Atomic(jsonRoll, JsonSerializer.SerializeToUtf8Bytes(index, DiagnosticJson.Default.DiagnosticLogIndex), flushToDisk);
                File.Move(logRoll, path, true);
                File.Move(jsonRoll, FileFor(id, ".json"), true);
            }
            if (!needRoll)
                SafeFiles.Atomic(FileFor(id, ".json"), JsonSerializer.SerializeToUtf8Bytes(index, DiagnosticJson.Default.DiagnosticLogIndex), flushToDisk);
            if (previous is null) Cleanup();
        }
    }
    internal byte[]? ReadCompleteLogBytes(string sessionId)
    {
        lock (gate) {
            if (!Guid.TryParseExact(sessionId, "N", out _)) return null;
            var index = ReadIndex(sessionId);
            if (index is null) return null;
            var filePath = FileFor(sessionId);
            if (!File.Exists(filePath)) return null;
            using var source = new FileStream(filePath, FileMode.Open, FileAccess.Read, FileShare.Read);
            if (source.Length < index.Bytes || source.Length > MaxLog || index.Bytes > MaxLog) return null;
            using var dest = new MemoryStream((int)index.Bytes + 2048);
            using var reader = new StreamReader(source, new UTF8Encoding(false, true), false, 65536, true);
            long bytes = 0;
            while (reader.ReadLine() is { } line) {
                var raw = Encoding.UTF8.GetBytes(line + "\n");
                if (bytes + raw.Length > index.Bytes) break;
                using var parsed = JsonDocument.Parse(line);
                dest.Write(raw, 0, raw.Length); bytes += raw.Length;
                if (bytes == index.Bytes) break;
            }
            if (bytes != index.Bytes) return null;
            var summary = Envelope("log_summary", index);
            dest.Write(summary, 0, summary.Length);
            return dest.ToArray();
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
