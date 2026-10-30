#if ANTICHEAT
using System.Diagnostics;
using System.Security.Cryptography;
using System.Text.Json;
using Dnf90.Launcher.AntiCheat;
using ServerS5A3;

// Interactive test runner only. No fixture/profile loader is included in the shipping Host.
internal static class AntiCheatUacChecks
{
    internal static PlayerProfile Profile() => JsonSerializer.Deserialize(
        File.ReadAllText(Path.Combine(AppContext.BaseDirectory, "uac-fixture.json")), CoreJson.Default.PlayerProfile)!;

    internal static async Task Run(string directory, string output, string action)
    {
        if (action is not ("cancel" or "approve")) throw new ArgumentException("Expected cancel or approve");
        directory = Path.GetFullPath(directory); output = Path.GetFullPath(output);
        Directory.CreateDirectory(output);
        var profile = Profile();
        foreach (var (name, expected) in new[] { ("DNF.exe", profile.ExeSha256), ("Script.pvf", profile.PvfSha256) })
            if (!Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(Path.Combine(directory,name)))).Equals(expected,StringComparison.OrdinalIgnoreCase))
                throw new InvalidDataException("Fixture hash mismatch");
        using var self = Process.GetCurrentProcess();
        if (GuardNative.Elevated(self)) throw new Exception("Test parent must be unelevated");
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(120));
        AntiCheatSession? session = null;
        try {
            Console.WriteLine("UAC " + action + ": isolated fixture only");
            try { session = await AntiCheatSession.Start(directory, null, output, message => File.AppendAllText(Path.Combine(output,"failure.log"),message + "\n"), timeout.Token); }
            catch (InvalidOperationException error) when (action == "cancel" && error.Message.StartsWith("AC02",StringComparison.Ordinal)) {
                File.WriteAllText(Path.Combine(output,"cancel.json"),JsonSerializer.Serialize(new {passed=true, code="AC02", gameCreated=false}));
                Console.WriteLine("PASS UAC cancellation prevents game launch"); return;
            }
            if (action == "cancel") throw new Exception("Expected UAC rejection");
            using var game = await session.Launch(Path.Combine(directory,"DNF.exe"),"");
            await Task.Delay(1500,timeout.Token);
            if (game.HasExited || GuardNative.Elevated(game)) throw new Exception("Fixture did not run unelevated");
            var locked = false;
            try { using var write = File.OpenWrite(Path.Combine(directory,"Script.pvf")); } catch (IOException) { locked = true; }
            if (!locked) throw new Exception("Guard did not hold PVF lease");
            File.WriteAllText(Path.Combine(output,"ready.json"),JsonSerializer.Serialize(new {parentPid=self.Id,gamePid=game.Id,parentElevated=false,gameElevated=false,pvfLocked=true,ipcReady=true}));
            Console.WriteLine("PASS elevated IPC, unelevated fixture, protected file lease; waiting for parent crash check");
            await Task.Delay(45000,timeout.Token);
            throw new TimeoutException("Driver did not terminate test parent");
        } finally { session?.Dispose(); }
    }
}
#endif
