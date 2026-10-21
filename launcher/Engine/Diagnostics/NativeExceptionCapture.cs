using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;

namespace Dnf90.Launcher.Diagnostics;

// Runs on one dedicated helper thread. Exceptions are forwarded to the game's
// own handlers; only the initial debugger-attach breakpoint is consumed.
internal static class NativeExceptionCapture
{
    internal static void Run(ObserverTarget target, Func<bool> stopping, Action<ObserverMessage> emit)
    {
        if (!OperatingSystem.IsWindows() || IntPtr.Size != 8) { emit(new("platform_unsupported", [])); return; }
        using var identity = new ReadOnlyGameProcess(target.Pid, target.Path, target.Created);
        GameOperationCapture? operations = null;
        try { operations = new(target.Pid, emit); }
        catch (Win32Exception e) { emit(new("operations_unavailable", new() { ["win32"] = new(e.NativeErrorCode) })); }
        var attached = DebugActiveProcess(target.Pid);
        if (!attached) {
            var error = Marshal.GetLastWin32Error();
            emit(new(error == 5 ? "access_denied" : "attach_failed", new() { ["win32"] = new(error) }));
        } else if (!DebugSetProcessKillOnExit(false)) {
            DebugActiveProcessStop(target.Pid); attached = false; emit(new("attach_failed", []));
        } else emit(new("crash_ready", []));
        var initialBreakpoint = true; var captured = 0; var terminalCaptured = 0; var limited = false;
        try {
            var buffer = new byte[176]; // DEBUG_EVENT64, including EXCEPTION_RECORD64.
            while (!stopping() && !identity.Exited(out _)) {
                operations?.Pump();
                if (!attached) { Thread.Sleep(20); continue; }
                if (!WaitForDebugEvent(buffer, 20)) continue;
                var kind = BitConverter.ToUInt32(buffer); var pid = BitConverter.ToUInt32(buffer, 4); var tid = BitConverter.ToUInt32(buffer, 8);
                var disposition = 0x00010002u; // DBG_CONTINUE for non-exception events.
                try {
                    if (kind == 1) {
                        var code = BitConverter.ToUInt32(buffer, 16); var first = BitConverter.ToUInt32(buffer, 168) != 0;
                        disposition = 0x80010001; // DBG_EXCEPTION_NOT_HANDLED: preserve original semantics.
                        if (initialBreakpoint && first && code is 0x80000003 or 0x4000001f) { initialBreakpoint = false; disposition = 0x10002; }
                        else if (!first || code is 0xc0000005 or 0xc0000006 or 0xc000001d or 0xc0000094 or 0xc00000fd or 0xc0000374 or 0xc0000409) {
                            if (first ? captured < 16 : terminalCaptured < 2) {
                                if (first) captured++; else terminalCaptured++;
                                var timer = Stopwatch.StartNew(); var stack = Stack(target.Pid, (int)tid, BitConverter.ToUInt64(buffer, 32));
                                emit(new("native_exception", new() { ["exception_code"] = new(code), ["first_chance"] = new(first ? 1 : 0), ["thread_id"] = new(tid),
                                    ["capture_ms"] = new(timer.Elapsed.TotalMilliseconds), ["frames"] = new(stack.Length), ["stack_complete"] = new(null, "unsupported", "native_unwind_best_effort") },
                                    new("native_exception", Stack: stack)));
                            } else if (!limited) { limited = true; emit(new("exception_limit", new() { ["limit"] = new(16) })); }
                        }
                    } else if (kind is 3 or 6) { var file = (nint)BitConverter.ToInt64(buffer, 16); if (file != 0 && file != -1) CloseHandle(file); }
                } catch (Exception) { emit(new("stack_unavailable", [])); }
                finally { ContinueDebugEvent(pid, tid, disposition); }
                if (kind == 5) break;
            }
        } finally {
            // Detach never terminates or restarts the target. No persistent WER or debugger registry changes.
            if (attached) DebugActiveProcessStop(target.Pid);
            operations?.Dispose(); emit(new("observer_stopped", []));
        }
    }
    private static DiagnosticStackFrame[] Stack(int pid, int tid, ulong exceptionAddress)
    {
        var process = OpenProcess(0x410, false, pid); var thread = OpenThread(0x48, false, tid);
        if (process == 0 || thread == 0) { if (process != 0) CloseHandle(process); if (thread != 0) CloseHandle(thread); return []; }
        var raw = Marshal.AllocHGlobal(1280); var context = (nint)((raw.ToInt64() + 15) & ~15L); var frame = Marshal.AllocHGlobal(1024);
        var initialized = false;
        try {
            var modules = new List<(ulong Base, ulong End, string Name)>();
            using (var p = Process.GetProcessById(pid)) foreach (ProcessModule m in p.Modules) if (modules.Count < 512) modules.Add(((ulong)m.BaseAddress, (ulong)m.BaseAddress + (uint)m.ModuleMemorySize, DiagnosticFailure.SafeName(Path.GetFileName(m.FileName))));
            DiagnosticStackFrame Locate(ulong pc) {
                var module = modules.FirstOrDefault(m => m.Base <= pc && pc < m.End);
                return module.Name is null ? new("unmapped", 0) : new(module.Name, pc - module.Base);
            }
            var result = new List<DiagnosticStackFrame> { Locate(exceptionAddress) };
            Marshal.Copy(new byte[1232], 0, context, 1232); Marshal.Copy(new byte[1024], 0, frame, 1024);
            if (!IsWow64Process(process, out var wow64)) return [.. result];
            Marshal.WriteInt32(context, wow64 ? 0 : 48, wow64 ? 0x10007 : 0x10000b);
            if (!(wow64 ? Wow64GetThreadContext(thread, context) : GetThreadContext(thread, context))) return [.. result];
            ulong ReadRegister(int offset) => wow64 ? (uint)Marshal.ReadInt32(context, offset) : (ulong)Marshal.ReadInt64(context, offset);
            void Address(int offset, ulong value) { Marshal.WriteInt64(frame, offset, (long)value); Marshal.WriteInt32(frame, offset + 12, 3); }
            Address(0, ReadRegister(wow64 ? 184 : 248)); Address(32, ReadRegister(wow64 ? 180 : 160)); Address(48, ReadRegister(wow64 ? 196 : 152));
            // Explicit local search path, no symbol server/environment path or dialogs.
            SymSetOptions(0x80 | 0x200 | 0x400 | 0x1000 | 0x2000 | 0x20000 | 0x40000 | 0x80000 | 0x2000000);
            initialized = SymInitialize(process, AppContext.BaseDirectory, true);
            if (!initialized) return [.. result];
            var timer = Stopwatch.StartNew(); var seen = new HashSet<(ulong, ulong)>();
            while (result.Count < 64 && timer.ElapsedMilliseconds < 150 && StackWalk64(wow64 ? 0x14cu : 0x8664u, process, thread, frame, context, 0, FunctionTable, ModuleBase, 0)) {
                var pc = (ulong)Marshal.ReadInt64(frame); var sp = (ulong)Marshal.ReadInt64(frame, 48);
                if (pc == 0 || !seen.Add((pc, sp))) break;
                var location = Locate(pc); if (result.Count == 1 && location == result[0]) continue;
                result.Add(location);
            }
            return [.. result];
        } finally { if (initialized) SymCleanup(process); Marshal.FreeHGlobal(frame); Marshal.FreeHGlobal(raw); CloseHandle(thread); CloseHandle(process); }
    }
    private delegate nint FunctionTableCallback(nint process, ulong address);
    private delegate ulong ModuleBaseCallback(nint process, ulong address);
    private static readonly FunctionTableCallback FunctionTable = SymFunctionTableAccess64;
    private static readonly ModuleBaseCallback ModuleBase = SymGetModuleBase64;
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool DebugActiveProcess(int pid);
    [DllImport("kernel32.dll")] private static extern bool DebugActiveProcessStop(int pid);
    [DllImport("kernel32.dll")] private static extern bool DebugSetProcessKillOnExit(bool kill);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool WaitForDebugEvent([Out] byte[] debugEvent, uint milliseconds);
    [DllImport("kernel32.dll")] private static extern bool ContinueDebugEvent(uint pid, uint tid, uint disposition);
    [DllImport("kernel32.dll")] private static extern nint OpenProcess(uint access, bool inherit, int pid);
    [DllImport("kernel32.dll")] private static extern nint OpenThread(uint access, bool inherit, int tid);
    [DllImport("kernel32.dll")] private static extern bool CloseHandle(nint handle);
    [DllImport("kernel32.dll")] private static extern bool IsWow64Process(nint process, out bool wow64);
    [DllImport("kernel32.dll")] private static extern bool GetThreadContext(nint thread, nint context);
    [DllImport("kernel32.dll")] private static extern bool Wow64GetThreadContext(nint thread, nint context);
    [DllImport("dbghelp.dll")] private static extern uint SymSetOptions(uint options);
    [DllImport("dbghelp.dll", EntryPoint = "SymInitializeW", CharSet = CharSet.Unicode)] private static extern bool SymInitialize(nint process, string path, bool invade);
    [DllImport("dbghelp.dll")] private static extern bool SymCleanup(nint process);
    [DllImport("dbghelp.dll")] private static extern nint SymFunctionTableAccess64(nint process, ulong address);
    [DllImport("dbghelp.dll")] private static extern ulong SymGetModuleBase64(nint process, ulong address);
    [DllImport("dbghelp.dll")] private static extern bool StackWalk64(uint machine, nint process, nint thread, nint frame, nint context, nint read, FunctionTableCallback function, ModuleBaseCallback module, nint translate);
}
