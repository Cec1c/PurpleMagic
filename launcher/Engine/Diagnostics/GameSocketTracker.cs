using System.Buffers.Binary;
using System.Diagnostics;
using System.Net;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;

namespace Dnf90.Launcher.Diagnostics;

internal readonly record struct RawTcpEntry(
    string AddressFamily,
    string LocalAddress,
    int LocalPort,
    string RemoteAddress,
    int RemotePort,
    uint StateCode);

internal readonly record struct RawUdpEntry(
    string AddressFamily,
    string LocalAddress,
    int LocalPort);

internal sealed record SocketTransition(
    bool IsOpen,
    string Protocol,
    string StreamId,
    string AddressFamily,
    string LocalAddress,
    int LocalPort,
    string? RemoteAddress,
    int? RemotePort,
    string? TcpState,
    string? BindingState,
    string? PreviousStreamId,
    bool ReconnectToRemote,
    string? CloseReason,
    string? CloseReasonSource,
    double? DurationMs);

internal sealed class GameSocketTracker
{
    public const int MaxTrackedSockets = 512;
    private const uint AfInet = 2;
    private const uint AfInet6 = 23;
    private const int TcpTableOwnerPidAll = 5;
    private const int UdpTableOwnerPid = 1;
    private const uint ErrorInsufficientBuffer = 122;
    private const uint MaxTableBufferBytes = 16 * 1024 * 1024;

    private readonly int targetPid;
    private readonly long expectedCreatedFileTimeUtc;
    private readonly Func<(bool Verified, List<RawTcpEntry> Tcp, List<RawUdpEntry> Udp)>? customReader;

    private sealed class ActiveTcp(string streamId, RawTcpEntry initial, long openedTicks, string? previousStreamId, bool reconnect)
    {
        public string StreamId { get; } = streamId;
        public string AddressFamily { get; } = initial.AddressFamily;
        public string LocalAddress { get; } = initial.LocalAddress;
        public int LocalPort { get; } = initial.LocalPort;
        public string RemoteAddress { get; } = initial.RemoteAddress;
        public int RemotePort { get; } = initial.RemotePort;
        public uint LastStateCode { get; set; } = initial.StateCode;
        public long OpenedTicks { get; } = openedTicks;
        public string? PreviousStreamId { get; } = previousStreamId;
        public bool Reconnect { get; } = reconnect;
    }

    private sealed class ActiveUdp(string streamId, RawUdpEntry initial, long openedTicks)
    {
        public string StreamId { get; } = streamId;
        public string AddressFamily { get; } = initial.AddressFamily;
        public string LocalAddress { get; } = initial.LocalAddress;
        public int LocalPort { get; } = initial.LocalPort;
        public long OpenedTicks { get; } = openedTicks;
    }

    private readonly Dictionary<(string Af, string LocalIp, int LocalPort, string RemoteIp, int RemotePort), ActiveTcp> activeTcp = [];
    private readonly Dictionary<(string Af, string RemoteIp, int RemotePort), string> lastStreamByRemote = [];
    private readonly Dictionary<(string Af, string LocalIp, int LocalPort), ActiveUdp> activeUdp = [];

    public int DroppedOverflowCount { get; private set; }

    public GameSocketTracker(int pid, long createdFileTimeUtc, Func<(bool Verified, List<RawTcpEntry> Tcp, List<RawUdpEntry> Udp)>? reader = null)
    {
        targetPid = pid;
        expectedCreatedFileTimeUtc = createdFileTimeUtc;
        customReader = reader;
    }

    public SocketTransition[] Poll()
    {
        var (verified, tcpRows, udpRows) = customReader is not null ? customReader() : ReadOwnedSockets(targetPid, expectedCreatedFileTimeUtc);
        if (!verified) return [];

        var nowTicks = Stopwatch.GetTimestamp();
        var transitions = new List<SocketTransition>();

        var seenTcp = new HashSet<(string Af, string LocalIp, int LocalPort, string RemoteIp, int RemotePort)>();
        foreach (var row in tcpRows)
        {
            var key = (row.AddressFamily, row.LocalAddress, row.LocalPort, row.RemoteAddress, row.RemotePort);
            if (IsActiveTcpState(row.StateCode))
            {
                seenTcp.Add(key);
                if (activeTcp.TryGetValue(key, out var existing))
                {
                    existing.LastStateCode = row.StateCode;
                    continue;
                }
                if (activeTcp.Count + activeUdp.Count >= MaxTrackedSockets)
                {
                    DroppedOverflowCount++;
                    continue;
                }
                var remoteKey = (row.AddressFamily, row.RemoteAddress, row.RemotePort);
                var hasPrior = lastStreamByRemote.TryGetValue(remoteKey, out var priorStreamId);
                var streamId = "tcp-" + Guid.NewGuid().ToString("N")[..12];
                lastStreamByRemote[remoteKey] = streamId;
                var opened = new ActiveTcp(streamId, row, nowTicks, hasPrior ? priorStreamId : null, hasPrior);
                activeTcp[key] = opened;
                transitions.Add(new SocketTransition(
                    IsOpen: true,
                    Protocol: "tcp",
                    StreamId: streamId,
                    AddressFamily: row.AddressFamily,
                    LocalAddress: row.LocalAddress,
                    LocalPort: row.LocalPort,
                    RemoteAddress: row.RemoteAddress,
                    RemotePort: row.RemotePort,
                    TcpState: FormatTcpState(row.StateCode),
                    BindingState: null,
                    PreviousStreamId: opened.PreviousStreamId,
                    ReconnectToRemote: opened.Reconnect,
                    CloseReason: null,
                    CloseReasonSource: null,
                    DurationMs: null));
            }
            else if (activeTcp.TryGetValue(key, out var closing))
            {
                activeTcp.Remove(key);
                transitions.Add(new SocketTransition(
                    IsOpen: false,
                    Protocol: "tcp",
                    StreamId: closing.StreamId,
                    AddressFamily: closing.AddressFamily,
                    LocalAddress: closing.LocalAddress,
                    LocalPort: closing.LocalPort,
                    RemoteAddress: closing.RemoteAddress,
                    RemotePort: closing.RemotePort,
                    TcpState: FormatTcpState(row.StateCode),
                    BindingState: null,
                    PreviousStreamId: closing.PreviousStreamId,
                    ReconnectToRemote: closing.Reconnect,
                    CloseReason: "tcp_terminal_state_" + FormatTcpState(row.StateCode).ToLowerInvariant(),
                    CloseReasonSource: "os_socket_table_poll",
                    DurationMs: ElapsedMs(closing.OpenedTicks, nowTicks)));
            }
        }

        foreach (var (key, gone) in activeTcp.ToArray())
        {
            if (seenTcp.Contains(key)) continue;
            activeTcp.Remove(key);
            var graceful = gone.LastStateCode is 6 or 7 or 8 or 9 or 10;
            transitions.Add(new SocketTransition(
                IsOpen: false,
                Protocol: "tcp",
                StreamId: gone.StreamId,
                AddressFamily: gone.AddressFamily,
                LocalAddress: gone.LocalAddress,
                LocalPort: gone.LocalPort,
                RemoteAddress: gone.RemoteAddress,
                RemotePort: gone.RemotePort,
                TcpState: FormatTcpState(gone.LastStateCode),
                BindingState: null,
                PreviousStreamId: gone.PreviousStreamId,
                ReconnectToRemote: gone.Reconnect,
                CloseReason: graceful ? "tcp_closing_handshake" : "os_socket_table_disappeared",
                CloseReasonSource: "os_socket_table_poll",
                DurationMs: ElapsedMs(gone.OpenedTicks, nowTicks)));
        }

        var seenUdp = new HashSet<(string Af, string LocalIp, int LocalPort)>();
        foreach (var row in udpRows)
        {
            var key = (row.AddressFamily, row.LocalAddress, row.LocalPort);
            if (!seenUdp.Add(key)) continue;
            if (activeUdp.ContainsKey(key)) continue;
            if (activeTcp.Count + activeUdp.Count >= MaxTrackedSockets)
            {
                DroppedOverflowCount++;
                continue;
            }
            var streamId = "udp-bind-" + Guid.NewGuid().ToString("N")[..12];
            activeUdp[key] = new ActiveUdp(streamId, row, nowTicks);
            transitions.Add(new SocketTransition(
                IsOpen: true,
                Protocol: "udp",
                StreamId: streamId,
                AddressFamily: row.AddressFamily,
                LocalAddress: row.LocalAddress,
                LocalPort: row.LocalPort,
                RemoteAddress: null,
                RemotePort: null,
                TcpState: null,
                BindingState: "bound_local_only",
                PreviousStreamId: null,
                ReconnectToRemote: false,
                CloseReason: null,
                CloseReasonSource: null,
                DurationMs: null));
        }

        foreach (var (key, gone) in activeUdp.ToArray())
        {
            if (seenUdp.Contains(key)) continue;
            activeUdp.Remove(key);
            transitions.Add(new SocketTransition(
                IsOpen: false,
                Protocol: "udp",
                StreamId: gone.StreamId,
                AddressFamily: gone.AddressFamily,
                LocalAddress: gone.LocalAddress,
                LocalPort: gone.LocalPort,
                RemoteAddress: null,
                RemotePort: null,
                TcpState: null,
                BindingState: "unbound",
                PreviousStreamId: null,
                ReconnectToRemote: false,
                CloseReason: "udp_local_binding_closed",
                CloseReasonSource: "os_socket_table_poll",
                DurationMs: ElapsedMs(gone.OpenedTicks, nowTicks)));
        }

        return [.. transitions];
    }

    public SocketTransition[] CloseAll(string closeReason, string closeReasonSource)
    {
        var nowTicks = Stopwatch.GetTimestamp();
        var transitions = new List<SocketTransition>(activeTcp.Count + activeUdp.Count);
        foreach (var tcp in activeTcp.Values)
        {
            transitions.Add(new SocketTransition(
                IsOpen: false,
                Protocol: "tcp",
                StreamId: tcp.StreamId,
                AddressFamily: tcp.AddressFamily,
                LocalAddress: tcp.LocalAddress,
                LocalPort: tcp.LocalPort,
                RemoteAddress: tcp.RemoteAddress,
                RemotePort: tcp.RemotePort,
                TcpState: FormatTcpState(tcp.LastStateCode),
                BindingState: null,
                PreviousStreamId: tcp.PreviousStreamId,
                ReconnectToRemote: tcp.Reconnect,
                CloseReason: closeReason,
                CloseReasonSource: closeReasonSource,
                DurationMs: ElapsedMs(tcp.OpenedTicks, nowTicks)));
        }
        activeTcp.Clear();
        foreach (var udp in activeUdp.Values)
        {
            transitions.Add(new SocketTransition(
                IsOpen: false,
                Protocol: "udp",
                StreamId: udp.StreamId,
                AddressFamily: udp.AddressFamily,
                LocalAddress: udp.LocalAddress,
                LocalPort: udp.LocalPort,
                RemoteAddress: null,
                RemotePort: null,
                TcpState: null,
                BindingState: "unbound",
                PreviousStreamId: null,
                ReconnectToRemote: false,
                CloseReason: closeReason,
                CloseReasonSource: closeReasonSource,
                DurationMs: ElapsedMs(udp.OpenedTicks, nowTicks)));
        }
        activeUdp.Clear();
        return [.. transitions];
    }

    private static double ElapsedMs(long startTicks, long endTicks) =>
        Math.Round(Math.Max(0, (endTicks - startTicks) * 1000.0 / Stopwatch.Frequency), 3);

    private static bool IsActiveTcpState(uint state) => state is >= 3 and <= 10;

    internal static string FormatTcpState(uint state) => state switch
    {
        1 => "CLOSED",
        2 => "LISTEN",
        3 => "SYN_SENT",
        4 => "SYN_RCVD",
        5 => "ESTABLISHED",
        6 => "FIN_WAIT1",
        7 => "FIN_WAIT2",
        8 => "CLOSE_WAIT",
        9 => "CLOSING",
        10 => "LAST_ACK",
        11 => "TIME_WAIT",
        12 => "DELETE_TCB",
        _ => "UNKNOWN_" + state
    };

    internal static (bool Verified, List<RawTcpEntry> Tcp, List<RawUdpEntry> Udp) ReadOwnedSockets(int pid, long expectedCreatedUtc)
    {
        if (!OperatingSystem.IsWindows() || pid <= 0) return (false, [], []);
        if (!VerifyProcessAliveAndSameBirth(pid, expectedCreatedUtc)) return (false, [], []);

        var tcp = new List<RawTcpEntry>();
        var udp = new List<RawUdpEntry>();
        ReadTcpV4(pid, tcp);
        ReadTcpV6(pid, tcp);
        ReadUdpV4(pid, udp);
        ReadUdpV6(pid, udp);

        // Re-verify process birth timestamp after reading OS tables so PID reuse
        // during the poll window can never attribute another process's sockets.
        if (!VerifyProcessAliveAndSameBirth(pid, expectedCreatedUtc)) return (false, [], []);
        return (true, tcp, udp);
    }

    internal static bool VerifyProcessAliveAndSameBirth(int pid, long expectedCreatedUtc)
    {
        if (!OperatingSystem.IsWindows()) return false;
        using var handle = OpenProcess(0x1000, false, pid);
        if (handle.IsInvalid) return false;
        if (!GetProcessTimes(handle, out var created, out var exited, out _, out _)) return false;
        if (created != expectedCreatedUtc || exited != 0) return false;
        if (!GetExitCodeProcess(handle, out var exitCode)) return false;
        return exitCode == 259;
    }

    private static byte[]? ReadTable(bool tcp, uint af, int tableClass)
    {
        uint size = 16384;
        for (var attempt = 0; attempt < 4; attempt++)
        {
            if (size is 0 or > MaxTableBufferBytes) return null;
            var buffer = new byte[size];
            uint status;
            var pinned = GCHandle.Alloc(buffer, GCHandleType.Pinned);
            try
            {
                var ptr = pinned.AddrOfPinnedObject();
                status = tcp
                    ? GetExtendedTcpTable(ptr, ref size, false, af, tableClass, 0)
                    : GetExtendedUdpTable(ptr, ref size, false, af, tableClass, 0);
            }
            finally
            {
                pinned.Free();
            }
            if (status == 0) return buffer;
            if (status != ErrorInsufficientBuffer) return null;
            size = Math.Min(MaxTableBufferBytes, Math.Max(size, (uint)buffer.Length * 2) + 4096);
        }
        return null;
    }

    private static int DecodeNetworkPort(ReadOnlySpan<byte> field) =>
        BinaryPrimitives.ReverseEndianness(BinaryPrimitives.ReadUInt16LittleEndian(field));

    private static void ReadTcpV4(int pid, List<RawTcpEntry> output)
    {
        var raw = ReadTable(true, AfInet, TcpTableOwnerPidAll);
        if (raw is null || raw.Length < 4) return;
        var span = raw.AsSpan();
        var count = BinaryPrimitives.ReadUInt32LittleEndian(span);
        const int rowSize = 24;
        var maxRows = (span.Length - 4) / rowSize;
        var limit = (int)Math.Min(count, (uint)maxRows);
        for (var i = 0; i < limit; i++)
        {
            var row = span.Slice(4 + i * rowSize, rowSize);
            var owner = (int)BinaryPrimitives.ReadUInt32LittleEndian(row.Slice(20, 4));
            if (owner != pid) continue;
            var state = BinaryPrimitives.ReadUInt32LittleEndian(row[..4]);
            if (state == 2) continue; // Ignore LISTEN sockets without remote peer.
            var localIp = new IPAddress(row.Slice(4, 4)).ToString();
            var localPort = DecodeNetworkPort(row.Slice(8, 4));
            var remoteIp = new IPAddress(row.Slice(12, 4)).ToString();
            var remotePort = DecodeNetworkPort(row.Slice(16, 4));
            output.Add(new RawTcpEntry("ipv4", localIp, localPort, remoteIp, remotePort, state));
        }
    }

    private static void ReadTcpV6(int pid, List<RawTcpEntry> output)
    {
        var raw = ReadTable(true, AfInet6, TcpTableOwnerPidAll);
        if (raw is null || raw.Length < 4) return;
        var span = raw.AsSpan();
        var count = BinaryPrimitives.ReadUInt32LittleEndian(span);
        const int rowSize = 56;
        var maxRows = (span.Length - 4) / rowSize;
        var limit = (int)Math.Min(count, (uint)maxRows);
        for (var i = 0; i < limit; i++)
        {
            var row = span.Slice(4 + i * rowSize, rowSize);
            var owner = (int)BinaryPrimitives.ReadUInt32LittleEndian(row.Slice(52, 4));
            if (owner != pid) continue;
            var state = BinaryPrimitives.ReadUInt32LittleEndian(row.Slice(48, 4));
            if (state == 2) continue;
            var localScope = BinaryPrimitives.ReadUInt32LittleEndian(row.Slice(16, 4));
            var localIp = new IPAddress(row[..16], localScope).ToString();
            var localPort = DecodeNetworkPort(row.Slice(20, 4));
            var remoteScope = BinaryPrimitives.ReadUInt32LittleEndian(row.Slice(40, 4));
            var remoteIp = new IPAddress(row.Slice(24, 16), remoteScope).ToString();
            var remotePort = DecodeNetworkPort(row.Slice(44, 4));
            output.Add(new RawTcpEntry("ipv6", localIp, localPort, remoteIp, remotePort, state));
        }
    }

    private static void ReadUdpV4(int pid, List<RawUdpEntry> output)
    {
        var raw = ReadTable(false, AfInet, UdpTableOwnerPid);
        if (raw is null || raw.Length < 4) return;
        var span = raw.AsSpan();
        var count = BinaryPrimitives.ReadUInt32LittleEndian(span);
        const int rowSize = 12;
        var maxRows = (span.Length - 4) / rowSize;
        var limit = (int)Math.Min(count, (uint)maxRows);
        for (var i = 0; i < limit; i++)
        {
            var row = span.Slice(4 + i * rowSize, rowSize);
            var owner = (int)BinaryPrimitives.ReadUInt32LittleEndian(row.Slice(8, 4));
            if (owner != pid) continue;
            var localIp = new IPAddress(row[..4]).ToString();
            var localPort = DecodeNetworkPort(row.Slice(4, 4));
            output.Add(new RawUdpEntry("ipv4", localIp, localPort));
        }
    }

    private static void ReadUdpV6(int pid, List<RawUdpEntry> output)
    {
        var raw = ReadTable(false, AfInet6, UdpTableOwnerPid);
        if (raw is null || raw.Length < 4) return;
        var span = raw.AsSpan();
        var count = BinaryPrimitives.ReadUInt32LittleEndian(span);
        const int rowSize = 28;
        var maxRows = (span.Length - 4) / rowSize;
        var limit = (int)Math.Min(count, (uint)maxRows);
        for (var i = 0; i < limit; i++)
        {
            var row = span.Slice(4 + i * rowSize, rowSize);
            var owner = (int)BinaryPrimitives.ReadUInt32LittleEndian(row.Slice(24, 4));
            if (owner != pid) continue;
            var localScope = BinaryPrimitives.ReadUInt32LittleEndian(row.Slice(16, 4));
            var localIp = new IPAddress(row[..16], localScope).ToString();
            var localPort = DecodeNetworkPort(row.Slice(20, 4));
            output.Add(new RawUdpEntry("ipv6", localIp, localPort));
        }
    }

    [DllImport("iphlpapi.dll", ExactSpelling = true)]
    private static extern uint GetExtendedTcpTable(nint pTcpTable, ref uint pdwSize, [MarshalAs(UnmanagedType.Bool)] bool bOrder, uint ulAf, int tableClass, uint reserved);

    [DllImport("iphlpapi.dll", ExactSpelling = true)]
    private static extern uint GetExtendedUdpTable(nint pUdpTable, ref uint pdwSize, [MarshalAs(UnmanagedType.Bool)] bool bOrder, uint ulAf, int tableClass, uint reserved);

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern SafeProcessHandle OpenProcess(uint access, [MarshalAs(UnmanagedType.Bool)] bool inherit, int pid);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetProcessTimes(SafeProcessHandle handle, out long created, out long exited, out long kernel, out long user);

    [DllImport("kernel32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetExitCodeProcess(SafeProcessHandle handle, out int code);
}
