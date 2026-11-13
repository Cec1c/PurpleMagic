using System.Security.Cryptography;
using System.Text.Json;
using Dnf90.Launcher;
using ServerS5A3;

internal static class NativePluginPathChecks
{
    internal static void Run()
    {
        var passed = 0;
        void Check(bool condition, string name) { if (!condition) throw new Exception(name); passed++; Console.WriteLine("PASS " + name); }
        void Reject(Action action, string name)
        {
            try { action(); }
            catch (Exception error) when (error is InvalidDataException or CryptographicException) { Check(true, name); return; }
            throw new Exception("Accepted: " + name);
        }

        using var signer = RSA.Create(2048);
        var trusted = new PlayerProfile("https://fixture.test", new string('a', 64), signer.ExportSubjectPublicKeyInfoPem(), "", "192.0.2.1", 7001, new string('b', 64), new string('c', 64), "");
        ContentFile[] baseline = [new("DNF.exe", 10, trusted.ExeSha256, true, "none", "", 0), new("Script.pvf", 20, trusted.PvfSha256, true, "none", "", 0)];
        var content = new ContentRelease("fixture", "local-guild", "stable", 5, new string('d', 64), trusted.ApiUrl, trusted.CertificateSha256, baseline);
        var original = new Bootstrap("DNF90-Flutter", 1, 205, trusted, [], content, null);
        byte[] Sign(Bootstrap value)
        {
            var payload = JsonSerializer.SerializeToUtf8Bytes(value, WireJson.Default.Bootstrap);
            return JsonSerializer.SerializeToUtf8Bytes(new SignedEnvelope(Convert.ToBase64String(payload), Convert.ToBase64String(signer.SignData(payload, HashAlgorithmName.SHA256, RSASignaturePadding.Pss))), CoreJson.Default.SignedEnvelope);
        }
        ContentFile Plugin(string path) => new(path, 123, new string('e', 64), true, "gzip", new string('f', 64), 99);
        Bootstrap WithPlugin(ContentFile file) => original with { Content = content with { Files = [..baseline, file] } };

        foreach (var path in new[] { "CombatPower.dll", "DpsMeter.dll", "combatpower.DLL", "DPSMETER.dll" })
            Check(Trust.Verify(Sign(WithPlugin(Plugin(path))), trusted).Content!.Files.Length == 3, "allow exact root plugin path " + path);
        foreach (var path in new[] { "evil.dll", "x/CombatPower.dll", "ImagePacks2/DpsMeter.dll", "CombatPower.dll.exe", "CombatPower.dll.", "CombatPower.dll ", "../CombatPower.dll", "/DpsMeter.dll", "C:/DpsMeter.dll", "x\\DpsMeter.dll", ".dnf90-update/CombatPower.dll" })
            Reject(() => Trust.Verify(Sign(WithPlugin(Plugin(path))), trusted), "reject non-root or alternate plugin path " + path);
        var combatPower = Plugin("CombatPower.dll");
        foreach (var invalid in new[] { combatPower with { Length = -1 }, combatPower with { Length = 8L * 1024 * 1024 * 1024 + 1 }, combatPower with { Sha256 = "invalid" }, combatPower with { Compression = "zip" }, combatPower with { BlobSha256 = "invalid" }, combatPower with { BlobLength = 0 } })
            Reject(() => Trust.Verify(Sign(WithPlugin(invalid)), trusted), "retain plugin length/hash/compression checks");
        var duplicate = WithPlugin(combatPower) with { Content = content with { Files = [..baseline, combatPower, combatPower with { Path = "COMBATPOWER.DLL" }] } };
        Reject(() => Trust.Verify(Sign(duplicate), trusted), "plugin duplicate remains case-insensitive");
        Check(Trust.Verify(Sign(WithPlugin(Plugin("ImagePacks2/fixture.npk"))), trusted).Content!.Files.Length == 3, "existing NPK path rule remains accepted");
        Console.WriteLine("PASS TOTAL " + passed + " native plugin path checks; isolated data only.");
    }
}
