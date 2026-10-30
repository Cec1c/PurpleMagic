using System.Net;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Text.Json;
using Dnf90.Launcher;
using ServerS5A3;

internal static class AutomaticUpdateChecks
{
    internal static async Task Run(string temp, PlayerProfile profile, Action<bool, string> check)
    {
        check(new Preferences().AutoCheck, "new installations enable automatic updates");
        check(JsonSerializer.Deserialize("{}", WireJson.Default.Preferences)!.AntiCheatConsentVersion == 0, "legacy preferences require initial protection disclosure consent");
        check(JsonSerializer.Deserialize("{}", WireJson.Default.Preferences)!.AutoCheck, "missing legacy preference defaults to enabled");
        var state = Path.Combine(temp, "auto-preferences");
        using (var engine = new LauncherEngine(profile, state))
        {
            using var serialized = JsonDocument.Parse(JsonSerializer.Serialize(engine.State, WireJson.Default.State));
            check(serialized.RootElement.GetProperty("launcherVersion").GetString() == LauncherEngine.VersionText,
                "wire state reports running launcher version independently of content version");
            await engine.SavePreferences(new(AutoCheck: false, AntiCheatConsentVersion: 1));
        }
        using (var engine = new LauncherEngine(profile, state))
        {
            check(!engine.State.Preferences.AutoCheck, "explicit opt-out survives engine restart");
            check(engine.State.Preferences.AntiCheatConsentVersion == 1, "protection disclosure consent survives core restart");
            check(!await engine.AutomaticUpdate(-1) && !File.Exists(Path.Combine(state, "bootstrap.json")), "disabled automatic updates do not contact metadata or start installers");
        }

        // A developer may be playing while running this suite. Preserve the real
        // process guard; do not require closing their game for isolated checks.
        var games = System.Diagnostics.Process.GetProcessesByName("DNF");
        var gameRunning = games.Length != 0;
        foreach (var game in games) game.Dispose();
        if (gameRunning)
        {
            using var guarded = new LauncherEngine(profile, Path.Combine(temp, "auto-running-game"));
            try { await guarded.AutomaticUpdate(-1); throw new Exception("running game was ignored"); }
            catch (InvalidOperationException error)
            {
                check(error.Message == "更新或恢复文件前请退出所有游戏客户端。", "running game blocks automatic update before network or file installation");
            }
            Console.WriteLine("SKIP self-update priority TLS fixture while a real DNF process is running");
            return;
        }

        // Real local TLS and signed metadata exercise the priority decision without
        // starting a UI/helper. The test executable is deliberately not an installation.
        using var signing = RSA.Create(2048);
        using var tlsKey = RSA.Create(2048);
        var certificateRequest = new CertificateRequest("CN=localhost", tlsKey, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);
        var names = new SubjectAlternativeNameBuilder(); names.AddIpAddress(IPAddress.Loopback);
        certificateRequest.CertificateExtensions.Add(names.Build());
        using var generated = certificateRequest.CreateSelfSigned(DateTimeOffset.UtcNow.AddMinutes(-1), DateTimeOffset.UtcNow.AddHours(1));
        using var certificate = new X509Certificate2(generated.Export(X509ContentType.Pfx));
        using var listener = new TcpListener(IPAddress.Loopback, 0); listener.Start();
        var endpoint = "https://127.0.0.1:" + ((IPEndPoint)listener.LocalEndpoint).Port;
        var trusted = profile with { ApiUrl = endpoint, CertificateSha256 = Trust.Digest(certificate.RawData), UpdatePublicKey = signing.ExportSubjectPublicKeyInfoPem(), DefaultClientDirectory = "" };
        var release = new BundleRelease("99.0.0", endpoint + "/bundle.zip", 1, new string('d', 64)) { BuildFlavor = LauncherBuild.Flavor };
        var content = new ContentRelease("fixture", "author", "stable", 1, new string('e', 64), endpoint, trusted.CertificateSha256,
            [new("DNF.exe", 10, trusted.ExeSha256, false, "", "", 0), new("Script.pvf", 20, trusted.PvfSha256, false, "", "", 0)]);
        var payload = JsonSerializer.SerializeToUtf8Bytes(new Bootstrap("DNF90-Flutter", 1, 1, trusted, [], content, release), WireJson.Default.Bootstrap);
        var signed = JsonSerializer.SerializeToUtf8Bytes(new SignedEnvelope(Convert.ToBase64String(payload), Convert.ToBase64String(signing.SignData(payload, HashAlgorithmName.SHA256, RSASignaturePadding.Pss))), CoreJson.Default.SignedEnvelope);
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(20));
        var server = Task.Run(async () =>
        {
            for (var i = 0; i < 2; i++)
            {
                using var tcp = await listener.AcceptTcpClientAsync(timeout.Token);
                using var stream = new SslStream(tcp.GetStream());
                await stream.AuthenticateAsServerAsync(new SslServerAuthenticationOptions { ServerCertificate = certificate }, timeout.Token);
                using var reader = new StreamReader(stream, Encoding.ASCII, false, 1024, true);
                var request = await reader.ReadLineAsync(timeout.Token);
                if (request != "GET " + LauncherBuild.ChannelPath + "bootstrap.json HTTP/1.1") throw new Exception("unexpected fixture request");
                while (!string.IsNullOrEmpty(await reader.ReadLineAsync(timeout.Token))) { }
                await stream.WriteAsync(Encoding.ASCII.GetBytes($"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {signed.Length}\r\nConnection: close\r\n\r\n"), timeout.Token);
                await stream.WriteAsync(signed, timeout.Token);
            }
        });
        foreach (var directory in new[] { "", Path.Combine(temp, "missing-baseline") })
        {
            using var engine = new LauncherEngine(trusted, Path.Combine(temp, "auto-priority-" + Guid.NewGuid().ToString("N")));
            await engine.SavePreferences(new(directory));
            try { await engine.AutomaticUpdate(-1); throw new Exception("installer guard was bypassed"); }
            catch (InvalidDataException error)
            {
                check(error.Message == "当前不是支持整体更新的登录器安装目录。", "self-update precedes missing client directory or unavailable baseline");
            }
            check(engine.State.HasLauncherUpdate && engine.State.Phase == "error", "self-update failure stays visible and prevents client installation");
            await engine.SavePreferences(new(directory, AutoCheck: false));
            check(!await engine.AutomaticUpdate(-1), "self-update failure releases operation lock and permits opt-out");
        }
        await server;
    }
}
