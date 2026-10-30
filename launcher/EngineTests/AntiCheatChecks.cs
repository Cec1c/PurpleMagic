using System.Diagnostics;
using System.Security.Cryptography;
using System.Text.Json;
using Dnf90.Launcher;
using ServerS5A3;
#if ANTICHEAT
using Dnf90.Launcher.AntiCheat;
using Dnf90.Launcher.Diagnostics;
using System.IO.Pipes;
#endif

internal static class AntiCheatChecks
{
    internal static void Policy(string temp, Action<bool, string> check)
    {
        var empty = JsonSerializer.Deserialize("{\"ApiUrl\":\"\",\"AntiCheat\":{\"Enabled\":false}}", CoreJson.Default.PlayerProfile)!;
        check(empty.AntiCheat is { Enabled: false, Mode: "audit" }, "anti-cheat is opt-in with audit default (actual mode: " + (empty.AntiCheat?.Mode ?? "null") + ")");
        empty.AntiCheat!.Validate();
        void Reject(AntiCheatPolicy value) { try { value.Validate(); } catch (InvalidDataException) { check(true, "invalid protection policy rejected"); return; } throw new Exception("invalid policy accepted"); }
        new AntiCheatPolicy().Validate();
        Reject(new() { Mode = "kill-all" });
        Reject(new() { RequiredFiles = [new("../foreign.dll", new('a',64))] });
        Reject(new() { RequiredFiles = [new("CON.dll", new('a',64))] });
        Reject(new() { Rules = [new("rule", "process", "cheat.exe")] });
        Reject(new() { CodeRegions = [new("code", new('a',64), long.MaxValue, 32, new('b',64))] });
        var folder = Path.Combine(temp,"flavor"); Directory.CreateDirectory(folder);
        BundleInstaller.RequireBuildFlavor(folder,"standard");
        var refused=false; try{BundleInstaller.RequireBuildFlavor(folder,"anticheat");}catch(InvalidDataException){refused=true;}
        check(refused,"unmarked old bundle cannot replace anticheat core");
        File.WriteAllText(Path.Combine(folder,"build-flavor.json"),"{\"format\":1,\"flavor\":\"anticheat\"}");
        BundleInstaller.RequireBuildFlavor(folder,"anticheat");
        refused=false;try{LauncherBuild.RequireFlavor(LauncherBuild.Flavor=="standard"?"anticheat":"standard");}catch(InvalidOperationException){refused=true;}
        check(refused,"cross flavor signed updates rejected");
        var guard = typeof(LauncherEngine).Assembly.GetType("Dnf90.Launcher.AntiCheat.AntiCheatGuard");
        check((guard is not null)==LauncherBuild.AntiCheat,"guard implementation exists only in the anticheat flavor");
    }
#if ANTICHEAT
    internal static async Task Native(string fixture, string temp, Action<bool,string> check)
    {
        Directory.CreateDirectory(temp); fixture=Path.GetFullPath(fixture);
        Policy(temp, check);
        var dir=Path.GetDirectoryName(fixture)!;var pvf=Path.Combine(dir,"Script.pvf");File.WriteAllText(pvf,"isolated fixture data");
        var hash=Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(fixture)));
        var profile=new PlayerProfile("https://fixture.test",new('a',64),"","","192.0.2.1",7001,hash,Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(pvf))),"");
        using(var lease=new ClientFileLease(dir,profile)) {
            using(var read=File.OpenRead(pvf)) check(read.Length>0,"game can still read protected PVF");
            var blocked=false;try{using var write=File.OpenWrite(pvf);}catch(IOException){blocked=true;}
            check(blocked,"locked PVF refuses writes");
            blocked=false;try{File.Move(pvf,pvf+".moved");}catch(IOException){blocked=true;}
            check(blocked,"locked PVF refuses rename/replacement");
        }
        File.AppendAllText(pvf,"released"); check(true,"PVF writable after lease release");
        var rejected = false;
        try { using var invalid = new ClientFileLease(dir, profile); } catch (InvalidDataException) { rejected = true; }
        check(rejected, "changed PVF rejected before game startup");
        using (var cancelled = new CancellationTokenSource()) {
            cancelled.Cancel(); rejected = false;
            try { using var invalid = new ClientFileLease(dir, profile, cancelled.Token); } catch (OperationCanceledException) { rejected = true; }
            check(rejected, "file verification observes parent cancellation and releases leases");
        }
        using var self=Process.GetCurrentProcess();
        using var unrelated=Process.Start(new ProcessStartInfo(fixture){UseShellExecute=false,CreateNoWindow=true})!;
        using var game=GuardNative.StartSuspended(fixture,"");
        try {
            check(!game.Process.HasExited,"suspended game exists before guard attachment");
            check(GuardNative.Parent(game.Process)==self.Id && GuardNative.User(game.Process)==GuardNative.User(self),"target parent and user identity checked");
            using(var guardJob=GuardNative.Job()) {
                GuardNative.Assign(guardJob,game.Process);game.Resume();await Task.Delay(150);
                check(!game.Process.HasExited,"nested guard job allows normal game execution");
            }
            await game.Process.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(3));
            check(game.Process.HasExited&&!unrelated.HasExited,"guard loss kills only its assigned game");
            using var second=GuardNative.StartSuspended(fixture,"");second.Resume();second.Dispose();
            await second.Process.WaitForExitAsync().WaitAsync(TimeSpan.FromSeconds(3));
            check(second.Process.HasExited,"launcher job closure ends its game");
            var events=new List<GuardEvent>();
            var audit=new GuardScanner(new(){Mode="audit",Rules=[new("fixture-known","process",hash)]},hash,events.Add);
            using var auditTimeout = new CancellationTokenSource(TimeSpan.FromSeconds(30));
            do { auditTimeout.Token.ThrowIfCancellationRequested(); audit.Processes(self.SessionId,auditTimeout.Token); }
            while (!events.Any(e=>e.Kind=="rule_match"));
            check(events.Any(e=>e.Kind=="rule_match")&&!audit.Blocked,"known process in audit mode records without blocking");
            events.Clear();var enforce=new GuardScanner(new(){Mode="enforce",Rules=[new("fixture-known","process",hash)]},hash,events.Add);
            using var enforceTimeout = new CancellationTokenSource(TimeSpan.FromSeconds(30));
            do { enforceTimeout.Token.ThrowIfCancellationRequested(); enforce.Processes(self.SessionId,enforceTimeout.Token); } while (!enforce.Blocked);
            check(enforce.Blocked,"exact process hash rule blocks in enforce mode");
            events.Clear();
            var modules = new GuardScanner(new() { Mode = "enforce", CodeRegions = [new("fixture-code", hash, 0, 1, new('0',64))] }, hash, events.Add);
            modules.Game(unrelated, CancellationToken.None);
            check(!modules.Blocked && events.Any(e => e.Kind == "module_observed") && events.Any(e => e.Kind == "code_region_changed"), "unknown modules and code changes only record in enforce mode");
            var knownModule = new GuardScanner(new() { Mode = "enforce", Rules = [new("fixture-module", "module", hash)] }, hash, _ => {});
            knownModule.Game(unrelated, CancellationToken.None);
            check(knownModule.Blocked, "exact game module hash rule can block");
            using (var protectedGame = GuardNative.StartSuspended(fixture, "")) {
                protectedGame.Resume();
                using var read = new ReadOnlyGameProcess(protectedGame.Process.Id, fixture);
                using var observer = new DiagnosticObserverClient(read.Identity, false);
                var clock = Stopwatch.StartNew(); var attached = false;
                while (!attached && clock.ElapsedMilliseconds < 10000) {
                    while (observer.Take(out var row)) if (row?.Kind == "crash_ready") attached = true;
                    await Task.Delay(25);
                }
                modules.Game(protectedGame.Process, CancellationToken.None);
                check(attached && !modules.Blocked && !protectedGame.Process.HasExited, "diagnostic debugger coexists with protected game and scanner");
            }
            check(await AntiCheatGuard.Run(["--anticheat-guard","wrong","1","0"],profile)==2,"guard rejects malformed IPC identity");
            if (!GuardNative.Elevated(self))
                check(await AntiCheatGuard.Run(["--anticheat-guard",GuardWire.Prefix + Guid.NewGuid().ToString("N"),self.Id.ToString(),self.StartTime.ToUniversalTime().Ticks.ToString()],profile)==3,"guard refuses to run without UAC elevation");
            await Protocol(check);
        } finally { if(!unrelated.HasExited)unrelated.Kill();await unrelated.WaitForExitAsync(); }
    }
    private static async Task Protocol(Action<bool,string> check)
    {
        var name = GuardWire.Prefix + Guid.NewGuid().ToString("N");
        using var server = new NamedPipeServerStream(name, PipeDirection.InOut, 1, PipeTransmissionMode.Byte, PipeOptions.Asynchronous | PipeOptions.CurrentUserOnly);
        using var client = new NamedPipeClientStream(".", name, PipeDirection.InOut, PipeOptions.Asynchronous | PipeOptions.CurrentUserOnly);
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(5));
        await Task.WhenAll(server.WaitForConnectionAsync(timeout.Token), client.ConnectAsync(timeout.Token));
        var write = GuardWire.Write(client, [1,2,3], timeout.Token);
        check((await GuardWire.Read(server, 8, timeout.Token)).SequenceEqual(new byte[]{1,2,3}), "bounded IPC frame roundtrip");
        await write;
        var large = client.WriteAsync(BitConverter.GetBytes(int.MaxValue), timeout.Token);
        var rejected=false;try{await GuardWire.Read(server,8,timeout.Token);}catch(InvalidDataException){rejected=true;}
        await large;
        check(rejected,"oversized IPC frame rejected before allocation");
        using var shortWait = new CancellationTokenSource(TimeSpan.FromMilliseconds(100));
        rejected=false;try{await GuardWire.Read(server,8,shortWait.Token);}catch(OperationCanceledException){rejected=true;}
        check(rejected,"idle IPC read honors timeout");
        client.Dispose(); rejected=false;try{await GuardWire.Read(server,8,timeout.Token);}catch(EndOfStreamException){rejected=true;}
        check(rejected,"guard IPC detects peer disconnect");
    }
#endif
}
