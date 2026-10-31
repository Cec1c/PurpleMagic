using System.ComponentModel;
using System.Diagnostics;
using System.IO.Pipes;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using Microsoft.Win32.SafeHandles;
using System.Security.Principal;

namespace Dnf90.Launcher.Diagnostics;

internal sealed class DiagnosticObserverClient : IDisposable
{
    private readonly CancellationTokenSource cancel = new();
    private readonly Task task;
    private int disposed;
    private int dropped;
    private int connected;
    private readonly ObserverBuffer messages = new();
    internal bool Completed => task.IsCompleted;
    internal bool Take(out ObserverMessage? message) {
        if (messages.TryRead(out message)) return true;
        var count = Interlocked.Exchange(ref dropped, 0);
        if (count == 0) return false;
        message = new("observer_dropped", new() { ["count"] = new(count) }); return true;
    }
    internal DiagnosticObserverClient(ProcessIdentity identity, bool elevated)
    {
        void receive(ObserverMessage message) { if (!messages.TryWrite(message)) Interlocked.Increment(ref dropped); }
        task = Task.Run(async () => {
            try {
                using var parent = Process.GetCurrentProcess();
                var name = "PurpleMagic.Diagnostics." + Guid.NewGuid().ToString("N");
                using var server = new NamedPipeServerStream(name, PipeDirection.InOut, 1, PipeTransmissionMode.Byte, PipeOptions.Asynchronous | PipeOptions.CurrentUserOnly);
                var start = new ProcessStartInfo(Environment.ProcessPath!) { UseShellExecute = elevated, CreateNoWindow = true, WindowStyle = ProcessWindowStyle.Hidden };
                if (elevated) start.Verb = "runas";
                foreach (var arg in new[] { "--diagnostic-observer", name, parent.Id.ToString(), parent.StartTime.ToUniversalTime().ToFileTimeUtc().ToString() }) start.ArgumentList.Add(arg);
                using var child = Process.Start(start) ?? throw new IOException();
                using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancel.Token); timeout.CancelAfter(TimeSpan.FromSeconds(20));
                await server.WaitForConnectionAsync(timeout.Token);
                if (!GetNamedPipeClientProcessId(server.SafePipeHandle, out var client) || client != child.Id) throw new IOException();
                using var writer = new StreamWriter(server, new UTF8Encoding(false), 1024, leaveOpen: true) { AutoFlush = true };
                using var reader = new StreamReader(server, Encoding.UTF8, false, 4096, leaveOpen: true);
                await writer.WriteLineAsync(JsonSerializer.Serialize(new ObserverTarget(identity.Pid, identity.Created, identity.Path), DiagnosticJson.Default.ObserverTarget));
                Volatile.Write(ref connected, 1);
                Task stop = Task.Delay(Timeout.Infinite, cancel.Token); var stopping = false;
                var read = reader.ReadLineAsync();
                while (true) {
                    if (await Task.WhenAny(read, stop) == stop) {
                        if (stopping) break;
                        await writer.WriteLineAsync("stop").WaitAsync(TimeSpan.FromSeconds(1));
                        stopping = true; stop = Task.Delay(2000); continue;
                    }
                    var line = await read; if (line is null) break;
                    if (line.Length > 65535) throw new InvalidDataException();
                    var message = JsonSerializer.Deserialize(line, DiagnosticJson.Default.ObserverMessage) ?? throw new InvalidDataException();
                    receive(message);
                    read = reader.ReadLineAsync();
                }
                if (!cancel.IsCancellationRequested) receive(new("observer_disconnected", []));
            } catch (Win32Exception e) { receive(new(e.NativeErrorCode == 1223 ? "elevation_cancelled" : "observer_failed", new() { ["win32"] = new(e.NativeErrorCode) })); }
            catch (OperationCanceledException) { if (!cancel.IsCancellationRequested) receive(new("observer_timeout", [])); }
            catch (Exception) { if (!cancel.IsCancellationRequested) receive(new("observer_failed", [])); }
        });
    }
    public void Dispose() {
        if (Interlocked.Exchange(ref disposed, 1) != 0) return;
        cancel.Cancel();
        if (Volatile.Read(ref connected) != 0) task.Wait(TimeSpan.FromSeconds(3));
        _ = task.ContinueWith(_ => cancel.Dispose(), TaskScheduler.Default);
    }
    [DllImport("kernel32.dll")] private static extern bool GetNamedPipeClientProcessId(SafePipeHandle pipe, out int pid);
}

public static class DiagnosticObserver
{
    // This entry point has no file writes or general command execution. Only the
    // same executable, same user, live parent and exact PID/creation time may bind.
    public static async Task<int> Run(string[] args)
    {
        if (!OperatingSystem.IsWindows() || args.Length != 4 || args[0] != "--diagnostic-observer" ||
            !args[1].StartsWith("PurpleMagic.Diagnostics.", StringComparison.Ordinal) || args[1].Length != 56 ||
            !Guid.TryParseExact(args[1][24..], "N", out _) || !int.TryParse(args[2], out var parentId) || !long.TryParse(args[3], out var created)) return 2;
        try {
            using var parent = Process.GetProcessById(parentId);
            using var self = Process.GetCurrentProcess();
            if (parent.StartTime.ToUniversalTime().ToFileTimeUtc() != created || parent.SessionId != self.SessionId || !SameUser(parent) ||
                !string.Equals(parent.MainModule?.FileName, Environment.ProcessPath, StringComparison.OrdinalIgnoreCase)) return 3;
            // UAC changes the token owner to Administrators, but retains TokenUser.
            // Server ACL + same user/session + exact peer PID authenticate both ends.
            using var pipe = new NamedPipeClientStream(".", args[1], PipeDirection.InOut, PipeOptions.Asynchronous, TokenImpersonationLevel.Anonymous);
            await pipe.ConnectAsync(10000);
            if (!GetNamedPipeServerProcessId(pipe.SafePipeHandle, out var server) || server != parentId) return 4;
            using var reader = new StreamReader(pipe, Encoding.UTF8, false, 4096, leaveOpen: true);
            using var writer = new StreamWriter(pipe, new UTF8Encoding(false), 4096, leaveOpen: true) { AutoFlush = true };
            using var stop = new CancellationTokenSource();
            using var handshake = new CancellationTokenSource(10000);
            var line = await reader.ReadLineAsync(handshake.Token);
            if (line is null || line.Length > 4096) return 5;
            var target = JsonSerializer.Deserialize(line, DiagnosticJson.Default.ObserverTarget) ?? throw new InvalidDataException();
            if (!Path.IsPathFullyQualified(target.Path) || !Path.GetFileName(target.Path).Equals("DNF.exe", StringComparison.OrdinalIgnoreCase)) return 6;
            // Disconnect/parent exit ends capture even if the launcher crashes.
            var disconnected = Task.Run(async () => { try { await reader.ReadLineAsync(stop.Token); } catch (Exception) { } finally { stop.Cancel(); } });
            var messages = new ObserverBuffer();
            var dropped = 0;
            void Emit(ObserverMessage m) {
                m.Fields["observed_unix_ms"] = new(DateTimeOffset.UtcNow.ToUnixTimeMilliseconds());
                if (!messages.TryWrite(m)) Interlocked.Increment(ref dropped);
            }
            var send = Task.Run(async () => {
                try { await foreach (var message in messages.ReadAll()) {
                    await writer.WriteLineAsync(JsonSerializer.Serialize(message, DiagnosticJson.Default.ObserverMessage));
                    var count = Interlocked.Exchange(ref dropped, 0);
                    if (count > 0) await writer.WriteLineAsync(JsonSerializer.Serialize(new ObserverMessage("observer_dropped", new() { ["count"] = new(count) }), DiagnosticJson.Default.ObserverMessage));
                } } catch (Exception) { stop.Cancel(); }
            });
            await Task.Factory.StartNew(() => {
                try { NativeExceptionCapture.Run(target, () => stop.IsCancellationRequested || parent.HasExited, Emit); }
                catch (Exception) { Emit(new("observer_failed", [])); }
                finally { messages.Complete(); }
            }, CancellationToken.None, TaskCreationOptions.LongRunning, TaskScheduler.Default);
            stop.Cancel(); await send; await disconnected; return 0;
        } catch (Exception) { return 1; }
    }
    [System.Runtime.Versioning.SupportedOSPlatform("windows")]
    private static bool SameUser(Process parent)
    {
        if (!OpenProcessToken(parent.Handle, 8, out var token)) return false;
        using (token) using (var remote = new WindowsIdentity(token.DangerousGetHandle())) using (var self = WindowsIdentity.GetCurrent())
            return self.User is not null && self.User == remote.User;
    }
    [DllImport("advapi32.dll", SetLastError = true)] private static extern bool OpenProcessToken(nint process, uint access, out SafeAccessTokenHandle token);
    [DllImport("kernel32.dll")] private static extern bool GetNamedPipeServerProcessId(SafePipeHandle pipe, out int pid);
}
