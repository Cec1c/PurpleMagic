using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;
using System.Threading.Channels;
using Microsoft.Win32.SafeHandles;

namespace Dnf90.Launcher.Diagnostics;

// Loaded addresses/PE headers come from the debug events and bounded remote reads.
// File names, versions and disk hashes are resolved on a separate worker. No file
// hashing or Process.Modules enumeration occurs in the exception pause.
internal sealed class NativeModuleCatalog : IDisposable
{
    private readonly object gate = new();
    private readonly Dictionary<ulong, NativeModule> active = [];
    private readonly Channel<(NativeModule Module, SafeFileHandle File)> pending = Channel.CreateBounded<(NativeModule, SafeFileHandle)>(32);
    private readonly CancellationTokenSource stop = new();
    private readonly Task worker;
    private readonly Action<ObserverMessage> emit;
    private int generation;
    private readonly string prefix = Guid.NewGuid().ToString("N");
    internal string Status { get; private set; } = "ok";
    internal NativeModuleCatalog(Action<ObserverMessage> emit)
    {
        this.emit = emit;
        worker = Task.Run(async () => {
            await foreach (var job in pending.Reader.ReadAllAsync()) {
                using var file = job.File;
                var module = job.Module;
                try {
                    if (stop.IsCancellationRequested) continue;
                    var path = new StringBuilder(32768);
                    var n = GetFinalPathNameByHandle(file, path, (uint)path.Capacity, 0);
                    var name = n is > 0 and < 32768 ? DiagnosticFailure.SafeName(Path.GetFileName(path.ToString())) : "unknown";
                    string? version = null;
                    if (n is > 0 and < 32768) try {
                        var value = FileVersionInfo.GetVersionInfo(path.ToString()).FileVersion;
                        if (!string.IsNullOrEmpty(value)) version = DiagnosticFailure.SafeName(value);
                    } catch (Exception) { }
                    module = module with { Name = name, FileVersion = version };
                    using var input = new FileStream(file, FileAccess.Read, 65536);
                    input.Position = 0;
                    if (input.Length > 512L * 1024 * 1024) module = module with { HashStatus = "size_limit" };
                    else {
                        using var hash = IncrementalHash.CreateHash(HashAlgorithmName.SHA256);
                        var buffer = new byte[65536]; var timer = Stopwatch.StartNew(); int read;
                        while (!stop.IsCancellationRequested && timer.ElapsedMilliseconds < 10000 && (read = input.Read(buffer)) > 0) hash.AppendData(buffer, 0, read);
                        module = input.Position == input.Length ? module with { Sha256 = Convert.ToHexString(hash.GetHashAndReset()).ToLowerInvariant(), HashStatus = "ok" }
                            : module with { HashStatus = stop.IsCancellationRequested ? "cancelled" : "time_limit" };
                    }
                } catch (Exception) { module = module with { HashStatus = "read_failed" }; }
                lock (gate) {
                    var address = NativeEvidenceReader.Value(module.BaseAddress);
                    if (active.TryGetValue(address, out var current) && current.Id == module.Id) active[address] = module;
                    else module = module with { Status = "unloaded" };
                }
                emit(new("module_identity", [], new("module_identity", Module: module)));
            }
        });
    }
    internal NativeModule[] Snapshot() { lock (gate) return [.. active.Values]; }
    internal void Load(nint process, ulong address, nint file, int bits)
    {
        var owned = new SafeFileHandle(file, true);
        if (++generation > 512) { Status = "module_limit"; owned.Dispose(); return; }
        uint size = 0, timestamp = 0; var architecture = "unknown"; var identity = "header_unavailable";
        var dos = new byte[64]; var pe = new byte[88];
        if (NativeCaptureApi.ReadProcessMemory(process, (nint)address, dos, 64, out var read) && read == 64 && BitConverter.ToUInt16(dos) == 0x5a4d) {
            var offset = BitConverter.ToUInt32(dos, 60);
            if (offset < 65536 && NativeCaptureApi.ReadProcessMemory(process, (nint)(address + offset), pe, 88, out read) && read == 88 && BitConverter.ToUInt32(pe) == 0x4550) {
                architecture = BitConverter.ToUInt16(pe, 4) switch { 0x14c => "x86", 0x8664 => "x64", _ => "unknown" };
                timestamp = BitConverter.ToUInt32(pe, 8); size = BitConverter.ToUInt32(pe, 80); identity = "loaded_pe_header";
            }
        }
        var moduleBits = architecture == "x64" || address > uint.MaxValue ? 64 : bits;
        var module = new NativeModule(prefix + ":m" + generation, "unknown", NativeEvidenceReader.Hex(address, moduleBits), NativeEvidenceReader.Hex(size, moduleBits), architecture,
            identity == "loaded_pe_header" ? NativeEvidenceReader.Hex(timestamp, 32) : null, null, null, owned.IsInvalid ? "file_unavailable" : "pending", "debug_event_disk_file", "loaded", identity);
        lock (gate) active[address] = module;
        emit(new("module_loaded", [], new("module_identity", Module: module)));
        if (owned.IsInvalid) { owned.Dispose(); return; }
        if (!pending.Writer.TryWrite((module, owned))) {
            owned.Dispose(); lock (gate) active[address] = module with { HashStatus = "queue_limit" };
        }
    }
    internal void Unload(ulong address)
    {
        NativeModule? module; lock (gate) { active.Remove(address, out module); }
        if (module is not null) emit(new("module_unloaded", [], new("module_identity", Module: module with { Status = "unloaded" })));
    }
    public void Dispose()
    {
        stop.Cancel(); pending.Writer.TryComplete();
        // Disk I/O may outlive detach; the worker owns and closes every queued handle.
        _ = worker.ContinueWith(_ => stop.Dispose(), TaskScheduler.Default);
    }
    [DllImport("kernel32.dll", EntryPoint = "GetFinalPathNameByHandleW", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern uint GetFinalPathNameByHandle(SafeFileHandle file, StringBuilder path, uint size, uint flags);
}
