using System.Text.Json;
using Dnf90.Launcher;
using ServerS5A3;

internal static class ClientDirectoryChecks
{
    internal static async Task Run(string temp, PlayerProfile profile, Action<bool, string> check)
    {
        var client = Path.Combine(temp, "客户端 root");
        Directory.CreateDirectory(client);
        File.WriteAllBytes(Path.Combine(client, "DNF.exe"), "MZ isolated old client"u8.ToArray());
        File.WriteAllBytes(Path.Combine(client, "Script.pvf"), "isolated old pvf"u8.ToArray());
        var previous = Environment.GetEnvironmentVariable("DNF90_LAUNCH_DIRECTORY");
        try
        {
            Environment.SetEnvironmentVariable("DNF90_LAUNCH_DIRECTORY", client);
            var state = Path.Combine(temp, "discovered-client-state");
            using (var engine = new LauncherEngine(profile, state))
            {
                check(engine.State.Preferences.ClientDirectory == client, "portable entry directory is discovered outside the app cache");
                var saved = JsonSerializer.Deserialize(File.ReadAllBytes(Path.Combine(state, "preferences.json")), WireJson.Default.Preferences)!;
                check(saved.ClientDirectory == client, "discovered directory is persisted before update or login");
                await engine.SavePreferences(saved with { AutoCheck = false, Account = "remembered" });
            }
            Environment.SetEnvironmentVariable("DNF90_LAUNCH_DIRECTORY", null);
            using (var engine = new LauncherEngine(profile, state))
                check(engine.State.Preferences.ClientDirectory == client && !engine.State.Preferences.AutoCheck && engine.State.Preferences.Account == "remembered", "discovered directory and user preferences survive restart without the original entry");
        }
        finally { Environment.SetEnvironmentVariable("DNF90_LAUNCH_DIRECTORY", previous); }

        var install = Path.Combine(temp, "installed-client");
        Directory.CreateDirectory(Path.Combine(install, "app"));
        File.Copy(Path.Combine(client, "DNF.exe"), Path.Combine(install, "DNF.exe"));
        File.Copy(Path.Combine(client, "Script.pvf"), Path.Combine(install, "Script.pvf"));
        var cache = Path.Combine(temp, "portable-cache", "app");
        Directory.CreateDirectory(cache);
        check(ClientDirectoryDiscovery.Find("", client, Path.Combine(install, "app"), null, temp) == install, "installed app finds the adjacent client before a configured default");
        check(ClientDirectoryDiscovery.Find(client, "", cache, install, temp) == client, "valid manually selected client is preserved");
        check(ClientDirectoryDiscovery.Find(Path.Combine(temp, "removed-client"), "", cache, install, temp) == install, "stale remembered path falls back to the current entry");
        check(ClientDirectoryDiscovery.Find("", "", cache, null, client) == client, "explicit working directory can identify the client");
        var nested = Path.Combine(temp, "portable-root", "client");
        Directory.CreateDirectory(nested);
        File.Copy(Path.Combine(client, "DNF.exe"), Path.Combine(nested, "DNF.exe"));
        File.Copy(Path.Combine(client, "Script.pvf"), Path.Combine(nested, "Script.pvf"));
        check(ClientDirectoryDiscovery.Find("", "", cache, Path.GetDirectoryName(nested), temp) == nested, "portable entry also discovers its client subdirectory");
        var hidden = Path.Combine(temp, "hidden-root", ".client"); Directory.CreateDirectory(hidden);
        File.Copy(Path.Combine(client, "DNF.exe"), Path.Combine(hidden, "DNF.exe"));
        File.Copy(Path.Combine(client, "Script.pvf"), Path.Combine(hidden, "Script.pvf"));
        check(ClientDirectoryDiscovery.Find("", "", cache, Path.GetDirectoryName(hidden), temp) == hidden, "portable root discovers .client");
        check(ClientDirectoryDiscovery.Find("", "", cache, null, Path.GetDirectoryName(hidden)!) == hidden, "working directory discovers .client");
        check(ClientDirectoryDiscovery.Find(client, "", cache, Path.GetDirectoryName(hidden), temp) == client, "explicit valid choice wins over discovered .client");
        var fallback = Path.Combine(Path.GetDirectoryName(hidden)!, "client"); Directory.CreateDirectory(fallback);
        File.Copy(Path.Combine(client, "DNF.exe"), Path.Combine(fallback, "DNF.exe"));
        File.Copy(Path.Combine(client, "Script.pvf"), Path.Combine(fallback, "Script.pvf"));
        File.WriteAllBytes(Path.Combine(hidden, "Script.pvf"), []);
        check(ClientDirectoryDiscovery.Find("", "", cache, Path.GetDirectoryName(hidden), temp) == fallback, "invalid .client falls through to valid client");
        var empty = Path.Combine(temp, "incomplete-client");
        Directory.CreateDirectory(empty);
        File.WriteAllBytes(Path.Combine(empty, "DNF.exe"), []);
        check(ClientDirectoryDiscovery.Find("", "", cache, empty, temp) == "", "incomplete and empty client files are not selected");
        File.WriteAllBytes(Path.Combine(empty, "Script.pvf"), [1]);
        check(ClientDirectoryDiscovery.Find("", "", cache, empty, temp) == "", "a zero-byte EXE cannot become an automatic target");
        check(ClientDirectoryDiscovery.Find("", client, cache, "bad\0path", temp) == client, "invalid discovery hints do not block a valid fallback");
        try { await GameContract.VerifyClient(client, profile); throw new Exception("version check bypassed by discovery"); }
        catch (InvalidDataException) { check(true, "discovery does not bypass the EXE and PVF version gate"); }
    }
}
