using System.Net.Http.Json;
using System.Net.Security;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json;

namespace ServerS5A3;

public sealed record LoginReply(bool Ok, string? Account_id, string? Token, string? Message);

public sealed class PlayerApi : IDisposable
{
    public HttpClient Http { get; }
    public PlayerProfile Profile { get; }
    private readonly Action<int>? responseObserved;
    public static readonly JsonSerializerOptions Json = new() { PropertyNameCaseInsensitive = true, TypeInfoResolver = CoreJson.Default };
    public PlayerApi(PlayerProfile profile, Action<int>? responseObserved = null)
    {
        Profile = profile;
        this.responseObserved = responseObserved;
        Http = new HttpClient(new HttpClientHandler {
            UseProxy = false, AllowAutoRedirect = false,
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
    public async Task<LoginReply> Submit(string action, string account, string password, string device)
    {
        if (action is not ("register" or "login")) throw new ArgumentException(nameof(action));
        using var response = await Http.PostAsJsonAsync(Profile.ApiUrl + "/api/account/" + action, new AccountRequest(account, password, device), CoreJson.Default.AccountRequest);
        try { responseObserved?.Invoke((int)response.StatusCode); } catch (Exception) { }
        var reply = await response.Content.ReadFromJsonAsync(CoreJson.Default.LoginReply);
        if (reply?.Ok != true) throw new InvalidOperationException(reply?.Message ?? "账号服务暂不可用。");
        return reply;
    }
    public async Task CheckStatus()
    {
        using var response = await Http.GetAsync(Profile.ApiUrl + "/api/status");
        response.EnsureSuccessStatusCode();
    }
    public void Dispose() => Http.Dispose();
}
