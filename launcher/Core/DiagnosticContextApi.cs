using System.Net;
using System.Net.Http.Json;
using System.Text.Json;

namespace ServerS5A3;

public sealed record DiagnosticGrant(int Version, string Correlation_id, string Read_token, string Expires_utc);
public sealed record ContextValue(string? Value, string Status, string Reason);
public sealed record ContextSnapshot(string Correlation_id, string Server_instance, ulong Connection_generation,
    string Observed_at, string Source, string Status, Dictionary<string, ContextValue> Fields);
public sealed record ContextEvent(ulong Sequence, string Kind, ContextSnapshot Snapshot);
public sealed record ContextReply(int Version, string Correlation_id, string Server_received_utc, string Server_sent_utc,
    ulong Last_sequence, bool Events_lost, ContextSnapshot Snapshot, ContextEvent[] Events);
public sealed record ContextRequest(string read_token, string correlation_id, ulong after);

public sealed class DiagnosticContextApi(PlayerProfile profile, DiagnosticGrant grant) : IDisposable
{
    private readonly PlayerApi api = new(profile);
    public async Task<ContextReply> Read(ulong after, CancellationToken cancellation)
    {
        using var request = new HttpRequestMessage(HttpMethod.Post, profile.ApiUrl + "/api/diagnostics/context") {
            Content = JsonContent.Create(new ContextRequest(grant.Read_token, grant.Correlation_id, after), CoreJson.Default.ContextRequest)
        };
        using var response = await api.Http.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellation);
        if (response.StatusCode is HttpStatusCode.NotFound or HttpStatusCode.Gone or HttpStatusCode.Unauthorized or HttpStatusCode.Forbidden)
            throw new ContextUnavailableException();
        response.EnsureSuccessStatusCode();
        await using var stream = await response.Content.ReadAsStreamAsync(cancellation);
        using var body = new MemoryStream(); var buffer = new byte[8192];
        while (true) {
            var count = await stream.ReadAsync(buffer, cancellation); if (count == 0) break;
            if (body.Length + count > 256 * 1024) throw new InvalidDataException("context_response_limit");
            body.Write(buffer, 0, count);
        }
        return JsonSerializer.Deserialize(body.ToArray(), CoreJson.Default.ContextReply) ?? throw new InvalidDataException("context_empty");
    }
    public void Dispose() => api.Dispose();
}
public sealed class ContextUnavailableException : IOException { }
