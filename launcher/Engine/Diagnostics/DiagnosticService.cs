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
    public const int CaptureLimit = 512 * 1024;
    public readonly DiagnosticRing Ring = new(process is null ? 256 * 1024 : 2 * 1024 * 1024);
    public At5tModule? Module;
    public DiagnosticObserverClient? Observer;
    public string CrashStatus = "starting";
    public double ExitObserved;
    public bool HasOperations, HasExceptions;
    public bool CriticalPending;
    public long Sequence, Errors, Dropped;
    public long LoginAttempt;
    public double NextBasic, NextSample, SamplingUntil, LastSample, LastFull, NextFlush, TimeOffset;
    public int Interval = 5, Late;
    public uint AutoScene;
    public bool Fresh, Truncated;
    public string StartedUtc = process is null ? DateTime.UtcNow.ToString("O") : DateTime.FromFileTimeUtc(process.Identity.Created).ToString("O");
    public int CaptureBytes;
    public readonly List<DiagnosticLine> Capture = [];
    public DiagnosticLine? Identity;
    public bool HasSamples;
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
    // Bounded batches: two pending plus one being written, at most 512 KiB each.
    private readonly Channel<DiagnosticSave> saves = Channel.CreateBounded<DiagnosticSave>(new BoundedChannelOptions(2) { SingleReader = true, FullMode = BoundedChannelFullMode.Wait });
    private readonly List<DiagnosticSession> sessions = [];
    private readonly Dictionary<string, ReadOnlyGameProcess> candidates = [];
    private readonly Dictionary<int, ProcessIdentity> managed = [];
    private readonly Dictionary<int, long> pendingStarts = [];
    private readonly Dictionary<long, DiagnosticSession> launchAttempts = [];
    private readonly Dictionary<int, double> retryAfter = [];
    private readonly Task worker, writer;
    private readonly Task notifications;
    private DiagnosticOptions options = new();
    private string clientDirectory = "", pvfHash = "";
    private long publication;
    private volatile bool closing;
    private DiagnosticSession? launcher;
    private volatile DiagnosticClip[] clips = [];
    private Task<(ReadOnlyGameProcess Process, long Attempt, DiagnosticLogIndex? Index, DiagnosticObserverClient? Observer)>? binding;
    private long attempt;
    private int exporting;
    private int bindingPid;
    private DiagnosticObserverClient? bindingObserver;
    private ProcessIdentity[]? registryToWrite;
    private string error = "";
    private bool writeFailed;
    private volatile DiagnosticStatus status = new(false, "off", "已关闭", [], []);
    public DiagnosticStatus Status => status;
    public event Action? Changed;
    public DiagnosticService(string stateRoot)
    {
        root = Path.Combine(stateRoot, "diagnostics"); storage = new(Path.Combine(root, "logs"));
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
        value = value with { Enabled = value.Enabled && value.HasConsent };
        Post(() => {
            if (options.Enabled && (!value.Enabled || directory != clientDirectory)) EndAll(value.Enabled ? "client_directory_changed" : "diagnostics_disabled");
            options = value with { ProblemType = "auto" }; clientDirectory = directory; pvfHash = pvf; publication = sequence;
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
    public void Failure(string code, Exception exception) => Event(code, number: exception.HResult, exception: exception);
    public void Event(string code, double durationMs = 0, double? number = null, Exception? exception = null)
    {
        if (!EventCodes.Contains(code)) return;
        Post(() => {
            if (!options.Enabled || launcher is null) return;
            var evidence = exception is null ? null : DiagnosticFailure.Capture(exception);
            if (code == "login_started") {
                attempt++;
                var earlier = launcher.Ring.Snapshot();
                launcher = new(null, pvfHash, publication) { LoginAttempt = attempt };
                launchAttempts[attempt] = launcher;
                foreach (var old in launchAttempts.Keys.Order().Take(Math.Max(0, launchAttempts.Count - 2)).ToArray()) launchAttempts.Remove(old);
                foreach (var line in earlier) {
                    var record = JsonSerializer.Deserialize(line.Bytes, DiagnosticJson.Default.DiagnosticRecord)!;
                    if (record.Reason is "metadata_verified" or "update_complete" or "update_error") {
                        var fields = new Dictionary<string, DiagnosticField>(record.Fields) { ["attempt"] = new(attempt) };
                        Record(launcher, record.Type, record.Module, fields, record.Quality, record.ReadMs, record.Reason, observedTime: record.MonotonicMs, observedUtc: record.Utc, evidence: record.Evidence);
                    }
                }
            }
            Record(launcher, "event", "launcher", new() { ["duration_ms"] = new(durationMs), ["attempt"] = new(attempt), ["value"] = number is null ? new(null, "not_applicable") : new(number) }, "ok", 0, code, evidence: evidence);
            foreach (var s in sessions.Where(s => attempt > 0 && s.LoginAttempt == attempt)) {
                Record(s, "event", "launcher", new() { ["duration_ms"] = new(durationMs), ["attempt"] = new(attempt), ["value"] = number is null ? new(null, "not_applicable") : new(number) }, "ok", 0, code, evidence: evidence);

            }
            if (code is "login_error" or "update_error" or "operation_error" && !sessions.Any(s => s.LoginAttempt == attempt)) {
                launcher.Capture.AddRange(launcher.Ring.Snapshot()); Flush(launcher, code);
            }
        });
    }
    private static readonly HashSet<string> EventCodes = ["metadata_verified", "update_complete", "update_error", "login_started", "client_verified", "channel_verified", "account_http_response", "account_verified", "process_started", "login_error", "operation_error"];
    public Task Elevate(string id) => Send(() => {
        var s = sessions.SingleOrDefault(s => s.Id == id);
        if (!options.Enabled || !options.HasConsent || s?.Process is null || s.CrashStatus is not ("access_denied" or "elevation_cancelled")) throw new InvalidOperationException("此启动无需提升诊断权限。");
        s.Observer?.Dispose(); s.CrashStatus = "elevating";
        s.Observer = new(s.Process.Identity, true); Publish();
    });
    private void Observe(DiagnosticSession s, bool allowRestart = true)
    {
        while (s.Observer?.Take(out var message) == true && message is not null) {
            if (message.Kind == "crash_ready") s.CrashStatus = "recording";
            else if (message.Kind is "access_denied" or "elevation_cancelled" or "attach_failed" or "observer_failed" or "observer_timeout" or "platform_unsupported") s.CrashStatus = message.Kind;
            else if (message.Kind == "observer_disconnected" && s.CrashStatus is "recording" or "starting") s.CrashStatus = "observer_disconnected";
            else if (message.Kind == "observer_stopped" && s.CrashStatus == "recording") s.CrashStatus = "stopped";
            if (message.Kind == "operations_unavailable") s.Missing.Add("player_operations:unavailable");
            if (message.Kind == "player_operations") s.HasOperations = true;
            if (message.Kind == "native_exception") s.HasExceptions = true;
            if (message.Kind == "native_exception") s.CriticalPending = true;
            if (message.Kind == "observer_dropped") s.Dropped += (long)(message.Fields.GetValueOrDefault("count")?.Value ?? 0);
            Record(s, message.Kind == "native_exception" ? "exception" : "event", "observer", message.Fields,
                message.Kind == "native_exception" ? "partial" : "ok", 0, message.Kind, evidence: message.Evidence);
            if (message.Kind == "elevation_cancelled" && allowRestart && !closing) { s.Observer.Dispose(); s.Observer = new(s.Process!.Identity, false); break; }
        }
    }
    public async Task<DiagnosticExport> Export(string sessionId, string target)
    {
        if (Interlocked.CompareExchange(ref exporting, 1, 0) != 0) throw new InvalidOperationException("正在导出，请稍候。");
        try {
            // Flush the selected running launch and wait for a writer barrier.
            var completion = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
            await Send(() => {
                var active = sessions.SingleOrDefault(s => s.Id == sessionId);
                if (active is not null) Flush(active, "recording", wait: true);
            });
            await saves.Writer.WriteAsync(new(new(sessionId, sessionId, "", "", 0, "", "barrier", 0, 0, 0, 0, false, [], [], 0), [], "", "") { Completion = completion });
            await completion.Task;
            return await Task.Run(() => storage.Export(sessionId, target));
        } finally { Volatile.Write(ref exporting, 0); }
    }
    private void StartBinding(ReadOnlyGameProcess process, long loginAttempt)
    {
        if (closing) { process.Dispose(); return; }
        bindingPid = process.Identity.Pid;
        // Attach before full EXE/PVF hashing so early crashes are not lost while hashing.
        var observer = bindingObserver = options.HasConsent && options.Enabled ? new DiagnosticObserverClient(process.Identity, false) : null;
        binding = Task.Run(async () => {
            try {
                process.VerifyImage();
                var ready = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                var id = process.Identity.Id;
                await saves.Writer.WriteAsync(new(new(id, id, "", "", 0, "", "resume_barrier", 0, 0, 0, 0, false, [], [], 0), [], "", "") { Completion = ready });
                await ready.Task;
                var index = storage.ReadIndex(id);
                // Keep the old stream exportable. A new contract starts an explicit
                // new log segment instead of appending v3 records under a v2 header.
                if (index is not null && index.SchemaVersion != 4) { process.StartNewLog(); index = null; }
                return (process, loginAttempt, index, observer);
            } catch { observer?.Dispose(); process.Dispose(); throw; }
        });
    }
    private void Attach(ReadOnlyGameProcess process, long loginAttempt, DiagnosticLogIndex? index, DiagnosticObserverClient? observer)
    {
        if (sessions.Any(s => s.Process!.Identity.Pid == process.Identity.Pid && s.Process!.Identity.Created == process.Identity.Created) || sessions.Count >= 2) { observer?.Dispose(); process.Dispose(); return; }
        try
        {
            if (pendingStarts.TryGetValue(process.Identity.Pid, out var trackedAttempt)) loginAttempt = trackedAttempt;
            var s = new DiagnosticSession(process, pvfHash, publication) { LoginAttempt = loginAttempt,
                Sequence = index?.LastSequence ?? 0, TimeOffset = Math.Max(0, (index?.LastTime ?? 0) - clock.Elapsed.TotalMilliseconds + 1),
                Errors = index?.Summary.Errors ?? 0, Dropped = index?.Summary.Dropped ?? 0,
                Truncated = index?.Summary.Truncated ?? false, Observer = observer };
            if (index is not null) {
                s.StartedUtc = index.StartedUtc; s.HasSamples = index.Summary.Categories.Contains("at5t");
                s.HasOperations = index.Summary.Categories.Contains("player_operations"); s.HasExceptions = index.Summary.Categories.Contains("native_exception");
            }
            pendingStarts.Remove(process.Identity.Pid);
            // Copy only this launch attempt, never another game's process records.
            if (launchAttempts.TryGetValue(loginAttempt, out var launch))
                foreach (var line in launch.Ring.Snapshot()) {
                    var r = JsonSerializer.Deserialize(line.Bytes, DiagnosticJson.Default.DiagnosticRecord)!;
                    if (loginAttempt > 0 && r.Fields.GetValueOrDefault("attempt")?.Value == loginAttempt)
                        Record(s, r.Type, r.Module, r.Fields, r.Quality, r.ReadMs, r.Reason, observedTime: r.MonotonicMs, observedUtc: r.Utc, evidence: r.Evidence);
                }
            launchAttempts.Remove(loginAttempt);
            if (process.Version != "unsupported") s.Module = new(process, process.ModuleBase); else s.Phase = "unsupported";
            sessions.Add(s); managed[process.Identity.Pid] = process.Identity; SaveRegistry();
            s.Identity = Record(s, "identity", "collector", new() {
                ["pid"] = new(process.Identity.Pid), ["created_unix_ms"] = new(new DateTimeOffset(DateTime.FromFileTimeUtc(process.Identity.Created)).ToUnixTimeMilliseconds()),
                ["windows_session"] = new(process.Identity.WindowsSession), ["publication_sequence"] = new(publication),
                ["server_events"] = new(null, "unsupported", "not_connected") }, "ok", 0, "game_bound");
            Flush(s, "recording");
        }
        catch (Exception e) when (e is IOException or InvalidOperationException or System.ComponentModel.Win32Exception or UnauthorizedAccessException or OverflowException) { observer?.Dispose(); retryAfter[process.Identity.Pid] = clock.Elapsed.TotalMilliseconds + 30000; process.Dispose(); error = "process_binding_failed"; }
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
                        } else { read.Dispose(); StartBinding(new(p.Id, executable, known.Created, known.Id), 0); }
                        return;
                    }
                    candidates.Add(read.Identity.Id, read);
                }
                catch (Exception e) when (e is IOException or InvalidDataException or InvalidOperationException or System.ComponentModel.Win32Exception or UnauthorizedAccessException) { }
            }
        }
        if (candidates.Count > 0 && sessions.Count < 2) { var p = candidates.Values.First(); candidates.Remove(p.Identity.Id); StartBinding(p, 0); }
    }
    private DiagnosticLine Record(DiagnosticSession s, string type, string module, Dictionary<string, DiagnosticField> fields, string quality, double readMs, string? reason = null, bool full = true, double? observedTime = null, string? observedUtc = null, DiagnosticEvidence? evidence = null)
    {
        var now = (observedTime ?? clock.Elapsed.TotalMilliseconds) + s.TimeOffset; var n = ++s.Sequence;
        foreach (var (name, value) in fields)
            if (s.Missing.Count < 128 && value.Status is "unsupported" or "read_failed") s.Missing.Add(name + ":" + value.Status);
        var record = new DiagnosticRecord(n, observedUtc ?? DateTime.UtcNow.ToString("O"), now, type, module, s.Id, readMs, quality, fields, reason, type == "sample" && !full ? s.FullSequence : null, evidence);
        if (type == "sample" && full) s.FullSequence = n;
        var raw = JsonSerializer.SerializeToUtf8Bytes(record, DiagnosticJson.Default.DiagnosticRecord);
        if (raw.Length > 65535) { s.Dropped++; s.Previous.Clear(); s.FullSequence = 0; type = "event"; full = false; raw = JsonSerializer.SerializeToUtf8Bytes(record with { Type = "event", Fields = [], Quality = "dropped", Reason = "record_budget", BaseSequence = null, Evidence = null }, DiagnosticJson.Default.DiagnosticRecord); }
        var bytes = new byte[raw.Length + 1]; raw.CopyTo(bytes, 0); bytes[^1] = 10;
        var line = new DiagnosticLine(bytes, now, full, type); s.Ring.Add(line);
        if (quality is "partial" or "unstable" or "read_failed") s.Errors++;
        if (s.Process is not null) {
            if (s.CaptureBytes + line.Cost <= DiagnosticSession.CaptureLimit) { s.Capture.Add(line); s.CaptureBytes += line.Cost; }
            else { s.Dropped++; s.Previous.Clear(); s.FullSequence = 0; }
            if (type == "sample") s.HasSamples = true;
        }
        return line;
    }
    private void Flush(DiagnosticSession s, string reason, bool wait = false)
    {
        var missing = new List<string> { "server_events_not_connected", "frame_time_not_instrumented", "native_distance_result_not_instrumented", "function_order_is_not_observed" };
        if (s.Process is null) missing.Add("no_game_process");
        else { if (s.Module is null) missing.Add("at5t_unsupported_exe"); if (s.Process.PvfHash == "") missing.Add("pvf_hash_unavailable"); }
        if (s.Process is not null && s.CrashStatus is not ("recording" or "stopped")) missing.Add("native_exceptions:" + s.CrashStatus);
        var categories = new List<string> { "basic" };
        if (s.Process is not null) categories.Add("performance");
        if (s.HasSamples) categories.Add("at5t"); if (s.HasOperations) categories.Add("player_operations"); if (s.HasExceptions) categories.Add("native_exception");
        missing.AddRange(s.Missing);
        var summary = new DiagnosticSummary(s.Id, s.Id, s.Process?.Hash ?? "", s.Process?.PvfHash ?? "", s.Publication, s.Process?.Version ?? "launcher",
            reason, 0, s.Errors, s.Dropped, s.Ring.Overwritten, s.Truncated, [.. missing],
            [.. categories], s.Interval, CorrelationId: s.Id, ExpectedPvfSha256: s.PvfHash, ReleaseChannel: ServerS5A3.LauncherBuild.Flavor);
        DiagnosticLine[] lines = [.. s.Capture]; var omitted = 0;
        if (s.CriticalPending) {
            var tail = new List<DiagnosticLine>(); var bytes = 0;
            foreach (var line in s.Capture.Concat(s.Ring.Snapshot()).Where(l => l.Type != "sample").Distinct()
                .OrderByDescending(l => JsonSerializer.Deserialize(l.Bytes, DiagnosticJson.Default.DiagnosticRecord)!.Sequence)) {
                if (bytes + line.Cost > DiagnosticSession.CaptureLimit) break;
                tail.Add(line); bytes += line.Cost;
            }
            tail.Reverse(); lines = [.. tail]; omitted = s.Capture.Except(lines).Count();
            summary = summary with { Dropped = s.Dropped + omitted };
        }
        var job = new DiagnosticSave(summary, lines, s.StartedUtc, DateTime.UtcNow.ToString("O")) { CriticalTail = s.CriticalPending };
        var queued = saves.Writer.TryWrite(job);
        if (!queued && (closing || wait)) { saves.Writer.WriteAsync(job).AsTask().GetAwaiter().GetResult(); queued = true; }
        if (queued) {
            if (s.CriticalPending) { s.Previous.Clear(); s.FullSequence = 0; s.Dropped += omitted; }
            s.Capture.Clear(); s.CaptureBytes = 0; s.CriticalPending = false;
        }
        else error = "writer_queue_full";
        s.NextFlush = clock.Elapsed.TotalMilliseconds + 1000;
    }
    private void Sample(DiagnosticSession s, double now)
    {
        Observe(s);
        if (s.Process is not null && s.Process.Exited(out var exit)) {
            if (s.ExitObserved == 0) s.ExitObserved = now;
            if (s.Observer is not null && !s.Observer.Completed && now - s.ExitObserved < 2000) return;
            Observe(s); s.CriticalPending = true; Record(s, "event", "process", new() { ["exit_code"] = new(exit) }, "ok", 0, "game_exit"); End(s, "game_exit"); return;
        }
        if (now >= s.NextBasic)
        {
            var start = clock.Elapsed.TotalMilliseconds;
            if (s.Process is not null) {
                var perf = s.Process.Performance();
                Record(s, "performance", "process", perf, perf.Values.Any(f => f.Status == "read_failed") ? "partial" : "ok", clock.Elapsed.TotalMilliseconds - start);
            }
            s.NextBasic = now + 1000;
            if (s.Module is not null && s.SamplingUntil == 0)
            {
                var scene = s.Module.Sample(now, false);
                Record(s, "scene", "at5t", scene, s.Module.Quality, clock.Elapsed.TotalMilliseconds - start);
                if (s.Module.HasVehicle && s.AutoScene != s.Module.Scene)
                { s.AutoScene = s.Module.Scene; StartSampling(s, now, 30000); Record(s, "event", "collector", [], "ok", 0, "at5t_scene_detected"); }
                if (!s.Module.HasVehicle) s.AutoScene = 0;
            }
        }
        if (s.SamplingUntil > 0)
        {
            if (now >= s.SamplingUntil) { s.SamplingUntil = 0; s.Phase = s.Module is null ? "unsupported" : "basic"; Record(s, "event", "collector", [], "ok", 0, "sampling_limit"); }
            else if (s.Module is not null && now >= s.NextSample)
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
                if (s.Module.Fresh && !s.Fresh) Record(s, "event", "collector", [], "ok", 0, "candidate_input_edge"); s.Fresh = s.Module.Fresh;
                if (s.Module.Quality == "ok" && !s.Module.HasVehicle) { s.SamplingUntil = 0; Record(s, "event", "collector", [], "ok", 0, "scene_exit"); }
            }
            else if (s.Module is null) s.Phase = s.Process is null ? "basic" : "unsupported";
        }
        if (s.Process is not null && now >= s.NextFlush) Flush(s, "recording");
    }
    private void End(DiagnosticSession s, string reason)
    {
        if (reason == "collector_error" && s.Process is not null) retryAfter[s.Process.Identity.Pid] = clock.Elapsed.TotalMilliseconds + 30000;
        s.Observer?.Dispose(); Observe(s, allowRestart: false);
        if (reason != "game_exit") Record(s, "event", "collector", [], "ok", 0, reason);
        Flush(s, reason, wait: true); sessions.Remove(s); s.Process?.Dispose();
    }
    private void EndAll(string reason) { bindingObserver?.Dispose(); launcher = null; launchAttempts.Clear(); foreach (var s in sessions.ToArray()) End(s, reason); foreach (var p in candidates.Values) p.Dispose(); candidates.Clear(); }
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
                    try { var result = binding.GetAwaiter().GetResult(); if (options.Enabled && !closing && result.Process.Identity.Path.Equals(Path.Combine(clientDirectory, "DNF.exe"), StringComparison.OrdinalIgnoreCase)) Attach(result.Process, result.Attempt, result.Index, result.Observer); else { result.Observer?.Dispose(); result.Process.Dispose(); } }
                    catch (Exception) { retryAfter[bindingPid] = clock.Elapsed.TotalMilliseconds + 30000; error = "process_binding_failed"; }
                    binding = null; bindingObserver = null;
                }
                var now = clock.Elapsed.TotalMilliseconds;
                if (options.Enabled)
                {
                    if (now >= discover) { try { Discover(); } catch (Exception) { error = "discovery_failed"; } discover = now + 2000; }
                    foreach (var s in sessions.ToArray()) try { Sample(s, clock.Elapsed.TotalMilliseconds); } catch (Exception) { error = "collector_read_failed"; End(s, "collector_error"); }
                }
                if (now >= publish) { Publish(); publish = now + 1000; }
                pacer.Wait(options.Enabled && sessions.Any(s => s.SamplingUntil > now) ? 5 : 100);
            }
        }
        finally {
            if (binding is not null) try { var result = binding.GetAwaiter().GetResult(); result.Observer?.Dispose(); result.Process.Dispose(); } catch (Exception) { }
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
                try {
                    if (job.Completion is not null) {
                        if (writeFailed && job.Summary.EndReason == "barrier") job.Completion.SetException(new IOException("日志尚未写入，请检查保存位置后重试。"));
                        else job.Completion.SetResult();
                        continue;
                    }
                    storage.Save(job); clips = storage.List(); writeFailed = false;
                    if (error is "diagnostic_write_failed" or "writer_queue_full") error = "";
                } catch (Exception) { writeFailed = true; error = "diagnostic_write_failed"; }
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
        var rows = sessions.Select(s => new DiagnosticInstance(s.Id, s.Process!.Window, s.Process!.Identity.Pid, s.StartedUtc, s.Phase, s.Process!.Version, true, s.CrashStatus)).ToArray();
        var phase = !options.Enabled ? "off" : error != "" ? "error" : sessions.Count > 0 ? "basic" : "waiting";
        var message = phase switch { "off" => "已关闭", "error" => "日志记录异常：" + error, "basic" => "正在记录本次启动", _ => binding is not null ? "正在读取游戏版本" : "等待游戏启动" };
        status = new(options.Enabled, phase, message, rows, clips);
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
