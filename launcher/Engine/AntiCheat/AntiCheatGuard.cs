using System.Diagnostics;
using System.IO.Pipes;
using System.Runtime.InteropServices;
using System.Text.Json;
using System.Threading.Channels;
using Microsoft.Win32.SafeHandles;
using ServerS5A3;

namespace Dnf90.Launcher.AntiCheat;

// No file writes, process launch, arbitrary PID control, credentials or network API.
public static class AntiCheatGuard
{
    public static async Task<int> Run(string[] args, PlayerProfile root)
    {
        if (!OperatingSystem.IsWindows() || IntPtr.Size != 8 || args.Length != 4 || args[0] != "--anticheat-guard" ||
            !args[1].StartsWith(GuardWire.Prefix, StringComparison.Ordinal) || args[1].Length != GuardWire.Prefix.Length + 32 ||
            !Guid.TryParseExact(args[1][GuardWire.Prefix.Length..], "N", out _) || !int.TryParse(args[2], out var parentPid) || !long.TryParse(args[3], out var created)) return 2;
        try {
            using var self = Process.GetCurrentProcess(); using var parent = Process.GetProcessById(parentPid);
            if (!GuardNative.Elevated(self) || parent.StartTime.ToUniversalTime().Ticks != created ||
                GuardNative.User(self) != GuardNative.User(parent) || self.SessionId != parent.SessionId ||
                !GuardNative.Image(parent).Equals(Environment.ProcessPath, StringComparison.OrdinalIgnoreCase)) return 3;
            var parentElevated = GuardNative.Elevated(parent);
            // Hold the authenticated host file against replacement for this lifetime.
            using var host = new FileStream(Environment.ProcessPath!, FileMode.Open, FileAccess.Read, FileShare.Read);
            // CurrentUserOnly on the elevated client compares token owners (Administrators vs user).
            // The server ACL restricts the user SID; verify its exact PID below and deny impersonation.
            using var pipe = new NamedPipeClientStream(".", args[1], PipeDirection.InOut, PipeOptions.Asynchronous, System.Security.Principal.TokenImpersonationLevel.Anonymous);
            using var stop = new CancellationTokenSource();
            using var start = CancellationTokenSource.CreateLinkedTokenSource(stop.Token); start.CancelAfter(TimeSpan.FromSeconds(90));
            // Cover preparation/hash verification as well as the attached-game lifetime.
            var parents = Task.Run(async () => { try { await parent.WaitForExitAsync(stop.Token); } catch { } finally { stop.Cancel(); } });
            Task disconnected = Task.CompletedTask;
            try {
                await pipe.ConnectAsync(start.Token);
                if (!GetNamedPipeServerProcessId(pipe.SafePipeHandle, out var server) || server != parentPid) return 4;
                var request = JsonSerializer.Deserialize(await GuardWire.Read(pipe, Trust.MaxMetadata * 2, start.Token), GuardJson.Default.GuardRequest) ?? throw new InvalidDataException();
                var profile = request.Envelope is null ? root : Trust.Verify(Convert.FromBase64String(request.Envelope), root).Profile;
                profile.AntiCheat?.Validate();
                if (profile.AntiCheat is not { Enabled: true } policy || !Path.IsPathFullyQualified(request.Directory) || request.Directory.StartsWith(@"\\", StringComparison.Ordinal)) return 5;
                using var files = new ClientFileLease(request.Directory, profile, start.Token);
                var events = Channel.CreateBounded<GuardEvent>(new BoundedChannelOptions(64) { SingleReader = true, FullMode = BoundedChannelFullMode.Wait });
                void Emit(GuardEvent value) => events.Writer.TryWrite(value);
                var sender = Task.Run(async () => {
                    try { await foreach (var value in events.Reader.ReadAllAsync(stop.Token)) await GuardWire.Write(pipe, JsonSerializer.SerializeToUtf8Bytes(value, GuardJson.Default.GuardEvent), stop.Token); }
                    catch { stop.Cancel(); }
                });
                try {
                    var scanner = new GuardScanner(policy, files.ExeSha256, Emit);
                    scanner.Processes(parent.SessionId, start.Token);
                    if (scanner.Blocked) { await events.Writer.WriteAsync(new("blocked", "process-rule"), stop.Token); return 6; }
                    await events.Writer.WriteAsync(new("ready"), stop.Token);
                    var target = JsonSerializer.Deserialize(await GuardWire.Read(pipe, 1024, start.Token), GuardJson.Default.GuardTarget) ?? throw new InvalidDataException();
                    using var game = Process.GetProcessById(target.Pid);
                    if (game.StartTime.ToUniversalTime().Ticks != target.Created || game.StartTime < parent.StartTime || GuardNative.Parent(game) != parentPid ||
                        game.SessionId != parent.SessionId || GuardNative.User(game) != GuardNative.User(parent) || GuardNative.Elevated(game) != parentElevated ||
                        !GuardNative.Image(game).Equals(SafeFiles.Target(request.Directory, "DNF.exe"), StringComparison.OrdinalIgnoreCase)) return 7;
                    using var job = GuardNative.Job(); GuardNative.Assign(job, game);
                    await events.Writer.WriteAsync(new("attached"), stop.Token);
                    // Fixed protocol: only one game can be attached. Further input means stop.
                    disconnected = Task.Run(async () => { try { await GuardWire.Read(pipe, 16, stop.Token); } catch { } finally { stop.Cancel(); } });
                    try {
                        while (!stop.IsCancellationRequested && !game.HasExited) {
                            scanner.Game(game, stop.Token); scanner.Processes(parent.SessionId, stop.Token);
                            if (scanner.Blocked) { await events.Writer.WriteAsync(new("blocked", "runtime-rule"), stop.Token); break; }
                            await Task.Delay(3000, stop.Token);
                        }
                    } catch (OperationCanceledException) { }
                    if (!scanner.Blocked && !stop.IsCancellationRequested) await events.Writer.WriteAsync(new("ended"), stop.Token);
                    // Closing the private job also covers guard crash/kill. The launcher owns a second job.
                    return 0;
                } finally {
                    events.Writer.TryComplete();
                    try { await sender.WaitAsync(TimeSpan.FromSeconds(1)); } catch { stop.Cancel(); try { await sender; } catch { } }
                    stop.Cancel();
                }
            } finally { stop.Cancel(); await Task.WhenAll(disconnected, parents); }
        } catch { return 8; }
    }
    [DllImport("kernel32.dll")] private static extern bool GetNamedPipeServerProcessId(SafePipeHandle pipe, out int pid);
}
