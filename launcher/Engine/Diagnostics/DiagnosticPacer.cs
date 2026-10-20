using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;

namespace Dnf90.Launcher.Diagnostics;

// Process-owned waitable timer; does not change the system timer period.
internal sealed class DiagnosticPacer : IDisposable
{
    private readonly SafeWaitHandle? timer;
    public bool HighResolution => timer is { IsInvalid: false };
    public DiagnosticPacer()
    {
        if (OperatingSystem.IsWindows()) timer = CreateWaitableTimerEx(0, 0, 2, 0x1f0003);
    }
    public void Wait(int milliseconds)
    {
        if (timer is { IsInvalid: false }) {
            long due = -Math.Max(1, milliseconds) * 10000L;
            if (SetWaitableTimer(timer, ref due, 0, 0, 0, false) && WaitForSingleObject(timer, (uint)milliseconds + 100) == 0) return;
        }
        Thread.Sleep(milliseconds);
    }
    public void Dispose() => timer?.Dispose();
    [DllImport("kernel32.dll", EntryPoint = "CreateWaitableTimerExW")] private static extern SafeWaitHandle CreateWaitableTimerEx(nint attributes, nint name, uint flags, uint access);
    [DllImport("kernel32.dll")] [return: MarshalAs(UnmanagedType.Bool)] private static extern bool SetWaitableTimer(SafeWaitHandle h, ref long due, int period, nint routine, nint argument, [MarshalAs(UnmanagedType.Bool)] bool resume);
    [DllImport("kernel32.dll")] private static extern uint WaitForSingleObject(SafeWaitHandle h, uint milliseconds);
}
