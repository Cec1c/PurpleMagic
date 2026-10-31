using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;

namespace Dnf90.Launcher.Diagnostics;

// Dedicated x64 helper thread. Game exceptions always use NOT_HANDLED;
// only the debugger's initial attach breakpoint is consumed.
internal static class NativeExceptionCapture
{
    internal static void Run(ObserverTarget target, Func<bool> stopping, Action<ObserverMessage> emit)
    {
        if (!OperatingSystem.IsWindows() || IntPtr.Size != 8) { emit(new("platform_unsupported", [])); return; }
        using var identity = new ReadOnlyGameProcess(target.Pid, target.Path, target.Created);
        var process = OpenProcess(0x410, false, target.Pid);
        var processError = process == 0 ? Marshal.GetLastWin32Error() : (int?)null;
        var wow64 = false; var architectureKnown = process != 0 && IsWow64Process(process, out wow64); var bits = wow64 ? 32 : 64;
        if (process != 0 && !architectureKnown) processError = Marshal.GetLastWin32Error();
        // Symbol initialization may do file I/O: run before attaching/stopping the game.
        SymSetOptions(0x80 | 0x200 | 0x400 | 0x1000 | 0x2000 | 0x20000 | 0x40000 | 0x80000 | 0x2000000);
        var symbols = process != 0 && SymInitialize(process, AppContext.BaseDirectory, true);
        var symbolError = symbols ? (int?)null : Marshal.GetLastWin32Error();
        using var modules = new NativeModuleCatalog(emit);
        GameOperationCapture? operations = null;
        try { operations = new(target.Pid, emit); }
        catch (Win32Exception e) { emit(new("operations_unavailable", new() { ["win32"] = new(e.NativeErrorCode) })); }
        var attached = DebugActiveProcess(target.Pid);
        if (!attached) {
            var error = Marshal.GetLastWin32Error(); emit(new(error == 5 ? "access_denied" : "attach_failed", new() { ["win32"] = new(error) }));
        } else if (!DebugSetProcessKillOnExit(false)) {
            var error = Marshal.GetLastWin32Error(); DebugActiveProcessStop(target.Pid); attached = false; emit(new("attach_failed", new() { ["win32"] = new(error) }));
        } else emit(new("crash_ready", []));
        var initialBreakpoint = true; var captured = 0; var terminalCaptured = 0; var limited = false; long serial = 0;
        var prefix = Guid.NewGuid().ToString("N");
        var preceding = new Dictionary<uint, (NativeExceptionRecord Exception, string? Sp, long Time)>();
        try {
            var buffer = new byte[176]; // Verified by the native sizeof/offsetof fixture.
            while (!stopping() && !identity.Exited(out _)) {
                operations?.Pump();
                if (!attached) { Thread.Sleep(20); continue; }
                if (!WaitForDebugEvent(buffer, 20)) continue;
                var pause = Stopwatch.StartNew();
                var kind = BitConverter.ToUInt32(buffer); var pid = BitConverter.ToUInt32(buffer, 4); var tid = BitConverter.ToUInt32(buffer, 8);
                var disposition = 0x00010002u; ObserverMessage? message = null;
                try {
                    if (kind == 1) {
                        var record = NativeEvidenceReader.Parse(buffer, bits, prefix + ":" + ++serial); disposition = 0x80010001;
                        if (initialBreakpoint && record.FirstChance && record.ExceptionCode is "0x80000003" or "0x4000001F") { initialBreakpoint = false; disposition = 0x10002; }
                        else {
                            var previous = preceding.GetValueOrDefault(tid); preceding.Remove(tid);
                            if (!record.FirstChance || record.ExceptionCode is "0xC0000005" or "0xC0000006" or "0xC000001D" or "0xC0000094" or "0xC00000FD" or "0xC0000374" or "0xC0000409") {
                                if (record.FirstChance ? captured < 16 : terminalCaptured < 2) {
                                    if (record.FirstChance) captured++; else terminalCaptured++;
                                    message = Capture(process, processError, (int)tid, record, architectureKnown, wow64, symbols, symbolError, modules, pause);
                                    var native = message.Evidence!.Native!;
                                    var sp = native.Context.Registers.GetValueOrDefault(wow64 ? "ESP" : "RSP");
                                    // Correlation hint, never a merge: match the immediately preceding
                                    // same-thread full record and original SP, within two seconds.
                                    if (!record.FirstChance && previous.Exception is { } first && sp is not null && sp == previous.Sp &&
                                        Environment.TickCount64 - previous.Time <= 2000 && first.ExceptionCode == record.ExceptionCode &&
                                        first.ExceptionFlags == record.ExceptionFlags && first.ExceptionAddress == record.ExceptionAddress &&
                                        first.ParameterCount == record.ParameterCount && first.Parameters.SequenceEqual(record.Parameters))
                                        record = record with { RelatedExceptionId = first.ExceptionId, CorrelationStatus = "adjacent_same_thread_signature" };
                                    if (record.FirstChance) { if (preceding.Count >= 64) preceding.Clear(); preceding[tid] = (record, sp, Environment.TickCount64); }
                                    message = message with { Evidence = message.Evidence with { Native = native with { Exception = record } } };
                                } else if (!limited) { limited = true; emit(new("exception_limit", new() { ["first_limit"] = new(16), ["terminal_limit"] = new(2) })); }
                            }
                        }
                    } else if (kind is 3 or 6) {
                        modules.Load(process, BitConverter.ToUInt64(buffer, kind == 3 ? 40 : 24), (nint)BitConverter.ToInt64(buffer, 16), bits);
                        if (kind == 3) { Close(BitConverter.ToInt64(buffer, 24)); Close(BitConverter.ToInt64(buffer, 32)); }
                    } else if (kind == 2) Close(BitConverter.ToInt64(buffer, 16));
                    else if (kind == 4) preceding.Remove(tid);
                    else if (kind == 7) modules.Unload(BitConverter.ToUInt64(buffer, 16));
                } catch (Exception) {
                    if (kind == 1 && disposition == 0x80010001) message = Fallback(NativeEvidenceReader.Parse(buffer, bits, prefix + ":" + serial), bits);
                    else emit(new("capture_failed", []));
                } finally {
                    var beforeContinue = pause.Elapsed.TotalMilliseconds;
                    var continued = ContinueDebugEvent(pid, tid, disposition); var continueError = continued ? 0 : Marshal.GetLastWin32Error();
                    if (message?.Evidence?.Native is { } native) {
                        native.StageMs["before_continue"] = beforeContinue; native.StageMs["continue"] = pause.Elapsed.TotalMilliseconds - beforeContinue;
                        var elapsed = pause.Elapsed.TotalMilliseconds; message.Fields["capture_ms"] = new(elapsed);
                        emit(message with { Evidence = message.Evidence with { Native = native with { PauseMs = elapsed, BudgetExceeded = elapsed >= NativeEvidenceReader.PauseBudgetMs } } });
                    }
                    if (!continued) emit(new("continue_failed", new() { ["win32"] = new(continueError) }));
                }
                if (kind == 5) break;
            }
        } finally {
            if (attached) DebugActiveProcessStop(target.Pid);
            if (symbols) SymCleanup(process);
            if (process != 0) NativeCaptureApi.CloseHandle(process);
            operations?.Dispose(); emit(new("observer_stopped", []));
        }
    }
    private static void Close(long handle) { if (handle != 0 && handle != -1) NativeCaptureApi.CloseHandle((nint)handle); }
    private static ObserverMessage Fallback(NativeExceptionRecord record, int bits) => new("native_exception", new() {
        ["exception_code"] = new(NativeEvidenceReader.Value(record.ExceptionCode)), ["first_chance"] = new(record.FirstChance ? 1 : 0), ["thread_id"] = new(record.ThreadId)
    }, new("native_exception", Stack: [new("unmapped", NativeEvidenceReader.Hex(0, bits), Address: record.ExceptionAddress)],
        Native: new(record, new(bits == 32 ? "x86" : "x64", bits, NativeEvidenceReader.Hex(0, 32), "capture_failed", null, [], 0),
            new("failed", "capture_failed", null, NativeEvidenceReader.FrameLimit, 0), [], new("capture_failed", null), [], "unavailable", [], 0, false)));

    internal static ObserverMessage Capture(nint process, int? processError, int tid, NativeExceptionRecord record, bool architectureKnown, bool wow64,
        bool symbols, int? symbolError, NativeModuleCatalog catalog, Stopwatch pause)
    {
        var bits = wow64 ? 32 : 64; var modules = catalog.Snapshot(); var stages = new Dictionary<string, double>();
        var contextInfo = new NativeContext(architectureKnown ? wow64 ? "x86" : "x64" : "unknown", bits, NativeEvidenceReader.Hex(wow64 ? 0x10007u : 0x10000bu, 32), "unavailable", processError, [], 0);
        var unwind = new NativeUnwind("failed", "context_unavailable", null, NativeEvidenceReader.FrameLimit, 0);
        var frames = new List<DiagnosticStackFrame> { NativeEvidenceReader.Locate(NativeEvidenceReader.Value(record.ExceptionAddress), bits, modules) };
        var raw = Marshal.AllocHGlobal(1280); var context = (nint)((raw.ToInt64() + 15) & ~15L); nint thread = 0;
        try {
            var start = pause.Elapsed.TotalMilliseconds;
            try {
                thread = OpenThread(0x48, false, tid);
                if (thread == 0) contextInfo = contextInfo with { ContextStatus = "open_thread_failed", Win32Error = Marshal.GetLastWin32Error() };
                else if (!architectureKnown) contextInfo = contextInfo with { ContextStatus = "architecture_unavailable" };
                else {
                    Marshal.Copy(new byte[1232], 0, context, 1232); Marshal.WriteInt32(context, wow64 ? 0 : 48, wow64 ? 0x10007 : 0x10000b);
                    if (!(wow64 ? Wow64GetThreadContext(thread, context) : GetThreadContext(thread, context))) contextInfo = contextInfo with { ContextStatus = "get_context_failed", Win32Error = Marshal.GetLastWin32Error() };
                    else {
                        var original = new byte[1232]; Marshal.Copy(context, original, 0, original.Length);
                        contextInfo = contextInfo with { ContextStatus = "ok", Win32Error = null,
                            ContextFlags = NativeEvidenceReader.Hex(BitConverter.ToUInt32(original, wow64 ? 0 : 48), 32), Registers = NativeEvidenceReader.Registers(original, wow64) };
                    }
                }
            } catch (Exception) { contextInfo = contextInfo with { ContextStatus = "capture_failed" }; }
            contextInfo = contextInfo with { ElapsedMs = pause.Elapsed.TotalMilliseconds - start }; stages["context"] = contextInfo.ElapsedMs;
            start = pause.Elapsed.TotalMilliseconds; NativePage[] pages;
            try { pages = NativeEvidenceReader.Pages(process, record, contextInfo, pause); }
            catch (Exception) { pages = [new(["exception_ip"], record.ExceptionAddress, "capture_failed", null)]; }
            stages["pages"] = pause.Elapsed.TotalMilliseconds - start;
            start = pause.Elapsed.TotalMilliseconds; NativeCode code;
            try { code = NativeEvidenceReader.Code(process, NativeEvidenceReader.Value(record.ExceptionAddress), bits, modules, pause); }
            catch (Exception) { code = new("capture_failed", null); }
            stages["code"] = pause.Elapsed.TotalMilliseconds - start;
            start = pause.Elapsed.TotalMilliseconds;
            try {
                if (contextInfo.ContextStatus != "ok") unwind = unwind with { StopReason = contextInfo.ContextStatus, Win32Error = contextInfo.Win32Error };
                else if (!symbols) unwind = unwind with { StopReason = "symbol_initialization_failed", Win32Error = symbolError };
                else unwind = Walk(process, thread, context, contextInfo, frames, modules, pause);
            } catch (Exception) { unwind = unwind with { StopReason = "capture_failed" }; }
            unwind = unwind with { ElapsedMs = pause.Elapsed.TotalMilliseconds - start }; stages["unwind"] = unwind.ElapsedMs;
            var used = frames.Select(f => f.ModuleId).Append(code.ModuleId).Where(id => id is not null).ToHashSet();
            return new("native_exception", new() { ["exception_code"] = new(NativeEvidenceReader.Value(record.ExceptionCode)), ["first_chance"] = new(record.FirstChance ? 1 : 0),
                ["thread_id"] = new(record.ThreadId), ["frames"] = new(frames.Count) }, new("native_exception", Stack: [.. frames],
                Native: new(record, contextInfo, unwind, pages, code, modules.Where(m => used.Contains(m.Id)).Take(16).ToArray(), used.Count > 16 ? "reference_limit" : catalog.Status, stages, 0, false)));
        } finally { if (thread != 0) NativeCaptureApi.CloseHandle(thread); Marshal.FreeHGlobal(raw); }
    }
    private static NativeUnwind Walk(nint process, nint thread, nint context, NativeContext original, List<DiagnosticStackFrame> result, NativeModule[] modules, Stopwatch pause)
    {
        var frame = Marshal.AllocHGlobal(1024); var timer = Stopwatch.StartNew(); var wow64 = original.PointerBits == 32;
        try {
            Marshal.Copy(new byte[1024], 0, frame, 1024);
            void Address(int offset, string register) { Marshal.WriteInt64(frame, offset, (long)NativeEvidenceReader.Value(original.Registers[register])); Marshal.WriteInt32(frame, offset + 12, 3); }
            Address(0, wow64 ? "EIP" : "RIP"); Address(32, wow64 ? "EBP" : "RBP"); Address(48, wow64 ? "ESP" : "RSP");
            var seen = new HashSet<(ulong, ulong)>();
            while (true) {
                string? reason = result.Count >= NativeEvidenceReader.FrameLimit ? "frame_limit" : timer.ElapsedMilliseconds >= 150 || pause.ElapsedMilliseconds >= NativeEvidenceReader.PauseBudgetMs ? "time_limit" : null;
                if (reason is not null) return new("partial", reason, null, NativeEvidenceReader.FrameLimit, 0);
                Marshal.SetLastPInvokeError(0);
                if (!StackWalk64(wow64 ? 0x14cu : 0x8664u, process, thread, frame, context, 0, FunctionTable, ModuleBase, 0)) return new("partial", "api_failed", Marshal.GetLastWin32Error(), NativeEvidenceReader.FrameLimit, 0);
                var pc = (ulong)Marshal.ReadInt64(frame); var sp = (ulong)Marshal.ReadInt64(frame, 48);
                if (pc == 0) return new("complete", "end_of_stack", null, NativeEvidenceReader.FrameLimit, 0);
                if (!seen.Add((pc, sp))) return new("partial", "repeated_frame", null, NativeEvidenceReader.FrameLimit, 0);
                var location = NativeEvidenceReader.Locate(pc, original.PointerBits, modules);
                if (result.Count == 1 && location.Address == result[0].Address) continue;
                result.Add(location);
            }
        } finally { Marshal.FreeHGlobal(frame); }
    }
    private delegate nint FunctionTableCallback(nint process, ulong address);
    private delegate ulong ModuleBaseCallback(nint process, ulong address);
    private static readonly FunctionTableCallback FunctionTable = SymFunctionTableAccess64;
    private static readonly ModuleBaseCallback ModuleBase = SymGetModuleBase64;
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool DebugActiveProcess(int pid);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool DebugActiveProcessStop(int pid);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool DebugSetProcessKillOnExit(bool kill);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool WaitForDebugEvent([Out] byte[] debugEvent, uint milliseconds);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool ContinueDebugEvent(uint pid, uint tid, uint disposition);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern nint OpenProcess(uint access, bool inherit, int pid);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern nint OpenThread(uint access, bool inherit, int tid);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool IsWow64Process(nint process, out bool wow64);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool GetThreadContext(nint thread, nint context);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool Wow64GetThreadContext(nint thread, nint context);
    [DllImport("dbghelp.dll")] private static extern uint SymSetOptions(uint options);
    [DllImport("dbghelp.dll", EntryPoint = "SymInitializeW", CharSet = CharSet.Unicode, SetLastError = true)] private static extern bool SymInitialize(nint process, string path, bool invade);
    [DllImport("dbghelp.dll", SetLastError = true)] private static extern bool SymCleanup(nint process);
    [DllImport("dbghelp.dll")] private static extern nint SymFunctionTableAccess64(nint process, ulong address);
    [DllImport("dbghelp.dll")] private static extern ulong SymGetModuleBase64(nint process, ulong address);
    [DllImport("dbghelp.dll", SetLastError = true)] private static extern bool StackWalk64(uint machine, nint process, nint thread, nint frame, nint context, nint read, FunctionTableCallback function, ModuleBaseCallback module, nint translate);
}
