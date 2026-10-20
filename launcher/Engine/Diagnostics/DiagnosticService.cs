using System.Diagnostics;
using System.Text.Json;
using System.Threading.Channels;

namespace Dnf90.Launcher.Diagnostics;

internal sealed class DiagnosticSession(ReadOnlyGameProcess? process, string pvfHash, long publication)
{
    public readonly ReadOnlyGameProcess? Process = process;
    public readonly string PvfHash = pvfHash;
    public readonly long Publication = publication;
    public readonly string Id = process?.Identity.Id ?? Guid.NewGuid().ToString("N");
    public int CaptureLimit => Process is null ? 512 * 1024 : 3 * 1024 * 1024;
    public readonly DiagnosticRing Ring = new(process is null ? 256 * 1024 : 2 * 1024 * 1024);
    public At5tModule? Module;
    public long Sequence, Errors, Dropped;
    public long LoginAttempt;
    public double NextBasic, NextSample, SamplingUntil, CountdownUntil, LastSample, LastFull, LastTrigger = -60000, CaptureUntil, CaptureStarted;
    public int Interval = 5, Late;
    public uint AutoScene;
    public bool Fresh, Truncated;
    public string? IncidentId;
    public string IncidentReason = "", StartedUtc = "", EndReason = "";
    public int CaptureBytes;
    public readonly List<DiagnosticLine> Capture = [];
    public DiagnosticLine? Identity;
    public Dictionary<string, DiagnosticField> Previous = [];
    public long FullSequence;
    public string Phase = "basic";
    public readonly HashSet<string> Missing = [];
}

internal sealed class DiagnosticService : IDisposable
{
    private readonly string root;
    private readonly DiagnosticStorage storage;
    private readonly Stopwatch clock = Stopwatch.StartNew();
    private readonly Channel<Action> commands = Channel.CreateBounded<Action>(new BoundedChannelOptions(64) { SingleReader = true, FullMode = BoundedChannelFullMode.Wait });
    // 2 pending jobs + 1 being written, each <= 3 MiB. Two sessions each use
    // <= 2 MiB ring + 3 MiB capture; launcher uses 0.75 MiB. With the
    // writer's three jobs this accounts for <= 19.75 MiB, leaving workspace.
    private readonly Channel<DiagnosticSave> saves = Channel.CreateBounded<DiagnosticSave>(new BoundedChannelOptions(2) { SingleReader = true, FullMode = BoundedChannelFullMode.Wait });
    private readonly List<DiagnosticSession> sessions = [];
    private readonly Dictionary<string, ReadOnlyGameProcess> candidates = [];
    private readonly Dictionary<int, ProcessIdentity> managed = [];
    private readonly Dictionary<int, long> pendingStarts = [];
    private readonly Dictionary<int, double> retryAfter = [];
    private readonly Task worker, writer;
    private readonly Task notifications;
    private DiagnosticOptions options = new();
    private string clientDirectory = "", pvfHash = "";
    private long publication;
    private volatile bool closing;
    private DiagnosticSession? launcher;
    private volatile DiagnosticClip[] clips = [];
    private Task<(ReadOnlyGameProcess Process, long Attempt)>? binding;
    private long attempt;
    private int exporting;
    private int bindingPid;
    private ProcessIdentity[]? registryToWrite;
    private string error = "";
    private volatile DiagnosticStatus status = new(false, "off", "已关闭", [], []);
    public DiagnosticStatus Status => status;
    public event Action? Changed;
    public DiagnosticService(string stateRoot)
    {
        root = Path.Combine(stateRoot, "diagnostics"); storage = new(Path.Combine(root, "clips"));
        LoadRegistry(); writer = Task.Run(WriteLoop); worker = Task.Factory.StartNew(Loop, CancellationToken.None, TaskCreationOptions.LongRunning, TaskScheduler.Default);
        notifications = Task.Run(NotifyLoop);
    }
    private void LoadRegistry()
    {
        var path = Path.Combine(root, "processes.json");
        try
        {
            SafeFiles.NoLinks(path);
            if (!File.Exists(path) || new FileInfo(path).Length > 16384) return;
            foreach (var p in JsonSerializer.Deserialize(File.ReadAllBytes(path), DiagnosticJson.Default.ProcessIdentityArray) ?? []) if (managed.Count < 2) managed[p.Pid] = p;
        }
        catch (Exception e) when (e is IOException or JsonException or UnauthorizedAccessException) { }
    }
    private void SaveRegistry()
    {
        Interlocked.Exchange(ref registryToWrite, managed.Values.Take(2).ToArray());
    }
    private Task Send(Action action)
    {
        var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        if (closing) return Task.FromException(new InvalidOperationException("诊断已结束。"));
        if (!commands.Writer.TryWrite(() => { try { action(); completion.SetResult(); } catch (Exception e) { completion.SetException(e); } })) throw new InvalidOperationException("诊断操作正在排队，请稍后重试。");
        return completion.Task;
    }
    // Integration events are best effort and never throw into login/update paths.
    private void Post(Action action)
    {
        if (!closing && !commands.Writer.TryWrite(() => { try { action(); } catch (Exception) { error = "diagnostic_command_failed"; } }))
            error = "command_queue_full";
    }
    public void Configure(DiagnosticOptions value, string directory, string pvf, long sequence)
    {
        Post(() => {
            if (value.ProblemType is not ("auto" or "quests" or "interaction" or "connection" or "performance")) { error = "invalid_problem_type"; return; }
            if (options.Enabled && (!value.Enabled || directory != clientDirectory)) EndAll(value.Enabled ? "client_directory_changed" : "diagnostics_disabled");
            if (options.ProblemType != value.ProblemType)
                foreach (var s in sessions) { s.CountdownUntil = s.SamplingUntil = 0; Finish(s, "problem_type_changed"); s.Phase = "basic"; }
            options = value; clientDirectory = directory; pvfHash = pvf; publication = sequence;
            if (value.Enabled && launcher is null) { launcher = new(null, pvf, sequence); launcher.Identity = Record(launcher, "identity", "launcher", new() { ["publication_sequence"] = new(sequence) }, "ok", 0, "diagnostics_enabled"); }
            Publish();
        });
    }
    public void Track(Process game, string executable)
    {
        try {
            var pid = game.Id; var created = game.StartTime.ToUniversalTime().ToFileTimeUtc();
            Post(() => {
                using var identity = new ReadOnlyGameProcess(pid, executable, created);
                managed[pid] = identity.Identity;
                pendingStarts[pid] = attempt;
                if (managed.Count > 2) { var old = managed.Keys.First(k => k != pid); managed.Remove(old); pendingStarts.Remove(old); }
                SaveRegistry();
                if (options.Enabled && binding is null) StartBinding(new(pid, executable, created, identity.Identity.Id), attempt);
            });
        } catch (Exception) { error = "process_tracking_failed"; }
    }
    public void Event(string code, double durationMs = 0, double? number = null)
    {
        if (!EventCodes.Contains(code)) return;
        Post(() => {
            if (!options.Enabled || launcher is null) return;
            if (code == "login_started") attempt++;
            Record(launcher, "event", "launcher", new() { ["duration_ms"] = new(durationMs), ["attempt"] = new(attempt), ["value"] = number is null ? new(null, "not_applicable") : new(number) }, "ok", 0, code);
            foreach (var s in sessions.Where(s => attempt > 0 && s.LoginAttempt == attempt)) {
                Record(s, "event", "launcher", new() { ["duration_ms"] = new(durationMs), ["attempt"] = new(attempt), ["value"] = number is null ? new(null, "not_applicable") : new(number) }, "ok", 0, code);
                if (code == "login_error") Begin(s, code, clock.Elapsed.TotalMilliseconds, 10000);
            }
            if (code is "login_error" or "update_error") Begin(launcher, code, clock.Elapsed.TotalMilliseconds, 10000);
        });
    }
    private static readonly HashSet<string> EventCodes = ["metadata_verified", "update_complete", "update_error", "login_started", "client_verified", "channel_verified", "account_http_response", "account_verified", "process_started", "login_error", "operation_error"];
    public Task Select(string id) => Send(() => {
        if (!candidates.Remove(id, out var process)) throw new InvalidDataException("游戏窗口已变化，请重新选择。");
        if (binding is not null) { candidates[id] = process; throw new InvalidOperationException("正在核验游戏版本，请稍候。"); }
        StartBinding(process, 0); Publish();
    });
    public Task Mark(string id) => Send(() => { var s = Find(id); Begin(s, "manual_marker", clock.Elapsed.TotalMilliseconds, 10000); Publish(); });
    public Task Reproduce(string id) => Send(() => {
        var s = Find(id); if (s.CountdownUntil > 0 || s.SamplingUntil > clock.Elapsed.TotalMilliseconds) throw new InvalidOperationException("采集正在进行，可先停止本次采集。");
        Finish(s, "reproduction_requested");
        s.CountdownUntil = clock.Elapsed.TotalMilliseconds + 5000; s.Phase = "countdown"; Publish();
    });
    public Task Stop(string id) => Send(() => {
        var s = Find(id); s.CountdownUntil = 0; s.SamplingUntil = 0; s.Phase = "basic";
        Record(s, "event", "collector", [], "ok", 0, "user_stop"); Finish(s, "user_stop"); Publish();
    });
    public async Task<DiagnosticExport> Export(string sessionId, string[] incidents, string directory)
    {
        if (Interlocked.CompareExchange(ref exporting, 1, 0) != 0) throw new InvalidOperationException("正在导出，请稍候。");
        try { return await Task.Run(() => storage.Export(sessionId, incidents, directory)); }
        finally { Volatile.Write(ref exporting, 0); }
    }
    private DiagnosticSession Find(string id) => options.Enabled ? (launcher?.Id == id ? launcher : sessions.SingleOrDefault(s => s.Id == id)) ?? throw new InvalidDataException("请选择正在运行的游戏窗口。") : throw new InvalidOperationException("请先开启增强诊断。");
    private void StartBinding(ReadOnlyGameProcess process, long loginAttempt)
    {
        if (closing) { process.Dispose(); return; }
        bindingPid = process.Identity.Pid;
        binding = Task.Run(() => { try { process.VerifyImage(); return (process, loginAttempt); } catch { process.Dispose(); throw; } });
    }
    private void Attach(ReadOnlyGameProcess process, long loginAttempt)
    {
        if (sessions.Any(s => s.Process!.Identity.Pid == process.Identity.Pid && s.Process!.Identity.Created == process.Identity.Created) || sessions.Count >= 2) { process.Dispose(); return; }
        try
        {
            var s = new DiagnosticSession(process, pvfHash, publication) { LoginAttempt = loginAttempt };
            pendingStarts.Remove(process.Identity.Pid);
            // Copy only this launch attempt, never another game's process records.
            if (launcher is not null)
                foreach (var line in launcher.Ring.Snapshot()) {
                    var r = JsonSerializer.Deserialize(line.Bytes, DiagnosticJson.Default.DiagnosticRecord)!;
                    if (loginAttempt > 0 && r.Fields.GetValueOrDefault("attempt")?.Value == loginAttempt)
                        Record(s, r.Type, r.Module, r.Fields, r.Quality, r.ReadMs, r.Reason, observedTime: r.MonotonicMs, observedUtc: r.Utc);
                }
            if (process.Version != "unsupported") s.Module = new(process, process.ModuleBase); else s.Phase = "unsupported";
            sessions.Add(s); managed[process.Identity.Pid] = process.Identity; SaveRegistry();
            s.Identity = Record(s, "identity", "collector", new() {
                ["pid"] = new(process.Identity.Pid), ["created_unix_ms"] = new(new DateTimeOffset(DateTime.FromFileTimeUtc(process.Identity.Created)).ToUnixTimeMilliseconds()),
                ["windows_session"] = new(process.Identity.WindowsSession), ["module_base"] = new(process.ModuleBase), ["publication_sequence"] = new(publication),
                ["server_events"] = new(null, "unsupported", "not_connected") }, "ok", 0, "game_bound");
        }
        catch (Exception e) when (e is IOException or InvalidOperationException or System.ComponentModel.Win32Exception or UnauthorizedAccessException or OverflowException) { retryAfter[process.Identity.Pid] = clock.Elapsed.TotalMilliseconds + 30000; process.Dispose(); error = "process_binding_failed"; }
    }
    private void Discover()
    {
        if (!OperatingSystem.IsWindows() || clientDirectory.Length == 0 || binding is not null) return;
        foreach (var id in retryAfter.Keys.Where(k => retryAfter[k] <= clock.Elapsed.TotalMilliseconds).ToArray()) retryAfter.Remove(id);
        var executable = Path.Combine(clientDirectory, "DNF.exe");
        foreach (var row in candidates.ToArray())
            if (row.Value.Exited(out _)) { row.Value.Dispose(); candidates.Remove(row.Key); }
        foreach (var p in Process.GetProcessesByName("DNF"))
        {
            using (p)
            {
                if (retryAfter.ContainsKey(p.Id) || sessions.Any(s => s.Process!.Identity.Pid == p.Id) || candidates.Values.Any(c => c.Identity.Pid == p.Id) || sessions.Count + candidates.Count >= 2) continue;
                try
                {
                    var known = managed.GetValueOrDefault(p.Id);
                    var read = new ReadOnlyGameProcess(p.Id, executable);
                    if (known is not null && known.Created == read.Identity.Created && known.WindowsSession == read.Identity.WindowsSession && known.Path.Equals(read.Identity.Path, StringComparison.OrdinalIgnoreCase))
                    {
                        if (pendingStarts.TryGetValue(p.Id, out var startedAttempt)) {
                            read.Dispose(); StartBinding(new(p.Id, executable, known.Created, known.Id), startedAttempt);
                        } else StartBinding(read, 0);
                        return;
                    }
                    candidates.Add(read.Identity.Id, read);
                }
                catch (Exception e) when (e is IOException or InvalidOperationException or System.ComponentModel.Win32Exception or UnauthorizedAccessException) { }
            }
        }
        if (candidates.Count == 1) { var p = candidates.Values.Single(); candidates.Clear(); StartBinding(p, 0); }
    }
    private DiagnosticLine Record(DiagnosticSession s, string type, string module, Dictionary<string, DiagnosticField> fields, string quality, double readMs, string? reason = null, bool full = true, double? observedTime = null, string? observedUtc = null)
    {
        var now = observedTime ?? clock.Elapsed.TotalMilliseconds; var n = ++s.Sequence;
        foreach (var (name, value) in fields)
            if (s.Missing.Count < 128 && value.Status is "unsupported" or "read_failed") s.Missing.Add(name + ":" + value.Status);
        var record = new DiagnosticRecord(n, observedUtc ?? DateTime.UtcNow.ToString("O"), now, type, module, s.Id, readMs, quality, fields, reason, type == "sample" && !full ? s.FullSequence : null);
        if (type == "sample" && full) s.FullSequence = n;
        var raw = JsonSerializer.SerializeToUtf8Bytes(record, DiagnosticJson.Default.DiagnosticRecord);
        if (raw.Length > 65535) { s.Dropped++; s.Previous.Clear(); s.FullSequence = 0; type = "event"; full = false; raw = JsonSerializer.SerializeToUtf8Bytes(record with { Type = "event", Fields = [], Quality = "dropped", Reason = "record_budget", BaseSequence = null }, DiagnosticJson.Default.DiagnosticRecord); }
        var bytes = new byte[raw.Length + 1]; raw.CopyTo(bytes, 0); bytes[^1] = 10;
        var line = new DiagnosticLine(bytes, now, full, type); s.Ring.Add(line);
        if (quality is "partial" or "unstable" or "read_failed") s.Errors++;
        if (s.IncidentId is not null)
        {
            if (s.CaptureBytes + line.Cost <= s.CaptureLimit) { s.Capture.Add(line); s.CaptureBytes += line.Cost; }
            else { s.Truncated = true; s.Dropped++; Finish(s, "capture_budget"); s.SamplingUntil = 0; }
        }
        return line;
    }
    private void Begin(DiagnosticSession s, string reason, double now, double after)
    {
        if (s.IncidentId is not null) { s.CaptureUntil = Math.Min(Math.Max(s.CaptureUntil, now + after), s.CaptureStarted + 60000); return; }
        if (reason != "manual_marker" && reason != "reproduction" && reason != "session_end" && now - s.LastTrigger < 30000) return;
        s.LastTrigger = now; s.IncidentId = Guid.NewGuid().ToString("N"); s.IncidentReason = reason; s.StartedUtc = DateTime.UtcNow.ToString("O");
        s.Capture.Clear(); s.CaptureBytes = 0; s.Truncated = false; s.CaptureStarted = now;
        if (s.Identity is not null) { s.Capture.Add(s.Identity); s.CaptureBytes += s.Identity.Cost; }
        foreach (var line in s.Ring.Snapshot())
        {
            if (line.Type == "identity") continue;
            if (s.CaptureBytes + line.Cost > s.CaptureLimit - 65536) { s.Truncated = true; break; }
            s.Capture.Add(line); s.CaptureBytes += line.Cost;
        }
        if (s.Capture.Count > 0) s.StartedUtc = JsonSerializer.Deserialize(s.Capture.First(x => x.Type != "identity" || s.Capture.Count == 1).Bytes, DiagnosticJson.Default.DiagnosticRecord)!.Utc;
        s.CaptureUntil = now + after;
        Record(s, "event", "collector", [], "ok", 0, reason);
    }
    private void Finish(DiagnosticSession s, string reason)
    {
        if (s.IncidentId is null) return;
        var missing = new List<string> { "server_events_not_connected", "frame_time_not_instrumented", "native_distance_result_not_instrumented", "function_order_is_not_observed" };
        if (s.Process is null) missing.Add("no_game_process");
        else { if (s.Module is null) missing.Add("at5t_unsupported_exe"); if (s.Process.PvfHash == "") missing.Add("pvf_hash_unavailable"); }
        missing.AddRange(s.Missing);
        var summary = new DiagnosticSummary(s.Id, s.IncidentId, s.Process?.Hash ?? "", s.Process?.PvfHash ?? "", s.Publication, s.Process?.Version ?? "launcher",
            reason, s.Capture.Count, s.Errors, s.Dropped, s.Ring.Overwritten, s.Truncated,
            [.. missing],
            s.Process is null ? ["basic"] : s.Capture.Any(l => l.Type == "sample") ? ["basic", "performance", "at5t"] : ["basic", "performance"], s.Interval, CorrelationId: s.Id, ExpectedPvfSha256: s.PvfHash);
        var job = new DiagnosticSave(summary, [.. s.Capture], s.StartedUtc, DateTime.UtcNow.ToString("O"));
        var queued = saves.Writer.TryWrite(job);
        // Host EOF closes the UI first. Flush its final sessions through the same
        // bounded writer; ordinary sampling never waits for the disk queue.
        if (!queued && closing) { saves.Writer.WriteAsync(job).AsTask().GetAwaiter().GetResult(); queued = true; }
        s.IncidentId = null; s.Capture.Clear(); s.CaptureBytes = 0;
        if (!queued) { s.Dropped++; error = "writer_queue_full"; Record(s, "event", "collector", [], "dropped", 0, "writer_queue_full"); }
    }
    private void Sample(DiagnosticSession s, double now)
    {
        if (s.Process is not null && s.Process.Exited(out var exit)) { Record(s, "event", "process", new() { ["exit_code"] = new(exit) }, "ok", 0, "game_exit"); End(s, "game_exit"); return; }
        if (now >= s.NextBasic)
        {
            var start = clock.Elapsed.TotalMilliseconds;
            if (s.Process is not null) {
                var perf = s.Process.Performance();
                Record(s, "performance", "process", perf, perf.Values.Any(f => f.Status == "read_failed") ? "partial" : "ok", clock.Elapsed.TotalMilliseconds - start);
            }
            s.NextBasic = now + 1000;
            if (s.Module is not null && s.SamplingUntil == 0 && s.CountdownUntil == 0 && options.ProblemType == "auto")
            {
                var scene = s.Module.Sample(now, false);
                Record(s, "scene", "at5t", scene, s.Module.Quality, clock.Elapsed.TotalMilliseconds - start);
                if (s.Module.HasVehicle && s.AutoScene != s.Module.Scene)
                { s.AutoScene = s.Module.Scene; StartSampling(s, now, 30000); Begin(s, "at5t_scene_detected", now, 30000); }
                if (!s.Module.HasVehicle) s.AutoScene = 0;
            }
        }
        if (s.CountdownUntil > 0 && now >= s.CountdownUntil)
        { s.CountdownUntil = 0; StartSampling(s, now, 20000); Begin(s, "reproduction", now, 20000); }
        if (s.SamplingUntil > 0)
        {
            if (now >= s.SamplingUntil) { s.SamplingUntil = 0; s.Phase = s.Module is null ? "unsupported" : "basic"; Record(s, "event", "collector", [], "ok", 0, "sampling_limit"); }
            else if (s.Module is not null && options.ProblemType is "auto" or "interaction" && now >= s.NextSample)
            {
                var start = clock.Elapsed.TotalMilliseconds; var sample = s.Module.Sample(now, true); var duration = clock.Elapsed.TotalMilliseconds - start;
                var full = s.Previous.Count == 0 || now - s.LastFull >= 1000;
                var delta = full ? new Dictionary<string, DiagnosticField>(sample) : sample.Where(x => !s.Previous.TryGetValue(x.Key, out var old) || old != x.Value).ToDictionary(x => x.Key, x => x.Value);
                foreach (var gone in s.Previous.Keys.Except(sample.Keys)) delta[gone] = new(null, "not_applicable", "field_removed");
                delta["timing.interval_ms"] = new(s.LastSample == 0 ? 0 : now - s.LastSample); delta["timing.target_ms"] = new(s.Interval);
                Record(s, "sample", "at5t", delta, s.Module.Quality, duration, full ? "full" : "delta", full);
                if (full) s.LastFull = now;
                s.Previous = s.FullSequence == 0 ? [] : sample;
                var interval = s.LastSample == 0 ? s.Interval : now - s.LastSample;
                s.LastSample = now;
                if (duration > s.Interval || interval > s.Interval * 1.8) { s.Dropped += Math.Max(1, (long)(Math.Max(duration, interval) / s.Interval) - 1); if (++s.Late >= 5) { s.Interval = Math.Min(100, s.Interval * 2); s.Late = 0; Record(s, "event", "collector", new() { ["interval_ms"] = new(s.Interval) }, "ok", 0, "sampling_backpressure"); } }
                s.NextSample = clock.Elapsed.TotalMilliseconds + s.Interval; s.Phase = "sampling";
                if (s.Module.Fresh && !s.Fresh) Begin(s, "candidate_input_edge", now, 10000); s.Fresh = s.Module.Fresh;
                if (s.Module.Quality == "ok" && !s.Module.HasVehicle && s.IncidentReason == "at5t_scene_detected") { s.SamplingUntil = 0; Record(s, "event", "collector", [], "ok", 0, "scene_exit"); Finish(s, "scene_exit"); }
            }
            else if (s.Module is null) s.Phase = s.Process is null ? "basic" : "unsupported";
        }
        if (s.IncidentId is not null && now >= s.CaptureUntil) Finish(s, "capture_complete");
    }
    private void End(DiagnosticSession s, string reason)
    {
        if (reason == "collector_error" && s.Process is not null) retryAfter[s.Process.Identity.Pid] = clock.Elapsed.TotalMilliseconds + 30000;
        if (s.IncidentId is null) Begin(s, "session_end", clock.Elapsed.TotalMilliseconds, 0);
        Record(s, "event", "collector", [], "ok", 0, reason); Finish(s, reason); sessions.Remove(s); s.Process?.Dispose();
    }
    private void EndAll(string reason) { if (launcher is not null) { End(launcher, reason); launcher = null; } foreach (var s in sessions.ToArray()) End(s, reason); foreach (var p in candidates.Values) p.Dispose(); candidates.Clear(); }
    private void Loop()
    {
        using var pacer = new DiagnosticPacer();
        double discover = 0, publish = 0;
        try
        {
            while (!closing)
            {
                while (commands.Reader.TryRead(out var action)) action();
                if (binding?.IsCompleted == true) {
                    try { var result = binding.GetAwaiter().GetResult(); if (options.Enabled && !closing && result.Process.Identity.Path.Equals(Path.Combine(clientDirectory, "DNF.exe"), StringComparison.OrdinalIgnoreCase)) Attach(result.Process, result.Attempt); else result.Process.Dispose(); }
                    catch (Exception) { retryAfter[bindingPid] = clock.Elapsed.TotalMilliseconds + 30000; error = "process_binding_failed"; }
                    binding = null;
                }
                var now = clock.Elapsed.TotalMilliseconds;
                if (options.Enabled)
                {
                    if (now >= discover) { try { Discover(); } catch (Exception) { error = "discovery_failed"; } discover = now + 2000; }
                    if (launcher is not null) Sample(launcher, now);
                    foreach (var s in sessions.ToArray()) try { Sample(s, clock.Elapsed.TotalMilliseconds); } catch (Exception) { error = "collector_read_failed"; End(s, "collector_error"); }
                }
                if (now >= publish) { Publish(); publish = now + 1000; }
                pacer.Wait(options.Enabled && sessions.Any(s => s.SamplingUntil > now) ? 5 : 100);
            }
        }
        finally {
            if (binding is not null) try { binding.GetAwaiter().GetResult().Process.Dispose(); } catch (Exception) { }
            while (commands.Reader.TryRead(out var pending)) pending();
            EndAll("launcher_closed"); saves.Writer.TryComplete();
        }
    }
    private async Task WriteLoop()
    {
        try { clips = storage.List(); } catch (Exception) { error = "diagnostic_list_failed"; }
        double refresh = 0;
        while (true)
        {
            while (saves.Reader.TryRead(out var job))
                try { storage.Save(job); clips = storage.List(); } catch (Exception) { error = "diagnostic_write_failed"; }
            var registry = Interlocked.Exchange(ref registryToWrite, null);
            if (registry is not null)
                try { SafeFiles.Atomic(Path.Combine(root, "processes.json"), JsonSerializer.SerializeToUtf8Bytes(registry, DiagnosticJson.Default.ProcessIdentityArray)); }
                catch (Exception) { error = "process_registry_write_failed"; }
            if (saves.Reader.Completion.IsCompleted) break;
            if (clock.Elapsed.TotalMilliseconds >= refresh) {
                try { clips = storage.List(); } catch (Exception) { error = "diagnostic_list_failed"; }
                refresh = clock.Elapsed.TotalMilliseconds + 30000;
            }
            await Task.Delay(200);
        }
    }
    private void Publish()
    {
        var now = clock.Elapsed.TotalMilliseconds;
        var rows = sessions.Select(s => new DiagnosticInstance(s.Id, s.Process!.Window, s.Process!.Identity.Pid, DateTime.FromFileTimeUtc(s.Process!.Identity.Created).ToString("O"), s.Phase, s.Process!.Version, true))
            .Concat(candidates.Values.Select(p => new DiagnosticInstance(p.Identity.Id, p.Window, p.Identity.Pid, DateTime.FromFileTimeUtc(p.Identity.Created).ToString("O"), "choose", "pending", false))).ToArray();
        if (launcher is not null) rows = [new(launcher.Id, "登录器 · 启动与连接", 0, "", launcher.Phase, "launcher", true), .. rows];
        var countdown = sessions.Concat(launcher is null ? [] : new[] { launcher }).Select(s => (int)Math.Ceiling(Math.Max(0, s.CountdownUntil - now) / 1000)).DefaultIfEmpty().Max();

        var phase = !options.Enabled ? "off" : error != "" ? "error" : countdown > 0 ? "countdown" : sessions.Any(s => s.Phase == "sampling") ? "sampling" : sessions.Any(s => s.IncidentId is not null) || launcher?.IncidentId is not null ? "capturing" : sessions.Any(s => s.Module is null) ? "unsupported" : sessions.Count > 0 ? "basic" : clips.Length > 0 ? "saved" : "waiting";
        var message = phase switch { "off" => "已关闭", "error" => "采集异常：" + error, "countdown" => $"{countdown} 秒后采集，请切回游戏", "sampling" => "专项采集中", "capturing" => "正在保存问题前后记录", "unsupported" => "基础记录中 · 专项不支持当前版本", "basic" => "基础记录中", "saved" => "已保存片段", _ => binding is not null ? "正在核验游戏版本" : candidates.Count > 1 ? "请选择游戏窗口" : "等待游戏" };
        status = new(options.Enabled, phase, message, rows, clips, countdown);
    }
    private static void StartSampling(DiagnosticSession s, double now, double duration)
    {
        s.SamplingUntil = now + duration; s.NextSample = now; s.LastSample = 0; s.LastFull = 0;
        s.Interval = 5; s.Late = 0; s.Fresh = false; s.Previous.Clear();
    }
    private async Task NotifyLoop()
    {
        DiagnosticStatus? last = null;
        while (!closing) {
            var current = status;
            if (!ReferenceEquals(last, current)) {
                last = current;
                try { Changed?.Invoke(); } catch (Exception) { /* A slow/broken UI pipe cannot block sampling. */ }
            }
            await Task.Delay(200);
        }
    }
    public void Dispose()
    {
        closing = true; commands.Writer.TryComplete();
        try { worker.GetAwaiter().GetResult(); writer.GetAwaiter().GetResult(); } finally { clock.Stop(); }
    }
}
