using System.Diagnostics;
using System.Runtime.InteropServices;

namespace Dnf90.Launcher.Diagnostics;

internal static class NativeEvidenceReader
{
    internal const int FrameLimit = 64;
    internal const int PauseBudgetMs = 250;
    internal static string Hex(ulong value, int bits) => "0x" + (bits == 32 ? ((uint)value).ToString("X8") : value.ToString("X16"));
    internal static ulong Value(string hex) => Convert.ToUInt64(hex[2..], 16);

    // The observer itself is x64, including when its target is WOW64.
    internal static NativeExceptionRecord Parse(byte[] buffer, int bits, string id)
    {
        var code = BitConverter.ToUInt32(buffer, 16); var count = BitConverter.ToUInt32(buffer, 40);
        var parameters = Enumerable.Range(0, (int)Math.Min(count, 15u)).Select(i => Hex(BitConverter.ToUInt64(buffer, 48 + i * 8), bits)).ToArray();
        string? access = null, fault = null, io = null;
        if (code is 0xc0000005 or 0xc0000006 && count >= 2) {
            access = Value(parameters[0]) switch { 0 => "read", 1 => "write", 8 => "execute", _ => "unknown" };
            fault = parameters[1];
            if (code == 0xc0000006 && count >= 3) io = parameters[2];
        }
        return new(Hex(code, 32), Hex(BitConverter.ToUInt32(buffer, 20), 32), BitConverter.ToUInt32(buffer, 168) != 0,
            BitConverter.ToUInt32(buffer, 8), id, null, "unpaired", Hex(BitConverter.ToUInt64(buffer, 32), bits),
            Hex(BitConverter.ToUInt64(buffer, 24), bits), count, parameters, count > 15 ? "invalid_count_truncated" : "ok", access, fault, io);
    }

    internal static Dictionary<string, string> Registers(byte[] context, bool wow64)
    {
        (string Name, int Offset)[] layout = wow64
            ? [("EIP",184),("ESP",196),("EBP",180),("EAX",176),("EBX",164),("ECX",172),("EDX",168),("ESI",160),("EDI",156),("EFlags",192)]
            : [("RIP",248),("RSP",152),("RBP",160),("RAX",120),("RBX",144),("RCX",128),("RDX",136),("RSI",168),("RDI",176),
               ("R8",184),("R9",192),("R10",200),("R11",208),("R12",216),("R13",224),("R14",232),("R15",240),("RFlags",68)];
        return layout.ToDictionary(r => r.Name, r => Hex(wow64 || r.Name == "RFlags" ? BitConverter.ToUInt32(context, r.Offset) : BitConverter.ToUInt64(context, r.Offset), wow64 ? 32 : 64));
    }

    internal static NativePage Query(nint process, ulong address, int bits, params string[] sources)
    {
        if (NativeCaptureApi.VirtualQueryEx(process, (nint)address, out var page, (nuint)Marshal.SizeOf<NativeCaptureApi.MemoryBasic>()) == 0)
            return new(sources, Hex(address, bits), "query_failed", Marshal.GetLastWin32Error());
        return new(sources, Hex(address, bits), page.State == 0x10000 ? "free" : "ok", null,
            Hex(page.Base, bits), Hex(page.AllocationBase, bits), Hex(page.Size, bits), Hex(page.State, 32), Hex(page.Protect, 32), Hex(page.Type, 32));
    }

    internal static NativePage[] Pages(nint process, NativeExceptionRecord exception, NativeContext context, Stopwatch pause)
    {
        var pointers = new List<(string Name, ulong Address)> { ("exception_ip", Value(exception.ExceptionAddress)) };
        if (exception.FaultAddress is not null) pointers.Insert(0, ("fault", Value(exception.FaultAddress)));
        foreach (var name in context.PointerBits == 32 ? new[] { "ESP", "EDI", "ESI", "EAX" } : new[] { "RSP", "RDI", "RSI", "RAX" })
            if (context.Registers.TryGetValue(name, out var value)) pointers.Add((name, Value(value)));
        var pages = new List<NativePage>();
        foreach (var pointer in pointers.Take(6)) {
            var found = pages.FindIndex(p => p.BaseAddress is not null && pointer.Address >= Value(p.BaseAddress) && pointer.Address - Value(p.BaseAddress) < Value(p.RegionSize!));
            if (found >= 0) { pages[found] = pages[found] with { Sources = [.. pages[found].Sources, pointer.Name] }; continue; }
            pages.Add(pause.ElapsedMilliseconds >= PauseBudgetMs ? new([pointer.Name], Hex(pointer.Address, context.PointerBits), "budget_exceeded", null)
                : Query(process, pointer.Address, context.PointerBits, pointer.Name));
        }
        return [.. pages];
    }

    internal static NativeCode Code(nint process, ulong ip, int bits, NativeModule[] modules, Stopwatch pause)
    {
        if (pause.ElapsedMilliseconds >= PauseBudgetMs) return new("budget_exceeded", null);
        var module = modules.FirstOrDefault(m => m.Status == "loaded" && ip >= Value(m.BaseAddress) && ip - Value(m.BaseAddress) < Value(m.Size));
        if (module is null) return new("outside_module", null, Address: Hex(ip, bits));
        var page = Query(process, ip, bits);
        if (page.Status == "query_failed") return new("query_failed", page.Win32Error, module.Id, Hex(ip, bits), Hex(ip - Value(module.BaseAddress), bits));
        if (page.Type != "0x01000000" || page.AllocationBase is null || Value(page.AllocationBase) != Value(module.BaseAddress))
            return new("outside_image_mapping", null, module.Id, Hex(ip, bits), Hex(ip - Value(module.BaseAddress), bits));
        var protection = Value(page.Protect!);
        if (page.State != "0x00001000" || (protection & 0x100) != 0 || (protection & 0xff) is not (2 or 4 or 8 or 0x20 or 0x40 or 0x80))
            return new("unreadable_page", null, module.Id, Hex(ip, bits), Hex(ip - Value(module.BaseAddress), bits));
        var requested = (int)Math.Min(64ul, Value(module.Size) - (ip - Value(module.BaseAddress)));
        // Stay on the proven readable page; report a partial window at its boundary.
        var length = (int)Math.Min((ulong)requested, Value(page.RegionSize!) - (ip - Value(page.BaseAddress!)));
        var bytes = new byte[length];
        var ok = NativeCaptureApi.ReadProcessMemory(process, (nint)ip, bytes, (nuint)length, out var read);
        var error = ok ? (int?)null : Marshal.GetLastWin32Error(); var actual = (int)Math.Min(read, (nuint)length);
        return new(actual == requested ? "ok" : actual > 0 ? "partial" : "read_failed", error, module.Id, Hex(ip, bits), Hex(ip - Value(module.BaseAddress), bits), requested, actual, Convert.ToHexString(bytes.AsSpan(0, actual)));
    }

    internal static DiagnosticStackFrame Locate(ulong address, int bits, NativeModule[] modules)
    {
        var module = modules.FirstOrDefault(m => m.Status == "loaded" && address >= Value(m.BaseAddress) && address - Value(m.BaseAddress) < Value(m.Size));
        return module is null ? new("unmapped", Hex(0, bits), Address: Hex(address, bits))
            : new(module.Name, Hex(address - Value(module.BaseAddress), bits), Address: Hex(address, bits), ModuleId: module.Id);
    }
}

internal static class NativeCaptureApi
{
    [StructLayout(LayoutKind.Sequential)] internal struct MemoryBasic {
        internal ulong Base, AllocationBase;
        internal uint AllocationProtect, Alignment;
        internal ulong Size;
        internal uint State, Protect, Type, Alignment2;
    }
    [DllImport("kernel32.dll", SetLastError = true)] internal static extern bool ReadProcessMemory(nint process, nint address, [Out] byte[] bytes, nuint size, out nuint read);
    [DllImport("kernel32.dll", SetLastError = true)] internal static extern nuint VirtualQueryEx(nint process, nint address, out MemoryBasic memory, nuint size);
    [DllImport("kernel32.dll", SetLastError = true)] internal static extern bool CloseHandle(nint handle);
}
