using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;
using Microsoft.Win32.SafeHandles;

namespace Dnf90.Launcher.Diagnostics;

internal interface IDiagnosticMemory { byte[] Read(uint address, int size); }

// Basic read-only identity/memory access; native exception observation is isolated.
internal sealed class ReadOnlyGameProcess : IDiagnosticMemory, IDisposable
{
    internal const uint Access = 0x1010; // QUERY_LIMITED_INFORMATION | VM_READ
    private readonly SafeProcessHandle handle;
    private readonly bool memoryAccess = true;
    public ProcessIdentity Identity { get; }
    public uint ModuleBase { get; private set; }
    public string Hash { get; private set; } = "";
    public string PvfHash { get; private set; } = "";
    public string Version { get; private set; } = "unsupported";
    public string Window { get; }
    public ReadOnlyGameProcess(int pid, string expectedPath, long? created = null, string? id = null)
    {
        if (!OperatingSystem.IsWindows()) throw new PlatformNotSupportedException();
        handle = OpenProcess(Access, false, pid);
        if (handle.IsInvalid) { handle.Dispose(); memoryAccess = false; handle = OpenProcess(0x1000, false, pid); }
        try
        {
            if (handle.IsInvalid) throw new Win32Exception();
            var path = new StringBuilder(32768); var length = path.Capacity;
            if (!QueryFullProcessImageName(handle, 0, path, ref length) || !GetProcessTimes(handle, out var start, out _, out _, out _) || !ProcessIdToSessionId(pid, out var session)) throw new Win32Exception();
            using var self = Process.GetCurrentProcess();
            if (session != self.SessionId || !Path.GetFullPath(path.ToString()).Equals(Path.GetFullPath(expectedPath), StringComparison.OrdinalIgnoreCase) || created is not null && start != created) throw new InvalidDataException("游戏进程身份已变化。");
            Identity = new(pid, start, session, path.ToString(), id ?? Guid.NewGuid().ToString("N"));
            using var p = Process.GetProcessById(pid);
            Window = p.MainWindowTitle.Length > 0 ? p.MainWindowTitle[..Math.Min(100, p.MainWindowTitle.Length)] : $"DNF · {DateTime.FromFileTimeUtc(start).ToLocalTime():HH:mm:ss}";
        }
        catch { handle.Dispose(); throw; }
    }
    public void VerifyImage()
    {
        using var file = new FileStream(Identity.Path, FileMode.Open, FileAccess.Read, FileShare.Read);
        Hash = Convert.ToHexString(SHA256.HashData(file)).ToLowerInvariant();
        Version = At5tModule.VersionFor(Hash, file.Length);
        var pvf = Path.Combine(Path.GetDirectoryName(Identity.Path)!, "Script.pvf");
        try { using var asset = new FileStream(pvf, FileMode.Open, FileAccess.Read, FileShare.Read); PvfHash = Convert.ToHexString(SHA256.HashData(asset)).ToLowerInvariant(); }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException) { /* Missing digest is explicit in the summary. */ }
        if (Exited(out _)) return; // Preserve the launch and exit log even when hashing outlives the game.
        using var snapshot = CreateToolhelp32Snapshot(0x18, Identity.Pid);
        var module = new ModuleEntry { Size = (uint)Marshal.SizeOf<ModuleEntry>() };
        var address = !snapshot.IsInvalid && Module32First(snapshot, ref module) ? module.Base.ToInt64() : 0;
        if (address is > 0 and <= uint.MaxValue) ModuleBase = (uint)address;
        if (ModuleBase == 0 || !memoryAccess) Version = "unsupported";
        if (Version != "unsupported")
        {
            // The approved images are PE32/i386. Validate the loaded image as well.
            if (ModuleBase == 0 || BitConverter.ToUInt16(Read(ModuleBase, 2)) != 0x5A4D) throw new InvalidDataException("游戏模块身份无效。");
            var offset = BitConverter.ToUInt32(Read(ModuleBase + 0x3C, 4));
            if (offset > 4096 || BitConverter.ToUInt32(Read(ModuleBase + offset, 4)) != 0x4550 || BitConverter.ToUInt16(Read(ModuleBase + offset + 4, 2)) != 0x14c || BitConverter.ToUInt16(Read(ModuleBase + offset + 24, 2)) != 0x10b) throw new InvalidDataException("游戏模块格式不匹配。");
        }
    }
    public byte[] Read(uint address, int size)
    {
        if (size < 0 || size > 65536 || address < 0x10000 || (ulong)address + (uint)size > (ulong)uint.MaxValue + 1) throw new IOException("memory_bounds");
        var bytes = new byte[size];
        if (!ReadProcessMemory(handle, (nint)(long)address, bytes, (nuint)size, out var read) || read != (nuint)size) throw new IOException("memory_read_failed");
        return bytes;
    }
    public bool Exited(out int code)
    {
        if (!GetExitCodeProcess(handle, out code)) throw new IOException("process_status_failed");
        if (code != 259) return true;
        if (!GetProcessTimes(handle, out _, out var exited, out _, out _)) throw new IOException("process_status_failed");
        return exited != 0; // A terminated process may itself return STILL_ACTIVE (259).
    }
    public Dictionary<string, DiagnosticField> Performance()
    {
        var result = new Dictionary<string, DiagnosticField>();
        if (GetProcessTimes(handle, out _, out _, out var kernel, out var user)) result["cpu_total_ms"] = new((kernel + user) / 10000d);
        else result["cpu_total_ms"] = new(null, "read_failed", "process_times");
        var memory = new MemoryCounters { Size = (uint)Marshal.SizeOf<MemoryCounters>() };
        if (GetProcessMemoryInfo(handle, ref memory, memory.Size)) { result["working_set_bytes"] = new(memory.WorkingSet); result["private_bytes"] = new(memory.Private); }
        else result["working_set_bytes"] = new(null, "read_failed", "process_memory");
        using var p = Process.GetProcessById(Identity.Pid);
        var window = p.MainWindowHandle;
        result["window_responsive"] = window == 0 ? new(null, "not_applicable", "window_not_created") : new(IsHungAppWindow(window) ? 0 : 1);
        result["frame_time_ms"] = new(null, "unsupported", "no_client_frame_events");
        return result;
    }
    public void Dispose() => handle.Dispose();
    [StructLayout(LayoutKind.Sequential)] private struct MemoryCounters { public uint Size, Faults; public nuint PeakWorkingSet, WorkingSet, PeakPaged, Paged, PeakNonPaged, NonPaged, Pagefile, PeakPagefile, Private; }
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] private struct ModuleEntry {
        public uint Size, ModuleId, ProcessId, GlobalUsage, ProcessUsage;
        public nint Base; public uint Bytes; public nint Module;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)] public string Name;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 260)] public string Path;
    }
    [DllImport("kernel32.dll")] private static extern SafeFileHandle CreateToolhelp32Snapshot(uint flags, int pid);
    [DllImport("kernel32.dll", EntryPoint = "Module32FirstW", CharSet = CharSet.Unicode)] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool Module32First(SafeFileHandle snapshot, ref ModuleEntry module);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern SafeProcessHandle OpenProcess(uint access, [MarshalAs(UnmanagedType.Bool)] bool inherit, int pid);
    [DllImport("kernel32.dll", EntryPoint = "QueryFullProcessImageNameW", CharSet = CharSet.Unicode, SetLastError = true)] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool QueryFullProcessImageName(SafeProcessHandle h, uint flags, StringBuilder path, ref int size);
    [DllImport("kernel32.dll")] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool GetProcessTimes(SafeProcessHandle h, out long created, out long exited, out long kernel, out long user);
    [DllImport("kernel32.dll")] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool ProcessIdToSessionId(int pid, out int session);
    [DllImport("kernel32.dll")] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool GetExitCodeProcess(SafeProcessHandle h, out int code);
    [DllImport("kernel32.dll", SetLastError = true)] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool ReadProcessMemory(SafeProcessHandle h, nint address, [Out] byte[] bytes, nuint size, out nuint read);
    [DllImport("psapi.dll")] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool GetProcessMemoryInfo(SafeProcessHandle h, ref MemoryCounters info, uint size);
    [DllImport("user32.dll")] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool IsHungAppWindow(nint h);
}
