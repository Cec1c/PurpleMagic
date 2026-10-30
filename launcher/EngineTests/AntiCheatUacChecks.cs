#if ANTICHEAT
using System.Diagnostics;
using System.Security.Cryptography;
using System.Security.AccessControl;
using System.Security.Principal;
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
        if (!OperatingSystem.IsWindows()) throw new PlatformNotSupportedException();
        if (action is not ("cancel" or "approve" or "elevated")) throw new ArgumentException("Expected cancel, approve or elevated");
        directory = Path.GetFullPath(directory); output = Path.GetFullPath(output);
        Directory.CreateDirectory(output);
        var profile = Profile();
        foreach (var (name, expected) in new[] { ("DNF.exe", profile.ExeSha256), ("Script.pvf", profile.PvfSha256) })
            if (!Convert.ToHexString(SHA256.HashData(File.ReadAllBytes(Path.Combine(directory,name)))).Equals(expected,StringComparison.OrdinalIgnoreCase))
                throw new InvalidDataException("Fixture hash mismatch");
        using var self = Process.GetCurrentProcess();
        var elevated = GuardNative.Elevated(self);
        if (elevated != (action == "elevated")) throw new Exception("Test parent elevation does not match scenario");
        var state = output;
        if (elevated) {
            // Reproduce state storage writable only by administrators, without touching AppData.
            state = Path.Combine(output, "admin-state");
            var directoryInfo = Directory.CreateDirectory(state);
            var acl = new DirectorySecurity();
            acl.SetAccessRuleProtection(true, false);
            foreach (var sid in new[] { WellKnownSidType.BuiltinAdministratorsSid, WellKnownSidType.LocalSystemSid })
                acl.AddAccessRule(new FileSystemAccessRule(new SecurityIdentifier(sid, null), FileSystemRights.FullControl,
                    InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit, PropagationFlags.None, AccessControlType.Allow));
            directoryInfo.SetAccessControl(acl);
        }
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(120));
        AntiCheatSession? session = null;
        try {
            Console.WriteLine("UAC " + action + ": isolated fixture only");
            try { session = await AntiCheatSession.Start(directory, null, state, message => File.AppendAllText(Path.Combine(output,"failure.log"),message + "\n"), timeout.Token); }
            catch (InvalidOperationException error) when (action == "cancel" && error.Message.StartsWith("AC02",StringComparison.Ordinal)) {
                File.WriteAllText(Path.Combine(output,"cancel.json"),JsonSerializer.Serialize(new {passed=true, code="AC02", gameCreated=false}));
                Console.WriteLine("PASS UAC cancellation prevents game launch"); return;
            }
            if (action == "cancel") throw new Exception("Expected UAC rejection");
            using var game = await session.Launch(Path.Combine(directory,"DNF.exe"),"");
            await Task.Delay(1500,timeout.Token);
            if (game.HasExited || GuardNative.Elevated(game) != elevated) throw new Exception("Fixture did not inherit launcher elevation");
            var locked = false;
            try { using var write = File.OpenWrite(Path.Combine(directory,"Script.pvf")); } catch (IOException) { locked = true; }
            if (!locked) throw new Exception("Guard did not hold PVF lease");
            var logs = Directory.GetFiles(Path.Combine(state, "anticheat"), "*.log");
            if (logs.Length != 1) throw new Exception("Expected one guard log");
            using var logReader = new StreamReader(new FileStream(logs[0], FileMode.Open, FileAccess.Read, FileShare.ReadWrite));
            if (!logReader.ReadToEnd().Contains("attached", StringComparison.Ordinal)) throw new Exception("Guard attachment was not persisted");
            File.WriteAllText(Path.Combine(output,"ready.json"),JsonSerializer.Serialize(new {parentPid=self.Id,gamePid=game.Id,parentElevated=elevated,gameElevated=elevated,pvfLocked=true,ipcReady=true,protectionLogWritten=true,adminOnlyState=elevated}));
            Console.WriteLine("PASS elevated IPC, inherited fixture privileges, protected file lease and state writes; waiting for parent crash check");
            await Task.Delay(45000,timeout.Token);
            throw new TimeoutException("Driver did not terminate test parent");
        } finally { session?.Dispose(); }
    }
}
#endif
