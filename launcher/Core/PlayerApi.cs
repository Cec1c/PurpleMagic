using System.Diagnostics;
using System.Net.Http.Json;
using System.Net.Security;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json;

namespace ServerS5A3;

public sealed record HttpTraceSample(string HttpTraceId, string EndpointKind, int HttpStatus, double DurationMs, string ObservedUtc, long MonoTicks, string? ServerDateUtc = null);

public sealed record LoginReply(bool Ok, string? Account_id, string? Token, string? Message)
{
    public DiagnosticGrant? Diagnostics { get; init; }
    public string? Code { get; init; }
    public long RetryAfter { get; init; }
    public string? BindingCode { get; init; }
    public string? BoundQQ { get; init; }
    public long ExpiresAt { get; init; }
    public string[] Groups { get; init; } = [];
}
public sealed class AccountApiException(LoginReply reply) : InvalidOperationException(reply.Message ?? "账号服务暂不可用。")
{
    public string? Code => reply.Code;
    public long RetryAfter => reply.RetryAfter;
}

public sealed class PlayerApi : IDisposable
{
    public HttpClient Http { get; }
    public PlayerProfile Profile { get; }
    private readonly Action<int>? responseObserved;
    private readonly Action<HttpTraceSample>? traceObserved;
    public static readonly JsonSerializerOptions Json = new() { PropertyNameCaseInsensitive = true, TypeInfoResolver = CoreJson.Default };
    public PlayerApi(PlayerProfile profile, Action<int>? responseObserved = null, Action<HttpTraceSample>? traceObserved = null)
    {
        Profile = profile;
        this.responseObserved = responseObserved;
        this.traceObserved = traceObserved;
        Http = new HttpClient(new HttpClientHandler {
            UseProxy = false, AllowAutoRedirect = false,
            AutomaticDecompression = System.Net.DecompressionMethods.GZip | System.Net.DecompressionMethods.Deflate,
            ServerCertificateCustomValidationCallback = (request, cert, chain, errors) =>
                CertificateAllowed(request.RequestUri!, cert, errors, profile)
        }) { Timeout = TimeSpan.FromSeconds(20) };
    }
    public static bool CertificateAllowed(Uri uri, X509Certificate2? cert, SslPolicyErrors errors, PlayerProfile profile)
    {
        if (cert == null || uri.Scheme != "https") return false;
        var origin = new Uri(profile.ApiUrl);
        if (uri.Host != origin.Host || uri.Port != origin.Port) return errors == SslPolicyErrors.None;
        return DateTime.UtcNow >= cert.NotBefore.ToUniversalTime() && DateTime.UtcNow <= cert.NotAfter.ToUniversalTime()
            && (errors & SslPolicyErrors.RemoteCertificateNameMismatch) == 0
            && Convert.ToHexString(SHA256.HashData(cert.RawData)).Equals(profile.CertificateSha256, StringComparison.OrdinalIgnoreCase);
    }
    public async Task<LoginReply> Submit(string action, string account, string password, string device, string? newPassword = null, string? diagnosticCorrelationId = null)
    {
        if (action is not ("register" or "login" or "binding-code" or "password")) throw new ArgumentException(nameof(action));
        var traceId = "http-" + Guid.NewGuid().ToString("N")[..16];
        var watch = Stopwatch.StartNew();
        using var request = new HttpRequestMessage(HttpMethod.Post, Profile.ApiUrl + "/api/account/" + action) {
            Content = JsonContent.Create(new AccountRequest(account, password, device) { newPassword = newPassword, requireQQBinding = Profile.RequireQQBinding, diagnosticCorrelationId = diagnosticCorrelationId }, CoreJson.Default.AccountRequest)
        };
        request.Headers.TryAddWithoutValidation("X-PurpleMagic-Trace", traceId);
        using var response = await Http.SendAsync(request);
        var elapsed = watch.Elapsed.TotalMilliseconds;
        var serverDate = response.Headers.Date?.UtcDateTime.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        try { responseObserved?.Invoke((int)response.StatusCode); } catch (Exception) { }
        try { traceObserved?.Invoke(new(traceId, "account_" + action, (int)response.StatusCode, Math.Round(elapsed, 3), DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ"), Stopwatch.GetTimestamp(), serverDate)); } catch (Exception) { }
        var reply = await response.Content.ReadFromJsonAsync(CoreJson.Default.LoginReply);
        if (reply?.Ok != true) throw new AccountApiException(reply ?? new(false, null, null, "账号服务暂不可用。"));
        if (action == "login" && Profile.RequireQQBinding && string.IsNullOrWhiteSpace(reply.BoundQQ))
            throw new InvalidOperationException("服务器未返回 QQ 绑定验证结果，请联系管理员更新账号服务。");
        return reply;
    }
    public async Task CheckStatus()
    {
        using var response = await Http.GetAsync(Profile.ApiUrl + "/api/status");
        response.EnsureSuccessStatusCode();
    }
    public void Dispose() => Http.Dispose();
}
