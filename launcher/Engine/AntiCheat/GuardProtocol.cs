using System.IO.Pipes;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Dnf90.Launcher.AntiCheat;

internal sealed record GuardRequest(string Directory, string? Envelope);
internal sealed record GuardTarget(int Pid, long Created);
internal sealed record GuardEvent(string Kind, string Rule = "", string Sha256 = "", int Count = 0);
[JsonSerializable(typeof(GuardRequest))]
[JsonSerializable(typeof(GuardTarget))]
[JsonSerializable(typeof(GuardEvent))]
internal partial class GuardJson : JsonSerializerContext { }

internal static class GuardWire
{
    internal const string Prefix = "PurpleMagic.AntiCheat.";
    // Length framing bounds memory before reading/deserializing any untrusted input.
    internal static async Task<byte[]> Read(PipeStream pipe, int maximum, CancellationToken token)
    {
        var header = new byte[4]; await pipe.ReadExactlyAsync(header, token);
        var length = System.Buffers.Binary.BinaryPrimitives.ReadInt32LittleEndian(header);
        if (length < 1 || length > maximum) throw new InvalidDataException("监护消息长度无效。");
        var raw = new byte[length]; await pipe.ReadExactlyAsync(raw, token); return raw;
    }
    internal static async Task Write(PipeStream pipe, byte[] raw, CancellationToken token)
    {
        var header = new byte[4]; System.Buffers.Binary.BinaryPrimitives.WriteInt32LittleEndian(header, raw.Length);
        await pipe.WriteAsync(header, token); await pipe.WriteAsync(raw, token); await pipe.FlushAsync(token);
    }
}
