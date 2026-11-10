using System.Diagnostics;
using System.IO.Compression;
using System.Net;
using System.Net.Sockets;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Dnf90.Launcher.Diagnostics;
using ServerS5A3;

internal static class ContractDiagnosticsChecks
{
    private static readonly HashSet<string> ForbiddenKeys = new(StringComparer.OrdinalIgnoreCase)
    {
        "password",
        "token",
        "login_token",
        "authorization",
        "arguments",
        "command_line",
        "chat",
        "account",
        "install_path",
        "login_request_body",
        "sensitive_command_line"
    };

    private static readonly string[] RequiredCommonFields =
    [
        "schema_version",
        "event",
        "source",
        "client_session_id",
        "event_seq",
        "observed_utc",
        "mono_ticks",
        "mono_frequency_hz",
        "pid",
        "process_created_utc"
    ];

    public static async Task SocketFixtureChild()
    {
        using var server = new TcpListener(IPAddress.Loopback, 0);
        server.Start();
        var serverPort = ((IPEndPoint)server.LocalEndpoint).Port;
        using var client = new TcpClient();
        var acceptTask = server.AcceptTcpClientAsync();
        await client.ConnectAsync(IPAddress.Loopback, serverPort);
        using var accepted = await acceptTask;
        var clientLocalPort = ((IPEndPoint)client.Client.LocalEndPoint!).Port;
        using var udp = new UdpClient(new IPEndPoint(IPAddress.Loopback, 0));
        var udpPort = ((IPEndPoint)udp.Client.LocalEndPoint!).Port;
        Console.WriteLine($"{clientLocalPort}:{serverPort}:{udpPort}");
        Console.Out.Flush();
        _ = await Console.In.ReadLineAsync();
    }

    private static void CheckNoForbiddenKeys(JsonElement element, Action<bool, string> check, string scope)
    {
        if (element.ValueKind == JsonValueKind.Object)
        {
            foreach (var prop in element.EnumerateObject())
            {
                if (ForbiddenKeys.Contains(prop.Name))
                {
                    check(false, $"forbidden secret key {prop.Name} in {scope}");
                }
                CheckNoForbiddenKeys(prop.Value, check, scope);
            }
        }
        else if (element.ValueKind == JsonValueKind.Array)
        {
            foreach (var item in element.EnumerateArray())
            {
                CheckNoForbiddenKeys(item, check, scope);
            }
        }
    }

    public static async Task Run(string root, Action<bool, string> check)
    {
        var work = Path.Combine(root, "contract-v1-checks");
        Directory.CreateDirectory(work);

        // 1. Real live Win32 iphlpapi TCP/UDP child process attribution + PID-reuse rejection
        if (OperatingSystem.IsWindows() && Environment.ProcessPath is { Length: > 0 } selfExe)
        {
            var psi = new ProcessStartInfo(selfExe)
            {
                UseShellExecute = false,
                RedirectStandardInput = true,
                RedirectStandardOutput = true,
                CreateNoWindow = true
            };
            psi.ArgumentList.Add("--socket-fixture-child");
            using var child = Process.Start(psi)!;
            try
            {
                var line = await child.StandardOutput.ReadLineAsync() ?? throw new InvalidOperationException("socket child failed");
                var parts = line.Split(':');
                var tcpClientPort = int.Parse(parts[0]);
                var tcpServerPort = int.Parse(parts[1]);
                var udpLocalPort = int.Parse(parts[2]);

                long createdFileTime;
                using (var probe = new ReadOnlyGameProcess(child.Id, selfExe, child.StartTime.ToUniversalTime().ToFileTimeUtc()))
                {
                    createdFileTime = probe.Identity.Created;
                }

                var realTracker = new GameSocketTracker(child.Id, createdFileTime);
                var liveEvents = realTracker.Poll();
                check(liveEvents.Any(e => e.IsOpen && e.Protocol == "tcp" && e.LocalPort == tcpClientPort && e.RemotePort == tcpServerPort && e.TcpState == "ESTABLISHED"),
                    "live Win32 GetExtendedTcpTable attributes child TCP connection");
                check(liveEvents.Any(e => e.IsOpen && e.Protocol == "udp" && e.LocalPort == udpLocalPort && e.BindingState == "bound_local_only"),
                    "live Win32 GetExtendedUdpTable attributes child UDP local bind");

                // PID-reuse safety: wrong ExpectedCreatedFileTimeUtc must reject attribution completely
                var wrongIdentityTracker = new GameSocketTracker(child.Id, createdFileTime + 50_000_000L);
                var mismatchedEvents = wrongIdentityTracker.Poll();
                check(mismatchedEvents.Length == 0, "PID-reuse start-time mismatch suppresses false socket attribution");
            }
            finally
            {
                try { await child.StandardInput.WriteLineAsync("exit"); await child.WaitForExitAsync(); }
                catch { try { child.Kill(); } catch { } }
            }
        }

        // 2. Dual-box Anton first-entry invisibility + reentry recovery + computed vs declared digests + path sanitization
        var userHome = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        var clientDir = !string.IsNullOrWhiteSpace(userHome) && Directory.Exists(userHome)
            ? Path.Combine(userHome, ".pm-contract-test-" + Guid.NewGuid().ToString("N")[..8])
            : Path.Combine(work, "client-dir");
        Directory.CreateDirectory(clientDir);
        try
        {
            var exeBytes = Encoding.UTF8.GetBytes("MZ-actual-disk-dnf-binary-fixture");
            var pvfBytes = Encoding.UTF8.GetBytes("PVF-actual-disk-script-fixture-payload");
            var actualExeSha = Convert.ToHexString(SHA256.HashData(exeBytes)).ToLowerInvariant();
            var actualPvfSha = Convert.ToHexString(SHA256.HashData(pvfBytes)).ToLowerInvariant();
            var declaredExeSha = new string('1', 64);
            var declaredPvfSha = new string('2', 64);

            File.WriteAllBytes(Path.Combine(clientDir, "DNF.exe"), exeBytes);
            File.WriteAllBytes(Path.Combine(clientDir, "Script.pvf"), pvfBytes);

            var stateDir = Path.Combine(work, "state");
            var exportsDir = Path.Combine(work, "exports");
            Directory.CreateDirectory(exportsDir);

            var currentTcpA = new List<RawTcpEntry>
            {
                new("ipv4", "127.0.0.1", 52100, "198.51.100.10", 7001, 5)
            };
            var currentUdpA = new List<RawUdpEntry>
            {
                new("ipv4", "0.0.0.0", 56601)
            };

            var profile = new PlayerProfile(
                "https://purplemagic.example.test",
                new string('a', 64),
                "",
                "",
                "198.51.100.10",
                7001,
                declaredExeSha,
                declaredPvfSha,
                "")
            {
                ChannelServerId = 11,
                PreviousChannelServerId = 10
            };

            string csA, csB, sharedIncident;
            using (var ledger = new ContractDiagnosticsLedger(stateDir, (pid, _) =>
                pid == 41001 ? (true, [.. currentTcpA], [.. currentUdpA]) : (true, [], [])))
            {
                ledger.Configure(true, clientDir, profile, 198, "V141", "tianyi-primary");
                ledger.RecordHttpTrace(new HttpTraceSample(
                    "http-trace0001",
                    "account_login",
                    200,
                    18.4,
                    "2026-11-11T00:00:01.000Z",
                    Stopwatch.GetTimestamp(),
                    "2026-11-11T00:00:01.000Z"));

                var fakeCreatedA = DateTime.Parse("2026-11-11T00:00:02.000Z").ToUniversalTime().ToFileTimeUtc();
                var fakeCreatedB = DateTime.Parse("2026-11-11T00:00:05.000Z").ToUniversalTime().ToFileTimeUtc();
                var exePath = Path.Combine(clientDir, "DNF.exe");
                var enhancedIdA = Guid.NewGuid().ToString("N");

                csA = ledger.TrackVerifiedProcess(41001, fakeCreatedA, 1, exePath, "anticheat_guard_child", enhancedIdA);
                csB = ledger.TrackVerifiedProcess(41002, fakeCreatedB, 1, exePath, "anticheat_guard_child");

                sharedIncident = ledger.SetIncident(new ContractIncidentInput("inc-anton-team-sync-01", false));
                check(sharedIncident == "inc-anton-team-sync-01", "custom shared incident_id retained across multi-client reproduction");

                // Manual character association on client A; authenticated server context on client B
                ledger.AssociateCharacter(new ContractCharacterInput(csA, "test1", 3));
                var serverCtx = new DiagnosticContext(
                    "corr-1",
                    "current",
                    "",
                    "authenticated_game_session",
                    "2026-11-11T00:00:08.000Z",
                    "2026-11-11T00:00:08.050Z",
                    "sirocco-ch11",
                    1,
                    new Dictionary<string, ContextValue>
                    {
                        ["character_id"] = new("1", "current", ""),
                        ["character_name"] = new("312da", "current", ""),
                        ["character_generation"] = new("1", "current", ""),
                        ["connection_id"] = new("conn-77", "current", ""),
                        ["channel_id"] = new("11", "current", ""),
                        ["party_id"] = new("party-9", "current", ""),
                        ["party_generation"] = new("1", "current", ""),
                        ["dungeon_id"] = new("anton_black_smoke", "current", ""),
                        ["map_id"] = new("201", "current", ""),
                        ["room"] = new("1,0", "current", ""),
                        ["instance_id"] = new("inst-500", "current", ""),
                        ["settlement_phase"] = new(null, "unavailable", "not_in_settlement"),
                        ["boss_dead"] = new(null, "unavailable", "not_applicable"),
                        ["business_state"] = new("in_dungeon", "current", ""),
                        ["reason_code"] = new(null, "unavailable", "none")
                    },
                    new DiagnosticClock("server_event", 12.5, 4.0, 480, "estimated"));
                ledger.UpdateServerContext(41002, null, "character_changed", serverCtx);

                // First entry failure reported 15s after actual onset, then recovered after reentry
                var obs1 = ledger.ReportObservation(new ContractObservationInput(
                    csA,
                    "missing",
                    "single_peer",
                    "312da",
                    1,
                    OnsetOffsetSeconds: 15,
                    TimeUncertaintyMs: 5000,
                    Note: "first entry invisible"));
                check(obs1.SceneEpoch == 1 && obs1.EstimatedOnsetUtc is not null && obs1.TimeUncertaintyMs == 5000,
                    "delayed observation preserves estimated_onset_utc and uncertainty separately from click time");

                var obs2 = ledger.ReportObservation(new ContractObservationInput(
                    csA,
                    "recovered_after_reentry",
                    "single_peer",
                    "312da",
                    1,
                    OnsetOffsetSeconds: 0,
                    TimeUncertaintyMs: 1000));
                check(obs2.SceneEpoch == 2 && obs2.EntryId != obs1.EntryId,
                    "recovered_after_reentry chains a distinct reentry scene epoch");

                // Simulate TCP reconnect on client A
                currentTcpA.Clear();
                ledger.PollSocketsOnce(csA);
                currentTcpA.Add(new RawTcpEntry("ipv4", "127.0.0.1", 52109, "198.51.100.10", 7001, 5));
                ledger.PollSocketsOnce(csA);

                // Multi-threaded generation-time event_seq concurrency stress on client B
                await Parallel.ForEachAsync(Enumerable.Range(0, 24), async (idx, _) =>
                {
                    await Task.Yield();
                    ledger.AppendSyntheticRoutineForBudgetTest(csB, $"concurrent_{idx}");
                });

                ledger.CompleteSessionExit(csA, 0);
                ledger.CompleteSessionExit(csB, -1073741819);
            }

            // 3. Reload from persisted disk state and export Contract v1 .zip bundle
            var bundlePath = Path.Combine(exportsDir, "PurpleMagic-Anton-Incident.zip");
            using (var reloaded = new ContractDiagnosticsLedger(stateDir))
            {
                var ui = reloaded.SnapshotUiStatus();
                check(ui.ActiveIncidentId == "inc-anton-team-sync-01" && ui.Sessions.Length == 2 && ui.Observations.Length == 2,
                    "persisted contract sessions and observations survive launcher restart");

                reloaded.Configure(false, clientDir, profile, 198, "stable75", "src-20261111");
                var exported = await reloaded.ExportBundleAsync(
                    new ContractBundleExportInput(bundlePath, IncidentId: "inc-anton-team-sync-01"));
                check(File.Exists(exported.Path) && exported.Bytes > 0 && exported.Incidents == 2,
                    "atomic .zip diagnostic bundle export succeeds");
            }

            // 4. Inspect exported bundle structure, hashes, schemas, sequence monotonicity, and secret exclusion
            using var archive = ZipFile.OpenRead(bundlePath);
            var entryNames = archive.Entries.Select(e => e.FullName).ToHashSet(StringComparer.Ordinal);
            check(entryNames.Contains("manifest.json") && entryNames.Contains("integrity.json"),
                "bundle contains manifest.json and integrity.json");
            check(entryNames.Contains($"sessions/{csA}/launcher-events.jsonl") && entryNames.Contains($"sessions/{csA}/observations.jsonl"),
                "bundle contains per-session launcher-events.jsonl and observations.jsonl");

            byte[] ReadZipBytes(string name)
            {
                using var s = archive.GetEntry(name)!.Open();
                using var ms = new MemoryStream();
                s.CopyTo(ms);
                return ms.ToArray();
            }

            using var manifestDoc = JsonDocument.Parse(ReadZipBytes("manifest.json"));
            var manifestRoot = manifestDoc.RootElement;
            CheckNoForbiddenKeys(manifestRoot, check, "manifest.json");
            check(manifestRoot.GetProperty("contract_name").GetString() == "purplemagic-anton-first-entry-diagnostics",
                "manifest declares contract_name purplemagic-anton-first-entry-diagnostics");
            check(manifestRoot.GetProperty("client_file_sha256").GetString() == actualExeSha &&
                  manifestRoot.GetProperty("pvf_file_sha256").GetString() == actualPvfSha,
                "manifest reports actual disk SHA-256 for DNF.exe and Script.pvf");
            check(manifestRoot.GetProperty("declared_manifest_digests").GetProperty("manifest_client_sha256").GetString() == declaredExeSha &&
                  manifestRoot.GetProperty("declared_manifest_digests").GetProperty("manifest_pvf_sha256").GetString() == declaredPvfSha,
                "manifest separates actual file SHA-256 from declared manifest digests");

            var caps = manifestRoot.GetProperty("capabilities");
            check(caps.GetProperty("launcher_sessions").GetBoolean() &&
                  caps.GetProperty("manual_observations").GetBoolean() &&
                  caps.GetProperty("game_socket_ownership").GetBoolean() &&
                  !caps.GetProperty("native_actor_events").GetBoolean() &&
                  !caps.GetProperty("client_side_packet_capture").GetBoolean(),
                "manifest truthfully declares P0=true and P1/P2=false capabilities");

            foreach (var f in manifestRoot.GetProperty("files").EnumerateArray())
            {
                var relPath = f.GetProperty("path").GetString()!;
                var raw = ReadZipBytes(relPath);
                var sha = Convert.ToHexString(SHA256.HashData(raw)).ToLowerInvariant();
                check(raw.Length == f.GetProperty("bytes").GetInt64() && sha == f.GetProperty("sha256").GetString(),
                    "manifest file SHA-256 and byte count match " + relPath);
            }

            foreach (var sid in new[] { csA, csB })
            {
                var lines = Encoding.UTF8.GetString(ReadZipBytes($"sessions/{sid}/launcher-events.jsonl"))
                    .Split('\n', StringSplitOptions.RemoveEmptyEntries);
                long prevSeq = 0;
                var events = new List<JsonElement>();
                foreach (var l in lines)
                {
                    using var doc = JsonDocument.Parse(l);
                    var el = doc.RootElement.Clone();
                    CheckNoForbiddenKeys(el, check, sid);
                    foreach (var req in RequiredCommonFields)
                    {
                        if (!el.TryGetProperty(req, out _)) check(false, $"missing common field {req}");
                    }
                    var seq = el.GetProperty("event_seq").GetInt64();
                    if (seq <= prevSeq) check(false, $"non-monotonic event_seq {seq} <= {prevSeq}");
                    prevSeq = seq;
                    events.Add(el);
                }
                check(prevSeq == lines.Length, $"generation-time event_seq is strictly 1..{lines.Length} for {sid}");

                if (sid == csA)
                {
                    var procStart = events.Single(e => e.GetProperty("event").GetString() == "process.started");
                    var sanitizedExe = procStart.GetProperty("executable_path").GetString()!;
                    if (!string.IsNullOrWhiteSpace(userHome) && clientDir.StartsWith(userHome, StringComparison.OrdinalIgnoreCase))
                    {
                        check(sanitizedExe.StartsWith("%USERPROFILE%", StringComparison.Ordinal),
                            "executable_path sanitizes Windows user profile prefix");
                    }
                    var sceneEntries = events.Where(e => e.GetProperty("event").GetString() == "scene.entry").ToArray();
                    check(sceneEntries.Length == 2 &&
                          sceneEntries[1].GetProperty("parent_entry_id").GetString() == sceneEntries[0].GetProperty("entry_id").GetString(),
                        "reentry scene.entry links parent_entry_id to failed first entry_id");

                    var tcpOpens = events.Where(e => e.GetProperty("event").GetString() == "connection.open" && e.GetProperty("protocol").GetString() == "tcp").ToArray();
                    check(tcpOpens.Length == 2 && tcpOpens[1].GetProperty("reconnect_to_remote").GetBoolean() &&
                          tcpOpens[1].GetProperty("previous_stream_id").GetString() == tcpOpens[0].GetProperty("stream_id").GetString(),
                        "TCP reconnect records previous_stream_id and reconnect_to_remote=true");

                    var udpOpen = events.Single(e => e.GetProperty("event").GetString() == "connection.open" && e.GetProperty("protocol").GetString() == "udp");
                    check(udpOpen.GetProperty("binding_state").GetString() == "bound_local_only" &&
                          udpOpen.GetProperty("peer_connected").ValueKind == JsonValueKind.Null &&
                          udpOpen.GetProperty("packets_in").ValueKind == JsonValueKind.Null,
                        "UDP binding truthfully reports bound_local_only with null packet counts and peer_connected");
                }
                else
                {
                    var exited = events.Single(e => e.GetProperty("event").GetString() == "process.exited");
                    check(exited.GetProperty("exit_code_hex").GetString() == "0xC0000005" &&
                          exited.GetProperty("exit_classification").GetString() == "ntstatus_crash_code",
                        "process.exited classifies 0xC0000005 crash exit code");
                }
            }
            archive.Dispose();

            // 5. Cross-validate real C#-exported Contract v1 bundle against scripts/analyze_diagnostics.py + Draft 2020-12 JSON Schema
            var analyzerScript = Path.GetFullPath(Path.Combine(AppContext.BaseDirectory, "..", "..", "..", "..", "..", "scripts", "analyze_diagnostics.py"));
            if (File.Exists(analyzerScript))
            {
                var py = new ProcessStartInfo("python")
                {
                    UseShellExecute = false,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true,
                    CreateNoWindow = true
                };
                py.ArgumentList.Add(analyzerScript);
                py.ArgumentList.Add(bundlePath);
                using var pyProc = Process.Start(py)!;
                var stdout = await pyProc.StandardOutput.ReadToEndAsync();
                var stderr = await pyProc.StandardError.ReadToEndAsync();
                await pyProc.WaitForExitAsync();
                check(pyProc.ExitCode == 0, $"analyze_diagnostics.py validates real C# Contract v1 bundle ({stderr.Trim()})");
                if (pyProc.ExitCode == 0)
                {
                    using var analyzedDoc = JsonDocument.Parse(stdout);
                    check(analyzedDoc.RootElement.GetProperty("verified").GetBoolean() &&
                          analyzedDoc.RootElement.GetProperty("contract_name").GetString() == "purplemagic-anton-first-entry-diagnostics" &&
                          analyzedDoc.RootElement.GetProperty("sessions").GetArrayLength() == 2,
                        "analyze_diagnostics.py confirms verified=true and 2 correlated sessions");
                }
            }
        }
        finally
        {
            if (Directory.Exists(clientDir))
            {
                try { Directory.Delete(clientDir, true); } catch { }
            }
        }
    }
}
