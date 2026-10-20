using System.Text.Json.Serialization;

namespace Dnf90.Launcher.Diagnostics;

public sealed record DiagnosticOptions(bool Enabled = false, string ProblemType = "auto");
public sealed record DiagnosticInstance(string Id, string Window, int Pid, string StartedUtc, string Status, string ExeVersion, bool Selected);
public sealed record DiagnosticClip(string Id, string SessionId, string StartedUtc, string EndedUtc, string Reason, long Bytes, string[] Categories);
public sealed record DiagnosticStatus(bool Enabled, string Phase, string Message, DiagnosticInstance[] Instances, DiagnosticClip[] Clips, int Countdown = 0);
public sealed record DiagnosticExport(string Path, long Bytes, string Sha256, int Incidents);
internal sealed record ProcessIdentity(int Pid, long Created, int WindowsSession, string Path, string Id);
internal sealed record DiagnosticField(double? Value, string Status = "ok", string? Reason = null);
internal sealed record DiagnosticRecord(long Sequence, string Utc, double MonotonicMs, string Type, string Module, string SessionId,
    double ReadMs, string Quality, Dictionary<string, DiagnosticField> Fields, string? Reason = null, long? BaseSequence = null);
internal sealed record DiagnosticFile(string Path, long Bytes, string Sha256);
internal sealed record DiagnosticManifest(int SchemaVersion, string CollectorVersion, string ModuleVersion, string SessionId,
    string IncidentId, string StartedUtc, string EndedUtc, string EndReason, DiagnosticFile[] Files);
internal sealed record DiagnosticSummary(string SessionId, string IncidentId, string ExeSha256, string PvfSha256,
    long PublicationSequence, string ExeVersion, string EndReason, long Records, long Errors, long Dropped,
    long Overwritten, bool Truncated, string[] Missing, string[] Categories, int TargetIntervalMs,
    bool ServerEventsConnected = false, double? ServerClockOffsetMs = null, double? ServerClockUncertaintyMs = null,
    string ObservationKind = "client_polling", string CounterScope = "session_cumulative",
    string? CorrelationId = null, string ExpectedPvfSha256 = "");

internal sealed record DiagnosticExportSummary(string SessionId, DiagnosticSummary[] Incidents, string[] OmittedCompleteIncidents, string EndReason);

[JsonSourceGenerationOptions(PropertyNamingPolicy = JsonKnownNamingPolicy.SnakeCaseLower, PropertyNameCaseInsensitive = true)]
[JsonSerializable(typeof(DiagnosticExportSummary))]
[JsonSerializable(typeof(DiagnosticRecord))]
[JsonSerializable(typeof(DiagnosticManifest))]
[JsonSerializable(typeof(DiagnosticSummary))]
[JsonSerializable(typeof(ProcessIdentity[]))]
[JsonSerializable(typeof(DiagnosticClip[]))]
internal partial class DiagnosticJson : JsonSerializerContext { }
