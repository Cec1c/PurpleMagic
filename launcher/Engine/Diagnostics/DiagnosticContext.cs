using System.Diagnostics;
using System.Globalization;
using System.Text.Json;
using ServerS5A3;

namespace Dnf90.Launcher.Diagnostics;

internal sealed record DiagnosticClock(string Source, double? OffsetMs, double? UncertaintyMs, int LocalUtcOffsetMinutes, string Status);
internal sealed record DiagnosticContext(string CorrelationId, string Status, string Reason, string Source,
    string? ObservedAt, string? ReceivedAt, string? ServerInstance, ulong ConnectionGeneration,
    Dictionary<string, ContextValue> Fields, DiagnosticClock Clock)
{
    internal static readonly string[] Keys = ["character_id", "character_name", "character_generation", "connection_id", "channel_id", "party_id", "party_generation", "dungeon_id", "map_id", "room", "instance_id", "settlement_phase", "boss_dead", "business_state", "reason_code"];
    internal static DiagnosticContext Unavailable(string correlation, string reason) => new(correlation, "unavailable", reason,
        "authenticated_game_session", null, null, null, 0, Keys.ToDictionary(k => k, _ => new ContextValue(null, "unavailable", reason)),
        new("local_system_clock", null, null, (int)DateTimeOffset.Now.Offset.TotalMinutes, "uncalibrated"));
}

// Per launch, never global. The credential is held only by the HTTP client and
// is intentionally absent from every serializable diagnostic type.
internal sealed class DiagnosticContextFeed : IDisposable
{
    private readonly object gate = new();
    private readonly CancellationTokenSource lifetime = new();
    private readonly string correlation;
    private DiagnosticContext context;
    private long receivedTicks;
    private ulong sequence;
    private ulong characterGeneration;
    private string? instance;
    internal DiagnosticContextFeed(string id, string reason = "awaiting_server") { correlation = id; context = DiagnosticContext.Unavailable(id, reason); }
    internal DiagnosticContext Snapshot()
    {
        lock (gate) {
            if (context.Status == "current" && Stopwatch.GetElapsedTime(receivedTicks).TotalSeconds > 10) return Stale(context, "refresh_expired");
            return context;
        }
    }
    private static DiagnosticContext Stale(DiagnosticContext value, string reason) => value with {
        Status = value.ObservedAt is null ? "unavailable" : "last_known", Reason = reason,
        Fields = value.Fields.ToDictionary(p => p.Key, p => p.Value.Value is null ? p.Value : p.Value with { Status = "last_known" })
    };
    internal DiagnosticContext MarkUnavailable(string reason) { lock (gate) { context = Stale(context, reason); return context; } }
    private static DateTimeOffset ParseTime(string text) => DateTimeOffset.Parse(text, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind);
    private void Validate(ContextSnapshot snapshot)
    {
        if (snapshot.Correlation_id != correlation || snapshot.Source != "authenticated_game_session" ||
            !Guid.TryParseExact(snapshot.Server_instance, "N", out _) ||
            snapshot.Status is not ("current" or "last_known" or "waiting") || snapshot.Fields.Count != DiagnosticContext.Keys.Length)
            throw new InvalidDataException("context_identity_invalid");
        _ = ParseTime(snapshot.Observed_at);
        foreach (var key in DiagnosticContext.Keys) {
            if (!snapshot.Fields.TryGetValue(key, out var field) || field.Status is not ("current" or "last_known" or "unavailable") ||
                field.Reason.Length > 64 || field.Reason.Any(c => !(char.IsAsciiLetterOrDigit(c) || c == '_')) ||
                field.Value is { } text && (text.Length > (key == "character_name" ? 64 : 128) || text.Any(char.IsControl)) ||
                (field.Status == "unavailable") != (field.Value is null)) throw new InvalidDataException("context_field_invalid");
        }
    }
    internal (string Kind, DiagnosticContext Context)[] Apply(ContextReply reply, DateTimeOffset sent, DateTimeOffset received, double elapsedMs)
    {
        lock (gate) {
            if (reply.Version != 1 || reply.Correlation_id != correlation || reply.Events.Length > 64 || reply.Last_sequence < sequence)
                throw new InvalidDataException("context_sequence_invalid");
            Validate(reply.Snapshot);
            if (instance is not null && instance != reply.Snapshot.Server_instance) throw new InvalidDataException("server_instance_changed");
            ulong previous = 0;
            foreach (var e in reply.Events) {
                Validate(e.Snapshot);
                if (e.Sequence <= previous || e.Sequence > reply.Last_sequence || e.Snapshot.Server_instance != reply.Snapshot.Server_instance ||
                    e.Kind is not ("context_changed" or "connection_changed" or "character_changed" or "settlement_changed" or "business_disconnect")) throw new InvalidDataException("context_event_invalid");
                previous = e.Sequence;
            }
            static ulong CharacterGeneration(ContextSnapshot s) => s.Fields["character_generation"].Value is { } value
                ? ulong.TryParse(value, out var number) ? number : throw new InvalidDataException("character_generation_invalid") : 0;
            var nextCharacterGeneration = characterGeneration;
            foreach (var e in reply.Events.Where(e => e.Sequence > sequence && e.Snapshot.Connection_generation >= context.ConnectionGeneration)) {
                var next = CharacterGeneration(e.Snapshot);
                if (next != 0 && next < nextCharacterGeneration) throw new InvalidDataException("character_generation_regressed");
                nextCharacterGeneration = Math.Max(next, nextCharacterGeneration);
            }
            var finalCharacterGeneration = CharacterGeneration(reply.Snapshot);
            if (finalCharacterGeneration != 0 && finalCharacterGeneration < nextCharacterGeneration) throw new InvalidDataException("character_generation_regressed");
            var serverReceived = ParseTime(reply.Server_received_utc); var serverSent = ParseTime(reply.Server_sent_utc);
            if (serverSent < serverReceived || elapsedMs < 0 || elapsedMs > 30000) throw new InvalidDataException("context_clock_invalid");
            var stableClock = Math.Abs((received - sent).TotalMilliseconds - elapsedMs) < 1000;
            var uncertainty = Math.Max(0, elapsedMs - (serverSent - serverReceived).TotalMilliseconds) / 2;
            var offset = ((serverReceived - sent).TotalMilliseconds + (serverSent - received).TotalMilliseconds) / 2;
            var clock = new DiagnosticClock("authenticated_server_utc", stableClock ? offset : null, stableClock ? uncertainty : null,
                (int)DateTimeOffset.Now.Offset.TotalMinutes, stableClock ? "estimated" : "local_clock_changed");
            DiagnosticContext Convert(ContextSnapshot s) => new(correlation, s.Status, "", s.Source, s.Observed_at, received.UtcDateTime.ToString("O"), s.Server_instance,
                s.Connection_generation, new(s.Fields), clock);
            var result = new List<(string, DiagnosticContext)>();
            var generation = context.ConnectionGeneration;
            foreach (var e in reply.Events.Where(e => e.Sequence > sequence)) {
                if (e.Snapshot.Connection_generation < generation) continue;
                generation = e.Snapshot.Connection_generation;
                result.Add((e.Kind, Convert(e.Snapshot)));
            }
            if (reply.Snapshot.Connection_generation < generation) throw new InvalidDataException("context_generation_regressed");
            context = Convert(reply.Snapshot);
            if (context.Status == "current" && (serverSent - ParseTime(reply.Snapshot.Observed_at)).TotalSeconds > 10) context = Stale(context, "server_observation_expired");
            instance = reply.Snapshot.Server_instance; receivedTicks = Stopwatch.GetTimestamp(); sequence = reply.Last_sequence;
            characterGeneration = Math.Max(nextCharacterGeneration, finalCharacterGeneration);
            if (reply.Events_lost) result.Insert(0, ("server_event_gap", context));
            return [.. result];
        }
    }
    internal void Start(PlayerProfile profile, DiagnosticGrant grant, Action<string, DiagnosticContext> observed)
    {
        if (grant.Version != 1 || grant.Correlation_id != correlation || grant.Read_token.Length != 64 ||
            !grant.Read_token.All(char.IsAsciiHexDigit) || !DateTimeOffset.TryParse(grant.Expires_utc, out var expiry) || expiry <= DateTimeOffset.UtcNow) {
            observed("server_context_unavailable", MarkUnavailable("invalid_grant")); return;
        }
        _ = Task.Run(async () => {
            using var api = new DiagnosticContextApi(profile, grant); string? lastFailure = null;
            while (!lifetime.IsCancellationRequested && DateTimeOffset.UtcNow < expiry) {
                try {
                    using var timeout = CancellationTokenSource.CreateLinkedTokenSource(lifetime.Token); timeout.CancelAfter(TimeSpan.FromSeconds(5));
                    ulong after; lock (gate) after = sequence;
                    var sent = DateTimeOffset.UtcNow; var watch = Stopwatch.StartNew();
                    var reply = await api.Read(after, timeout.Token);
                    var updates = Apply(reply, sent, DateTimeOffset.UtcNow, watch.Elapsed.TotalMilliseconds);
                    foreach (var (kind, snapshot) in updates) if (!lifetime.IsCancellationRequested) observed(kind, snapshot);
                    if (lastFailure is not null) observed("server_context_restored", Snapshot());
                    lastFailure = null;
                } catch (OperationCanceledException) when (lifetime.IsCancellationRequested) { break; }
                catch (Exception error) {
                    var reason = error is ContextUnavailableException ? "endpoint_unavailable" : error is InvalidDataException or FormatException or JsonException ? "invalid_response" : "request_failed";
                    if (lastFailure != reason && !lifetime.IsCancellationRequested) observed("server_context_unavailable", MarkUnavailable(reason));
                    lastFailure = reason;
                    if (reason == "endpoint_unavailable") break;
                }
                try { await Task.Delay(lastFailure is null ? 2000 : 10000, lifetime.Token); } catch (OperationCanceledException) { break; }
            }
            if (!lifetime.IsCancellationRequested && DateTimeOffset.UtcNow >= expiry)
                observed("server_context_unavailable", MarkUnavailable("grant_expired"));
        });
    }
    public void Dispose() { lifetime.Cancel(); }
}
