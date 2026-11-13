using System.Net;
using System.Net.Security;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Text.Json;
using Dnf90.Launcher;
using ServerS5A3;

internal static class NativePluginUpdateChecks
{
    internal static async Task Run(string temp, Action<bool, string> check)
    {
        using var signer = RSA.Create(2048);
        using var tlsKey = RSA.Create(2048);
        var request = new CertificateRequest("CN=localhost", tlsKey, HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);
        var names = new SubjectAlternativeNameBuilder(); names.AddIpAddress(IPAddress.Loopback);
        request.CertificateExtensions.Add(names.Build());
        using var generated = request.CreateSelfSigned(DateTimeOffset.UtcNow.AddMinutes(-1), DateTimeOffset.UtcNow.AddHours(1));
        using var certificate = new X509Certificate2(generated.Export(X509ContentType.Pfx));
        using var listener = new TcpListener(IPAddress.Loopback, 0); listener.Start();
        var origin = "https://127.0.0.1:" + ((IPEndPoint)listener.LocalEndpoint).Port;
        var root = new PlayerProfile(origin, Trust.Digest(certificate.RawData), signer.ExportSubjectPublicKeyInfoPem(), "", "192.0.2.1", 7001, new('b', 64), new('c', 64), "")
        { MinimumLauncherVersion = "1.0.12" };
        var app = new BundleRelease("1.0.12", origin + "/app.zip", 1234, new('a', 64)) { BuildFlavor = LauncherBuild.Flavor };
        var bridge = new Bootstrap("DNF90-Flutter", 1, 41, root, [], null, app);
        var content = new ContentRelease("fixture", "fixture", "stable", 1, new('d', 64), origin, root.CertificateSha256,
        [
            new("DNF.exe", 10, root.ExeSha256, true, "none", "", 0),
            new("Script.pvf", 20, root.PvfSha256, true, "none", "", 0),
            new("CombatPower.dll", 30, new('e', 64), true, "none", "", 0),
            new("DpsMeter.dll", 40, new('f', 64), true, "none", "", 0)
        ]);
        var full = bridge with { Sequence = 42, Content = content };
        byte[] Sign(Bootstrap value)
        {
            var payload = JsonSerializer.SerializeToUtf8Bytes(value, WireJson.Default.Bootstrap);
            return JsonSerializer.SerializeToUtf8Bytes(new SignedEnvelope(Convert.ToBase64String(payload),
                Convert.ToBase64String(signer.SignData(payload, HashAlgorithmName.SHA256, RSASignaturePadding.Pss))), CoreJson.Default.SignedEnvelope);
        }
        var state = Path.Combine(temp, "native-plugin-update"); Directory.CreateDirectory(state);
        var cache = Path.Combine(state, "bootstrap.json");
        var bridgeRaw = Sign(bridge); var fullRaw = Sign(full);
        File.WriteAllBytes(cache, bridgeRaw);
        using var engine = new LauncherEngine(root, state);
        check(!engine.State.HasLauncherUpdate, "updated APP recognizes its version in a cached APP-only release");
        check(LauncherEngine.SupportsContent(root), "updated APP satisfies the signed 1.0.12 content requirement");

        async Task Serve(byte[] body)
        {
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(20));
            using var tcp = await listener.AcceptTcpClientAsync(timeout.Token);
            using var tls = new SslStream(tcp.GetStream());
            await tls.AuthenticateAsServerAsync(new SslServerAuthenticationOptions { ServerCertificate = certificate }, timeout.Token);
            using var reader = new StreamReader(tls, Encoding.ASCII, false, 1024, true);
            var first = await reader.ReadLineAsync(timeout.Token);
            var headers = new List<string>(); string? line;
            while (!string.IsNullOrEmpty(line = await reader.ReadLineAsync(timeout.Token))) headers.Add(line);
            check(first == "GET " + LauncherBuild.ChannelPath + "bootstrap.json HTTP/1.1", "capability request preserves the build's existing bootstrap route");
            var capabilities = headers.Where(h => h.StartsWith("X-DNF90-Content-Capabilities:", StringComparison.OrdinalIgnoreCase)).ToArray();
            check(capabilities.Length == 1 && capabilities[0] == "X-DNF90-Content-Capabilities: native-plugins-v1", "real TLS request sends exactly one native-plugins-v1 capability header");
            // The capability is a request hint; an existing server need not echo it.
            await tls.WriteAsync(Encoding.ASCII.GetBytes($"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {body.Length}\r\nConnection: close\r\n\r\n"), timeout.Token);
            await tls.WriteAsync(body, timeout.Token);
        }
        async Task Fetch(byte[] body, bool rejected)
        {
            var server = Serve(body);
            try
            {
                try { await engine.Check(); if (rejected) throw new Exception("Untrusted or stale release was accepted"); }
                catch (Exception error) when (rejected && error is InvalidDataException or CryptographicException)
                { check(true, "capability negotiation retains rejection of invalid signed release data"); }
            }
            finally { await server; }
        }

        await Fetch(fullRaw, false);
        check(File.ReadAllBytes(cache).SequenceEqual(fullRaw), "cached APP-only release advances to the exact signed full release");
        check(Trust.Verify(File.ReadAllBytes(cache), root).Content!.Files.Length == 4, "full release retains EXE, PVF and both named plugins");
        check(!Directory.Exists(Path.Combine(state, "downloads")), "metadata check does not install or download game resources");

        await Fetch(bridgeRaw, true);
        check(File.ReadAllBytes(cache).SequenceEqual(fullRaw), "full release cannot roll back to the older APP-only release");
        await Fetch(Sign(full with { Content = null }), true);
        check(File.ReadAllBytes(cache).SequenceEqual(fullRaw), "same-sequence payload changes cannot replace the cached full release");
        var envelope = JsonSerializer.Deserialize(Sign(full with { Sequence = 43 }), CoreJson.Default.SignedEnvelope)!;
        var signature = Convert.FromBase64String(envelope.Signature); signature[0] ^= 1;
        await Fetch(JsonSerializer.SerializeToUtf8Bytes(envelope with { Signature = Convert.ToBase64String(signature) }, CoreJson.Default.SignedEnvelope), true);
        check(File.ReadAllBytes(cache).SequenceEqual(fullRaw), "invalid signature cannot replace the cached full release");
    }
}
