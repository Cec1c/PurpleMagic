using System.Diagnostics;
using System.IO.Compression;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;
using System.Threading.Channels;
using ServerS5A3;

namespace Dnf90.Launcher.Diagnostics;

internal sealed class ContractDiagnosticsLedger : IDisposable
{
    public const int SchemaVersion = 1;
    public const string ContractName = "purplemagic-anton-first-entry-diagnostics";
    public const string DiagnosticModuleVersion = "pm-contract-v1.0";
    public const int PreTriggerSeconds = 120;
    public const int PostTriggerSeconds = 60;
    public const long MaxBytesPerSession = 33_554_432L;
    public const long CriticalReserveBytes = 256 * 1024L;
    public const int MaxIncidents = 5;
    private const int MaxRetainedSessions = 8;

    private static readonly string[] RequiredCapabilityKeys =
    [
        "launcher_sessions",
        "manual_observations",
        "game_socket_ownership",
        "client_side_packet_capture",
        "application_frames",
        "native_actor_events",
        "native_registry_events",
        "native_context_events",
        "native_render_events"
    ];

    private sealed record StoredLine(long Seq, long MonoTicks, byte[] Bytes, bool IsObservation);

    private sealed class LedgerSession
    {
        public readonly object Gate = new();
        public required string ClientSessionId { get; init; }
        public required string LaunchInstanceId { get; init; }
        public required long LaunchOrdinal { get; init; }
        public required int ActiveSlot { get; init; }
        public required string StartedUtc { get; init; }
        public required long StartedMonoTicks { get; init; }
        public string HandoffMode { get; set; } = "direct_child";
        public int? Pid { get; set; }
        public long? ProcessCreatedFileTimeUtc { get; set; }
        public string? ProcessCreatedUtc { get; set; }
        public string? ExecutablePath { get; set; }
        public string? ExecutablePathSha256 { get; set; }
        public string? EnhancedSessionId { get; set; }
        public bool Running { get; set; }
        public string? EndedUtc { get; set; }
        public string EndState { get; set; } = "prelaunch";
        public string? IncidentId { get; set; }
        public long? ReceiverCharacterId { get; set; }
        public string? ReceiverCharacterName { get; set; }
        public string? AssociationSource { get; set; }
        public string? LastServerCharacterKey { get; set; }
        public string? LastServerSceneKey { get; set; }
        public bool ServerClockAnchored { get; set; }
        public string? LastBusinessDisconnectReason { get; set; }
        public string? CurrentEntryId { get; set; }
        public int SceneEpoch { get; set; }
        public bool LastEntryHadMissingObservation { get; set; }
        public readonly List<ContractEntryDescriptor> Entries = [];
        public readonly List<ContractObservationItem> ObservationItems = [];
        public readonly List<StoredLine> CriticalLines = [];
        public readonly List<StoredLine> PinnedRoutineLines = [];
        public readonly Queue<StoredLine> SlidingRoutineRing = [];
        public long SlidingRoutineBytes;
        public long CriticalBytes;
        public long PinnedRoutineBytes;
        public long NextEventSeq;
        public long FirstRetainedSeq;
        public long LastEventSeq;
        public long QueueDropped;
        public long CriticalEventsLost;
        public long TruncatedRecords;
        public long RingEvictedRecords;
        public bool BulkCollectionPaused;
        public long PostTriggerUntilTicks;
        public long NextClockAnchorTicks;
        public GameSocketTracker? SocketTracker;
        public bool Dirty;
        public string LatestEventUtc { get; set; } = "";

        public long TotalRetainedBytes => CriticalBytes + PinnedRoutineBytes + SlidingRoutineBytes;
    }

    private readonly string root;
    private readonly string sessionsRoot;
    private readonly object stateGate = new();
    private readonly List<LedgerSession> sessions = [];
    private readonly List<ContractHttpTraceItem> pendingHttpTraces = [];
    private readonly Dictionary<string, (long Length, long LastWriteTicks, string Sha256)> digestCache = new(StringComparer.OrdinalIgnoreCase);
    private readonly Channel<Func<Task>> diskQueue = Channel.CreateBounded<Func<Task>>(new BoundedChannelOptions(256) { SingleReader = true, FullMode = BoundedChannelFullMode.Wait });
    private readonly CancellationTokenSource lifetime = new();
    private readonly Task diskWorker;
    private readonly Task pollWorker;

    private string clientDirectory = "";
    private string manifestClientSha256 = "";
    private string manifestPvfSha256 = "";
    private long publicationSequence;
    private string contentVersion = "";
    private string sourceId = "";
    private string protocolProfile = "s5a3-bootstrap-v1";
    private ContractServerEndpoint serverEndpoint = new("", 0, 0, null, "");
    private bool enhancedDiagnosticsEnabled;
    private string? activeIncidentId;
    private long launchCounter;
    private int exporting;
    private Func<int, long, (bool Verified, List<RawTcpEntry> Tcp, List<RawUdpEntry> Udp)>? customSocketReader;

    public event Action? Changed;

    public ContractDiagnosticsLedger(string stateRoot, Func<int, long, (bool Verified, List<RawTcpEntry> Tcp, List<RawUdpEntry> Udp)>? socketReader = null)
    {
        customSocketReader = socketReader;
        root = Path.Combine(Path.GetFullPath(stateRoot), "diagnostics", "contract-v1");
        sessionsRoot = Path.Combine(root, "sessions");
        LoadPersistedSessions();
        diskWorker = Task.Run(DiskLoop);
        pollWorker = Task.Run(PollLoop);
    }

    public string ActiveIncidentId
    {
        get { lock (stateGate) return activeIncidentId ?? ""; }
    }

    public void Configure(
        bool enhancedEnabled,
        string directory,
        PlayerProfile profile,
        long sequence,
        string? publishedContentVersion = null,
        string? publishedSourceId = null)
    {
        string? dirToWarm = null;
        lock (stateGate)
        {
            enhancedDiagnosticsEnabled = enhancedEnabled;
            if (!string.Equals(clientDirectory, directory, StringComparison.OrdinalIgnoreCase))
            {
                clientDirectory = directory;
                if (!string.IsNullOrWhiteSpace(directory)) dirToWarm = directory;
            }
            manifestClientSha256 = profile.ExeSha256;
            manifestPvfSha256 = profile.PvfSha256;
            publicationSequence = sequence;
            contentVersion = publishedContentVersion ?? "";
            sourceId = publishedSourceId ?? "";
            protocolProfile = profile.ChannelHistory?.Adapter is { Length: > 0 } adapter ? adapter : "s5a3-bootstrap-v1";
            var apiOrigin = Uri.TryCreate(profile.ApiUrl, UriKind.Absolute, out var uri) ? uri.GetLeftPart(UriPartial.Authority) : "";
            serverEndpoint = new ContractServerEndpoint(
                profile.GameHost,
                profile.BootstrapPort,
                profile.ChannelServerId,
                profile.PreviousChannelServerId,
                apiOrigin);
        }
        if (dirToWarm is not null)
        {
            _ = Task.Run(() => WarmFileDigests(dirToWarm));
        }
    }

    public void RecordHttpTrace(HttpTraceSample sample)
    {
        var item = new ContractHttpTraceItem(
            SanitizeToken(sample.HttpTraceId, 48),
            SanitizeToken(sample.EndpointKind, 48),
            sample.HttpStatus,
            sample.DurationMs,
            sample.ObservedUtc,
            sample.MonoTicks);

        LedgerSession? active = null;
        lock (stateGate)
        {
            pendingHttpTraces.Add(item);
            if (pendingHttpTraces.Count > 16) pendingHttpTraces.RemoveAt(0);
            active = sessions.LastOrDefault(s => s.Running);
        }

        if (active is not null && sample.ServerDateUtc is not null)
        {
            EmitClockAnchor(active, "http_date_header", sample.ServerDateUtc, null, null, "http_date_one_second_granularity", item.HttpTraceId);
            SchedulePersist(active);
        }
    }

    public string TrackLaunch(Process game, string executablePath, string handoffMode, string? enhancedSessionId = null)
    {
        var pid = game.Id;
        long createdFileTime;
        int winSession;
        string verifiedFullPath;

        if (OperatingSystem.IsWindows())
        {
            using var probe = new ReadOnlyGameProcess(pid, executablePath, game.StartTime.ToUniversalTime().ToFileTimeUtc());
            createdFileTime = probe.Identity.Created;
            winSession = probe.Identity.WindowsSession;
            verifiedFullPath = probe.Identity.Path;
        }
        else
        {
            createdFileTime = game.StartTime.ToUniversalTime().ToFileTimeUtc();
            winSession = 0;
            verifiedFullPath = Path.GetFullPath(executablePath);
        }

        return TrackVerifiedProcess(pid, createdFileTime, winSession, verifiedFullPath, handoffMode, enhancedSessionId);
    }

    internal string TrackVerifiedProcess(
        int pid,
        long createdFileTimeUtc,
        int windowsSessionId,
        string verifiedExecutablePath,
        string handoffMode,
        string? enhancedSessionId = null)
    {
        var createdUtc = DateTime.FromFileTimeUtc(createdFileTimeUtc).ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var sanitizedPath = SanitizeExecutablePath(verifiedExecutablePath);
        var pathSha256 = Sha256Hex(Encoding.UTF8.GetBytes(Path.GetFullPath(verifiedExecutablePath).ToLowerInvariant()));

        LedgerSession session;
        ContractHttpTraceItem[] correlatedTraces;
        ContractServerEndpoint endpoint;
        string profileName, declaredExe, declaredPvf, dir;
        bool enhanced;
        string? currentIncident;

        lock (stateGate)
        {
            var existing = sessions.FirstOrDefault(s => s.Pid == pid && s.ProcessCreatedUtc == createdUtc);
            if (existing is not null)
            {
                if (enhancedSessionId is not null) existing.EnhancedSessionId = enhancedSessionId;
                return existing.ClientSessionId;
            }

            var ordinal = ++launchCounter;
            var usedSlots = sessions.Where(s => s.Running).Select(s => s.ActiveSlot).ToHashSet();
            var slot = 1;
            while (usedSlots.Contains(slot) && slot < 16) slot++;

            currentIncident = activeIncidentId;
            var sessionId = "cs-" + Guid.NewGuid().ToString("N");
            var nowTicks = Stopwatch.GetTimestamp();
            var nowUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");

            Func<(bool Verified, List<RawTcpEntry> Tcp, List<RawUdpEntry> Udp)>? reader =
                customSocketReader is null ? null : () => customSocketReader(pid, createdFileTimeUtc);

            session = new LedgerSession
            {
                ClientSessionId = sessionId,
                LaunchInstanceId = $"slot-{slot}#launch-{ordinal}",
                LaunchOrdinal = ordinal,
                ActiveSlot = slot,
                StartedUtc = nowUtc,
                StartedMonoTicks = nowTicks,
                LatestEventUtc = nowUtc,
                HandoffMode = SanitizeToken(handoffMode, 48),
                Pid = pid,
                ProcessCreatedFileTimeUtc = createdFileTimeUtc,
                ProcessCreatedUtc = createdUtc,
                ExecutablePath = sanitizedPath,
                ExecutablePathSha256 = pathSha256,
                EnhancedSessionId = enhancedSessionId,
                Running = true,
                EndState = "running",
                IncidentId = currentIncident,
                NextClockAnchorTicks = nowTicks + 30 * Stopwatch.Frequency,
                SocketTracker = new GameSocketTracker(pid, createdFileTimeUtc, reader)
            };

            if (currentIncident is not null)
            {
                session.PostTriggerUntilTicks = nowTicks + PostTriggerSeconds * Stopwatch.Frequency;
            }

            correlatedTraces = [.. pendingHttpTraces];
            pendingHttpTraces.Clear();
            endpoint = serverEndpoint;
            profileName = protocolProfile;
            declaredExe = manifestClientSha256;
            declaredPvf = manifestPvfSha256;
            dir = Path.GetDirectoryName(verifiedExecutablePath) ?? clientDirectory;
            enhanced = enhancedDiagnosticsEnabled;

            sessions.Add(session);
            TrimInMemorySessionsLocked();
        }

        EmitSessionStart(session, profileName, endpoint, enhanced, correlatedTraces);
        EmitClockAnchor(session, "local_system_clock", null, null, null, enhanced ? "awaiting_authenticated_server_context" : "enhanced_context_disabled", correlatedTraces.LastOrDefault()?.HttpTraceId);

        var (exeSha, exeBytes, exeReason) = ComputeFileDigest(Path.Combine(dir, "DNF.exe"));
        var (pvfSha, pvfBytes, pvfReason) = ComputeFileDigest(Path.Combine(dir, "Script.pvf"), allowAsyncDefer: true);
        EmitProcessStarted(session, sanitizedPath, pathSha256, windowsSessionId, exeSha, exeBytes, exeReason, pvfSha, pvfBytes, pvfReason, declaredExe, declaredPvf);
        EmitTraceIntegrity(session, "session_start");

        PollSessionSockets(session);
        SchedulePersist(session);
        NotifyChanged();
        return session.ClientSessionId;
    }

    public void BindEnhancedSession(int pid, long createdFileTimeUtc, string enhancedSessionId)
    {
        var createdUtc = DateTime.FromFileTimeUtc(createdFileTimeUtc).ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        lock (stateGate)
        {
            var match = sessions.LastOrDefault(s => s.Pid == pid && s.ProcessCreatedUtc == createdUtc);
            if (match is not null) match.EnhancedSessionId = enhancedSessionId;
        }
    }

    public string? FindSharedIncidentForEnhanced(string enhancedSessionId)
    {
        lock (stateGate)
        {
            return sessions.LastOrDefault(s => s.EnhancedSessionId == enhancedSessionId)?.IncidentId ?? activeIncidentId;
        }
    }

    internal void UpdateServerContext(int? pid, string? enhancedSessionId, string kind, DiagnosticContext context)
    {
        LedgerSession? session;
        lock (stateGate)
        {
            session = sessions.LastOrDefault(s =>
                (enhancedSessionId is not null && s.EnhancedSessionId == enhancedSessionId) ||
                (pid is > 0 && s.Pid == pid && s.Running))
                ?? sessions.LastOrDefault(s => s.Running);
        }
        if (session is null) return;

        var changed = false;
        if (context.Clock.Status == "estimated" && (!session.ServerClockAnchored || kind is "character_changed" or "context_changed" or "business_disconnect"))
        {
            session.ServerClockAnchored = true;
            EmitClockAnchor(session, context.Clock.Source, context.ObservedAt, context.Clock.OffsetMs, context.Clock.UncertaintyMs, null, null);
            changed = true;
        }

        var charIdText = context.Fields.TryGetValue("character_id", out var cidField) && cidField.Status == "current" ? cidField.Value : null;
        var charName = context.Fields.TryGetValue("character_name", out var cnameField) && cnameField.Status == "current" ? cnameField.Value : null;
        var channelId = context.Fields.TryGetValue("channel_id", out var chField) && chField.Status != "unavailable" ? chField.Value : null;
        var partyId = context.Fields.TryGetValue("party_id", out var partyField) && partyField.Status != "unavailable" ? partyField.Value : null;

        if (charIdText is not null || charName is not null)
        {
            var key = $"{charIdText}:{charName}:{channelId}:{partyId}";
            if (session.LastServerCharacterKey != key)
            {
                session.LastServerCharacterKey = key;
                long? parsedCharId = long.TryParse(charIdText, out var n) ? n : null;
                AssociateCharacterCore(session, parsedCharId, charName, "authenticated_server_context", channelId, partyId);
                changed = true;
            }
        }

        var dungeonId = context.Fields.TryGetValue("dungeon_id", out var dField) && dField.Status == "current" ? dField.Value : null;
        var mapId = context.Fields.TryGetValue("map_id", out var mField) && mField.Status == "current" ? mField.Value : null;
        var roomKey = context.Fields.TryGetValue("room", out var rField) && rField.Status == "current" ? rField.Value : null;
        var instanceId = context.Fields.TryGetValue("instance_id", out var iField) && iField.Status == "current" ? iField.Value : null;

        if (dungeonId is not null || instanceId is not null || roomKey is not null)
        {
            var sceneKey = $"{dungeonId}|{mapId}|{roomKey}|{instanceId}";
            if (session.LastServerSceneKey != sceneKey)
            {
                var entryKind = session.LastServerSceneKey is null ? "initial_entry"
                    : session.LastServerSceneKey.Split('|')[0] != (dungeonId ?? "") || session.LastServerSceneKey.Split('|')[3] != (instanceId ?? "")
                        ? "reentry"
                        : "room_change";
                session.LastServerSceneKey = sceneKey;
                AppendSceneEntry(session, entryKind, "authenticated_server_context", dungeonId, mapId, roomKey, instanceId);
                changed = true;
            }
        }

        if (kind == "business_disconnect")
        {
            var reasonCode = context.Fields.TryGetValue("reason_code", out var rc) && rc.Value is not null ? rc.Value : "business_disconnect";
            session.LastBusinessDisconnectReason = SanitizeToken(reasonCode, 64);
            changed = true;
        }

        if (changed)
        {
            SchedulePersist(session);
            NotifyChanged();
        }
    }

    public string SetIncident(ContractIncidentInput input)
    {
        string normalized;
        if (input.GenerateNew || string.IsNullOrWhiteSpace(input.IncidentId))
        {
            normalized = NewIncidentId();
        }
        else
        {
            normalized = ValidateIncidentId(input.IncidentId!);
        }

        LedgerSession[] affected;
        lock (stateGate)
        {
            activeIncidentId = normalized;
            affected = sessions.Where(s => s.Running || s.IncidentId is null).ToArray();
        }

        foreach (var s in affected)
        {
            PinIncidentOnSession(s, normalized);
            EmitTraceIntegrity(s, "incident_triggered");
            SchedulePersist(s);
        }
        diskQueue.Writer.TryWrite(() =>
        {
            try { SafeFiles.Atomic(Path.Combine(root, "active-incident.txt"), Encoding.UTF8.GetBytes(normalized)); }
            catch (Exception) { }
            return Task.CompletedTask;
        });
        NotifyChanged();
        return normalized;
    }

    public void AssociateCharacter(ContractCharacterInput input)
    {
        var session = RequireSession(input.ClientSessionId);
        var cleanName = SanitizeDisplayName(input.ReceiverCharacterName, 64);
        if (cleanName is null && input.ReceiverCharacterId is null)
        {
            throw new InvalidDataException("请填写角色名或角色编号。");
        }
        if (input.ReceiverCharacterId is < 0)
        {
            throw new InvalidDataException("角色编号无效。");
        }
        AssociateCharacterCore(session, input.ReceiverCharacterId, cleanName, "user_selected", null, null);
        SchedulePersist(session);
        NotifyChanged();
    }

    public string MarkNewEntry(ContractEntryInput input)
    {
        var session = RequireSession(input.ClientSessionId);
        var kind = input.EntryKind is "initial_entry" or "reentry" or "room_change" or "user_marked_entry"
            ? input.EntryKind
            : "user_marked_entry";
        var entry = AppendSceneEntry(session, kind, "user_marked", null, null, null, null);
        SchedulePersist(session);
        NotifyChanged();
        return entry.EntryId;
    }

    public ContractObservationItem ReportObservation(ContractObservationInput input)
    {
        if (input.State is not ("missing" or "late_appeared" or "appeared" or "recovered_after_reentry"))
        {
            throw new InvalidDataException("可见性观察状态无效。");
        }
        if (input.PeerScope is not ("single_peer" or "all_mutual_invisible" or "unknown_peer"))
        {
            throw new InvalidDataException("队友范围选项无效。");
        }
        if (input.OnsetOffsetSeconds is < 0 or > 3600)
        {
            throw new InvalidDataException("补报时间偏移超出范围。");
        }

        var session = RequireSession(input.ClientSessionId);
        string incident;
        LedgerSession[] runningSiblings;
        lock (stateGate)
        {
            incident = session.IncidentId ?? activeIncidentId ?? NewIncidentId();
            activeIncidentId = incident;
            runningSiblings = sessions.Where(s => s.Running && !ReferenceEquals(s, session)).ToArray();
        }

        PinIncidentOnSession(session, incident);
        foreach (var sibling in runningSiblings)
        {
            PinIncidentOnSession(sibling, incident);
            SchedulePersist(sibling);
        }

        // Preserve failed first entry + successful re-entry parent linkage automatically
        // when the user clicks "recovered_after_reentry" without having manually clicked new entry.
        if (input.State == "recovered_after_reentry" && (session.CurrentEntryId is null || session.LastEntryHadMissingObservation))
        {
            if (session.CurrentEntryId is null)
            {
                AppendSceneEntry(session, "initial_entry", "user_marked", null, null, null, null);
            }
            AppendSceneEntry(session, "reentry", "user_marked", null, null, null, null);
        }
        else if (session.CurrentEntryId is null)
        {
            AppendSceneEntry(session, "initial_entry", "user_marked", null, null, null, null);
        }

        if (input.State is "missing" or "late_appeared")
        {
            session.LastEntryHadMissingObservation = true;
        }
        else if (input.State == "recovered_after_reentry")
        {
            session.LastEntryHadMissingObservation = false;
        }

        long? peerId = null;
        string? peerName = null;
        string? peerUnknownReason = null;
        if (input.PeerScope == "all_mutual_invisible")
        {
            peerUnknownReason = "all_teammates_mutually_invisible";
        }
        else if (input.PeerScope == "unknown_peer")
        {
            peerUnknownReason = "user_unknown_peer_name";
        }
        else
        {
            peerName = SanitizeDisplayName(input.PeerCharacterName, 64);
            peerId = input.PeerCharacterId is > 0 ? input.PeerCharacterId : null;
            if (peerName is null && peerId is null)
            {
                peerUnknownReason = "user_unknown_peer_name";
            }
        }

        var nowUtcDt = DateTime.UtcNow;
        var observedUtc = nowUtcDt.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var monoTicks = Stopwatch.GetTimestamp();
        var threadId = CurrentThreadId();
        var seq = Interlocked.Increment(ref session.NextEventSeq);

        string? estimatedOnsetUtc = null;
        double? uncertaintyMs = null;
        if (input.OnsetOffsetSeconds is > 0)
        {
            estimatedOnsetUtc = nowUtcDt.AddSeconds(-input.OnsetOffsetSeconds.Value).ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
            uncertaintyMs = input.TimeUncertaintyMs is > 0
                ? Math.Round(input.TimeUncertaintyMs.Value, 1)
                : Math.Max(1000, input.OnsetOffsetSeconds.Value * 500.0);
        }

        var cleanNote = SanitizeDisplayName(input.Note, 120);
        string entryId;
        string? parentEntryId;
        int sceneEpoch;
        long? receiverId;
        string? receiverName;
        lock (session.Gate)
        {
            entryId = session.CurrentEntryId!;
            parentEntryId = session.Entries.LastOrDefault()?.ParentEntryId;
            sceneEpoch = session.SceneEpoch;
            receiverId = session.ReceiverCharacterId;
            receiverName = session.ReceiverCharacterName;
        }

        var receiverUnknown = receiverId is null && receiverName is null
            ? "character_not_associated_by_server_or_user"
            : null;

        var ev = new ObservationReportedEvent(
            SchemaVersion: SchemaVersion,
            EventName: "observation.reported",
            Source: "launcher",
            ClientSessionId: session.ClientSessionId,
            IncidentId: session.IncidentId,
            EventSeq: seq,
            ObservedUtc: observedUtc,
            MonoTicks: monoTicks,
            MonoFrequencyHz: Stopwatch.Frequency,
            Pid: session.Pid,
            ProcessCreatedUtc: session.ProcessCreatedUtc,
            ThreadId: threadId,
            LaunchInstanceId: session.LaunchInstanceId,
            EntryId: entryId,
            ParentEntryId: parentEntryId,
            SceneEpoch: sceneEpoch,
            ReceiverCharacterId: receiverId,
            ReceiverCharacterName: receiverName,
            ReceiverUnknownReason: receiverUnknown,
            PeerScope: input.PeerScope,
            PeerCharacterId: peerId,
            PeerCharacterName: peerName,
            PeerUnknownReason: peerUnknownReason,
            State: input.State,
            ObservationSource: "user",
            EstimatedOnsetUtc: estimatedOnsetUtc,
            TimeUncertaintyMs: uncertaintyMs,
            Note: cleanNote);

        var bytes = SerializeLine(ev, ContractDiagnosticsJson.Default.ObservationReportedEvent);
        StoreLine(session, new StoredLine(seq, monoTicks, bytes, IsObservation: true), critical: true, observedUtc);

        var uiItem = new ContractObservationItem(
            session.ClientSessionId,
            session.LaunchInstanceId,
            session.Pid,
            seq,
            observedUtc,
            entryId,
            sceneEpoch,
            receiverName,
            input.PeerScope,
            peerName,
            input.State,
            estimatedOnsetUtc,
            uncertaintyMs);

        lock (session.Gate)
        {
            session.ObservationItems.Add(uiItem);
            if (session.ObservationItems.Count > 32) session.ObservationItems.RemoveAt(0);
        }

        SchedulePersist(session);
        NotifyChanged();
        return uiItem;
    }

    public (string ActiveIncidentId, ContractSessionStatus[] Sessions, ContractObservationItem[] Observations) SnapshotUiStatus()
    {
        LedgerSession[] list;
        string incident;
        lock (stateGate)
        {
            incident = activeIncidentId ?? "";
            list = [.. sessions];
        }

        var sessionDtos = new List<ContractSessionStatus>(list.Length);
        var obsDtos = new List<ContractObservationItem>();
        foreach (var s in list.OrderByDescending(x => x.StartedUtc).ThenByDescending(x => x.LaunchOrdinal).Take(MaxRetainedSessions))
        {
            lock (s.Gate)
            {
                sessionDtos.Add(new ContractSessionStatus(
                    s.ClientSessionId,
                    s.LaunchInstanceId,
                    s.Pid,
                    s.ProcessCreatedUtc,
                    s.Running,
                    s.StartedUtc,
                    s.EndedUtc,
                    s.IncidentId,
                    s.ReceiverCharacterId,
                    s.ReceiverCharacterName,
                    s.AssociationSource,
                    s.CurrentEntryId,
                    s.SceneEpoch,
                    s.LastEventSeq,
                    s.ObservationItems.Count));
                obsDtos.AddRange(s.ObservationItems);
            }
        }

        var sortedObs = obsDtos
            .OrderByDescending(o => o.ObservedUtc)
            .ThenByDescending(o => o.EventSeq)
            .Take(12)
            .ToArray();

        return (incident, [.. sessionDtos], sortedObs);
    }

    public async Task<DiagnosticExport> ExportBundleAsync(
        ContractBundleExportInput input,
        Func<string, byte[]?>? enhancedLogProvider = null)
    {
        if (Interlocked.CompareExchange(ref exporting, 1, 0) != 0)
        {
            throw new InvalidOperationException("正在导出诊断包，请稍候。");
        }
        try
        {
            var target = Path.GetFullPath(input.Path);
            if (!Path.GetExtension(target).Equals(".zip", StringComparison.OrdinalIgnoreCase))
            {
                throw new InvalidDataException("诊断包请保存为 .zip 文件。");
            }
            SafeFiles.NoLinks(target);
            var parent = Path.GetDirectoryName(target) ?? throw new InvalidDataException("保存路径无效。");
            var diagRoot = Path.GetDirectoryName(root)!;
            var relative = Path.GetRelativePath(diagRoot, target);
            if (!Directory.Exists(parent) || (!relative.StartsWith(".." + Path.DirectorySeparatorChar, StringComparison.Ordinal) && !Path.IsPathRooted(relative)))
            {
                throw new InvalidDataException("请选择诊断存储目录以外的保存位置。");
            }

            LedgerSession[] selected;
            string bundleIncidentId;
            string dir, declaredExe, declaredPvf, contentVer, srcId, protoProfile;
            long pubSeq;
            ContractServerEndpoint endpoint;
            bool enhanced;

            lock (stateGate)
            {
                dir = clientDirectory;
                declaredExe = manifestClientSha256;
                declaredPvf = manifestPvfSha256;
                pubSeq = publicationSequence;
                contentVer = contentVersion;
                srcId = sourceId;
                protoProfile = protocolProfile;
                endpoint = serverEndpoint;
                enhanced = enhancedDiagnosticsEnabled;

                var requestedIncident = string.IsNullOrWhiteSpace(input.IncidentId) ? null : ValidateIncidentId(input.IncidentId!);
                if (!string.IsNullOrWhiteSpace(input.ClientSessionId))
                {
                    var primary = sessions.FirstOrDefault(s => s.ClientSessionId == input.ClientSessionId)
                        ?? throw new InvalidDataException("所选游戏会话不存在或已清理。");
                    bundleIncidentId = requestedIncident ?? primary.IncidentId ?? activeIncidentId ?? NewIncidentId();
                    selected = primary.IncidentId is { } shared
                        ? sessions.Where(s => s.ClientSessionId == primary.ClientSessionId || s.IncidentId == shared).ToArray()
                        : [primary];
                }
                else
                {
                    var targetIncident = requestedIncident ?? activeIncidentId;
                    if (targetIncident is not null)
                    {
                        var matching = sessions.Where(s => s.IncidentId == targetIncident || s.Running).ToArray();
                        selected = matching.Length > 0 ? matching : sessions.TakeLast(MaxIncidents).ToArray();
                        bundleIncidentId = targetIncident;
                    }
                    else
                    {
                        var running = sessions.Where(s => s.Running).ToArray();
                        selected = running.Length > 0 ? running : sessions.TakeLast(MaxIncidents).ToArray();
                        bundleIncidentId = NewIncidentId();
                    }
                }

                if (selected.Length > 0) activeIncidentId ??= bundleIncidentId;
            }

            var transientPrelaunch = false;
            if (selected.Length == 0)
            {
                selected = [CreatePrelaunchSnapshotSession(bundleIncidentId, protoProfile, endpoint, enhanced)];
                transientPrelaunch = true;
            }

            foreach (var s in selected)
            {
                PinIncidentOnSession(s, bundleIncidentId);
                if (s.Running)
                {
                    PollSessionSockets(s);
                    EmitClockAnchor(s, "local_system_clock", null, null, null, s.ServerClockAnchored ? null : "local_export_anchor", null);
                }
                EmitTraceIntegrity(s, "bundle_export");
                if (!transientPrelaunch) SchedulePersist(s);
            }

            await FlushDiskBarrierAsync();

            var orderedSessions = selected
                .OrderBy(s => s.StartedUtc, StringComparer.Ordinal)
                .ThenBy(s => s.LaunchOrdinal)
                .ToArray();

            var exePath = string.IsNullOrWhiteSpace(dir) ? "" : Path.Combine(dir, "DNF.exe");
            var pvfPath = string.IsNullOrWhiteSpace(dir) ? "" : Path.Combine(dir, "Script.pvf");
            var (exeSha, exeBytes, exeUnknown) = string.IsNullOrWhiteSpace(exePath)
                ? (null, null, "client_directory_not_configured")
                : await Task.Run(() => ComputeFileDigest(exePath));
            var (pvfSha, pvfBytes, pvfUnknown) = string.IsNullOrWhiteSpace(pvfPath)
                ? (null, null, "client_directory_not_configured")
                : await Task.Run(() => ComputeFileDigest(pvfPath, allowAsyncDefer: false));

            var descriptors = new List<ContractSessionDescriptor>(orderedSessions.Length);
            var sessionIntegrities = new List<ContractSessionIntegrity>(orderedSessions.Length);
            var sessionPayloads = new List<(string ArchivePath, byte[] Content)>(orderedSessions.Length * 2);
            var extraEnhancedLogs = new List<(string ArchivePath, byte[] Bytes)>();

            foreach (var s in orderedSessions)
            {
                var (lines, obsLines, descriptor, integrity) = SnapshotSessionForExport(s);
                using var sessionLauncherStream = new MemoryStream();
                using var sessionObsStream = new MemoryStream();
                foreach (var line in lines) sessionLauncherStream.Write(line, 0, line.Length);
                foreach (var obs in obsLines) sessionObsStream.Write(obs, 0, obs.Length);
                sessionPayloads.Add(($"sessions/{s.ClientSessionId}/launcher-events.jsonl", sessionLauncherStream.ToArray()));
                sessionPayloads.Add(($"sessions/{s.ClientSessionId}/observations.jsonl", sessionObsStream.ToArray()));
                descriptors.Add(descriptor);
                sessionIntegrities.Add(integrity);

                if (enhancedLogProvider is not null && s.EnhancedSessionId is { } enhId)
                {
                    var enhBytes = enhancedLogProvider(enhId);
                    if (enhBytes is { Length: > 0 })
                    {
                        extraEnhancedLogs.Add(($"enhanced/{enhId}.log", enhBytes));
                    }
                }
            }

            var exportedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");

            var firstSeq = sessionIntegrities.Where(i => i.FirstEventSeq > 0).Select(i => i.FirstEventSeq).DefaultIfEmpty(0).Min();
            var lastSeq = sessionIntegrities.Select(i => i.LastEventSeq).DefaultIfEmpty(0).Max();
            var covStart = sessionIntegrities.Select(i => i.CoverageStartedUtc).OrderBy(x => x, StringComparer.Ordinal).FirstOrDefault() ?? exportedUtc;
            var covEnd = sessionIntegrities.Select(i => i.CoverageEndedUtc).OrderByDescending(x => x, StringComparer.Ordinal).FirstOrDefault() ?? exportedUtc;
            var allCompleted = sessionIntegrities.All(i => i.CompletionStatus is "process_exited" or "prelaunch_snapshot");

            var integrityReport = new ContractIntegrityReport(
                SchemaVersion: SchemaVersion,
                IncidentId: bundleIncidentId,
                GeneratedUtc: exportedUtc,
                QueueDropped: sessionIntegrities.Sum(i => i.QueueDropped),
                CriticalEventsLost: sessionIntegrities.Sum(i => i.CriticalEventsLost),
                TruncatedRecords: sessionIntegrities.Sum(i => i.TruncatedRecords),
                RingEvictedRecords: sessionIntegrities.Sum(i => i.RingEvictedRecords),
                CaptureDroppedPackets: null,
                CaptureDroppedPacketsUnknownReason: "client_side_packet_capture_disabled",
                FirstEventSeq: firstSeq,
                LastEventSeq: lastSeq,
                CoverageStartedUtc: covStart,
                CoverageEndedUtc: covEnd,
                CompletionStatus: allCompleted ? "complete" : "active_session_snapshot",
                NativeEventsCapability: false,
                NativeEventsUnknownReason: "launcher_only_no_client_event_provider",
                Sessions: [.. sessionIntegrities]);

            var integrityBytes = AppendNewline(JsonSerializer.SerializeToUtf8Bytes(integrityReport, ContractDiagnosticsJson.Default.ContractIntegrityReport));

            var payloadEntries = new List<(string ArchivePath, byte[] Content)>(sessionPayloads.Count + 1 + extraEnhancedLogs.Count);
            payloadEntries.AddRange(sessionPayloads);
            payloadEntries.Add(("integrity.json", integrityBytes));
            foreach (var extra in extraEnhancedLogs.DistinctBy(e => e.ArchivePath))
            {
                payloadEntries.Add(extra);
            }

            var manifestFiles = payloadEntries
                .Select(p => new ContractBundleFile(p.ArchivePath, p.Content.LongLength, Sha256Hex(p.Content)))
                .ToArray();

            var capabilities = new Dictionary<string, bool>
            {
                ["launcher_sessions"] = true,
                ["manual_observations"] = true,
                ["game_socket_ownership"] = OperatingSystem.IsWindows(),
                ["client_side_packet_capture"] = false,
                ["application_frames"] = false,
                ["native_actor_events"] = false,
                ["native_registry_events"] = false,
                ["native_context_events"] = false,
                ["native_render_events"] = false
            };

            var capabilityReasons = new Dictionary<string, string?>
            {
                ["launcher_sessions"] = null,
                ["manual_observations"] = null,
                ["game_socket_ownership"] = OperatingSystem.IsWindows() ? null : "windows_iphlpapi_only",
                ["client_side_packet_capture"] = "not_implemented_in_launcher_p0",
                ["application_frames"] = "no_tcp_reassembly_or_packet_capture_in_launcher",
                ["native_actor_events"] = "no_native_client_callback_provider",
                ["native_registry_events"] = "no_native_client_callback_provider",
                ["native_context_events"] = "no_native_client_callback_provider",
                ["native_render_events"] = "no_native_client_callback_provider"
            };

            foreach (var key in RequiredCapabilityKeys)
            {
                if (!capabilities.ContainsKey(key)) capabilities[key] = false;
            }

            var optionalPayloads = new Dictionary<string, ContractOptionalPayload>
            {
                ["frames.jsonl"] = new(false, "application_frames_capability_disabled"),
                ["frames/"] = new(false, "application_frames_capability_disabled"),
                ["native.jsonl"] = new(false, "native_client_callbacks_capability_disabled")
            };

            var manifest = new ContractManifest(
                SchemaVersion: SchemaVersion,
                ContractName: ContractName,
                IncidentId: bundleIncidentId,
                ExportedUtc: exportedUtc,
                LauncherVersion: LauncherEngine.VersionText,
                LauncherCommit: LauncherBuild.Commit,
                DiagnosticModuleVersion: DiagnosticModuleVersion,
                BuildFlavor: LauncherBuild.Flavor,
                ClientFileSha256: exeSha,
                ClientFileBytes: exeBytes,
                ClientFileUnknownReason: exeUnknown,
                PvfFileSha256: pvfSha,
                PvfFileBytes: pvfBytes,
                PvfFileUnknownReason: pvfUnknown,
                DeclaredManifestDigests: new ContractDeclaredDigests(
                    ManifestClientSha256: declaredExe,
                    ManifestPvfSha256: declaredPvf,
                    PublicationSequence: pubSeq,
                    ContentVersion: contentVer,
                    SourceId: srcId),
                ServerVersion: null,
                ServerVersionUnknownReason: "not_exposed_by_player_gateway_protocol",
                ProtocolProfile: protoProfile,
                ServerEndpoint: endpoint,
                TimePolicy: new ContractTimePolicy(
                    UtcForCrossMachineAlignment: true,
                    QpcForLocalIntervals: true,
                    CompareQpcAcrossHosts: false,
                    RawQpcFrequencyRequired: true,
                    HostMonoFrequencyHz: Stopwatch.Frequency),
                Limits: new ContractLimits(
                    PreTriggerSeconds: PreTriggerSeconds,
                    PostTriggerSeconds: PostTriggerSeconds,
                    MaxBytesPerSession: MaxBytesPerSession,
                    MaxIncidents: MaxIncidents),
                Capabilities: capabilities,
                CapabilityReasons: capabilityReasons,
                OptionalPayloads: optionalPayloads,
                Sessions: [.. descriptors],
                Integrity: integrityReport,
                Files: manifestFiles);

            var manifestBytes = AppendNewline(JsonSerializer.SerializeToUtf8Bytes(manifest, ContractDiagnosticsJson.Default.ContractManifest));

            var tempPath = Path.Combine(parent, ".purplemagic-bundle-" + Guid.NewGuid().ToString("N") + ".writing");
            try
            {
                using (var fs = new FileStream(tempPath, FileMode.CreateNew, FileAccess.ReadWrite, FileShare.None))
                {
                    using (var archive = new ZipArchive(fs, ZipArchiveMode.Create, leaveOpen: true))
                    {
                        WriteZipEntry(archive, "manifest.json", manifestBytes);
                        foreach (var (archivePath, content) in payloadEntries)
                        {
                            WriteZipEntry(archive, archivePath, content);
                        }
                    }
                    fs.Flush(true);
                }

                long writtenBytes;
                string bundleSha256;
                using (var verifyStream = File.OpenRead(tempPath))
                {
                    using var verifyZip = new ZipArchive(verifyStream, ZipArchiveMode.Read, leaveOpen: true);
                    if (verifyZip.GetEntry("manifest.json") is null ||
                        verifyZip.GetEntry("integrity.json") is null ||
                        payloadEntries.Any(p => verifyZip.GetEntry(p.ArchivePath) is null))
                    {
                        throw new InvalidDataException("诊断包校验未通过。");
                    }
                    verifyStream.Position = 0;
                    writtenBytes = verifyStream.Length;
                    bundleSha256 = Convert.ToHexString(SHA256.HashData(verifyStream)).ToLowerInvariant();
                }

                SafeFiles.NoLinks(target);
                File.Move(tempPath, target, overwrite: true);
                NotifyChanged();
                return new DiagnosticExport(target, writtenBytes, bundleSha256, orderedSessions.Length);
            }
            finally
            {
                if (File.Exists(tempPath))
                {
                    try { File.Delete(tempPath); } catch (IOException) { }
                }
            }
        }
        finally
        {
            Volatile.Write(ref exporting, 0);
        }
    }

    private LedgerSession CreatePrelaunchSnapshotSession(string incidentId, string protoProfile, ContractServerEndpoint endpoint, bool enhanced)
    {
        ContractHttpTraceItem[] traces;
        lock (stateGate) traces = [.. pendingHttpTraces];
        var nowUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var nowTicks = Stopwatch.GetTimestamp();
        var s = new LedgerSession
        {
            ClientSessionId = "cs-" + Guid.NewGuid().ToString("N"),
            LaunchInstanceId = "slot-0#prelaunch",
            LaunchOrdinal = 0,
            ActiveSlot = 0,
            StartedUtc = nowUtc,
            StartedMonoTicks = nowTicks,
            LatestEventUtc = nowUtc,
            HandoffMode = "prelaunch_no_game_started",
            Running = false,
            EndedUtc = nowUtc,
            EndState = "prelaunch_snapshot",
            IncidentId = incidentId
        };
        EmitSessionStart(s, protoProfile, endpoint, enhanced, traces);
        EmitClockAnchor(s, "local_system_clock", null, null, null, "no_game_session_started", traces.LastOrDefault()?.HttpTraceId);
        return s;
    }

    private static void WriteZipEntry(ZipArchive archive, string entryName, byte[] content)
    {
        var entry = archive.CreateEntry(entryName, CompressionLevel.Optimal);
        using var stream = entry.Open();
        stream.Write(content, 0, content.Length);
    }

    private void AssociateCharacterCore(LedgerSession session, long? characterId, string? characterName, string source, string? channelId, string? partyId)
    {
        var seq = Interlocked.Increment(ref session.NextEventSeq);
        var monoTicks = Stopwatch.GetTimestamp();
        var observedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var threadId = CurrentThreadId();

        lock (session.Gate)
        {
            session.ReceiverCharacterId = characterId;
            session.ReceiverCharacterName = characterName;
            session.AssociationSource = source;
        }

        var ev = new CharacterAssociatedEvent(
            SchemaVersion: SchemaVersion,
            EventName: "character.associated",
            Source: "launcher",
            ClientSessionId: session.ClientSessionId,
            IncidentId: session.IncidentId,
            EventSeq: seq,
            ObservedUtc: observedUtc,
            MonoTicks: monoTicks,
            MonoFrequencyHz: Stopwatch.Frequency,
            Pid: session.Pid,
            ProcessCreatedUtc: session.ProcessCreatedUtc,
            ThreadId: threadId,
            LaunchInstanceId: session.LaunchInstanceId,
            ReceiverCharacterId: characterId,
            ReceiverCharacterIdUnknownReason: characterId is null ? (source == "user_selected" ? "user_provided_name_only" : "not_reported_by_server") : null,
            ReceiverCharacterName: characterName,
            ReceiverCharacterNameUnknownReason: characterName is null ? "user_provided_id_only" : null,
            AssociationSource: source,
            ChannelId: channelId,
            ChannelUnknownReason: channelId is null ? "channel_not_observed" : null,
            PartyId: partyId,
            PartyUnknownReason: partyId is null ? "party_not_observed" : null);

        StoreLine(session, new StoredLine(seq, monoTicks, SerializeLine(ev, ContractDiagnosticsJson.Default.CharacterAssociatedEvent), false), critical: true, observedUtc);
    }

    private ContractEntryDescriptor AppendSceneEntry(
        LedgerSession session,
        string entryKind,
        string entrySource,
        string? dungeonId,
        string? mapId,
        string? roomKey,
        string? instanceId)
    {
        var seq = Interlocked.Increment(ref session.NextEventSeq);
        var monoTicks = Stopwatch.GetTimestamp();
        var observedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var threadId = CurrentThreadId();

        var entryId = "entry-" + Guid.NewGuid().ToString("N")[..12];
        string? parentEntryId;
        int epoch;
        ContractEntryDescriptor descriptor;

        lock (session.Gate)
        {
            parentEntryId = session.CurrentEntryId;
            epoch = ++session.SceneEpoch;
            session.CurrentEntryId = entryId;
            session.LastEntryHadMissingObservation = false;
            descriptor = new ContractEntryDescriptor(
                entryId,
                parentEntryId,
                epoch,
                entryKind,
                entrySource,
                observedUtc,
                monoTicks,
                dungeonId,
                mapId,
                roomKey,
                instanceId);
            session.Entries.Add(descriptor);
        }

        var unknownReason = entrySource == "user_marked" ? "user_marked_without_server_context" : "not_reported_by_server";
        var ev = new SceneEntryEvent(
            SchemaVersion: SchemaVersion,
            EventName: "scene.entry",
            Source: "launcher",
            ClientSessionId: session.ClientSessionId,
            IncidentId: session.IncidentId,
            EventSeq: seq,
            ObservedUtc: observedUtc,
            MonoTicks: monoTicks,
            MonoFrequencyHz: Stopwatch.Frequency,
            Pid: session.Pid,
            ProcessCreatedUtc: session.ProcessCreatedUtc,
            ThreadId: threadId,
            LaunchInstanceId: session.LaunchInstanceId,
            EntryId: entryId,
            ParentEntryId: parentEntryId,
            SceneEpoch: epoch,
            EntryKind: entryKind,
            EntrySource: entrySource,
            DungeonId: dungeonId,
            DungeonUnknownReason: dungeonId is null ? unknownReason : null,
            MapId: mapId,
            MapUnknownReason: mapId is null ? unknownReason : null,
            RoomKey: roomKey,
            RoomUnknownReason: roomKey is null ? unknownReason : null,
            InstanceId: instanceId,
            InstanceUnknownReason: instanceId is null ? unknownReason : null);

        StoreLine(session, new StoredLine(seq, monoTicks, SerializeLine(ev, ContractDiagnosticsJson.Default.SceneEntryEvent), false), critical: true, observedUtc);
        return descriptor;
    }

    private void EmitSessionStart(
        LedgerSession session,
        string protoProfile,
        ContractServerEndpoint endpoint,
        bool enhanced,
        ContractHttpTraceItem[] correlatedTraces)
    {
        var seq = Interlocked.Increment(ref session.NextEventSeq);
        var monoTicks = Stopwatch.GetTimestamp();
        var observedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var threadId = CurrentThreadId();

        var ev = new SessionStartEvent(
            SchemaVersion: SchemaVersion,
            EventName: "session.start",
            Source: "launcher",
            ClientSessionId: session.ClientSessionId,
            IncidentId: session.IncidentId,
            EventSeq: seq,
            ObservedUtc: observedUtc,
            MonoTicks: monoTicks,
            MonoFrequencyHz: Stopwatch.Frequency,
            Pid: session.Pid,
            ProcessCreatedUtc: session.ProcessCreatedUtc,
            ThreadId: threadId,
            LaunchInstanceId: session.LaunchInstanceId,
            LaunchOrdinal: session.LaunchOrdinal,
            ActiveSlot: session.ActiveSlot,
            LauncherVersion: LauncherEngine.VersionText,
            LauncherCommit: LauncherBuild.Commit,
            DiagnosticModuleVersion: DiagnosticModuleVersion,
            BuildFlavor: LauncherBuild.Flavor,
            ProtocolProfile: protoProfile,
            ServerEndpoint: endpoint,
            HandoffMode: session.HandoffMode,
            EnhancedDiagnosticsEnabled: enhanced,
            CorrelatedHttpTraces: correlatedTraces,
            PidUnknownReason: session.Pid is null ? "process_not_started_yet" : null);

        StoreLine(session, new StoredLine(seq, monoTicks, SerializeLine(ev, ContractDiagnosticsJson.Default.SessionStartEvent), false), critical: true, observedUtc);
    }

    private void EmitProcessStarted(
        LedgerSession session,
        string sanitizedExePath,
        string exePathSha256,
        int windowsSessionId,
        string? exeSha256,
        long? exeBytes,
        string? exeUnknownReason,
        string? pvfSha256,
        long? pvfBytes,
        string? pvfUnknownReason,
        string declaredExeSha256,
        string declaredPvfSha256)
    {
        var seq = Interlocked.Increment(ref session.NextEventSeq);
        var monoTicks = Stopwatch.GetTimestamp();
        var observedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var threadId = CurrentThreadId();

        var ev = new ProcessStartedEvent(
            SchemaVersion: SchemaVersion,
            EventName: "process.started",
            Source: "launcher",
            ClientSessionId: session.ClientSessionId,
            IncidentId: session.IncidentId,
            EventSeq: seq,
            ObservedUtc: observedUtc,
            MonoTicks: monoTicks,
            MonoFrequencyHz: Stopwatch.Frequency,
            Pid: session.Pid,
            ProcessCreatedUtc: session.ProcessCreatedUtc,
            ThreadId: threadId,
            LaunchInstanceId: session.LaunchInstanceId,
            ExecutablePath: sanitizedExePath,
            ExecutablePathSha256: exePathSha256,
            HandoffMode: session.HandoffMode,
            ParentHandoffVerified: true,
            WindowsSessionId: windowsSessionId,
            ClientFileSha256: exeSha256,
            ClientFileBytes: exeBytes,
            ClientFileUnknownReason: exeUnknownReason,
            PvfFileSha256: pvfSha256,
            PvfFileBytes: pvfBytes,
            PvfFileUnknownReason: pvfUnknownReason,
            ManifestClientSha256: declaredExeSha256,
            ManifestPvfSha256: declaredPvfSha256);

        StoreLine(session, new StoredLine(seq, monoTicks, SerializeLine(ev, ContractDiagnosticsJson.Default.ProcessStartedEvent), false), critical: true, observedUtc);
    }

    private void EmitSocketTransition(LedgerSession session, SocketTransition t)
    {
        var seq = Interlocked.Increment(ref session.NextEventSeq);
        var monoTicks = Stopwatch.GetTimestamp();
        var observedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var threadId = CurrentThreadId();

        if (t.IsOpen)
        {
            var isUdp = t.Protocol == "udp";
            var ev = new ConnectionOpenEvent(
                SchemaVersion: SchemaVersion,
                EventName: "connection.open",
                Source: "launcher",
                ClientSessionId: session.ClientSessionId,
                IncidentId: session.IncidentId,
                EventSeq: seq,
                ObservedUtc: observedUtc,
                MonoTicks: monoTicks,
                MonoFrequencyHz: Stopwatch.Frequency,
                Pid: session.Pid,
                ProcessCreatedUtc: session.ProcessCreatedUtc,
                ThreadId: threadId,
                LaunchInstanceId: session.LaunchInstanceId,
                Protocol: t.Protocol,
                StreamId: t.StreamId,
                PreviousStreamId: t.PreviousStreamId,
                ReconnectToRemote: t.ReconnectToRemote,
                AddressFamily: t.AddressFamily,
                LocalAddress: t.LocalAddress,
                LocalPort: t.LocalPort,
                RemoteAddress: t.RemoteAddress,
                RemotePort: t.RemotePort,
                TcpState: t.TcpState,
                BindingState: t.BindingState,
                PeerConnected: isUdp ? null : true,
                PeerUnknownReason: isUdp ? "udp_table_local_bind_only_no_packet_capture" : null,
                PacketsIn: null,
                PacketsOut: null,
                FirstPacketUtc: null,
                LastPacketUtc: null,
                PacketStatsUnknownReason: "client_side_packet_capture_disabled",
                ConfiguredForceRelay: null,
                ConfiguredForceRelayUnknownReason: "client_config_not_observed_by_launcher",
                ObservedPathMode: "unknown",
                PathModeSource: "unobserved_no_packet_capture");

            StoreLine(session, new StoredLine(seq, monoTicks, SerializeLine(ev, ContractDiagnosticsJson.Default.ConnectionOpenEvent), false), critical: false, observedUtc);
        }
        else
        {
            var ev = new ConnectionCloseEvent(
                SchemaVersion: SchemaVersion,
                EventName: "connection.close",
                Source: "launcher",
                ClientSessionId: session.ClientSessionId,
                IncidentId: session.IncidentId,
                EventSeq: seq,
                ObservedUtc: observedUtc,
                MonoTicks: monoTicks,
                MonoFrequencyHz: Stopwatch.Frequency,
                Pid: session.Pid,
                ProcessCreatedUtc: session.ProcessCreatedUtc,
                ThreadId: threadId,
                LaunchInstanceId: session.LaunchInstanceId,
                Protocol: t.Protocol,
                StreamId: t.StreamId,
                PreviousStreamId: t.PreviousStreamId,
                ReconnectToRemote: t.ReconnectToRemote,
                AddressFamily: t.AddressFamily,
                LocalAddress: t.LocalAddress,
                LocalPort: t.LocalPort,
                RemoteAddress: t.RemoteAddress,
                RemotePort: t.RemotePort,
                TcpState: t.TcpState,
                BindingState: t.BindingState,
                CloseReason: t.CloseReason ?? "unknown",
                CloseReasonSource: t.CloseReasonSource ?? "os_socket_table_poll",
                DurationMs: t.DurationMs);

            StoreLine(session, new StoredLine(seq, monoTicks, SerializeLine(ev, ContractDiagnosticsJson.Default.ConnectionCloseEvent), false), critical: false, observedUtc);
        }
    }

    private void EmitClockAnchor(
        LedgerSession session,
        string clockSource,
        string? serverUtc,
        double? offsetMs,
        double? uncertaintyMs,
        string? unknownReason,
        string? httpTraceId)
    {
        var seq = Interlocked.Increment(ref session.NextEventSeq);
        var monoTicks = Stopwatch.GetTimestamp();
        var observedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var threadId = CurrentThreadId();

        var ev = new ClockAnchorEvent(
            SchemaVersion: SchemaVersion,
            EventName: "clock.anchor",
            Source: "launcher",
            ClientSessionId: session.ClientSessionId,
            IncidentId: session.IncidentId,
            EventSeq: seq,
            ObservedUtc: observedUtc,
            MonoTicks: monoTicks,
            MonoFrequencyHz: Stopwatch.Frequency,
            Pid: session.Pid,
            ProcessCreatedUtc: session.ProcessCreatedUtc,
            ThreadId: threadId,
            LaunchInstanceId: session.LaunchInstanceId,
            ClockSource: clockSource,
            LocalUtcOffsetMinutes: (int)DateTimeOffset.Now.Offset.TotalMinutes,
            ServerUtc: serverUtc,
            ServerClockOffsetMs: offsetMs,
            ServerClockUncertaintyMs: uncertaintyMs,
            ServerClockUnknownReason: unknownReason,
            HttpTraceId: httpTraceId,
            QpcCrossHostComparable: false);

        StoreLine(session, new StoredLine(seq, monoTicks, SerializeLine(ev, ContractDiagnosticsJson.Default.ClockAnchorEvent), false), critical: clockSource != "local_system_clock", observedUtc);
    }

    private void EmitProcessExited(LedgerSession session, int? exitCode, string classification)
    {
        var seq = Interlocked.Increment(ref session.NextEventSeq);
        var monoTicks = Stopwatch.GetTimestamp();
        var observedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var threadId = CurrentThreadId();
        var durationMs = Math.Round(Math.Max(0, (monoTicks - session.StartedMonoTicks) * 1000.0 / Stopwatch.Frequency), 3);

        var ev = new ProcessExitedEvent(
            SchemaVersion: SchemaVersion,
            EventName: "process.exited",
            Source: "launcher",
            ClientSessionId: session.ClientSessionId,
            IncidentId: session.IncidentId,
            EventSeq: seq,
            ObservedUtc: observedUtc,
            MonoTicks: monoTicks,
            MonoFrequencyHz: Stopwatch.Frequency,
            Pid: session.Pid,
            ProcessCreatedUtc: session.ProcessCreatedUtc,
            ThreadId: threadId,
            LaunchInstanceId: session.LaunchInstanceId,
            ExitCode: exitCode,
            ExitCodeHex: exitCode is { } code ? $"0x{(uint)code:X8}" : null,
            ExitClassification: classification,
            ServerBusinessReasonCode: session.LastBusinessDisconnectReason,
            ServerBusinessReasonUnknownReason: session.LastBusinessDisconnectReason is null ? "no_server_business_disconnect_reported" : null,
            SessionDurationMs: durationMs);

        StoreLine(session, new StoredLine(seq, monoTicks, SerializeLine(ev, ContractDiagnosticsJson.Default.ProcessExitedEvent), false), critical: true, observedUtc);
    }

    private void EmitTraceIntegrity(LedgerSession session, string reason)
    {
        var seq = Interlocked.Increment(ref session.NextEventSeq);
        var monoTicks = Stopwatch.GetTimestamp();
        var observedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var threadId = CurrentThreadId();

        TraceIntegrityEvent ev;
        lock (session.Gate)
        {
            var firstSeq = ComputeFirstRetainedSeqLocked(session, seq);
            ev = new TraceIntegrityEvent(
                SchemaVersion: SchemaVersion,
                EventName: "trace.integrity",
                Source: "launcher",
                ClientSessionId: session.ClientSessionId,
                IncidentId: session.IncidentId,
                EventSeq: seq,
                ObservedUtc: observedUtc,
                MonoTicks: monoTicks,
                MonoFrequencyHz: Stopwatch.Frequency,
                Pid: session.Pid,
                ProcessCreatedUtc: session.ProcessCreatedUtc,
                ThreadId: threadId,
                LaunchInstanceId: session.LaunchInstanceId,
                QueueDropped: session.QueueDropped,
                CriticalEventsLost: session.CriticalEventsLost,
                TruncatedRecords: session.TruncatedRecords + (session.SocketTracker?.DroppedOverflowCount ?? 0),
                RingEvictedRecords: session.RingEvictedRecords,
                CaptureDroppedPackets: null,
                CaptureDroppedPacketsUnknownReason: "client_side_packet_capture_disabled",
                FirstEventSeq: firstSeq,
                LastEventSeq: seq,
                BudgetBytesLimit: MaxBytesPerSession,
                BudgetBytesUsed: session.TotalRetainedBytes,
                BulkCollectionPaused: session.BulkCollectionPaused,
                NativeEventsCapability: false,
                NativeEventsUnknownReason: "launcher_only_no_client_event_provider",
                Reason: reason);
        }

        StoreLine(session, new StoredLine(seq, monoTicks, SerializeLine(ev, ContractDiagnosticsJson.Default.TraceIntegrityEvent), false), critical: true, observedUtc);
    }

    private void StoreLine(LedgerSession session, StoredLine line, bool critical, string observedUtc)
    {
        var hitThresholdNow = false;
        lock (session.Gate)
        {
            session.LastEventSeq = Math.Max(session.LastEventSeq, line.Seq);
            session.LatestEventUtc = observedUtc;

            if (critical)
            {
                if (session.TotalRetainedBytes + line.Bytes.Length > MaxBytesPerSession)
                {
                    // Reclaim unpinned routine ring space first before ever losing a critical event.
                    while (session.SlidingRoutineRing.Count > 0 && session.TotalRetainedBytes + line.Bytes.Length > MaxBytesPerSession)
                    {
                        var evicted = session.SlidingRoutineRing.Dequeue();
                        session.SlidingRoutineBytes -= evicted.Bytes.Length;
                        session.RingEvictedRecords++;
                    }
                }
                if (session.TotalRetainedBytes + line.Bytes.Length > MaxBytesPerSession)
                {
                    session.CriticalEventsLost++;
                    return;
                }
                session.CriticalLines.Add(line);
                session.CriticalBytes += line.Bytes.Length;
                session.Dirty = true;
            }
            else
            {
                var routineLimit = MaxBytesPerSession - CriticalReserveBytes;
                EvictExpiredRoutineLocked(session, line.MonoTicks);
                if (session.BulkCollectionPaused || session.TotalRetainedBytes + line.Bytes.Length > routineLimit)
                {
                    if (!session.BulkCollectionPaused)
                    {
                        session.BulkCollectionPaused = true;
                        hitThresholdNow = true;
                    }
                    session.TruncatedRecords++;
                    return;
                }
                if (line.MonoTicks <= session.PostTriggerUntilTicks)
                {
                    session.PinnedRoutineLines.Add(line);
                    session.PinnedRoutineBytes += line.Bytes.Length;
                    session.Dirty = true;
                }
                else
                {
                    session.SlidingRoutineRing.Enqueue(line);
                    session.SlidingRoutineBytes += line.Bytes.Length;
                }
            }
            session.FirstRetainedSeq = ComputeFirstRetainedSeqLocked(session, session.FirstRetainedSeq);
        }

        if (hitThresholdNow)
        {
            EmitTraceIntegrity(session, "budget_threshold_reached");
        }
    }

    private static void EvictExpiredRoutineLocked(LedgerSession session, long nowTicks)
    {
        var windowTicks = PreTriggerSeconds * Stopwatch.Frequency;
        while (session.SlidingRoutineRing.Count > 0 && nowTicks - session.SlidingRoutineRing.Peek().MonoTicks > windowTicks)
        {
            var old = session.SlidingRoutineRing.Dequeue();
            session.SlidingRoutineBytes -= old.Bytes.Length;
            session.RingEvictedRecords++;
        }
    }

    private static long ComputeFirstRetainedSeqLocked(LedgerSession session, long fallback)
    {
        var min = long.MaxValue;
        if (session.CriticalLines.Count > 0) min = Math.Min(min, session.CriticalLines.Min(l => l.Seq));
        if (session.PinnedRoutineLines.Count > 0) min = Math.Min(min, session.PinnedRoutineLines.Min(l => l.Seq));
        if (session.SlidingRoutineRing.Count > 0) min = Math.Min(min, session.SlidingRoutineRing.Min(l => l.Seq));
        return min == long.MaxValue ? fallback : min;
    }

    private static void PinIncidentOnSession(LedgerSession session, string incidentId)
    {
        var nowTicks = Stopwatch.GetTimestamp();
        lock (session.Gate)
        {
            session.IncidentId ??= incidentId;
            EvictExpiredRoutineLocked(session, nowTicks);
            while (session.SlidingRoutineRing.Count > 0)
            {
                var line = session.SlidingRoutineRing.Dequeue();
                session.SlidingRoutineBytes -= line.Bytes.Length;
                session.PinnedRoutineLines.Add(line);
                session.PinnedRoutineBytes += line.Bytes.Length;
            }
            session.PostTriggerUntilTicks = Math.Max(session.PostTriggerUntilTicks, nowTicks + PostTriggerSeconds * Stopwatch.Frequency);
            session.Dirty = true;
        }
    }

    private (byte[][] AllLines, byte[][] ObservationLines, ContractSessionDescriptor Descriptor, ContractSessionIntegrity Integrity) SnapshotSessionForExport(LedgerSession s)
    {
        lock (s.Gate)
        {
            var merged = s.CriticalLines
                .Concat(s.PinnedRoutineLines)
                .Concat(s.SlidingRoutineRing)
                .OrderBy(l => l.Seq)
                .ToArray();

            var allBytes = merged.Select(l => l.Bytes).ToArray();
            var obsBytes = merged.Where(l => l.IsObservation).Select(l => l.Bytes).ToArray();
            var firstSeq = merged.Length > 0 ? merged[0].Seq : s.FirstRetainedSeq;
            var lastSeq = merged.Length > 0 ? merged[^1].Seq : s.LastEventSeq;
            var endedOrLatest = s.EndedUtc ?? (s.LatestEventUtc.Length > 0 ? s.LatestEventUtc : s.StartedUtc);

            var descriptor = new ContractSessionDescriptor(
                ClientSessionId: s.ClientSessionId,
                LaunchInstanceId: s.LaunchInstanceId,
                LaunchOrdinal: s.LaunchOrdinal,
                ActiveSlot: s.ActiveSlot,
                Pid: s.Pid,
                ProcessCreatedUtc: s.ProcessCreatedUtc,
                ExecutablePath: s.ExecutablePath,
                ExecutablePathSha256: s.ExecutablePathSha256,
                HandoffMode: s.HandoffMode,
                StartedUtc: s.StartedUtc,
                EndedUtc: s.EndedUtc,
                EndState: s.EndState,
                ReceiverCharacterId: s.ReceiverCharacterId,
                ReceiverCharacterName: s.ReceiverCharacterName,
                AssociationSource: s.AssociationSource,
                Entries: [.. s.Entries],
                FirstEventSeq: firstSeq,
                LastEventSeq: lastSeq,
                RetainedEvents: merged.Length,
                ObservationsCount: obsBytes.Length);

            var integrity = new ContractSessionIntegrity(
                ClientSessionId: s.ClientSessionId,
                LaunchInstanceId: s.LaunchInstanceId,
                Pid: s.Pid,
                ProcessCreatedUtc: s.ProcessCreatedUtc,
                FirstEventSeq: firstSeq,
                LastEventSeq: lastSeq,
                RetainedEvents: merged.Length,
                QueueDropped: s.QueueDropped,
                CriticalEventsLost: s.CriticalEventsLost,
                TruncatedRecords: s.TruncatedRecords + (s.SocketTracker?.DroppedOverflowCount ?? 0),
                RingEvictedRecords: s.RingEvictedRecords,
                CaptureDroppedPackets: null,
                CaptureDroppedPacketsUnknownReason: "client_side_packet_capture_disabled",
                BudgetBytesLimit: MaxBytesPerSession,
                BudgetBytesUsed: s.TotalRetainedBytes,
                BulkCollectionPaused: s.BulkCollectionPaused,
                CoverageStartedUtc: s.StartedUtc,
                CoverageEndedUtc: endedOrLatest,
                CompletionStatus: s.Running ? "running" : s.EndState == "prelaunch_snapshot" ? "prelaunch_snapshot" : "process_exited");

            return (allBytes, obsBytes, descriptor, integrity);
        }
    }

    private void PollSessionSockets(LedgerSession session)
    {
        if (session.SocketTracker is null || !session.Running) return;
        var transitions = session.SocketTracker.Poll();
        foreach (var t in transitions)
        {
            EmitSocketTransition(session, t);
        }
    }

    private void CheckProcessExitAndSockets(LedgerSession session)
    {
        if (!session.Running || session.Pid is not { } pid || session.ProcessCreatedFileTimeUtc is not { } created) return;

        PollSessionSockets(session);

        var nowTicks = Stopwatch.GetTimestamp();
        if (nowTicks >= session.NextClockAnchorTicks)
        {
            session.NextClockAnchorTicks = nowTicks + 30 * Stopwatch.Frequency;
            EmitClockAnchor(session, "local_system_clock", null, null, null, session.ServerClockAnchored ? null : "periodic_local_anchor", null);
        }

        if (HasProcessExited(pid, created, out var exitCode))
        {
            CompleteSessionExit(session, exitCode);
        }
    }

    internal void PollSocketsOnce(string clientSessionId)
    {
        var session = RequireSession(clientSessionId);
        PollSessionSockets(session);
        SchedulePersist(session);
    }

    internal void CompleteSessionExit(string clientSessionId, int? exitCode, string? explicitClassification = null)
    {
        var session = RequireSession(clientSessionId);
        CompleteSessionExit(session, exitCode, explicitClassification);
    }

    internal void AppendSyntheticRoutineForBudgetTest(string clientSessionId, string payloadNote)
    {
        var session = RequireSession(clientSessionId);
        var seq = Interlocked.Increment(ref session.NextEventSeq);
        var monoTicks = Stopwatch.GetTimestamp();
        var observedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        var threadId = CurrentThreadId();
        var ev = new ClockAnchorEvent(
            SchemaVersion: SchemaVersion,
            EventName: "clock.anchor",
            Source: "launcher",
            ClientSessionId: session.ClientSessionId,
            IncidentId: session.IncidentId,
            EventSeq: seq,
            ObservedUtc: observedUtc,
            MonoTicks: monoTicks,
            MonoFrequencyHz: Stopwatch.Frequency,
            Pid: session.Pid,
            ProcessCreatedUtc: session.ProcessCreatedUtc,
            ThreadId: threadId,
            LaunchInstanceId: session.LaunchInstanceId,
            ClockSource: "local_system_clock",
            LocalUtcOffsetMinutes: (int)Math.Round(TimeZoneInfo.Local.GetUtcOffset(DateTime.UtcNow).TotalMinutes),
            ServerUtc: null,
            ServerClockOffsetMs: null,
            ServerClockUncertaintyMs: null,
            ServerClockUnknownReason: payloadNote,
            HttpTraceId: null,
            QpcCrossHostComparable: false);
        var bytes = SerializeLine(ev, ContractDiagnosticsJson.Default.ClockAnchorEvent);
        StoreLine(session, new StoredLine(seq, monoTicks, bytes, IsObservation: false), critical: false, observedUtc);
    }

    private void CompleteSessionExit(LedgerSession session, int? exitCode, string? explicitClassification = null)
    {
        lock (session.Gate)
        {
            if (!session.Running) return;
            session.Running = false;
            session.EndedUtc = DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ");
        }

        if (session.SocketTracker is not null)
        {
            var reason = session.LastBusinessDisconnectReason is not null ? "server_business_disconnect" : "process_exited";
            var source = session.LastBusinessDisconnectReason is not null ? "authenticated_server_context" : "process_lifecycle";
            foreach (var closed in session.SocketTracker.CloseAll(reason, source))
            {
                EmitSocketTransition(session, closed);
            }
        }

        var durationSec = (Stopwatch.GetTimestamp() - session.StartedMonoTicks) / (double)Stopwatch.Frequency;
        var classification = explicitClassification ?? ClassifyExit(exitCode, durationSec, session.LastBusinessDisconnectReason);
        session.EndState = classification;

        if (classification is not "normal_exit_zero" && session.IncidentId is not null)
        {
            PinIncidentOnSession(session, session.IncidentId);
        }

        EmitProcessExited(session, exitCode, classification);
        EmitTraceIntegrity(session, "session_end");
        SchedulePersist(session);
        NotifyChanged();
    }

    private static string ClassifyExit(int? exitCode, double durationSeconds, string? businessDisconnectReason)
    {
        if (businessDisconnectReason is not null) return "server_business_disconnect_then_exit";
        if (exitCode is null) return "exit_code_unavailable";
        if (exitCode == 0) return "normal_exit_zero";
        var unsigned = (uint)exitCode.Value;
        if (unsigned >= 0xC0000000u) return "ntstatus_crash_code";
        if (durationSeconds < 8.0) return "early_exit_within_8s";
        return "nonzero_exit_code";
    }

    private bool HasProcessExited(int pid, long expectedCreatedFileTimeUtc, out int? exitCode)
    {
        exitCode = null;
        if (customSocketReader is not null)
        {
            var (verified, _, _) = customSocketReader(pid, expectedCreatedFileTimeUtc);
            return !verified;
        }
        if (!OperatingSystem.IsWindows()) return false;
        using var handle = OpenProcess(0x1000, false, pid);
        if (handle.IsInvalid) return true;
        if (!GetProcessTimes(handle, out var created, out var exited, out _, out _)) return true;
        if (created != expectedCreatedFileTimeUtc) return true;
        if (!GetExitCodeProcess(handle, out var code)) return true;
        if (code != 259)
        {
            exitCode = code;
            return true;
        }
        if (exited != 0)
        {
            exitCode = code;
            return true;
        }
        return false;
    }

    private async Task PollLoop()
    {
        while (!lifetime.IsCancellationRequested)
        {
            try
            {
                LedgerSession[] active;
                lock (stateGate) active = sessions.Where(s => s.Running).ToArray();
                foreach (var s in active)
                {
                    CheckProcessExitAndSockets(s);
                    if (s.Dirty) SchedulePersist(s);
                }
            }
            catch (Exception)
            {
                // Never allow background diagnostic polling to fault host process.
            }
            try { await Task.Delay(500, lifetime.Token); } catch (OperationCanceledException) { break; }
        }
    }

    private void SchedulePersist(LedgerSession session)
    {
        lock (session.Gate) session.Dirty = false;
        if (!diskQueue.Writer.TryWrite(() => PersistSessionAsync(session)))
        {
            Interlocked.Increment(ref session.QueueDropped);
        }
    }

    private async Task FlushDiskBarrierAsync()
    {
        var tcs = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        await diskQueue.Writer.WriteAsync(() => { tcs.TrySetResult(); return Task.CompletedTask; });
        await tcs.Task;
    }

    private async Task DiskLoop()
    {
        while (await diskQueue.Reader.WaitToReadAsync())
        {
            while (diskQueue.Reader.TryRead(out var work))
            {
                try { await work(); } catch (Exception) { }
            }
        }
    }

    private Task PersistSessionAsync(LedgerSession session)
    {
        try
        {
            SafeFiles.NoLinks(root);
            Directory.CreateDirectory(sessionsRoot);
            SafeFiles.NoLinks(sessionsRoot);
            var dir = Path.Combine(sessionsRoot, session.ClientSessionId);
            Directory.CreateDirectory(dir);
            SafeFiles.NoLinks(dir);

            var (allLines, obsLines, descriptor, integrity) = SnapshotSessionForExport(session);
            using (var ms = new MemoryStream())
            {
                foreach (var l in allLines) ms.Write(l, 0, l.Length);
                SafeFiles.Atomic(Path.Combine(dir, "launcher.jsonl"), ms.ToArray());
            }
            using (var ms = new MemoryStream())
            {
                foreach (var l in obsLines) ms.Write(l, 0, l.Length);
                SafeFiles.Atomic(Path.Combine(dir, "observations.jsonl"), ms.ToArray());
            }
            var meta = new ContractPersistedSessionMeta(descriptor, integrity, session.IncidentId, DateTimeOffset.UtcNow.ToUnixTimeMilliseconds());
            SafeFiles.Atomic(Path.Combine(dir, "meta.json"), AppendNewline(JsonSerializer.SerializeToUtf8Bytes(meta, ContractDiagnosticsJson.Default.ContractPersistedSessionMeta)));
            CleanupPersistedDirectories();
        }
        catch (Exception)
        {
            // Disk errors are isolated from game/launcher operations.
        }
        return Task.CompletedTask;
    }

    private void CleanupPersistedDirectories()
    {
        try
        {
            if (!Directory.Exists(sessionsRoot)) return;
            var items = new List<(string Dir, string StartedUtc, string? IncidentId)>();
            foreach (var dir in Directory.EnumerateDirectories(sessionsRoot))
            {
                var metaFile = Path.Combine(dir, "meta.json");
                if (!File.Exists(metaFile)) continue;
                try
                {
                    var meta = JsonSerializer.Deserialize(File.ReadAllBytes(metaFile), ContractDiagnosticsJson.Default.ContractPersistedSessionMeta);
                    if (meta is not null) items.Add((dir, meta.Descriptor.StartedUtc, meta.IncidentId));
                }
                catch (Exception) { }
            }

            var ordered = items.OrderByDescending(i => i.StartedUtc, StringComparer.Ordinal).ToArray();
            var retainedIncidents = new HashSet<string>(StringComparer.Ordinal);
            var keptCount = 0;
            foreach (var item in ordered)
            {
                var keep = false;
                if (item.IncidentId is { Length: > 0 } inc)
                {
                    if (retainedIncidents.Contains(inc) || retainedIncidents.Count < MaxIncidents)
                    {
                        retainedIncidents.Add(inc);
                        keep = keptCount < MaxRetainedSessions;
                    }
                }
                else if (keptCount < MaxIncidents)
                {
                    keep = true;
                }

                if (keep) keptCount++;
                else
                {
                    try { Directory.Delete(item.Dir, recursive: true); } catch (IOException) { }
                }
            }
        }
        catch (Exception) { }
    }

    private void LoadPersistedSessions()
    {
        try
        {
            if (!Directory.Exists(sessionsRoot)) return;
            SafeFiles.NoLinks(sessionsRoot);
            var loaded = new List<LedgerSession>();
            foreach (var dir in Directory.EnumerateDirectories(sessionsRoot))
            {
                var name = Path.GetFileName(dir);
                if (!Regex.IsMatch(name, "^cs-[a-f0-9]{32}$")) continue;
                var metaPath = Path.Combine(dir, "meta.json");
                var launcherPath = Path.Combine(dir, "launcher.jsonl");
                if (!File.Exists(metaPath) || !File.Exists(launcherPath)) continue;
                var metaInfo = new FileInfo(metaPath);
                var launcherInfo = new FileInfo(launcherPath);
                if (metaInfo.Length > 256 * 1024 || launcherInfo.Length > MaxBytesPerSession) continue;

                var meta = JsonSerializer.Deserialize(File.ReadAllBytes(metaPath), ContractDiagnosticsJson.Default.ContractPersistedSessionMeta);
                if (meta is null || meta.Descriptor.ClientSessionId != name) continue;

                var s = new LedgerSession
                {
                    ClientSessionId = meta.Descriptor.ClientSessionId,
                    LaunchInstanceId = meta.Descriptor.LaunchInstanceId,
                    LaunchOrdinal = meta.Descriptor.LaunchOrdinal,
                    ActiveSlot = meta.Descriptor.ActiveSlot,
                    StartedUtc = meta.Descriptor.StartedUtc,
                    StartedMonoTicks = Stopwatch.GetTimestamp(),
                    LatestEventUtc = meta.Integrity.CoverageEndedUtc,
                    HandoffMode = meta.Descriptor.HandoffMode,
                    Pid = meta.Descriptor.Pid,
                    ProcessCreatedUtc = meta.Descriptor.ProcessCreatedUtc,
                    ExecutablePath = meta.Descriptor.ExecutablePath,
                    ExecutablePathSha256 = meta.Descriptor.ExecutablePathSha256,
                    Running = false,
                    EndedUtc = meta.Descriptor.EndedUtc ?? meta.Integrity.CoverageEndedUtc,
                    EndState = meta.Descriptor.EndState == "running" ? "launcher_restarted_recovered" : meta.Descriptor.EndState,
                    IncidentId = meta.IncidentId,
                    ReceiverCharacterId = meta.Descriptor.ReceiverCharacterId,
                    ReceiverCharacterName = meta.Descriptor.ReceiverCharacterName,
                    AssociationSource = meta.Descriptor.AssociationSource,
                    CurrentEntryId = meta.Descriptor.Entries.LastOrDefault()?.EntryId,
                    SceneEpoch = meta.Descriptor.Entries.LastOrDefault()?.SceneEpoch ?? 0,
                    NextEventSeq = meta.Integrity.LastEventSeq,
                    FirstRetainedSeq = meta.Integrity.FirstEventSeq,
                    LastEventSeq = meta.Integrity.LastEventSeq,
                    QueueDropped = meta.Integrity.QueueDropped,
                    CriticalEventsLost = meta.Integrity.CriticalEventsLost,
                    TruncatedRecords = meta.Integrity.TruncatedRecords,
                    RingEvictedRecords = meta.Integrity.RingEvictedRecords,
                    BulkCollectionPaused = meta.Integrity.BulkCollectionPaused
                };
                s.Entries.AddRange(meta.Descriptor.Entries);

                foreach (var rawLine in File.ReadLines(launcherPath))
                {
                    if (string.IsNullOrWhiteSpace(rawLine)) continue;
                    using var doc = JsonDocument.Parse(rawLine);
                    var rootEl = doc.RootElement;
                    var seq = rootEl.GetProperty("event_seq").GetInt64();
                    var ticks = rootEl.GetProperty("mono_ticks").GetInt64();
                    var evName = rootEl.GetProperty("event").GetString() ?? "";
                    var isObs = evName == "observation.reported";
                    var utf8 = Encoding.UTF8.GetBytes(rawLine + "\n");
                    s.CriticalLines.Add(new StoredLine(seq, ticks, utf8, isObs));
                    s.CriticalBytes += utf8.Length;
                    if (isObs)
                    {
                        var obs = JsonSerializer.Deserialize(rawLine, ContractDiagnosticsJson.Default.ObservationReportedEvent);
                        if (obs is not null)
                        {
                            s.ObservationItems.Add(new ContractObservationItem(
                                s.ClientSessionId,
                                s.LaunchInstanceId,
                                s.Pid,
                                obs.EventSeq,
                                obs.ObservedUtc,
                                obs.EntryId,
                                obs.SceneEpoch,
                                obs.ReceiverCharacterName,
                                obs.PeerScope,
                                obs.PeerCharacterName,
                                obs.State,
                                obs.EstimatedOnsetUtc,
                                obs.TimeUncertaintyMs));
                        }
                    }
                }
                loaded.Add(s);
            }

            string? persistedIncident = null;
            var incidentFile = Path.Combine(root, "active-incident.txt");
            if (File.Exists(incidentFile))
            {
                try
                {
                    var rawIncident = File.ReadAllText(incidentFile, Encoding.UTF8).Trim();
                    if (!string.IsNullOrWhiteSpace(rawIncident)) persistedIncident = ValidateIncidentId(rawIncident);
                }
                catch (Exception) { }
            }

            lock (stateGate)
            {
                foreach (var item in loaded.OrderBy(x => x.StartedUtc, StringComparer.Ordinal).TakeLast(MaxRetainedSessions))
                {
                    sessions.Add(item);
                    launchCounter = Math.Max(launchCounter, item.LaunchOrdinal);
                }
                activeIncidentId = persistedIncident
                    ?? sessions.LastOrDefault(x => !string.IsNullOrWhiteSpace(x.IncidentId))?.IncidentId;
            }
        }
        catch (Exception) { }
    }

    private void TrimInMemorySessionsLocked()
    {
        while (sessions.Count > MaxRetainedSessions)
        {
            var victim = sessions.FirstOrDefault(s => !s.Running);
            if (victim is null) break;
            sessions.Remove(victim);
        }
    }

    private LedgerSession RequireSession(string clientSessionId)
    {
        lock (stateGate)
        {
            if (string.IsNullOrWhiteSpace(clientSessionId))
            {
                return sessions.LastOrDefault(s => s.Running)
                    ?? sessions.LastOrDefault()
                    ?? throw new InvalidDataException("当前尚无游戏会话记录，请先启动一次游戏。");
            }
            return sessions.FirstOrDefault(s => s.ClientSessionId == clientSessionId)
                ?? throw new InvalidDataException("指定的游戏会话不存在或已清理。");
        }
    }

    private void WarmFileDigests(string directory)
    {
        try
        {
            ComputeFileDigest(Path.Combine(directory, "DNF.exe"));
            ComputeFileDigest(Path.Combine(directory, "Script.pvf"), allowAsyncDefer: false);
        }
        catch (Exception) { }
    }

    internal (string? Sha256, long? Bytes, string? UnknownReason) ComputeFileDigest(string fullPath, bool allowAsyncDefer = false)
    {
        try
        {
            if (string.IsNullOrWhiteSpace(fullPath)) return (null, null, "path_empty");
            var normalized = Path.GetFullPath(fullPath);
            if (!File.Exists(normalized)) return (null, null, "file_not_found");
            var info = new FileInfo(normalized);
            var len = info.Length;
            var ticks = info.LastWriteTimeUtc.Ticks;

            lock (digestCache)
            {
                if (digestCache.TryGetValue(normalized, out var cached) && cached.Length == len && cached.LastWriteTicks == ticks)
                {
                    return (cached.Sha256, len, null);
                }
            }

            if (allowAsyncDefer && len > 64 * 1024 * 1024)
            {
                _ = Task.Run(() => ComputeFileDigest(normalized, allowAsyncDefer: false));
                return (null, len, "computing_large_file_digest_async");
            }

            using var fs = new FileStream(normalized, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
            var hash = Convert.ToHexString(SHA256.HashData(fs)).ToLowerInvariant();
            lock (digestCache)
            {
                digestCache[normalized] = (len, ticks, hash);
            }
            return (hash, len, null);
        }
        catch (UnauthorizedAccessException)
        {
            return (null, null, "access_denied");
        }
        catch (IOException)
        {
            return (null, null, "io_read_failed");
        }
    }

    internal static string SanitizeExecutablePath(string fullPath)
    {
        if (string.IsNullOrWhiteSpace(fullPath)) return "";
        var normalized = Path.GetFullPath(fullPath);
        var userProfile = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        if (!string.IsNullOrWhiteSpace(userProfile))
        {
            var profilePrefix = Path.GetFullPath(userProfile).TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar);
            if (normalized.StartsWith(profilePrefix + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase))
            {
                return "%USERPROFILE%" + normalized[profilePrefix.Length..];
            }
        }
        return normalized;
    }

    private static string NewIncidentId() =>
        $"inc-{DateTime.UtcNow:yyyyMMddHHmmss}-{Guid.NewGuid().ToString("N")[..8]}";

    private static string ValidateIncidentId(string raw)
    {
        var trimmed = raw.Trim();
        if (trimmed.Length is < 4 or > 80 || !Regex.IsMatch(trimmed, "^[a-zA-Z0-9._:-]+$"))
        {
            throw new InvalidDataException("问题单号格式无效（仅限 4-80 位字母、数字、连字符或下划线）。");
        }
        return trimmed;
    }

    private static string SanitizeToken(string? text, int maxLen)
    {
        if (string.IsNullOrWhiteSpace(text)) return "unknown";
        var sb = new StringBuilder(Math.Min(text.Length, maxLen));
        foreach (var ch in text.Trim())
        {
            if (sb.Length >= maxLen) break;
            if (char.IsAsciiLetterOrDigit(ch) || ch is '_' or '-' or '.' or '#' or ':') sb.Append(ch);
        }
        return sb.Length > 0 ? sb.ToString() : "unknown";
    }

    private static string? SanitizeDisplayName(string? text, int maxLen)
    {
        if (string.IsNullOrWhiteSpace(text)) return null;
        var sb = new StringBuilder(Math.Min(text.Length, maxLen));
        foreach (var ch in text.Trim())
        {
            if (sb.Length >= maxLen) break;
            if (!char.IsControl(ch)) sb.Append(ch);
        }
        return sb.Length > 0 ? sb.ToString() : null;
    }

    private static byte[] SerializeLine<T>(T value, System.Text.Json.Serialization.Metadata.JsonTypeInfo<T> typeInfo) =>
        AppendNewline(JsonSerializer.SerializeToUtf8Bytes(value, typeInfo));

    private static byte[] AppendNewline(byte[] raw)
    {
        var result = new byte[raw.Length + 1];
        raw.CopyTo(result, 0);
        result[^1] = (byte)'\n';
        return result;
    }

    private static string Sha256Hex(ReadOnlySpan<byte> data) =>
        Convert.ToHexString(SHA256.HashData(data)).ToLowerInvariant();

    internal static uint CurrentThreadId() =>
        OperatingSystem.IsWindows() ? GetCurrentThreadId() : (uint)Environment.CurrentManagedThreadId;

    private void NotifyChanged()
    {
        try { Changed?.Invoke(); } catch (Exception) { }
    }

    public void Dispose()
    {
        lifetime.Cancel();
        try { pollWorker.GetAwaiter().GetResult(); } catch (Exception) { }

        LedgerSession[] active;
        lock (stateGate) active = sessions.Where(s => s.Running).ToArray();
        foreach (var s in active)
        {
            if (s.Pid is { } pid && s.ProcessCreatedFileTimeUtc is { } created && HasProcessExited(pid, created, out var code))
            {
                CompleteSessionExit(s, code);
            }
            else
            {
                EmitTraceIntegrity(s, "session_end");
                PersistSessionAsync(s).GetAwaiter().GetResult();
            }
        }

        diskQueue.Writer.TryComplete();
        try { diskWorker.GetAwaiter().GetResult(); } catch (Exception) { }
        lifetime.Dispose();
    }

    [DllImport("kernel32.dll")]
    private static extern uint GetCurrentThreadId();

    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern Microsoft.Win32.SafeHandles.SafeProcessHandle OpenProcess(uint access, [MarshalAs(UnmanagedType.Bool)] bool inherit, int pid);

    [DllImport("kernel32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetProcessTimes(Microsoft.Win32.SafeHandles.SafeProcessHandle handle, out long created, out long exited, out long kernel, out long user);

    [DllImport("kernel32.dll")]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool GetExitCodeProcess(Microsoft.Win32.SafeHandles.SafeProcessHandle handle, out int code);
}
