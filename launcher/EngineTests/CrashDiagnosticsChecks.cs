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
        var prefs = new Preferences(Diagnostics: new(true));
        File.WriteAllBytes(Path.Combine(path, "preferences.json"), JsonSerializer.SerializeToUtf8Bytes(prefs, WireJson.Default.Preferences));
        using var engine = new LauncherEngine(profile, path);
        check(engine.State.Preferences.Diagnostics?.Enabled == false, "old enabled preference is migrated off before collection expands");
        var refused = false; try { await engine.SavePreferences(prefs); } catch (InvalidOperationException) { refused = true; }
        check(refused, "host refuses enable request without consent revision");
        refused = false; try { await engine.ElevateDiagnostics("unrelated-session"); } catch (InvalidOperationException) { refused = true; }
        check(refused, "elevation endpoint rejects unrelated session without invoking UAC");
        await engine.SavePreferences(prefs with { Diagnostics = new(true, ConsentVersion: 1) });
        var saved = JsonSerializer.Deserialize(File.ReadAllBytes(Path.Combine(path, "preferences.json")), WireJson.Default.Preferences)!;
        check(saved.Diagnostics is { Enabled: true, HasConsent: true }, "explicit diagnostic consent persists through core RPC contract");
    }
    internal static async Task Run(string temp, Action<bool, string> check)
    {
        using (var service = new DiagnosticService(Path.Combine(temp, "no-consent"))) {
            service.Configure(new(true), "", "", 0); service.Failure("update_error", new IOException("SECRET_TOKEN"));
            await Task.Delay(400); check(!service.Status.Enabled && service.Status.Clips.Length == 0, "legacy enabled preference cannot collect without new consent");
        }
        var root = Path.Combine(temp, "failure-details");
        using (var service = new DiagnosticService(root)) {
            service.Configure(new(true, ConsentVersion: 1), "", "", 0);
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
        }
    }
    private static async Task Native(string executable, string root, string arch, Action<bool, string> check)
    {
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
                check(await Reply("debugged") == "yes", arch + " observer attached to exact disposable target");
                check(await Reply("handled") == "handled", arch + " first-chance exception forwarded to game handler");
                await Wait(() => rows.Any(r => r.Kind == "native_exception"), "first chance stack");
                var exception = rows.First(r => r.Kind == "native_exception");
                check(exception.Fields["exception_code"].Value == 0xc0000005 && exception.Evidence?.Stack?.Length >= 3 && exception.Evidence.Stack.Count(f => f.Module == "DNF.exe") >= 3, arch + " crash call chain contains fixture module offsets");
                observer.Dispose();
                for (var i = 0; i < 80 && await Reply("debugged") == "yes"; i++) await Task.Delay(50);
                check(!game.HasExited && await Reply("debugged") == "no", arch + " disabling observer detaches without killing game");
                observer = new(read.Identity, false); rows.Clear();
                await Wait(() => rows.Any(r => r.Kind == "crash_ready"), "reattach");
                await game.StandardInput.WriteLineAsync("crash");
                await game.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(10));
                await Wait(() => rows.Any(r => r.Kind == "native_exception" && r.Fields["first_chance"].Value == 0), "second chance stack");
                check((uint)game.ExitCode == 0xc0000005, arch + " unhandled exception preserves actual target exit code");
                Directory.CreateDirectory(Path.Combine(root, "crash-receipts"));
                File.WriteAllLines(Path.Combine(root, "crash-receipts", arch + ".jsonl"), rows.Select(r => JsonSerializer.Serialize(r, DiagnosticJson.Default.ObserverMessage)));
            } finally { observer.Dispose(); }
        } finally { if (!game.HasExited) { await game.StandardInput.WriteLineAsync("exit"); if (!game.WaitForExit(2000)) game.Kill(); } }
    }
}
