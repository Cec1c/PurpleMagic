using System.ComponentModel;
using System.Diagnostics;
using System.IO.Pipes;
using System.Runtime.InteropServices;
using System.Text.Json;
using Microsoft.Win32.SafeHandles;
using ServerS5A3;

namespace Dnf90.Launcher.AntiCheat;

internal sealed class AntiCheatSession : IDisposable
{
    private readonly NamedPipeServerStream pipe;
    private readonly CancellationTokenSource stop = new();
    private readonly FileStream log;
    private readonly TaskCompletionSource ready = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly TaskCompletionSource attached = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private readonly Action<string> failure;
    private readonly Task reader;
    private SuspendedGame? game;
    private readonly object gate = new();
    private int disposed;
    internal bool Completed => reader.IsCompleted;
    private AntiCheatSession(NamedPipeServerStream pipe, string logDirectory, Action<string> failure)
    {
        this.pipe = pipe; this.failure = failure;
        SafeFiles.NoLinks(logDirectory); Directory.CreateDirectory(logDirectory);
        foreach (var old in new DirectoryInfo(logDirectory).GetFiles("*.log").OrderByDescending(f => f.CreationTimeUtc).Skip(4)) { SafeFiles.NoLinks(old.FullName); old.Delete(); }
        log = new FileStream(Path.Combine(logDirectory, Guid.NewGuid().ToString("N") + ".log"), FileMode.CreateNew, FileAccess.Write, FileShare.Read);
        reader = ReadEvents();
    }
    internal static async Task<AntiCheatSession> Start(string directory, byte[]? envelope, string stateRoot, Action<string> failure, CancellationToken token)
    {
        using var parent = Process.GetCurrentProcess();
        if (GuardNative.Elevated(parent)) throw new InvalidOperationException("AC01：请以普通权限打开登录器；保护助手会单独请求管理员权限。");
        var name = GuardWire.Prefix + Guid.NewGuid().ToString("N");
        var pipe = new NamedPipeServerStream(name, PipeDirection.InOut, 1, PipeTransmissionMode.Byte, PipeOptions.Asynchronous | PipeOptions.CurrentUserOnly);
        AntiCheatSession? session = null;
        try {
            token.ThrowIfCancellationRequested();
            var info = new ProcessStartInfo(Environment.ProcessPath!) { UseShellExecute = true, Verb = "runas", WindowStyle = ProcessWindowStyle.Hidden, WorkingDirectory = AppContext.BaseDirectory };
            foreach (var arg in new[] { "--anticheat-guard", name, parent.Id.ToString(), parent.StartTime.ToUniversalTime().Ticks.ToString() }) info.ArgumentList.Add(arg);
            using var helper = Process.Start(info) ?? throw new IOException();
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(token); timeout.CancelAfter(TimeSpan.FromSeconds(90));
            var connection = pipe.WaitForConnectionAsync(timeout.Token);
            if (await Task.WhenAny(connection, helper.WaitForExitAsync(timeout.Token)) != connection) throw new IOException();
            await connection;
            if (!GetNamedPipeClientProcessId(pipe.SafePipeHandle, out var pid) || pid != helper.Id) throw new IOException();
            session = new(pipe, Path.Combine(stateRoot, "anticheat"), failure);
            await GuardWire.Write(pipe, JsonSerializer.SerializeToUtf8Bytes(new GuardRequest(directory, envelope is null ? null : Convert.ToBase64String(envelope)), GuardJson.Default.GuardRequest), timeout.Token);
            await session.ready.Task.WaitAsync(timeout.Token); return session;
        } catch (Win32Exception e) when (e.NativeErrorCode == 1223) { session?.Dispose(); pipe.Dispose(); throw new InvalidOperationException("AC02：已取消管理员授权，保护模式下未启动游戏。"); }
        catch (InvalidOperationException e) when (e.Message.StartsWith("AC06", StringComparison.Ordinal)) { session?.Dispose(); pipe.Dispose(); throw; }
        catch { session?.Dispose(); pipe.Dispose(); throw new InvalidOperationException("AC03：保护助手未就绪，请检查管理员授权和保护文件。"); }
    }
    internal async Task<Process> Launch(string executable, string arguments)
    {
        SuspendedGame child;
        lock (gate) {
            if (disposed != 0 || stop.IsCancellationRequested) throw new InvalidOperationException("AC04：保护助手已退出。");
            child = GuardNative.StartSuspended(executable, arguments);
            game = child;
        }
        try {
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(stop.Token); timeout.CancelAfter(TimeSpan.FromSeconds(15));
            await GuardWire.Write(pipe, JsonSerializer.SerializeToUtf8Bytes(new GuardTarget(child.Process.Id, child.Process.StartTime.ToUniversalTime().Ticks), GuardJson.Default.GuardTarget), timeout.Token);
            await attached.Task.WaitAsync(timeout.Token);
            lock (gate) { timeout.Token.ThrowIfCancellationRequested(); child.Resume(); }
            return child.Process;
        } catch { child.Dispose(); throw new InvalidOperationException("AC05：游戏保护关联失败，已结束本次启动。"); }
    }
    private async Task ReadEvents()
    {
        var ended = false;
        Exception? terminal = null;
        try {
            while (!stop.IsCancellationRequested) {
                var raw = await GuardWire.Read(pipe, 2048, stop.Token);
                var value = JsonSerializer.Deserialize(raw, GuardJson.Default.GuardEvent) ?? throw new InvalidDataException();
                var line = System.Text.Encoding.UTF8.GetBytes(DateTimeOffset.UtcNow.ToString("O") + " " + System.Text.Encoding.UTF8.GetString(raw) + "\n");
                if (log.Length + line.Length < 1024 * 1024) { await log.WriteAsync(line, stop.Token); await log.FlushAsync(stop.Token); }
                if (value.Kind == "ready") ready.TrySetResult();
                if (value.Kind == "attached") attached.TrySetResult();
                if (value.Kind == "ended") { ended = true; break; }
                if (value.Kind == "blocked") { ended = true; terminal = new InvalidOperationException("AC06：命中反作弊规则，本次游戏已停止。详情见本机保护记录。"); Notify(terminal.Message); break; }
            }
        } catch (Exception) { }
        finally {
            ready.TrySetException(terminal ?? new IOException()); attached.TrySetException(terminal ?? new IOException());
            try {
                if (!ended && disposed == 0 && game is { } active && !active.Process.HasExited) Notify("AC07：反作弊监护已结束，本次游戏随之关闭。");
            } catch (InvalidOperationException) { }
            finally { lock (gate) { stop.Cancel(); game?.Dispose(); } log.Dispose(); }
        }
    }
    private void Notify(string message) { try { failure(message); } catch { /* UI failures cannot prevent job cleanup. */ } }
    public void Dispose()
    {
        if (Interlocked.Exchange(ref disposed, 1) != 0) return;
        lock (gate) { stop.Cancel(); game?.Dispose(); pipe.Dispose(); }
        _ = reader.ContinueWith(_ => stop.Dispose(), TaskScheduler.Default);
    }
    [DllImport("kernel32.dll")] private static extern bool GetNamedPipeClientProcessId(SafePipeHandle pipe, out int pid);
}
