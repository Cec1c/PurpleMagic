using System.Diagnostics;
using System.Runtime.InteropServices;

namespace Dnf90.Launcher.Diagnostics;

// Raw input is filtered by foreground PID before reading its payload. Text keys
// become one anonymous counter; there is no character conversion or key sequence.
internal sealed class GameOperationCapture : IDisposable
{
    private readonly int pid;
    private readonly Action<ObserverMessage> emit;
    private readonly WindowProc procedure;
    private readonly string className = "PurpleMagicDiagnostics" + Guid.NewGuid().ToString("N");
    private readonly Dictionary<string, DiagnosticField> counts = [];
    private readonly bool[] down = new bool[256];
    private readonly Stopwatch clock = Stopwatch.StartNew();
    private readonly nint window;
    private long nextFlush;
    private bool foreground;
    internal GameOperationCapture(int pid, Action<ObserverMessage> emit)
    {
        this.pid = pid; this.emit = emit; procedure = OnMessage;
        var wc = new WindowClass { Size = (uint)Marshal.SizeOf<WindowClass>(), Procedure = procedure, Instance = GetModuleHandle(null), Name = className };
        if (RegisterClassEx(ref wc) == 0) throw new System.ComponentModel.Win32Exception();
        window = CreateWindowEx(0, className, "", 0, 0, 0, 0, 0, new nint(-3), 0, wc.Instance, 0);
        if (window == 0) { UnregisterClass(className, wc.Instance); throw new System.ComponentModel.Win32Exception(); }
        var devices = new[] { new RawDevice { Page = 1, Usage = 2, Flags = 0x100, Target = window }, new RawDevice { Page = 1, Usage = 6, Flags = 0x100, Target = window } };
        if (!RegisterRawInputDevices(devices, 2, (uint)Marshal.SizeOf<RawDevice>())) { Dispose(); throw new System.ComponentModel.Win32Exception(); }
        emit(new("operations_ready", []));
    }
    internal static string KeyCategory(int key) => key switch { 0x25 => "left", 0x26 => "up", 0x27 => "right", 0x28 => "down", _ => "keyboard" };
    private bool IsForeground() { GetWindowThreadProcessId(GetForegroundWindow(), out var active); return active == pid; }
    internal static void ObserveRaw(int expectedPid, int foregroundPid, ReadOnlySpan<byte> bytes, int header, Dictionary<string, DiagnosticField> counts, bool[] down)
    {
        if (expectedPid != foregroundPid || bytes.Length < header || header is not (16 or 24)) { Array.Clear(down); return; }
        void Count(string category, double amount = 1) => counts[category] = new(Math.Min(1000000, (counts.GetValueOrDefault(category)?.Value ?? 0) + amount));
        var type = BitConverter.ToUInt32(bytes); var body = bytes[header..];
        if (type == 1 && body.Length >= 16) {
            var key = BitConverter.ToUInt16(body[6..]); var released = (BitConverter.ToUInt16(body[2..]) & 1) != 0;
            if (key < 255) { if (!released && !down[key]) Count(KeyCategory(key)); down[key] = !released; }
        } else if (type == 0 && body.Length >= 24) {
            var buttons = BitConverter.ToUInt16(body[4..]);
            if ((buttons & 1) != 0) Count("mouse_left"); if ((buttons & 4) != 0) Count("mouse_right");
            if ((buttons & 0x10) != 0) Count("mouse_middle"); if ((buttons & 0x140) != 0) Count("mouse_extra");
            if ((buttons & 0xc00) != 0) Count("wheel", Math.Abs((int)BitConverter.ToInt16(body[6..])) / 120d);
            if (BitConverter.ToInt32(body[12..]) != 0 || BitConverter.ToInt32(body[16..]) != 0) Count("mouse_motion");
        }
    }
    private nint OnMessage(nint handle, uint message, nuint wParam, nint lParam)
    {
        if (message == 0xff && IsForeground())
        {
            var bytes = new byte[64]; uint size = (uint)bytes.Length;
            var header = (uint)(IntPtr.Size == 8 ? 24 : 16);
            if (GetRawInputData(lParam, 0x10000003, bytes, ref size, header) != uint.MaxValue && size <= bytes.Length)
                ObserveRaw(pid, IsForeground() ? pid : -1, bytes.AsSpan(0, (int)size), (int)header, counts, down);
        }
        return DefWindowProc(handle, message, wParam, lParam);
    }
    public void Pump()
    {
        var active = IsForeground();
        if (active != foreground) { foreground = active; Array.Clear(down); emit(new("game_focus", new() { ["foreground"] = new(active ? 1 : 0) })); }
        // Bounded work keeps debug events and stop requests responsive during input floods.
        for (var n = 0; n < 128 && PeekMessage(out var message, window, 0, 0, 1); n++) DispatchMessage(ref message);
        if (clock.ElapsedMilliseconds >= nextFlush) { Flush(); nextFlush = clock.ElapsedMilliseconds + 1000; }
    }
    private void Flush() { if (counts.Count == 0) return; emit(new("player_operations", new(counts))); counts.Clear(); }
    public void Dispose()
    {
        Flush(); if (window != 0) DestroyWindow(window);
        UnregisterClass(className, GetModuleHandle(null)); GC.KeepAlive(procedure);
    }
    private delegate nint WindowProc(nint hwnd, uint message, nuint wParam, nint lParam);
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] private struct WindowClass { public uint Size, Style; public WindowProc Procedure; public int ClassExtra, WindowExtra; public nint Instance, Icon, Cursor, Background; public string? Menu, Name; public nint SmallIcon; }
    [StructLayout(LayoutKind.Sequential)] private struct RawDevice { public ushort Page, Usage; public uint Flags; public nint Target; }
    [StructLayout(LayoutKind.Sequential)] private struct Message { public nint Window; public uint Id; public nuint WParam; public nint LParam; public uint Time; public int X, Y; public uint Private; }
    [DllImport("kernel32.dll", EntryPoint = "GetModuleHandleW", CharSet = CharSet.Unicode)] private static extern nint GetModuleHandle(string? name);
    [DllImport("user32.dll", EntryPoint = "RegisterClassExW", CharSet = CharSet.Unicode, SetLastError = true)] private static extern ushort RegisterClassEx(ref WindowClass c);
    [DllImport("user32.dll", EntryPoint = "CreateWindowExW", CharSet = CharSet.Unicode, SetLastError = true)] private static extern nint CreateWindowEx(uint ex, string cls, string name, uint style, int x, int y, int w, int h, nint parent, nint menu, nint instance, nint parameter);
    [DllImport("user32.dll", EntryPoint = "UnregisterClassW", CharSet = CharSet.Unicode)] private static extern bool UnregisterClass(string name, nint instance);
    [DllImport("user32.dll")] private static extern bool DestroyWindow(nint window);
    [DllImport("user32.dll", EntryPoint = "DefWindowProcW")] private static extern nint DefWindowProc(nint window, uint message, nuint w, nint l);
    [DllImport("user32.dll", SetLastError = true)] private static extern bool RegisterRawInputDevices([In] RawDevice[] devices, uint count, uint size);
    [DllImport("user32.dll")] private static extern uint GetRawInputData(nint input, uint command, [Out] byte[] data, ref uint size, uint header);
    [DllImport("user32.dll")] private static extern nint GetForegroundWindow();
    [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(nint window, out int pid);
    [DllImport("user32.dll", EntryPoint = "PeekMessageW")] private static extern bool PeekMessage(out Message message, nint window, uint first, uint last, uint remove);
    [DllImport("user32.dll", EntryPoint = "DispatchMessageW")] private static extern nint DispatchMessage(ref Message message);
}
