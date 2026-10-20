using System.Diagnostics;
using System.IO.Compression;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Dnf90.Launcher.Diagnostics;

internal static class DiagnosticChecks
{
    internal sealed class Memory : IDiagnosticMemory
    {
        public readonly Dictionary<uint, byte[]> Blocks = [];
        public Func<uint, int, byte[]?>? Intercept;
        public byte[] Read(uint address, int size)
        {
            if (Intercept?.Invoke(address, size) is { } replacement) return replacement;
            foreach (var (start, data) in Blocks)
                if (address >= start && (ulong)address + (uint)size <= (ulong)start + (uint)data.Length) return data.AsSpan((int)(address - start), size).ToArray();
            throw new IOException("fixture_unmapped");
        }
        public void Block(uint address, int bytes = 0x8000) => Blocks[address] = new byte[bytes];
        public void Put(uint address, byte[] data)
        {
            foreach (var (start, block) in Blocks)
                if (address >= start && (ulong)address + (uint)data.Length <= (ulong)start + (uint)block.Length) { data.CopyTo(block, (int)(address - start)); return; }
            throw new Exception("fixture write bounds");
        }
        public void U32(uint a, uint v) => Put(a, BitConverter.GetBytes(v));
        public void Float(uint a, float v) => Put(a, BitConverter.GetBytes(v));
    }
    internal static Memory Fixture()
    {
        var m = new Memory();
        foreach (var a in new uint[] { 0x057b6000, 0x057f8000, 0x10000000, 0x11000000, 0x12000000, 0x13000000, 0x14000000, 0x15000000, 0x16000000, 0x17000000, 0x18000000, 0x19000000 }) m.Block(a);
        m.U32(0x057b6fb0, 0x10000000); m.U32(0x057b6fb8, 0x19000000);
        m.U32(0x100000c8, 0x12000000); m.U32(0x1200000c, 0x12001000); m.U32(0x12000010, 0x12001004); m.U32(0x12001000, 0x11000000); m.U32(0x11000400, 61235);
        m.U32(0x10000000, 0x15000000); m.U32(0x15000cac, 0x02775fb0); m.U32(0x10003de0, 0x14000000); m.U32(0x1000643c, 0x13000000);
        m.U32(0x14000004, 0x14001000); m.U32(0x14000008, 0x1400101c); m.U32(0x14001004, 0x14002000); m.U32(0x14001008, 0x14002004); m.U32(0x14002000, 4);
        m.U32(0x14001014, 50); m.U32(0x14001018, 100); m.Put(0x14000030, [1]);
        m.U32(0x13000070, 0x13001000); m.U32(0x13000074, 0x13001008); m.U32(0x13001000, 0x14000000);
        m.U32(0x13000038, 0x13002000); m.U32(0x1300003c, 0x13002018); m.U32(0x13002000, 4); m.U32(0x13002004, 100); m.U32(0x13002008, 10000); m.Put(0x1300200c, [1, 1]);
        m.U32(0x057f8a18, 0x16000000); m.U32(0x16000024, 0x17000000);
        m.U32(0x100000b8, 0x18000000); m.U32(0x110000b8, 0x18001000);
        foreach (var p in new uint[] { 0x18000000, 0x18001000 }) { m.U32(p + 12, 0x15002000); m.Float(p + 16, p == 0x18000000 ? 655 : 615); m.Float(p + 20, 244); }
        m.U32(0x15002000, 0x0288e950); m.Block(0x057c2000, 0x1000); m.Float(0x057c2918, 2);
        return m;
    }
    internal static DiagnosticLine Line(string session, int n, double time, bool full = true, string type = "sample", Dictionary<string, DiagnosticField>? fields = null, long baseSequence = 1)
    {
        var r = new DiagnosticRecord(n, new DateTime(2026, 10, 20, 6, 0, 0, DateTimeKind.Utc).AddMilliseconds(time).ToString("O"), time, type, "fixture", session, .1, "ok", fields ?? [], full ? "full" : "delta", full ? null : baseSequence);
        var raw = JsonSerializer.SerializeToUtf8Bytes(r, DiagnosticJson.Default.DiagnosticRecord);
        return new([.. raw, 10], time, full, type);
    }
    internal static DiagnosticSave Job(string session, int records = 3)
    {
        var id = Guid.NewGuid().ToString("N"); var time = new DateTime(2026, 10, 20, 6, 0, 0, DateTimeKind.Utc);
        return new(new(session, id, new string('a', 64), new string('b', 64), 0, "synthetic", "fixture_complete", records, 0, 0, 0, false, ["synthetic_fixture_no_player_data"], ["basic", "at5t"], 5),
            Enumerable.Range(1, records).Select(i => Line(session, i, i * 5, true, i == 1 ? "identity" : "sample", new() { ["actor.action"] = new(0), ["unavailable"] = new(null, "unsupported", "fixture") })).ToArray(), time.AddMilliseconds(5).ToString("O"), time.AddMilliseconds(records * 5).ToString("O"));
    }
    internal static void VerifyLog(string path, Action<bool, string> check)
    {
        var lines = File.ReadAllLines(path); var sequence = 0L; double time = -1;
        check(JsonDocument.Parse(lines[0]).RootElement.GetProperty("type").GetString() == "log_header", "plain text log header");
        foreach (var line in lines.Skip(1).SkipLast(1)) {
            var row = JsonSerializer.Deserialize(line, DiagnosticJson.Default.DiagnosticRecord)!;
            check(row.Sequence > sequence && row.MonotonicMs >= time, "log sequence and time remain ordered"); sequence = row.Sequence; time = row.MonotonicMs;
        }
        check(JsonDocument.Parse(lines[^1]).RootElement.GetProperty("type").GetString() == "log_summary", "log includes final summary");
    }
    internal static async Task Run(string temp, Action<bool, string> check)
    {
        check(!new DiagnosticOptions().Enabled, "diagnostics default off");
        check(At5tModule.VersionFor(new string('a', 64), 111812608) == "unsupported", "unknown EXE cannot use memory layout");
        var m = Fixture(); var module = new At5tModule(m, 0x400000); var snapshot = module.Sample(0, true);
        check(module.Quality == "ok" && module.HasVehicle && module.Fresh, "AT5T stable full identity");
        check(snapshot["actor.action"].Value == 0 && snapshot["actor.action"].Status == "ok", "protected valid zero distinct from failure");
        check(snapshot["rule.0.logical.0"].Value == 4 && snapshot["rule.0.minimum_duration"].Value == 50 && snapshot["logical4.0.held_duration"].Value == 10000, "rules input time held row order");
        check(snapshot["descriptor.registered"].Value == 1 && snapshot["vehicle.0.distance_xy"].Value == 40 && snapshot["actor.z"].Value == 0, "registration and actual physics position distance");
        m.U32(0x14002000, 7); check(module.Sample(10, true)["rule.0.logical.0"].Value == 7, "nested rule change observed");
        m.U32(0x14001014, uint.MaxValue); check(module.Sample(12, true)["rule.0.minimum_duration"].Value == -1, "signed rule timing sentinel preserved");
        m.Put(0x18000098, [1]); m.U32(0x18000094, 0x19000000);
        check(module.Sample(15, true)["actor.position"].Status == "unsupported", "delegated physics not guessed");
        m = Fixture(); var reads = 0; m.Intercept = (address, _) => address == 0x14002000 && ++reads == 2 ? BitConverter.GetBytes(9u) : null;
        module = new(m, 0x400000); snapshot = module.Sample(0, true);
        check(module.Quality == "unstable" && snapshot["actor.address"].Value is null, "rule content mutation invalidates composite sample");
        m = Fixture(); m.Intercept = (address, _) => address == 0x057b6fb4 ? throw new IOException() : null;
        snapshot = new At5tModule(m, 0x400000).Sample(0, true); check(!snapshot.ContainsKey("actor.address"), "failed override never falls back to primary");
        m = Fixture(); m.U32(0x13000074, 0x13001009);
        snapshot = new At5tModule(m, 0x400000).Sample(0, true); check(snapshot["descriptor.registered"].Value is null && snapshot["descriptor.registered"].Status == "read_failed", "partial vector not mistaken for unregistered");
        m = Fixture(); m.U32(0x17002114, 0x12345678);
        check(new At5tModule(m, 0x400000).Sample(0, true)["actor.action"].Value is null, "protection integrity failure null");
        m = Fixture(); module = new(m, 0x400000); module.Sample(0, true); m.U32(0x100000c8, 0);
        check(!module.Sample(10, true).ContainsKey("vehicle.0.address"), "scene exit clears cached vehicle identity");
        var ring = new DiagnosticRing(1800); var sid = Guid.NewGuid().ToString("N");
        for (var i = 0; i < 1000; i++) ring.Add(Line(sid, i, i * 500, i % 10 == 0));
        var retained = ring.Snapshot(); check(ring.Bytes <= 1800 && ring.Overwritten > 0 && (retained.Length == 0 || retained.First().Full), "bounded ring retains complete delta base");
        var storageRoot = Path.Combine(temp, "diagnostics-storage"); var storage = new DiagnosticStorage(storageRoot);
        var exportRoot = Path.Combine(temp, "exports"); Directory.CreateDirectory(exportRoot);
        var job = Job(sid); storage.Save(job);
        var target = Path.Combine(exportRoot, "玩家日志.log");
        var export = storage.Export(sid, target); VerifyLog(export.Path, check);
        check(export.Bytes <= DiagnosticStorage.MaxLog && export.Path == target && File.ReadAllText(target).StartsWith("{"), "Save As exports real UTF8 .log to exact path");
        storage.Save(job with { Lines = [Line(sid, 4, 20)], EndedUtc = DateTime.UtcNow.ToString("O") });
        storage.Save(job with { Lines = [Line(sid, 4, 20)] });
        check(storage.List().Length == 1 && storage.ReadIndex(sid)!.Summary.Records == 4, "one launch accumulates batches without duplicate records");
        storage.Export(sid, target); VerifyLog(target, check);
        check(File.ReadAllLines(target).Length == 6, "Save As overwrite replaces complete file");
        var refused = false; try { storage.Export(sid, Path.ChangeExtension(target, ".zip")); } catch (InvalidDataException) { refused = true; }
        check(refused, "export requires log extension");
        var previous = File.ReadAllBytes(target); var local = Path.Combine(storageRoot, sid + ".log");
        var original = File.ReadAllBytes(local); var damaged = original.ToArray(); damaged[0] = (byte)'!'; File.WriteAllBytes(local, damaged);
        refused = false; try { storage.Export(sid, target); } catch (JsonException) { refused = true; }
        check(refused && File.ReadAllBytes(target).SequenceEqual(previous) && !Directory.EnumerateFiles(exportRoot, "*.writing").Any(), "failed export preserves old destination and removes partial file");
        File.WriteAllBytes(local, original); File.AppendAllText(local, "{partial crash");
        storage.Save(job with { Lines = [Line(sid, 5, 25)] });
        storage.Export(sid, target); VerifyLog(target, check);
        var expected = new List<string>();
        for (var i = 0; i < 7; i++) {
            var next = Job(Guid.NewGuid().ToString("N")) with { StartedUtc = DateTime.UtcNow.AddMinutes(i).ToString("O") };
            storage.Save(next); expected.Add(next.Summary.SessionId);
        }
        check(storage.List().Select(c => c.SessionId).SequenceEqual(expected.TakeLast(5).Reverse()) && File.Exists(export.Path), "exactly latest five launches retained, export untouched");
        storage.Save(job);
        check(storage.List().Length == 5 && storage.List().All(c => c.SessionId != sid), "old active launch cannot reenter latest five");
        foreach (var path in Directory.GetFiles(storageRoot, "*.log")) File.SetLastWriteTimeUtc(path, DateTime.UtcNow.AddDays(-8));
        check(storage.List().Length == 5, "five latest launches retained regardless of age");
        var largeStore = new DiagnosticStorage(Path.Combine(temp, "log-budget")); var largeId = Guid.NewGuid().ToString("N");
        var large = Job(largeId); var fields = Enumerable.Range(0, 300).ToDictionary(i => "fixture." + i, i => new DiagnosticField(i));
        for (var batch = 0; batch < 20; batch++) largeStore.Save(large with { Lines = Enumerable.Range(batch * 100 + 1, 100).Select(n => Line(largeId, n, n * 5, fields: fields)).ToArray() });
        var largeExport = largeStore.Export(largeId, Path.Combine(exportRoot, "large.log"));
        check(largeExport.Bytes <= DiagnosticStorage.MaxLog && largeStore.ReadIndex(largeId)!.Summary.Truncated, "log budget retains complete lines and explicit truncation summary");
        var broken = Path.Combine(temp, "disk-error"); File.WriteAllText(broken, "occupied");
        using (var failure = new DiagnosticService(broken)) { failure.Configure(new(true), "", "", 0); await Task.Delay(250); check(failure.Status.Enabled, "disk failure does not break service construction or configuration"); }
        var serviceRoot = Path.Combine(temp, "diagnostic-lifecycle");
        using (var service = new DiagnosticService(serviceRoot))
        {
            service.Configure(new(true, "connection"), "", new string('a', 64), 73); await Task.Delay(250);
            service.Event("login_started"); service.Event("login_error", 42, -1);
            await Task.Delay(400);
            service.Configure(new(false), "", "", 0); await Task.Delay(500);
            check(!service.Status.Enabled, "disable stops capture");
            var timer = Stopwatch.StartNew();
            for (var i = 0; i < 2000; i++) service.Event("operation_error", number: -1);
            check(timer.ElapsedMilliseconds < 1000, "bounded event saturation never blocks login caller");
        }
        var saved = new DiagnosticStorage(Path.Combine(serviceRoot, "diagnostics", "logs")).List();
        check(saved.Length >= 1 && saved.All(c => c.Categories.SequenceEqual(new[] { "basic" })), "prelaunch login failure exportable without game");
        if (OperatingSystem.IsWindows()) { await Native(temp, check); await MultipleGames(temp, check); }
    }
    private static async Task Native(string temp, Action<bool, string> check)
    {
        using var child = Process.Start(new ProcessStartInfo(Environment.ProcessPath!) { UseShellExecute = false, RedirectStandardOutput = true, RedirectStandardInput = true, ArgumentList = { "--diagnostic-memory-child" } })!;
        try {
            var address = uint.Parse((await child.StandardOutput.ReadLineAsync())!);
            using var read = new ReadOnlyGameProcess(child.Id, Environment.ProcessPath!, child.StartTime.ToUniversalTime().ToFileTimeUtc());
            read.VerifyImage(); check(read.Version == "unsupported" && BitConverter.ToUInt32(read.Read(address, 4)) == 0x12345678, "native read-only process identity hash and RPM");
            check(read.Performance()["cpu_total_ms"].Status == "ok", "native limited-query process metrics");
            var rejected = false; try { using var wrong = new ReadOnlyGameProcess(child.Id, Environment.ProcessPath!, 1); } catch (InvalidDataException) { rejected = true; }
            check(rejected, "PID creation mismatch rejected");
            rejected = false; try { using var wrong = new ReadOnlyGameProcess(child.Id, Path.Combine(temp, "wrong.exe")); } catch (InvalidDataException) { rejected = true; }
            check(rejected, "EXE path mismatch rejected");
            child.StandardInput.WriteLine("exit"); await child.WaitForExitAsync();
            check(read.Exited(out var code) && code == 259, "exit259 identified as terminated not PID reuse");
        } finally { if (!child.HasExited) child.Kill(); }
    }
    private static async Task Until(Func<bool> condition, string reason, int milliseconds = 10000)
    {
        var watch = Stopwatch.StartNew();
        while (!condition()) { if (watch.ElapsedMilliseconds > milliseconds) throw new Exception("Timed out: " + reason); await Task.Delay(50); }
    }
    private static async Task MultipleGames(string temp, Action<bool, string> check)
    {
        var client = Path.Combine(temp, "native-game-fixture"); Directory.CreateDirectory(client);
        foreach (var file in Directory.GetFiles(AppContext.BaseDirectory)) File.Copy(file, Path.Combine(client, Path.GetFileName(file)));
        var executable = Path.Combine(client, "DNF.exe"); File.Copy(Environment.ProcessPath!, executable);
        Process Start() => Process.Start(new ProcessStartInfo(executable) { UseShellExecute = false, RedirectStandardOutput = true, RedirectStandardInput = true, ArgumentList = { "--diagnostic-memory-child" } })!;
        using var a = Start(); using var b = Start();
        try {
            await a.StandardOutput.ReadLineAsync(); await b.StandardOutput.ReadLineAsync();
            var root = Path.Combine(temp, "multiple-games"); string[] ids;
            using (var service = new DiagnosticService(root)) {
                service.Configure(new(true, "connection"), client, "", 0);
                service.Event("login_started"); service.Event("account_verified"); service.Track(a, executable); service.Event("process_started");
                service.Event("login_started"); service.Event("account_verified"); service.Track(b, executable); service.Event("process_started");
                await Until(() => service.Status.Instances.Count(i => i.Pid != 0 && i.Selected) == 2, "both automatically bound");
                ids = service.Status.Instances.Select(i => i.Id).ToArray();
                await Until(() => service.Status.Clips.Length == 2, "automatic per launch logs");
                check(service.Status.Clips.Length == 2, "dual instances save separately without any manual action");
                foreach (var id in ids) {
                    var exported = await service.Export(id, Path.Combine(temp, id + ".log")); VerifyLog(exported.Path, check);
                    var rows = File.ReadAllLines(exported.Path).Skip(1).SkipLast(1).Select(l => JsonSerializer.Deserialize(l, DiagnosticJson.Default.DiagnosticRecord)!);
                    check(rows.All(r => r.SessionId == id), "active export contains only selected launch");
                    var attempts = rows.Where(r => r.Fields.ContainsKey("attempt")).Select(r => r.Fields["attempt"].Value).Distinct().ToArray();
                    check(attempts.Length == 1 && rows.Any(r => r.Reason == "account_verified"), "overlapping hash work preserves each launch connection events");
                }
                service.Configure(new(false), client, "", 0);
                await Until(() => !service.Status.Enabled, "disable ends collection");
                service.Configure(new(true), client, "", 0);
                await Until(() => service.Status.Instances.Length == 2, "reenable same games");
                check(service.Status.Instances.All(i => ids.Contains(i.Id)) && service.Status.Clips.Length == 2, "toggle resumes existing launch logs");
            }
            using (var recovered = new DiagnosticService(root)) {
                recovered.Configure(new(true, "connection"), client, "", 0);
                await Until(() => recovered.Status.Instances.Count(i => i.Pid != 0 && i.Selected) == 2, "registry recovery");
                check(recovered.Status.Instances.Where(i => i.Pid != 0).All(i => ids.Contains(i.Id)), "launcher restart resumes same game launch logs");
                a.StandardInput.WriteLine("exit"); await a.WaitForExitAsync();
                await Until(() => recovered.Status.Instances.Count(i => i.Pid != 0) == 1, "exit detach");
                check(recovered.Status.Instances.Single(i => i.Pid != 0).Pid == b.Id, "exited game removed and surviving instance unaffected");
                foreach (var id in ids) { var exported = await recovered.Export(id, Path.Combine(temp, "recovered-" + id + ".log")); VerifyLog(exported.Path, check); }
            }
        } finally { foreach (var p in new[] { a, b }) if (!p.HasExited) { p.StandardInput.WriteLine("exit"); await p.WaitForExitAsync(); } }
    }
    internal static void MemoryChild(bool at5t = false)
    {
        if (at5t) {
            var regions = new HashSet<uint>();
            foreach (var (start, data) in Fixture().Blocks) {
                var aligned = start & 0xffff0000;
                if (regions.Add(aligned) && VirtualAlloc((nint)(long)aligned, 65536, 0x3000, 4) == 0) throw new Exception("native AT5T fixture allocation");
                Marshal.Copy(data, 0, (nint)(long)start, data.Length);
            }
        }
        var block = VirtualAlloc((nint)0x20000000, 65536, 0x3000, 4);
        if (block == 0) throw new Exception("native fixture allocation");
        Marshal.WriteInt32(block, 0x12345678); Console.WriteLine(block.ToInt64()); Console.ReadLine();
        Environment.Exit(259);
    }
    [DllImport("kernel32.dll")] private static extern nint VirtualAlloc(nint address, nuint bytes, uint allocation, uint protection);
}
