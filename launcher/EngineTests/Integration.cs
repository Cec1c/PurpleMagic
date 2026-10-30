using System.Net;
using System.Text.Json;
using Dnf90.Launcher;
using ServerS5A3;

static class Integration
{
    public static async Task Run(string receipt)
    {
        try
        {
            using var doc = JsonDocument.Parse(File.ReadAllBytes(receipt)); var data = doc.RootElement;
            var profile = JsonSerializer.Deserialize(File.ReadAllBytes(data.GetProperty("profile").GetString()!), CoreJson.Default.PlayerProfile)!;
            var state = data.GetProperty("state").GetString()!; var game = data.GetProperty("game").GetString()!;
            if (!IPAddress.TryParse(new Uri(profile.ApiUrl).Host, out var ip) || !IPAddress.IsLoopback(ip) || !Path.GetFullPath(game).StartsWith(Path.GetTempPath(), StringComparison.OrdinalIgnoreCase) || !Path.GetFullPath(state).StartsWith(Path.GetTempPath(), StringComparison.OrdinalIgnoreCase)) throw new Exception("isolated loopback fixture required");
            File.WriteAllText(Path.Combine(game, "player-settings.ini"), "preserve-me");
            File.WriteAllBytes(Path.Combine(state, "preferences.json"), JsonSerializer.SerializeToUtf8Bytes(new Preferences(game, AutoCheck:false), WireJson.Default.Preferences));
            using var engine = new LauncherEngine(profile, state);
            if (await engine.AutomaticUpdate(-1) || File.Exists(Path.Combine(state, "bootstrap.json")) || File.Exists(Path.Combine(game, "DNF.exe"))) throw new Exception("disabled automatic update touched network/files");
            await engine.Check(); if (!engine.State.HasUpdate || engine.State.CanLogin) throw new Exception("required update gate failed");
            await engine.SavePreferences(engine.State.Preferences with { ReducedEffects = true });
            if (!engine.State.HasUpdate || engine.State.CanLogin) throw new Exception("settings change cleared pending update gate");
            using (var api = new PlayerApi(profile))
            {
                var raw = await api.Http.GetByteArrayAsync(profile.ApiUrl.TrimEnd('/') + LauncherBuild.ChannelPath + "bootstrap.json");
                var signed = Trust.Verify(raw, profile).Profile;
                if (signed.ChannelServerId != 1 || signed.PreviousChannelServerId != 0) throw new Exception("Go signed migration fields lost at C# boundary");
            }
            await engine.UpdateContent(); if (engine.State.HasUpdate || !engine.State.CanLogin) throw new Exception("install did not reach ready");
            await GameContract.VerifyClient(game, profile);
            File.WriteAllText(Path.Combine(game, "DNF.exe"), "outdated executable");
            File.WriteAllText(Path.Combine(game, "Script.pvf"), "outdated pvf");
            await engine.SavePreferences(engine.State.Preferences with { AutoCheck = true });
            if (await engine.AutomaticUpdate(-1) || engine.State.HasUpdate || !engine.State.CanLogin) throw new Exception("automatic content update did not reach ready");
            await GameContract.VerifyClient(game, profile);
            if (await engine.AutomaticUpdate(-1) || engine.State.HasUpdate) throw new Exception("repeated automatic update was not idempotent");
            if (File.ReadAllText(Path.Combine(game, "player-settings.ini")) != "preserve-me") throw new Exception("unmanaged file changed");
            await engine.Register(new Credentials("fixture-user", "fixture-password"));
            var saved = File.ReadAllText(Path.Combine(state, "preferences.json"));
            if (saved.Contains("fixture-password")) throw new Exception("password persisted");
            Console.WriteLine("PASS opt-out, manual install, preserved update gate, automatic EXE/PVF replacement and idempotent retry: Go author PKCS1 -> own PSS -> pinned TLS -> gzip -> paired hashes -> isolated account API; no game launched");
        }
        finally { File.WriteAllText(receipt + ".done", "done"); }
    }
}
