using System.Threading.Channels;

namespace Dnf90.Launcher.Diagnostics;

// FIFO order, with capacity ordinary activity cannot consume. Native capture has
// independent 16 first-chance / 2 terminal budgets; terminal messages also have
// eight exclusive slots. Both sides of the pipe use the same policy.
internal sealed class ObserverBuffer
{
    private readonly object gate = new();
    private readonly Channel<ObserverMessage> channel = Channel.CreateBounded<ObserverMessage>(256);
    internal bool TryWrite(ObserverMessage message)
    {
        var terminal = message.Kind == "native_exception" && message.Fields.GetValueOrDefault("first_chance")?.Value == 0;
        var ordinary = message.Kind is "player_operations" or "game_focus" or "module_loaded" or "module_unloaded" or "module_identity";
        lock (gate) return channel.Reader.Count < (terminal ? 256 : ordinary ? 128 : 248) && channel.Writer.TryWrite(message);
    }
    internal bool TryRead(out ObserverMessage? message) => channel.Reader.TryRead(out message);
    internal IAsyncEnumerable<ObserverMessage> ReadAll() => channel.Reader.ReadAllAsync();
    internal void Complete() => channel.Writer.TryComplete();
}
