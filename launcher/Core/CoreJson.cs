using System.Text.Json.Serialization;

namespace ServerS5A3;

// Explicit metadata keeps the shared wire contract usable by the native AOT host.
[JsonSourceGenerationOptions(PropertyNameCaseInsensitive = true)]
[JsonSerializable(typeof(PlayerProfile))]
[JsonSerializable(typeof(PlayerPreferences))]
[JsonSerializable(typeof(LoginReply))]
[JsonSerializable(typeof(AccountRequest))]
[JsonSerializable(typeof(ContextRequest))]
[JsonSerializable(typeof(ContextReply))]
[JsonSerializable(typeof(SignedEnvelope))]
[JsonSerializable(typeof(UpdateSettings))]
[JsonSerializable(typeof(ReleaseManifest))]
public partial class CoreJson : JsonSerializerContext { }

public sealed record AccountRequest(string account, string password, string device)
{
    [JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingNull)]
    public string? diagnosticCorrelationId { get; init; }
    public string? newPassword { get; init; }
    public bool requireQQBinding { get; init; }
}
