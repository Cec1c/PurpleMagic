using System.Text.Json.Serialization;

namespace Dnf90.Launcher.Diagnostics;

public sealed record ContractSessionStatus(
    string ClientSessionId,
    string LaunchInstanceId,
    int? Pid,
    string? ProcessCreatedUtc,
    bool Running,
    string StartedUtc,
    string? EndedUtc,
    string? IncidentId,
    long? ReceiverCharacterId,
    string? ReceiverCharacterName,
    string? AssociationSource,
    string? CurrentEntryId,
    int SceneEpoch,
    long EventCount,
    int ObservationCount);

public sealed record ContractObservationItem(
    string ClientSessionId,
    string LaunchInstanceId,
    int? Pid,
    long EventSeq,
    string ObservedUtc,
    string? EntryId,
    int SceneEpoch,
    string? ReceiverCharacterName,
    string PeerScope,
    string? PeerCharacterName,
    string State,
    string? EstimatedOnsetUtc,
    double? TimeUncertaintyMs);

public sealed record ContractCharacterInput(
    string ClientSessionId,
    string? ReceiverCharacterName,
    long? ReceiverCharacterId = null);

public sealed record ContractObservationInput(
    string ClientSessionId,
    string State,
    string PeerScope = "all_mutual_invisible",
    string? PeerCharacterName = null,
    long? PeerCharacterId = null,
    int? OnsetOffsetSeconds = null,
    double? TimeUncertaintyMs = null,
    string? Note = null);

public sealed record ContractIncidentInput(
    string? IncidentId = null,
    bool GenerateNew = false);

public sealed record ContractEntryInput(
    string ClientSessionId,
    string EntryKind = "user_marked_entry");

public sealed record ContractBundleExportInput(
    string Path,
    string? IncidentId = null,
    string? ClientSessionId = null);

internal sealed record ContractHttpTraceItem(
    string HttpTraceId,
    string EndpointKind,
    int HttpStatus,
    double DurationMs,
    string ObservedUtc,
    long MonoTicks);

internal sealed record ContractServerEndpoint(
    string GameHost,
    int BootstrapPort,
    int ChannelServerId,
    int? PreviousChannelServerId,
    string ApiOrigin);

internal sealed record ContractDeclaredDigests(
    string ManifestClientSha256,
    string ManifestPvfSha256,
    long PublicationSequence,
    string ContentVersion,
    string SourceId);

internal sealed record ContractTimePolicy(
    bool UtcForCrossMachineAlignment,
    bool QpcForLocalIntervals,
    bool CompareQpcAcrossHosts,
    bool RawQpcFrequencyRequired,
    long HostMonoFrequencyHz);

internal sealed record ContractLimits(
    int PreTriggerSeconds,
    int PostTriggerSeconds,
    long MaxBytesPerSession,
    int MaxIncidents);

internal sealed record ContractOptionalPayload(
    bool Present,
    string? UnknownReason);

internal sealed record ContractEntryDescriptor(
    string EntryId,
    string? ParentEntryId,
    int SceneEpoch,
    string EntryKind,
    string EntrySource,
    string ObservedUtc,
    long MonoTicks,
    string? DungeonId,
    string? MapId,
    string? RoomKey,
    string? InstanceId);

internal sealed record ContractSessionDescriptor(
    string ClientSessionId,
    string LaunchInstanceId,
    long LaunchOrdinal,
    int ActiveSlot,
    int? Pid,
    string? ProcessCreatedUtc,
    string? ExecutablePath,
    string? ExecutablePathSha256,
    string HandoffMode,
    string StartedUtc,
    string? EndedUtc,
    string EndState,
    long? ReceiverCharacterId,
    string? ReceiverCharacterName,
    string? AssociationSource,
    ContractEntryDescriptor[] Entries,
    long FirstEventSeq,
    long LastEventSeq,
    int RetainedEvents,
    int ObservationsCount);

internal sealed record ContractSessionIntegrity(
    string ClientSessionId,
    string LaunchInstanceId,
    int? Pid,
    string? ProcessCreatedUtc,
    long FirstEventSeq,
    long LastEventSeq,
    long RetainedEvents,
    long QueueDropped,
    long CriticalEventsLost,
    long TruncatedRecords,
    long RingEvictedRecords,
    long? CaptureDroppedPackets,
    string CaptureDroppedPacketsUnknownReason,
    long BudgetBytesLimit,
    long BudgetBytesUsed,
    bool BulkCollectionPaused,
    string CoverageStartedUtc,
    string CoverageEndedUtc,
    string CompletionStatus);

internal sealed record ContractIntegrityReport(
    int SchemaVersion,
    string IncidentId,
    string GeneratedUtc,
    long QueueDropped,
    long CriticalEventsLost,
    long TruncatedRecords,
    long RingEvictedRecords,
    long? CaptureDroppedPackets,
    string CaptureDroppedPacketsUnknownReason,
    long FirstEventSeq,
    long LastEventSeq,
    string CoverageStartedUtc,
    string CoverageEndedUtc,
    string CompletionStatus,
    bool NativeEventsCapability,
    string NativeEventsUnknownReason,
    [property: JsonPropertyName("per_session")] ContractSessionIntegrity[] Sessions);

internal sealed record ContractBundleFile(
    string Path,
    long Bytes,
    string Sha256);

internal sealed record ContractManifest(
    int SchemaVersion,
    string ContractName,
    string IncidentId,
    string ExportedUtc,
    string LauncherVersion,
    string LauncherCommit,
    string DiagnosticModuleVersion,
    string BuildFlavor,
    string? ClientFileSha256,
    long? ClientFileBytes,
    string? ClientFileUnknownReason,
    string? PvfFileSha256,
    long? PvfFileBytes,
    string? PvfFileUnknownReason,
    ContractDeclaredDigests DeclaredManifestDigests,
    string? ServerVersion,
    string ServerVersionUnknownReason,
    string ProtocolProfile,
    ContractServerEndpoint ServerEndpoint,
    ContractTimePolicy TimePolicy,
    ContractLimits Limits,
    Dictionary<string, bool> Capabilities,
    Dictionary<string, string?> CapabilityReasons,
    Dictionary<string, ContractOptionalPayload> OptionalPayloads,
    ContractSessionDescriptor[] Sessions,
    ContractIntegrityReport Integrity,
    ContractBundleFile[] Files);

internal sealed record SessionStartEvent(
    int SchemaVersion,
    [property: JsonPropertyName("event")] string EventName,
    string Source,
    string ClientSessionId,
    string? IncidentId,
    long EventSeq,
    string ObservedUtc,
    long MonoTicks,
    long MonoFrequencyHz,
    int? Pid,
    string? ProcessCreatedUtc,
    uint ThreadId,
    string LaunchInstanceId,
    long LaunchOrdinal,
    int ActiveSlot,
    string LauncherVersion,
    string LauncherCommit,
    string DiagnosticModuleVersion,
    string BuildFlavor,
    string ProtocolProfile,
    ContractServerEndpoint ServerEndpoint,
    string HandoffMode,
    bool EnhancedDiagnosticsEnabled,
    ContractHttpTraceItem[] CorrelatedHttpTraces,
    string? PidUnknownReason);

internal sealed record ProcessStartedEvent(
    int SchemaVersion,
    [property: JsonPropertyName("event")] string EventName,
    string Source,
    string ClientSessionId,
    string? IncidentId,
    long EventSeq,
    string ObservedUtc,
    long MonoTicks,
    long MonoFrequencyHz,
    int? Pid,
    string? ProcessCreatedUtc,
    uint ThreadId,
    string LaunchInstanceId,
    string ExecutablePath,
    string ExecutablePathSha256,
    string HandoffMode,
    bool ParentHandoffVerified,
    int WindowsSessionId,
    string? ClientFileSha256,
    long? ClientFileBytes,
    string? ClientFileUnknownReason,
    string? PvfFileSha256,
    long? PvfFileBytes,
    string? PvfFileUnknownReason,
    string ManifestClientSha256,
    string ManifestPvfSha256);

internal sealed record CharacterAssociatedEvent(
    int SchemaVersion,
    [property: JsonPropertyName("event")] string EventName,
    string Source,
    string ClientSessionId,
    string? IncidentId,
    long EventSeq,
    string ObservedUtc,
    long MonoTicks,
    long MonoFrequencyHz,
    int? Pid,
    string? ProcessCreatedUtc,
    uint ThreadId,
    string LaunchInstanceId,
    long? ReceiverCharacterId,
    string? ReceiverCharacterIdUnknownReason,
    string? ReceiverCharacterName,
    string? ReceiverCharacterNameUnknownReason,
    string AssociationSource,
    string? ChannelId,
    string? ChannelUnknownReason,
    string? PartyId,
    string? PartyUnknownReason);

internal sealed record SceneEntryEvent(
    int SchemaVersion,
    [property: JsonPropertyName("event")] string EventName,
    string Source,
    string ClientSessionId,
    string? IncidentId,
    long EventSeq,
    string ObservedUtc,
    long MonoTicks,
    long MonoFrequencyHz,
    int? Pid,
    string? ProcessCreatedUtc,
    uint ThreadId,
    string LaunchInstanceId,
    string EntryId,
    string? ParentEntryId,
    int SceneEpoch,
    string EntryKind,
    string EntrySource,
    string? DungeonId,
    string? DungeonUnknownReason,
    string? MapId,
    string? MapUnknownReason,
    string? RoomKey,
    string? RoomUnknownReason,
    string? InstanceId,
    string? InstanceUnknownReason);

internal sealed record ConnectionOpenEvent(
    int SchemaVersion,
    [property: JsonPropertyName("event")] string EventName,
    string Source,
    string ClientSessionId,
    string? IncidentId,
    long EventSeq,
    string ObservedUtc,
    long MonoTicks,
    long MonoFrequencyHz,
    int? Pid,
    string? ProcessCreatedUtc,
    uint ThreadId,
    string LaunchInstanceId,
    string Protocol,
    string StreamId,
    string? PreviousStreamId,
    bool ReconnectToRemote,
    string AddressFamily,
    string LocalAddress,
    int LocalPort,
    string? RemoteAddress,
    int? RemotePort,
    string? TcpState,
    string? BindingState,
    bool? PeerConnected,
    string? PeerUnknownReason,
    long? PacketsIn,
    long? PacketsOut,
    string? FirstPacketUtc,
    string? LastPacketUtc,
    string? PacketStatsUnknownReason,
    bool? ConfiguredForceRelay,
    string? ConfiguredForceRelayUnknownReason,
    string ObservedPathMode,
    string PathModeSource);

internal sealed record ConnectionCloseEvent(
    int SchemaVersion,
    [property: JsonPropertyName("event")] string EventName,
    string Source,
    string ClientSessionId,
    string? IncidentId,
    long EventSeq,
    string ObservedUtc,
    long MonoTicks,
    long MonoFrequencyHz,
    int? Pid,
    string? ProcessCreatedUtc,
    uint ThreadId,
    string LaunchInstanceId,
    string Protocol,
    string StreamId,
    string? PreviousStreamId,
    bool ReconnectToRemote,
    string AddressFamily,
    string LocalAddress,
    int LocalPort,
    string? RemoteAddress,
    int? RemotePort,
    string? TcpState,
    string? BindingState,
    string CloseReason,
    string CloseReasonSource,
    double? DurationMs);

internal sealed record ObservationReportedEvent(
    int SchemaVersion,
    [property: JsonPropertyName("event")] string EventName,
    string Source,
    string ClientSessionId,
    string? IncidentId,
    long EventSeq,
    string ObservedUtc,
    long MonoTicks,
    long MonoFrequencyHz,
    int? Pid,
    string? ProcessCreatedUtc,
    uint ThreadId,
    string LaunchInstanceId,
    string EntryId,
    string? ParentEntryId,
    int SceneEpoch,
    long? ReceiverCharacterId,
    string? ReceiverCharacterName,
    string? ReceiverUnknownReason,
    string PeerScope,
    long? PeerCharacterId,
    string? PeerCharacterName,
    string? PeerUnknownReason,
    string State,
    string ObservationSource,
    string? EstimatedOnsetUtc,
    double? TimeUncertaintyMs,
    string? Note);

internal sealed record ClockAnchorEvent(
    int SchemaVersion,
    [property: JsonPropertyName("event")] string EventName,
    string Source,
    string ClientSessionId,
    string? IncidentId,
    long EventSeq,
    string ObservedUtc,
    long MonoTicks,
    long MonoFrequencyHz,
    int? Pid,
    string? ProcessCreatedUtc,
    uint ThreadId,
    string LaunchInstanceId,
    string ClockSource,
    int LocalUtcOffsetMinutes,
    string? ServerUtc,
    double? ServerClockOffsetMs,
    double? ServerClockUncertaintyMs,
    string? ServerClockUnknownReason,
    string? HttpTraceId,
    bool QpcCrossHostComparable);

internal sealed record ProcessExitedEvent(
    int SchemaVersion,
    [property: JsonPropertyName("event")] string EventName,
    string Source,
    string ClientSessionId,
    string? IncidentId,
    long EventSeq,
    string ObservedUtc,
    long MonoTicks,
    long MonoFrequencyHz,
    int? Pid,
    string? ProcessCreatedUtc,
    uint ThreadId,
    string LaunchInstanceId,
    int? ExitCode,
    string? ExitCodeHex,
    string ExitClassification,
    string? ServerBusinessReasonCode,
    string? ServerBusinessReasonUnknownReason,
    double SessionDurationMs);

internal sealed record TraceIntegrityEvent(
    int SchemaVersion,
    [property: JsonPropertyName("event")] string EventName,
    string Source,
    string ClientSessionId,
    string? IncidentId,
    long EventSeq,
    string ObservedUtc,
    long MonoTicks,
    long MonoFrequencyHz,
    int? Pid,
    string? ProcessCreatedUtc,
    uint ThreadId,
    string LaunchInstanceId,
    long QueueDropped,
    long CriticalEventsLost,
    long TruncatedRecords,
    long RingEvictedRecords,
    long? CaptureDroppedPackets,
    string CaptureDroppedPacketsUnknownReason,
    long FirstEventSeq,
    long LastEventSeq,
    long BudgetBytesLimit,
    long BudgetBytesUsed,
    bool BulkCollectionPaused,
    bool NativeEventsCapability,
    string NativeEventsUnknownReason,
    string Reason);

internal sealed record ContractPersistedSessionMeta(
    ContractSessionDescriptor Descriptor,
    ContractSessionIntegrity Integrity,
    string? IncidentId,
    long UpdatedUnixMs);

[JsonSourceGenerationOptions(PropertyNamingPolicy = JsonKnownNamingPolicy.SnakeCaseLower, PropertyNameCaseInsensitive = true)]
[JsonSerializable(typeof(ContractManifest))]
[JsonSerializable(typeof(ContractIntegrityReport))]
[JsonSerializable(typeof(ContractPersistedSessionMeta))]
[JsonSerializable(typeof(SessionStartEvent))]
[JsonSerializable(typeof(ProcessStartedEvent))]
[JsonSerializable(typeof(CharacterAssociatedEvent))]
[JsonSerializable(typeof(SceneEntryEvent))]
[JsonSerializable(typeof(ConnectionOpenEvent))]
[JsonSerializable(typeof(ConnectionCloseEvent))]
[JsonSerializable(typeof(ObservationReportedEvent))]
[JsonSerializable(typeof(ClockAnchorEvent))]
[JsonSerializable(typeof(ProcessExitedEvent))]
[JsonSerializable(typeof(TraceIntegrityEvent))]
internal partial class ContractDiagnosticsJson : JsonSerializerContext { }
