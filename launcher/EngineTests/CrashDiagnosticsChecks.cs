using System.Diagnostics;
using System.Text.Json;
using Dnf90.Launcher.Diagnostics;
using Dnf90.Launcher;
using ServerS5A3;

internal static class CrashDiagnosticsChecks
{
    internal static async Task Consent(string temp, PlayerProfile profile, Action<bool, string> check)
    {
        var path = Path.Combine(temp, "consent-migration"); Directory.CreateDirectory(path);
        var prefs = new Preferences(Diagnostics: new(true, ConsentVersion: 1));
        File.WriteAllBytes(Path.Combine(path, "preferences.json"), JsonSerializer.SerializeToUtf8Bytes(prefs, WireJson.Default.Preferences));
        using var engine = new LauncherEngine(profile, path);
        check(engine.State.Preferences.Diagnostics?.Enabled == false, "old enabled preference is migrated off before collection expands");
        var refused = false; try { await engine.SavePreferences(prefs); } catch (InvalidOperationException) { refused = true; }
        check(refused, "host refuses enable request without consent revision");
        refused = false; try { await engine.ElevateDiagnostics("unrelated-session"); } catch (InvalidOperationException) { refused = true; }
        check(refused, "elevation endpoint rejects unrelated session without invoking UAC");
        await engine.SavePreferences(prefs with { Diagnostics = new(true, ConsentVersion: DiagnosticOptions.CurrentConsent) });
        var saved = JsonSerializer.Deserialize(File.ReadAllBytes(Path.Combine(path, "preferences.json")), WireJson.Default.Preferences)!;
        check(saved.Diagnostics is { Enabled: true, HasConsent: true }, "explicit diagnostic consent persists through core RPC contract");
    }
    internal static async Task Run(string temp, Action<bool, string> check)
    {
        EvidenceContracts(check);
        using (var service = new DiagnosticService(Path.Combine(temp, "no-consent"))) {
            service.Configure(new(true), "", "", 0); service.Failure("update_error", new IOException("SECRET_TOKEN"));
            await Task.Delay(400); check(!service.Status.Enabled && service.Status.Clips.Length == 0, "legacy enabled preference cannot collect without new consent");
        }
        var root = Path.Combine(temp, "failure-details");
        using (var service = new DiagnosticService(root)) {
            service.Configure(new(true, ConsentVersion: DiagnosticOptions.CurrentConsent), "", "", 0);
            try { throw new IOException("SECRET_TOKEN C:/Private/Player", new System.Net.Sockets.SocketException(10061)); }
            catch (Exception e) { service.Failure("update_error", e); }
            var until = Stopwatch.StartNew(); while (service.Status.Clips.Length == 0 && until.ElapsedMilliseconds < 5000) await Task.Delay(50);
            check(service.Status.Clips.Length == 1, "update failure before login creates exportable log");
            var export = await service.Export(service.Status.Clips.Single().SessionId, Path.Combine(temp, "failure.log"));
            var text = File.ReadAllText(export.Path);
            check(text.Contains("managed_exception") && text.Contains("socket") && text.Contains("10061") && text.Contains("CrashDiagnosticsChecks"), "managed error chain and real method stack captured");
            check(!text.Contains("SECRET_TOKEN") && !text.Contains("C:/Private/Player"), "exception messages and paths excluded from export");
        }
        check(Enumerable.Range(0x30, 0x5b - 0x30).All(k => GameOperationCapture.KeyCategory(k) == "keyboard") && GameOperationCapture.KeyCategory(0x25) == "left", "text keys collapse to anonymous activity, direction has explicit category");
        var counts = new Dictionary<string, DiagnosticField>(); var down = new bool[256]; var raw = new byte[48];
        BitConverter.GetBytes(1u).CopyTo(raw, 0); BitConverter.GetBytes((ushort)0x41).CopyTo(raw, 30);
        GameOperationCapture.ObserveRaw(10, 20, raw, 24, counts, down);
        check(counts.Count == 0, "another foreground process contributes no operation data");
        GameOperationCapture.ObserveRaw(10, 10, raw, 24, counts, down); GameOperationCapture.ObserveRaw(10, 10, raw, 24, counts, down);
        BitConverter.GetBytes((ushort)0x42).CopyTo(raw, 30); GameOperationCapture.ObserveRaw(10, 10, raw, 24, counts, down);
        check(counts.Count == 1 && counts["keyboard"].Value == 2, "distinct text inputs collapse to counts and held-key repeats do not reconstruct text");
        Array.Clear(raw); BitConverter.GetBytes((ushort)0x401).CopyTo(raw, 28); BitConverter.GetBytes((short)-120).CopyTo(raw, 30); BitConverter.GetBytes(12345).CopyTo(raw, 36);
        GameOperationCapture.ObserveRaw(10, 10, raw, 24, counts, down);
        check(counts["mouse_left"].Value == 1 && counts["wheel"].Value == 1 && counts["mouse_motion"].Value == 1 && counts.Values.All(v => v.Value != 12345), "mouse click and wheel recorded without cursor coordinates");
        check(await DiagnosticObserver.Run(["--diagnostic-observer", "wrong", "0", "0"]) != 0, "observer rejects untrusted invocation");
        foreach (var arch in new[] { "x86", "x64" }) {
            var exe = Environment.GetEnvironmentVariable("PURPLEMAGIC_CRASH_FIXTURE_" + arch.ToUpperInvariant());
            if (string.IsNullOrEmpty(exe)) continue;
            await Native(exe, temp, arch, check);
            foreach (var mode in new[] { "read", "write", "execute" }) await Terminal(exe, temp, arch, mode, check);
        }
    }
    private static void EvidenceContracts(Action<bool, string> check)
    {
        var raw = new byte[176]; BitConverter.GetBytes(0xc0000005u).CopyTo(raw, 16); BitConverter.GetBytes(1u).CopyTo(raw, 168);
        BitConverter.GetBytes(2u).CopyTo(raw, 40); BitConverter.GetBytes(8ul).CopyTo(raw, 48); BitConverter.GetBytes(ulong.MaxValue).CopyTo(raw, 56);
        var record = NativeEvidenceReader.Parse(raw, 64, "fixture:1");
        check(record.AccessKind == "execute" && record.FaultAddress == "0xFFFFFFFFFFFFFFFF", "64-bit exception values are lossless hex strings");
        var evidence = new DiagnosticEvidence("native_exception", Native: new(record, new("x64", 64, "0x0010000B", "ok", null, new() { ["RAX"] = "0xFFFFFFFFFFFFFFFF" }, 0), new("partial", "time_limit", null, 64, 0), [], new("outside_module", null), [], "ok", [], 0, false));
        var message = new ObserverMessage("native_exception", [], evidence);
        var roundtrip = JsonSerializer.Deserialize(JsonSerializer.Serialize(message, DiagnosticJson.Default.ObserverMessage), DiagnosticJson.Default.ObserverMessage)!;
        check(roundtrip.Evidence!.Native!.Exception.FaultAddress == record.FaultAddress && roundtrip.Evidence.Native.Context.Registers["RAX"] == "0xFFFFFFFFFFFFFFFF", "NativeAOT contract retains maximum pointer and register exactly");
        BitConverter.GetBytes(0xffffffffu).CopyTo(raw, 40);
        check(NativeEvidenceReader.Parse(raw, 64, "fixture:2") is { ParameterStatus: "invalid_count_truncated", Parameters.Length: 15 }, "corrupt parameter count is bounded and explicit");
        BitConverter.GetBytes(1u).CopyTo(raw, 40);
        check(NativeEvidenceReader.Parse(raw, 64, "fixture:3") is { AccessKind: null, FaultAddress: null }, "short exception record is not guessed");
        BitConverter.GetBytes(2u).CopyTo(raw, 40); BitConverter.GetBytes(7ul).CopyTo(raw, 48);
        check(NativeEvidenceReader.Parse(raw, 64, "fixture:4").AccessKind == "unknown", "unknown access preserves raw value");
        check(NativeEvidenceReader.Locate(ulong.MaxValue, 64, []).Address == "0xFFFFFFFFFFFFFFFF", "unmapped frame keeps absolute address");
        var budget = Stopwatch.StartNew(); Thread.Sleep(NativeEvidenceReader.PauseBudgetMs + 1);
        check(NativeEvidenceReader.Code(0, 0, 64, [], budget).Status == "budget_exceeded" &&
            NativeEvidenceReader.Pages(0, record, new("x64", 64, "0x0010000B", "unavailable", null, [], 0), budget).All(p => p.Status == "budget_exceeded"),
            "expired total budget skips optional page and code APIs with explicit status");
        if (OperatingSystem.IsWindows()) {
            var page = NativeEvidenceReader.Query(0, 0, 64, "fault");
            check(page.Status == "query_failed" && page.Win32Error != 0 && page.BaseAddress is null, "invalid handle query differs from MEM_FREE");
            using var catalog = new NativeModuleCatalog(_ => { });
            var failed = NativeExceptionCapture.Capture(0, 6, int.MaxValue, record, true, false, false, 6, catalog, Stopwatch.StartNew());
            check(failed.Evidence!.Native!.Context.ContextStatus == "open_thread_failed" && failed.Evidence.Native.Exception.FaultAddress == "0xFFFFFFFFFFFFFFFF" && failed.Evidence.Stack!.Length == 1,
                "thread/process failure preserves raw exception and first address frame");
        }
        var queue = new ObserverBuffer(); var accepted = 0;
        for (var i = 0; i < 500; i++) if (queue.TryWrite(new("player_operations", []))) accepted++;
        for (var i = 0; i < 16; i++) check(queue.TryWrite(new("native_exception", new() { ["first_chance"] = new(1) })), "ordinary saturation preserves first-chance reserve");
        for (var i = 0; i < 500; i++) queue.TryWrite(new("observer_failed", []));
        check(accepted == 128 && queue.TryWrite(new("native_exception", new() { ["first_chance"] = new(0) })), "terminal reserve survives ordinary and failure saturation");
    }
    private static async Task Terminal(string executable, string root, string arch, string mode, Action<bool, string> check)
    {
        using var game = Process.Start(new ProcessStartInfo(executable) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardInput = true, RedirectStandardOutput = true })!;
        try {
            await game.StandardOutput.ReadLineAsync();
            using var read = new ReadOnlyGameProcess(game.Id, executable);
            using var observer = new DiagnosticObserverClient(read.Identity, false);
            var rows = new List<ObserverMessage>();
            async Task Wait(Func<bool> condition) {
                var clock = Stopwatch.StartNew();
                while (!condition()) {
                    while (observer.Take(out var row)) if (row is not null) rows.Add(row);
                    if (clock.ElapsedMilliseconds > 15000) throw new Exception(arch + " terminal " + mode + ": " + string.Join(',', rows.Select(r => r.Kind)));
                    await Task.Delay(20);
                }
            }
            await Wait(() => rows.Any(r => r.Kind == "crash_ready"));
            if (mode == "read") for (var i = 0; i < 20; i++) {
                await game.StandardInput.WriteLineAsync("handled");
                check(await game.StandardOutput.ReadLineAsync().WaitAsync(TimeSpan.FromSeconds(10)) == "handled", arch + " repeated handled exception resumes");
            }
            await game.StandardInput.WriteLineAsync("fatal-" + mode);
            var expectedFault = await game.StandardOutput.ReadLineAsync().WaitAsync(TimeSpan.FromSeconds(10));
            await game.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(15));
            await Wait(() => rows.Any(r => r.Kind == "native_exception" && r.Fields["first_chance"].Value == 0));
            var exceptions = rows.Where(r => r.Kind == "native_exception").Select(r => r.Evidence!.Native!.Exception).ToArray();
            var final = exceptions.Last();
            check((uint)game.ExitCode == 0xc0000005 && final.AccessKind == mode && final.FaultAddress == expectedFault, arch + " terminal " + mode + " preserves precise fault and exit semantics");
            if (mode == "read") check(exceptions.Count(e => e.FirstChance) == 16 && exceptions.Count(e => !e.FirstChance) == 1 && final.RelatedExceptionId is null && exceptions.Select(e => e.ExceptionId).Distinct().Count() == exceptions.Length,
                arch + " terminal budget survives 20 handled exceptions without false association");
            Directory.CreateDirectory(Path.Combine(root, "crash-receipts"));
            File.WriteAllLines(Path.Combine(root, "crash-receipts", arch + "-" + mode + ".jsonl"), rows.Select(r => JsonSerializer.Serialize(r, DiagnosticJson.Default.ObserverMessage)));
        } finally { if (!game.HasExited) { await game.StandardInput.WriteLineAsync("exit"); if (!game.WaitForExit(2000)) game.Kill(); } }
    }
    private static async Task Native(string executable, string root, string arch, Action<bool, string> check)
    {
        Directory.CreateDirectory(Path.Combine(root, "crash-receipts"));
        using var game = Process.Start(new ProcessStartInfo(executable) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardInput = true, RedirectStandardOutput = true })!;
        async Task<string?> Reply(string value) { await game.StandardInput.WriteLineAsync(value); return await game.StandardOutput.ReadLineAsync().WaitAsync(TimeSpan.FromSeconds(10)); }
        try {
            check(await game.StandardOutput.ReadLineAsync() == "ready", arch + " native fixture ready");
            using var read = new ReadOnlyGameProcess(game.Id, executable);
            var observer = new DiagnosticObserverClient(read.Identity, false);
            var rows = new List<ObserverMessage>();
            async Task Wait(Func<bool> condition, string name) {
                var clock = Stopwatch.StartNew();
                while (!condition()) {
                    while (observer.Take(out var row)) if (row is not null) rows.Add(row);
                    if (clock.ElapsedMilliseconds > 10000) throw new Exception(arch + " " + name + ": " + string.Join(',', rows.Select(r => r.Kind)));
                    await Task.Delay(25);
                }
            }
            try {
                await Wait(() => rows.Any(r => r.Kind == "crash_ready"), "attach");
                await Wait(() => rows.Any(r => r.Kind == "module_identity" && r.Evidence?.Module is { Name: "DNF.exe", HashStatus: "ok" }), "module identity");
                var imageHash = Convert.ToHexString(System.Security.Cryptography.SHA256.HashData(File.ReadAllBytes(executable))).ToLowerInvariant();
                check(rows.Any(r => r.Evidence?.Module is { Name: "DNF.exe" } m && m.Sha256 == imageHash), arch + " cached module hash matches complete fixture file");
                check(await Reply("module-cycle") == "cycled", arch + " disposable module load/unload");
                await Wait(() => rows.Any(r => r.Kind == "module_unloaded"), "module unload");
                check(rows.Where(r => r.Kind == "module_unloaded").All(r => r.Evidence!.Module!.Status == "unloaded" && rows.Any(loaded => loaded.Kind == "module_loaded" && loaded.Evidence!.Module!.Id == r.Evidence.Module.Id)), arch + " module lifecycle preserves generation IDs");
                check(await Reply("debugged") == "yes", arch + " observer attached to exact disposable target");
                check(await Reply("handled") == "handled", arch + " first-chance exception forwarded to game handler");
                await Wait(() => rows.Any(r => r.Kind == "native_exception"), "first chance stack");
                var exception = rows.First(r => r.Kind == "native_exception");
                check(exception.Fields["exception_code"].Value == 0xc0000005 && exception.Evidence?.Stack?.Length >= 3 && exception.Evidence.Stack.Count(f => f.Module == "DNF.exe") >= 3, arch + " crash call chain contains fixture module offsets");
                foreach (var access in new[] { "read", "write", "execute" }) {
                    var prior = rows.Count(r => r.Kind == "native_exception");
                    var expectedText = await Reply("case-" + access);
                    File.WriteAllText(Path.Combine(root, "crash-receipts", arch + "-expected-" + access + ".json"), expectedText!);
                    using var expected = JsonDocument.Parse(expectedText!);
                    check(await game.StandardOutput.ReadLineAsync() == "handled", arch + " " + access + " is handled by fixture");
                    await Wait(() => rows.Count(r => r.Kind == "native_exception") > prior, access + " evidence");
                    var actual = rows.Last(r => r.Kind == "native_exception").Evidence!.Native!;
                    check(actual.Exception.AccessKind == access && actual.Exception.ExceptionAddress == expected.RootElement.GetProperty("address").GetString() && actual.Exception.FaultAddress == expected.RootElement.GetProperty("fault").GetString(), arch + " " + access + " exact IP and fault address from native handler");
                    var registers = expected.RootElement.GetProperty("registers").EnumerateObject().ToArray();
                    // RF is set by Windows when delivering the exception to a handler;
                    // the debugger context is captured before that delivery.
                    check(registers.All(r => r.Name.EndsWith("Flags") ?
                        (NativeEvidenceReader.Value(actual.Context.Registers[r.Name]) & ~0x10000ul) == (NativeEvidenceReader.Value(r.Value.GetString()!) & ~0x10000ul) : actual.Context.Registers[r.Name] == r.Value.GetString()), arch + " " + access + " every original general register matches native handler");
                    check(actual.Pages.Length is > 0 and <= 6 && actual.Pages.Any(p => p.Sources.Contains("fault") && p.Status == (access == "read" ? "free" : "ok")), arch + " " + access + " page evidence distinguishes free from committed");
                    check(access == "execute" ? actual.Code.Status == "outside_module" : actual.Code.Status == "ok" && actual.Code.ActualBytes == 64 && actual.Code.Bytes.Length == 128, arch + " " + access + " bounded module-only code window");
                    check(actual.PauseMs > 0 && actual.StageMs.ContainsKey("context") && actual.StageMs.ContainsKey("continue"), arch + " total pause and stage timing recorded");
                }
                foreach (var mode in new[] { "partial", "unreadable" }) {
                    var prior = rows.Count(r => r.Kind == "native_exception");
                    using var expected = JsonDocument.Parse((await Reply("code-" + mode))!);
                    check(await game.StandardOutput.ReadLineAsync() == "handled", arch + " code window fixture continues");
                    await Wait(() => rows.Count(r => r.Kind == "native_exception") > prior, "code " + mode);
                    var actual = rows.Last(r => r.Kind == "native_exception").Evidence!.Native!;
                    File.WriteAllLines(Path.Combine(root, "crash-receipts", arch + "-handled.jsonl"), rows.Select(r => JsonSerializer.Serialize(r, DiagnosticJson.Default.ObserverMessage)));
                    check(mode == "partial" ? actual.Code is { Status: "partial", RequestedBytes: 64, ActualBytes: 14 } : actual.Code is { Status: "unreadable_page", ActualBytes: 0 }, arch + " code " + mode + " window has precise length/status: " + actual.Code.Status + "/" + actual.Code.ActualBytes);
                    check(actual.Context.ContextStatus == "ok" && actual.Exception.FaultAddress == expected.RootElement.GetProperty("fault").GetString(), arch + " unavailable code does not discard exception/context");
                }
                observer.Dispose();
                for (var i = 0; i < 80 && await Reply("debugged") == "yes"; i++) await Task.Delay(50);
                check(!game.HasExited && await Reply("debugged") == "no", arch + " disabling observer detaches without killing game");
                observer = new(read.Identity, false); rows.Clear();
                await Wait(() => rows.Any(r => r.Kind == "crash_ready"), "reattach");
                await game.StandardInput.WriteLineAsync("crash");
                await game.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(10));
                await Wait(() => rows.Any(r => r.Kind == "native_exception" && r.Fields["first_chance"].Value == 0), "second chance stack");
                check((uint)game.ExitCode == 0xc0000005, arch + " unhandled exception preserves actual target exit code");
                var phases = rows.Where(r => r.Kind == "native_exception").Select(r => r.Evidence!.Native!.Exception).ToArray();
                check(phases.Length == 2 && phases[0].FirstChance && !phases[1].FirstChance && phases[1].RelatedExceptionId == phases[0].ExceptionId && phases[0].ExceptionId != phases[1].ExceptionId, arch + " exception stages are linked without merging records");
                Directory.CreateDirectory(Path.Combine(root, "crash-receipts"));
                File.WriteAllLines(Path.Combine(root, "crash-receipts", arch + ".jsonl"), rows.Select(r => JsonSerializer.Serialize(r, DiagnosticJson.Default.ObserverMessage)));
            } finally { observer.Dispose(); }
        } finally { if (!game.HasExited) { await game.StandardInput.WriteLineAsync("exit"); if (!game.WaitForExit(2000)) game.Kill(); } }
    }
}
