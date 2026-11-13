using System.Diagnostics;
using System.Text;
using System.Text.Json;
using Dnf90.Launcher.Diagnostics;
using ServerS5A3;

internal static class DiagnosticContextChecks
{
    internal static ContextSnapshot Snapshot(string correlation, string instance, ulong generation, string? character, string status = "current")
    {
        var fields = DiagnosticContext.Keys.ToDictionary(k => k, _ => new ContextValue(null, "unavailable", "not_in_scene"));
        foreach (var key in new[] { "character_id", "character_name", "character_generation" }) fields[key] = character is null ? new(null, "unavailable", "not_selected") : new(key == "character_name" ? character : "17", "current", "");
        fields["connection_id"] = new("game-000001", "current", "");
        fields["settlement_phase"] = new("cards_revealed", "current", "");
        return new(correlation, instance, generation, DateTimeOffset.UtcNow.ToString("O"), "authenticated_game_session", status, fields);
    }
    internal static async Task Run(string temp, Action<bool, string> check)
    {
        var id = Guid.NewGuid().ToString("N"); var instance = Guid.NewGuid().ToString("N");
        using var feed = new DiagnosticContextFeed(id);
        var utc = DateTimeOffset.UtcNow;
        ContextReply Reply(ulong seq, ContextSnapshot snapshot, params ContextEvent[] events) => new(1, id, utc.ToString("O"), utc.ToString("O"), seq, false, snapshot, events);
        var a = Snapshot(id, instance, 1, "示例角色甲");
        var b = Snapshot(id, instance, 1, "示例角色乙");
        feed.Apply(Reply(1, a, new ContextEvent(1, "character_changed", a)), utc.AddMilliseconds(-20), utc.AddMilliseconds(20), 40);
        check(feed.Snapshot().Fields["character_name"].Value == "示例角色甲", "authenticated current character collected");
        var selection = Snapshot(id, instance, 1, null);
        feed.Apply(Reply(2, selection, new ContextEvent(2, "character_changed", selection)), utc, utc, 0);
        check(feed.Snapshot().Fields["character_name"].Value is null, "return to selection clears previous character");
        feed.Apply(Reply(3, b, new ContextEvent(3, "character_changed", b)), utc, utc, 0);
        var reconnect = b with { Connection_generation = 2 };
        feed.Apply(Reply(4, reconnect, new ContextEvent(4, "connection_changed", reconnect)), utc, utc, 0);
        bool Reject(ContextReply value) { try { feed.Apply(value, utc, utc, 0); return false; } catch (InvalidDataException) { return true; } }
        check(Reject(Reply(5, a, new ContextEvent(5, "context_changed", a))), "retired connection cannot replace character B after reconnect");
        check(feed.Snapshot().Fields["character_name"].Value == "示例角色乙", "failed response leaves prior validated context intact");
        check(Reject(Reply(5, b with { Correlation_id = Guid.NewGuid().ToString("N") })), "another client correlation is rejected");
        var oldCharacter = new Dictionary<string, ContextValue>(reconnect.Fields) { ["character_generation"] = new("1", "current", "") };
        check(Reject(Reply(5, reconnect with { Fields = oldCharacter })), "retired character generation cannot override current identity within a connection");
        var malformed = new Dictionary<string, ContextValue>(b.Fields) { ["token"] = new("SECRET", "current", "") };
        check(Reject(Reply(5, b with { Fields = malformed })), "undeclared fields cannot enter private log");
        using (var clocks = new DiagnosticContextFeed(id)) {
            clocks.Apply(Reply(1, a), utc, utc.AddMinutes(1), 30);
            check(clocks.Snapshot().Clock is { Status: "local_clock_changed", OffsetMs: null }, "local clock jumps are not reported as server offset");
        }
        var stale = feed.MarkUnavailable("request_failed");
        check(stale.Status == "last_known" && stale.Fields["character_name"].Status == "last_known" && stale.ObservedAt is not null, "failed refresh retains explicitly aged identity and observation time");
        using var other = new DiagnosticContextFeed(Guid.NewGuid().ToString("N"), "server_not_supported");
        check(other.Snapshot().Fields.Values.All(v => v.Value is null) && other.Snapshot().Status == "unavailable", "unsupported server exports explicit unavailable fields without another client's data");
        var disconnected = reconnect with { Status = "last_known", Fields = reconnect.Fields.ToDictionary(p => p.Key, p => p.Value.Value is null ? p.Value : p.Value with { Status = "last_known" }) };
        var updates = feed.Apply(Reply(5, disconnected, new ContextEvent(5, "business_disconnect", disconnected)), utc, utc, 0);
        var context = updates.Single().Context;
        var root = Path.Combine(temp, "context-log"); var store = new DiagnosticStorage(root);
        var sid = Guid.NewGuid().ToString("N");
        var job = DiagnosticChecks.Job(sid) with { Summary = DiagnosticChecks.Job(sid).Summary with { Context = context, CorrelationId = id }, Lines = [] };
        DiagnosticLine Line(long seq, string reason) {
            var row = new DiagnosticRecord(seq, utc.ToString("O"), seq, "event", "server", sid, 0, "ok", new() { ["exit_code"] = new(0) }, reason, Context: context);
            return new([.. JsonSerializer.SerializeToUtf8Bytes(row, DiagnosticJson.Default.DiagnosticRecord), 10], seq, false, "event");
        }
        store.Save(job with { Lines = [Line(1, "business_disconnect"), Line(2, "game_exit")], CriticalTail = true });
        var exported = store.Export(sid, Path.Combine(temp, "character-context-synthetic.log"));
        var text = File.ReadAllText(exported.Path);
        check(text.Contains("character_name") && text.Contains("business_disconnect") && text.Contains("cards_revealed") && !text.Contains("read_token"), "private support log keeps character and business disconnect despite exit zero, without credentials");
        var script = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "../../../../../scripts/analyze_diagnostics.py"));
        var analyzerStart = new ProcessStartInfo("python") { ArgumentList = { script, exported.Path }, RedirectStandardOutput = true, RedirectStandardError = true, StandardOutputEncoding = Encoding.UTF8, StandardErrorEncoding = Encoding.UTF8, UseShellExecute = false, CreateNoWindow = true };
        analyzerStart.Environment["PYTHONIOENCODING"] = "utf-8";
        using var analyzer = Process.Start(analyzerStart)!;
        var output = await analyzer.StandardOutput.ReadToEndAsync(); var error = await analyzer.StandardError.ReadToEndAsync(); await analyzer.WaitForExitAsync();
        check(analyzer.ExitCode == 0, "real exported schema 5 accepted by analyzer: " + error);
        using var analysis = JsonDocument.Parse(output);
        check(analysis.RootElement.GetProperty("outcome").GetProperty("state").GetString() == "business_disconnected" && analysis.RootElement.GetProperty("support_summary").GetProperty("event_beijing").GetString()!.EndsWith("+08:00"), "support summary identifies business disconnect, character and Beijing time");
        var target = Environment.GetEnvironmentVariable("PURPLEMAGIC_CONTEXT_SAMPLE");
        if (!string.IsNullOrEmpty(target)) File.Copy(exported.Path, target, true);
        var wireFixture = Environment.GetEnvironmentVariable("PURPLEMAGIC_CONTEXT_WIRE_FIXTURE");
        if (!string.IsNullOrEmpty(wireFixture)) {
            var wire = JsonSerializer.Deserialize(File.ReadAllBytes(wireFixture), CoreJson.Default.ContextReply)!;
            using var interop = new DiagnosticContextFeed(wire.Correlation_id);
            var serverUtc = DateTimeOffset.Parse(wire.Server_sent_utc);
            interop.Apply(wire, serverUtc, serverUtc, 0);
            check(interop.Snapshot().Fields["character_name"].Value == "Fixture B" && interop.Snapshot().Fields["settlement_phase"].Value == "cards_revealed", "actual Go HTTP response deserializes through NativeAOT C# contract and retains B disconnect");
        }
    }
}
