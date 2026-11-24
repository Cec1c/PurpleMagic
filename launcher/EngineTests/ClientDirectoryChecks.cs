using System.ComponentModel;
using System.Diagnostics;
using System.Security.Cryptography;
using System.Text.Json;
using Dnf90.Launcher;
using ServerS5A3;

internal static class ClientDirectoryChecks
{
    internal static void SeedClientStructure(string directory, bool withFiles = true, bool useDllMarker = false)
    {
        Directory.CreateDirectory(directory);
        Directory.CreateDirectory(Path.Combine(directory, "ImagePacks2"));
        Directory.CreateDirectory(Path.Combine(directory, "SoundPacks"));
        if (useDllMarker)
        {
            File.WriteAllBytes(Path.Combine(directory, "fmodex.dll"), "MZ fmodex"u8.ToArray());
            File.WriteAllBytes(Path.Combine(directory, "ijl15.dll"), "MZ ijl15"u8.ToArray());
        }
        else
        {
            Directory.CreateDirectory(Path.Combine(directory, "Music"));
        }
        if (withFiles)
        {
            File.WriteAllBytes(Path.Combine(directory, "DNF.exe"), "MZ isolated old client"u8.ToArray());
            File.WriteAllBytes(Path.Combine(directory, "Script.pvf"), "isolated old pvf"u8.ToArray());
        }
    }

    internal static async Task Run(string temp, PlayerProfile profile, Action<bool, string> check)
    {
        var client = Path.Combine(temp, "客户端 root");
        SeedClientStructure(client);
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
        SeedClientStructure(install);
        var cache = Path.Combine(temp, "portable-cache", "app");
        Directory.CreateDirectory(cache);
        check(ClientDirectoryDiscovery.Find("", client, Path.Combine(install, "app"), null, temp) == install, "installed app finds the adjacent client before a configured default");
        check(ClientDirectoryDiscovery.Find(client, "", cache, install, temp) == client, "valid manually selected client is preserved");
        check(ClientDirectoryDiscovery.Find(Path.Combine(temp, "removed-client"), "", cache, install, temp) == install, "stale remembered path falls back to the current entry");
        check(ClientDirectoryDiscovery.Find("", "", cache, null, client) == client, "explicit working directory can identify the client");
        var nested = Path.Combine(temp, "portable-root", "client");
        SeedClientStructure(nested);
        check(ClientDirectoryDiscovery.Find("", "", cache, Path.GetDirectoryName(nested), temp) == nested, "portable entry also discovers its client subdirectory");
        var hidden = Path.Combine(temp, "hidden-root", ".client");
        SeedClientStructure(hidden);
        check(ClientDirectoryDiscovery.Find("", "", cache, Path.GetDirectoryName(hidden), temp) == hidden, "portable root discovers .client");
        check(ClientDirectoryDiscovery.Find("", "", cache, null, Path.GetDirectoryName(hidden)!) == hidden, "working directory discovers .client");
        check(ClientDirectoryDiscovery.Find(client, "", cache, Path.GetDirectoryName(hidden), temp) == client, "explicit valid choice wins over discovered .client");
        var fallback = Path.Combine(Path.GetDirectoryName(hidden)!, "client");
        SeedClientStructure(fallback);
        File.WriteAllBytes(Path.Combine(hidden, "Script.pvf"), []);
        check(ClientDirectoryDiscovery.Find("", "", cache, Path.GetDirectoryName(hidden), temp) == fallback, "invalid .client falls through to valid client");
        var empty = Path.Combine(temp, "incomplete-client");
        SeedClientStructure(empty, withFiles: false);
        File.WriteAllBytes(Path.Combine(empty, "DNF.exe"), []);
        check(ClientDirectoryDiscovery.Find("", "", cache, empty, temp) == "", "incomplete and empty client files are not selected");
        File.WriteAllBytes(Path.Combine(empty, "Script.pvf"), [1]);
        check(ClientDirectoryDiscovery.Find("", "", cache, empty, temp) == "", "a zero-byte EXE cannot become an automatic target");
        check(ClientDirectoryDiscovery.Find("", client, cache, "bad\0path", temp) == client, "invalid discovery hints do not block a valid fallback");
        try { await GameContract.VerifyClient(client, profile); throw new Exception("version check bypassed by discovery"); }
        catch (InvalidDataException) { check(true, "discovery does not bypass the EXE and PVF version gate"); }

        // 1.1.0 structural validation, polluted-folder rejection, bounded 1-level probe, and launch error checks.
        var dllOnlyClient = Path.Combine(temp, "dll-marker-client");
        SeedClientStructure(dllOnlyClient, withFiles: false, useDllMarker: true);
        check(ClientDirectoryDiscovery.IsValidStructure(dllOnlyClient), "real client with ImagePacks2, SoundPacks and core runtime DLLs passes even before EXE repair");

        var pollutedWrapper = Path.Combine(temp, "新客户端-polluted");
        Directory.CreateDirectory(Path.Combine(pollutedWrapper, "ImagePacks2"));
        Directory.CreateDirectory(Path.Combine(pollutedWrapper, "SoundPacks"));
        File.WriteAllBytes(Path.Combine(pollutedWrapper, "DNF.exe"), "MZ downloaded into wrong dir"u8.ToArray());
        File.WriteAllBytes(Path.Combine(pollutedWrapper, "Script.pvf"), "pvf downloaded into wrong dir"u8.ToArray());
        check(!ClientDirectoryDiscovery.IsValidStructure(pollutedWrapper), "wrong folder containing only downloaded DNF.exe/Script.pvf/ImagePacks2/SoundPacks is rejected");
        var realChild = Path.Combine(pollutedWrapper, "地下城与勇士");
        SeedClientStructure(realChild);
        var autoResolved = ClientDirectoryDiscovery.Resolve(pollutedWrapper);
        check(autoResolved.Kind == ClientDirectoryResolutionKind.AutoResolvedSubdirectory && autoResolved.Path == realChild,
            "polluted outer wrapper auto-resolves to its unique valid 1-level client subdirectory");
        check(ClientDirectoryDiscovery.Find(pollutedWrapper, "", cache, null, temp) == realChild,
            "startup discovery repairs previously saved wrapper folder to its real client child");

        var deepNested = Path.Combine(temp, "two-level-root", "level1", "level2");
        SeedClientStructure(deepNested);
        check(ClientDirectoryDiscovery.Resolve(Path.Combine(temp, "two-level-root")).Kind == ClientDirectoryResolutionKind.NotFoundOrInvalid,
            "auto-probe only scans 1 direct subdirectory level and never recurses deeper");

        var ambiguous = Path.Combine(temp, "ambiguous-root");
        SeedClientStructure(Path.Combine(ambiguous, "client-a"));
        SeedClientStructure(Path.Combine(ambiguous, "client-b"));
        var ambiguousRes = ClientDirectoryDiscovery.Resolve(ambiguous);
        check(ambiguousRes.Kind == ClientDirectoryResolutionKind.AmbiguousSubdirectories && ambiguousRes.MatchedSubdirectories == 2,
            "multiple matching 1-level client folders are rejected as ambiguous");

        var capped = Path.Combine(temp, "capped-root");
        Directory.CreateDirectory(capped);
        for (var i = 0; i < 12; i++) Directory.CreateDirectory(Path.Combine(capped, $"dir-{i:D2}"));
        var cappedRes = ClientDirectoryDiscovery.Resolve(capped, maxSubdirectoryScan: 5);
        check(cappedRes.Kind == ClientDirectoryResolutionKind.NotFoundOrInvalid && cappedRes.ScanTruncated && cappedRes.ScannedSubdirectories == 5,
            "1-level subdirectory scan strictly honors the numeric upper bound");
        check(ClientDirectoryDiscovery.DescribeInvalid(cappedRes).Contains("5 个子目录"),
            "truncated scan error explains the bounded scan limit in Chinese");

        var cappedWithClients = Path.Combine(temp, "capped-with-clients");
        Directory.CreateDirectory(cappedWithClients);
        SeedClientStructure(Path.Combine(cappedWithClients, "00-client-a"));
        for (var i = 1; i <= 65; i++) Directory.CreateDirectory(Path.Combine(cappedWithClients, $"{i:D2}-other"));
        SeedClientStructure(Path.Combine(cappedWithClients, "99-client-b"));
        var cappedHit = ClientDirectoryDiscovery.Resolve(cappedWithClients);
        check(cappedHit.Kind == ClientDirectoryResolutionKind.NotFoundOrInvalid && cappedHit.ScanTruncated && cappedHit.ScannedSubdirectories == ClientDirectoryDiscovery.MaxSubdirectoryScan && !cappedHit.IsValid,
            "truncated scan never auto-selects a single matched candidate when unscanned siblings remain");
        if (cappedHit.MatchedSubdirectories > 0)
            check(ClientDirectoryDiscovery.DescribeInvalid(cappedHit).Contains("无法确认目标唯一"),
                "truncated scan with matched candidate explains non-uniqueness in Chinese");

        // Verify engine blocks update/login on invalid/ambiguous directories without writing dirty files,
        // and auto-fixes unique 1-level wrapper directories on SavePreferences.
        using var signer = RSA.Create(2048);
        var signedProfile = profile with { UpdatePublicKey = signer.ExportSubjectPublicKeyInfoPem(), DefaultClientDirectory = "" };
        var content = new ContentRelease("fixture", "author", "stable", 1, new('d', 64), signedProfile.ApiUrl, signedProfile.CertificateSha256,
            [new("DNF.exe", 10, signedProfile.ExeSha256, true, "none", "", 0), new("Script.pvf", 20, signedProfile.PvfSha256, true, "none", "", 0)]);
        var payload = JsonSerializer.SerializeToUtf8Bytes(new Bootstrap("DNF90-Flutter", 1, 1, signedProfile, [], content, null), WireJson.Default.Bootstrap);
        var envelope = JsonSerializer.SerializeToUtf8Bytes(new SignedEnvelope(Convert.ToBase64String(payload),
            Convert.ToBase64String(signer.SignData(payload, HashAlgorithmName.SHA256, RSASignaturePadding.Pss))), CoreJson.Default.SignedEnvelope);

        var gateState = Path.Combine(temp, "dir-gate-state");
        Directory.CreateDirectory(gateState);
        File.WriteAllBytes(Path.Combine(gateState, "bootstrap.json"), envelope);
        var wrongDir = Path.Combine(temp, "empty-wrong-dir");
        Directory.CreateDirectory(wrongDir);
        using (var engine = new LauncherEngine(signedProfile, gateState))
        {
            await engine.SavePreferences(new(wrongDir, AutoCheck: false));
            check(!engine.State.CanLogin && !engine.State.HasUpdate && engine.State.Message.Contains("ImagePacks2"),
                "selecting an invalid directory disables login and shows Chinese structural guidance");
            try { await engine.UpdateContent(); throw new Exception("invalid dir allowed update"); }
            catch (InvalidOperationException error)
            {
                check(error.Message.Contains("ImagePacks2") && !Directory.Exists(Path.Combine(wrongDir, ".dnf90-update")) && !File.Exists(Path.Combine(wrongDir, "DNF.exe")),
                    "invalid directory blocks update before creating transaction or downloading DNF.exe");
            }
            try { await engine.Login(new("tester", "secret")); throw new Exception("invalid dir allowed login"); }
            catch (InvalidOperationException error)
            {
                check(error.Message.Contains("ImagePacks2"), "invalid directory blocks login with Chinese guidance");
            }

            await engine.SavePreferences(new(ambiguous, AutoCheck: false));
            check(!engine.State.CanLogin && engine.State.Message.Contains("多个"),
                "ambiguous outer directory disables login and explains multiple client subdirectories");

            await engine.SavePreferences(new(cappedWithClients, AutoCheck: false));
            check(engine.State.Preferences.ClientDirectory == cappedWithClients && !engine.State.CanLogin,
                "truncated outer directory with unscanned siblings does not auto-persist a child candidate");
            try { await engine.UpdateContent(); throw new Exception("truncated dir allowed update"); }
            catch (InvalidOperationException)
            {
                check(!Directory.Exists(Path.Combine(cappedWithClients, ".dnf90-update")) && !Directory.Exists(Path.Combine(cappedWithClients, "00-client-a", ".dnf90-update")),
                    "truncated outer directory blocks update transaction on both outer dir and matched child");
            }

            await engine.SavePreferences(new(pollutedWrapper, AutoCheck: false));
            check(engine.State.Preferences.ClientDirectory == realChild && engine.State.CanLogin && engine.State.Message.Contains("已自动定位"),
                "SavePreferences auto-resolves wrapper folder to its unique valid child subdirectory");
        }

        // Verify missing DNF.exe in VerifyClient returns friendly Chinese error instead of raw IO message.
        var missingExeDir = Path.Combine(temp, "missing-exe-dir");
        SeedClientStructure(missingExeDir, withFiles: false);
        try { await GameContract.VerifyClient(missingExeDir, profile); throw new Exception("missing exe accepted"); }
        catch (InvalidDataException error)
        {
            check(error.Message.Contains("缺少核心文件 DNF.exe") && error.Message.Contains("更改"),
                "VerifyClient reports missing core file in Chinese with path guidance");
        }

        // Verify GetProcessById-backed exited Process never throws 'Process was not started by this object...'
        // and FormatUserError translates raw English .NET launch exceptions into Chinese guidance.
        if (OperatingSystem.IsWindows())
        {
            using var spawned = Process.Start(new ProcessStartInfo("cmd.exe", "/c set /p _= & exit 7")
            {
                UseShellExecute = false,
                CreateNoWindow = true,
                RedirectStandardInput = true,
                RedirectStandardOutput = true,
            })!;
            using var opened = Process.GetProcessById(spawned.Id);
            using var queryHandle = LauncherEngine.TryOpenProcessForExitCode(opened);
            spawned.StandardInput.Close();
            await spawned.WaitForExitAsync();
            await opened.WaitForExitAsync();
            var readOk = LauncherEngine.TryGetProcessExitCode(opened, out var exitCode, queryHandle);
            check(readOk && exitCode == 7 && LauncherEngine.HasProcessExited(opened),
                "TryGetProcessExitCode reads Win32 exit code from GetProcessById handle without throwing");
            // Also verify calling without a retained handle never throws InvalidOperationException.
            _ = LauncherEngine.TryGetProcessExitCode(opened, out _);
            check(true, "TryGetProcessExitCode without retained handle degrades cleanly without throwing");
        }
        var rawDotnetError = LauncherEngine.FormatUserError(new InvalidOperationException("Process was not started by this object, so requested information cannot be determined."));
        check(rawDotnetError.Contains("客户端路径") && !rawDotnetError.Contains("Process was not started"),
            "FormatUserError translates raw English Process InvalidOperationException to Chinese path guidance");
        check(LauncherEngine.FormatUserError(new Win32Exception(2)).Contains("客户端路径"),
            "FormatUserError translates Win32Exception launch failure to Chinese path guidance");
        check(LauncherEngine.FormatUserError(new FileNotFoundException("DNF.exe")).Contains("客户端路径"),
            "FormatUserError translates FileNotFoundException to Chinese path guidance");
        check(LauncherEngine.FormatUserError(new InvalidOperationException("AC02：已取消管理员授权")) == "AC02：已取消管理员授权",
            "FormatUserError preserves intentional Chinese messages verbatim");
        check(LauncherEngine.FormatUserError(new AccountApiException(new(false, null, null, "invalid_credentials") { Code = "rate_limited" })) == "invalid_credentials",
            "FormatUserError preserves AccountApiException messages verbatim");
    }
}
