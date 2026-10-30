using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Security.Principal;
using System.Text;
using Microsoft.Win32.SafeHandles;

namespace Dnf90.Launcher.AntiCheat;

internal sealed class GuardHandle : SafeHandleZeroOrMinusOneIsInvalid
{
    public GuardHandle() : base(true) { }
    public GuardHandle(nint handle) : base(true) { SetHandle(handle); }
    protected override bool ReleaseHandle() => GuardNative.CloseHandle(handle);
}

internal sealed class SuspendedGame : IDisposable
{
    internal Process Process { get; }
    private readonly GuardHandle thread;
    private readonly GuardHandle process;
    private readonly GuardHandle job;
    internal SuspendedGame(GuardHandle job, GuardNative.ProcessInfo info)
    {
        this.job = job; thread = new(info.Thread); process = new(info.Process);
        Process = Process.GetProcessById(info.Pid);
    }
    internal void Resume() { if (GuardNative.ResumeThread(thread) == uint.MaxValue) throw new Win32Exception(); }
    public void Dispose() { job.Dispose(); thread.Dispose(); process.Dispose(); }
}

internal static class GuardNative
{
    internal static GuardHandle Job()
    {
        var handle = CreateJobObjectW(0, null);
        if (handle.IsInvalid) throw new Win32Exception();
        var info = new JobLimits { Basic = new() { Flags = 0x2000 } }; // KILL_ON_JOB_CLOSE
        if (!SetInformationJobObject(handle, 9, ref info, (uint)Marshal.SizeOf<JobLimits>())) { handle.Dispose(); throw new Win32Exception(); }
        return handle;
    }
    internal static SuspendedGame StartSuspended(string executable, string arguments)
    {
        var job = Job(); nint size = 0; nint attributes = 0; nint jobs = 0; var initialized = false;
        try {
            InitializeProcThreadAttributeList(0, 1, 0, ref size);
            attributes = Marshal.AllocHGlobal(size);
            if (!InitializeProcThreadAttributeList(attributes, 1, 0, ref size)) throw new Win32Exception();
            initialized = true; jobs = Marshal.AllocHGlobal(IntPtr.Size); Marshal.WriteIntPtr(jobs, job.DangerousGetHandle());
            // Assign atomically at creation: parent death cannot leave an unowned suspended child.
            if (!UpdateProcThreadAttribute(attributes, 0, (nuint)0x2000d, jobs, (nuint)IntPtr.Size, 0, 0)) throw new Win32Exception();
            var startup = new StartupEx { Startup = new() { Size = Marshal.SizeOf<StartupEx>() }, Attributes = attributes };
            var command = new StringBuilder('"' + executable + "\" " + arguments);
            if (!CreateProcessW(executable, command, 0, 0, false, 0x80004, 0, Path.GetDirectoryName(executable)!, ref startup, out var info)) throw new Win32Exception();
            try { return new(job, info); }
            catch { CloseHandle(info.Thread); CloseHandle(info.Process); throw; }
        } catch { job.Dispose(); throw; }
        finally { if (initialized) DeleteProcThreadAttributeList(attributes); if (attributes != 0) Marshal.FreeHGlobal(attributes); if (jobs != 0) Marshal.FreeHGlobal(jobs); }
    }
    internal static void Assign(GuardHandle job, Process process)
    {
        if (!AssignProcessToJobObject(job, process.SafeHandle)) throw new Win32Exception();
    }
    internal static int Parent(Process process)
    {
        if (NtQueryInformationProcess(process.SafeHandle, 0, out var info, Marshal.SizeOf<BasicProcess>(), out _) != 0) throw new Win32Exception();
        return checked((int)info.Parent);
    }
    internal static bool Elevated(Process process)
    {
        if (!OpenProcessToken(process.SafeHandle, 8, out var token)) throw new Win32Exception();
        using (token) { if (!GetTokenInformation(token, 20, out var elevation, sizeof(int), out _)) throw new Win32Exception(); return elevation != 0; }
    }
    internal static string User(Process process)
    {
        if (!OperatingSystem.IsWindows()) throw new PlatformNotSupportedException();
        if (!OpenProcessToken(process.SafeHandle, 8, out var token)) throw new Win32Exception();
        using (token) using (var identity = new WindowsIdentity(token.DangerousGetHandle())) return identity.User?.Value ?? throw new InvalidOperationException();
    }
    internal static string Image(Process process)
    {
        var path = new StringBuilder(32768); var size = path.Capacity;
        if (!QueryFullProcessImageNameW(process.SafeHandle, 0, path, ref size)) throw new Win32Exception();
        return Path.GetFullPath(path.ToString());
    }
    internal static string FinalPath(SafeFileHandle file)
    {
        var text = new StringBuilder(32768);
        var size = GetFinalPathNameByHandleW(file, text, (uint)text.Capacity, 0);
        if (size == 0 || size >= text.Capacity) throw new Win32Exception();
        var result = text.ToString(); return result.StartsWith(@"\\?\", StringComparison.Ordinal) ? result[4..] : result;
    }
    internal static byte[] Read(Process process, long address, int length)
    {
        var bytes = new byte[length];
        if (!ReadProcessMemory(process.SafeHandle, (nint)address, bytes, (nuint)bytes.Length, out var read) || read != (nuint)length) throw new Win32Exception();
        return bytes;
    }
    internal static int PrivateExecutableRegions(Process process)
    {
        // Only count regions, never copy or export their contents. Bounded address walk.
        ulong address = 0; var count = 0; var timer = Stopwatch.StartNew();
        for (var n = 0; n < 8192 && timer.ElapsedMilliseconds < 100; n++) {
            if (VirtualQueryEx(process.SafeHandle, (nint)address, out var region, (nuint)Marshal.SizeOf<MemoryInfo>()) == 0) break;
            if (region.State == 0x1000 && region.Type == 0x20000 && (region.Protect & 0xf0) != 0) count++;
            var next = (ulong)region.Base + (ulong)region.Size; if (next <= address) break; address = next;
        }
        return count;
    }
    [StructLayout(LayoutKind.Sequential)] private struct MemoryInfo { public nint Base, AllocationBase; public uint AllocationProtect; public ushort Partition; public nuint Size; public uint State, Protect, Type; }
    [StructLayout(LayoutKind.Sequential)] internal struct ProcessInfo { public nint Process, Thread; public int Pid, Tid; }
    [StructLayout(LayoutKind.Sequential)] private struct BasicProcess { public nint Reserved, Peb, Reserved2, Reserved3, Pid, Parent; }
    [StructLayout(LayoutKind.Sequential)] private struct Startup { public int Size; public nint Reserved, Desktop, Title; public uint X, Y, Width, Height, XChars, YChars, Fill, Flags; public ushort Show, ReservedSize; public nint ReservedBytes, Input, Output, Error; }
    [StructLayout(LayoutKind.Sequential)] private struct StartupEx { public Startup Startup; public nint Attributes; }
    [StructLayout(LayoutKind.Sequential)] private struct BasicLimits { public long ProcessTime, JobTime; public uint Flags; public nuint Minimum, Maximum; public uint Processes; public nuint Affinity; public uint Priority, Scheduling; }
    [StructLayout(LayoutKind.Sequential)] private struct IoCounters { public ulong ReadOps, WriteOps, OtherOps, ReadBytes, WriteBytes, OtherBytes; }
    [StructLayout(LayoutKind.Sequential)] private struct JobLimits { public BasicLimits Basic; public IoCounters Io; public nuint ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory; }
    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)] private static extern GuardHandle CreateJobObjectW(nint attributes, string? name);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool SetInformationJobObject(GuardHandle job, int kind, ref JobLimits info, uint size);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool AssignProcessToJobObject(GuardHandle job, SafeProcessHandle process);
    [DllImport("kernel32.dll", SetLastError = true)] internal static extern uint ResumeThread(GuardHandle thread);
    [DllImport("kernel32.dll", SetLastError = true)] internal static extern bool CloseHandle(nint handle);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool InitializeProcThreadAttributeList(nint list, int count, int flags, ref nint size);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool UpdateProcThreadAttribute(nint list, uint flags, nuint attribute, nint value, nuint size, nint previous, nint returned);
    [DllImport("kernel32.dll")] private static extern void DeleteProcThreadAttributeList(nint list);
    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)] private static extern bool CreateProcessW(string application, StringBuilder command, nint processAttributes, nint threadAttributes, bool inherit, uint flags, nint environment, string directory, ref StartupEx startup, out ProcessInfo info);
    [DllImport("ntdll.dll")] private static extern int NtQueryInformationProcess(SafeProcessHandle process, int kind, out BasicProcess info, int size, out int returned);
    [DllImport("advapi32.dll", SetLastError = true)] private static extern bool OpenProcessToken(SafeProcessHandle process, uint access, out GuardHandle token);
    [DllImport("advapi32.dll", SetLastError = true)] private static extern bool GetTokenInformation(GuardHandle token, int kind, out int info, int size, out int returned);
    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)] private static extern bool QueryFullProcessImageNameW(SafeProcessHandle process, uint flags, StringBuilder path, ref int size);
    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)] private static extern uint GetFinalPathNameByHandleW(SafeFileHandle file, StringBuilder path, uint size, uint flags);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern bool ReadProcessMemory(SafeProcessHandle process, nint address, byte[] buffer, nuint size, out nuint read);
    [DllImport("kernel32.dll", SetLastError = true)] private static extern nuint VirtualQueryEx(SafeProcessHandle process, nint address, out MemoryInfo info, nuint size);
}
