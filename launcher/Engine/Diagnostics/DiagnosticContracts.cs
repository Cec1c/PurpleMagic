using System.Text.Json.Serialization;

namespace Dnf90.Launcher.Diagnostics;

public sealed record DiagnosticOptions(bool Enabled = false, string ProblemType = "auto", int ConsentVersion = 0)
{
    public const int CurrentConsent = 1;
    public bool HasConsent => ConsentVersion == CurrentConsent;
}
public sealed record DiagnosticInstance(string Id, string Window, int Pid, string StartedUtc, string Status, string ExeVersion, bool Selected, string CrashStatus = "starting");
public sealed record DiagnosticClip(string Id, string SessionId, string StartedUtc, string EndedUtc, string Reason, long Bytes, string[] Categories);
public sealed record DiagnosticStatus(bool Enabled, string Phase, string Message, DiagnosticInstance[] Instances, DiagnosticClip[] Clips, int Countdown = 0);
public sealed record DiagnosticExport(string Path, long Bytes, string Sha256, int Incidents);
internal sealed record ProcessIdentity(int Pid, long Created, int WindowsSession, string Path, string Id);
internal sealed record DiagnosticField(double? Value, string Status = "ok", string? Reason = null);
internal sealed record DiagnosticRecord(long Sequence, string Utc, double MonotonicMs, string Type, string Module, string SessionId,
    double ReadMs, string Quality, Dictionary<string, DiagnosticField> Fields, string? Reason = null, long? BaseSequence = null, DiagnosticEvidence? Evidence = null);
internal sealed record DiagnosticStackFrame(string Module, ulong Offset, string? Method = null);
internal sealed record DiagnosticException(string Kind, int HResult, int? NativeCode, DiagnosticStackFrame[] Stack);
internal sealed record DiagnosticEvidence(string Kind, DiagnosticException[]? Exceptions = null, DiagnosticStackFrame[]? Stack = null);
internal sealed record ObserverMessage(string Kind, Dictionary<string, DiagnosticField> Fields, DiagnosticEvidence? Evidence = null);
internal sealed record ObserverTarget(int Pid, long Created, string Path);
internal sealed record DiagnosticSummary(string SessionId, string IncidentId, string ExeSha256, string PvfSha256,
    long PublicationSequence, string ExeVersion, string EndReason, long Records, long Errors, long Dropped,
    long Overwritten, bool Truncated, string[] Missing, string[] Categories, int TargetIntervalMs,
    bool ServerEventsConnected = false, double? ServerClockOffsetMs = null, double? ServerClockUncertaintyMs = null,
    string ObservationKind = "client_polling", string CounterScope = "session_cumulative",
    string? CorrelationId = null, string ExpectedPvfSha256 = "");

[JsonSourceGenerationOptions(PropertyNamingPolicy = JsonKnownNamingPolicy.SnakeCaseLower, PropertyNameCaseInsensitive = true)]
[JsonSerializable(typeof(DiagnosticLogIndex))]
[JsonSerializable(typeof(DiagnosticLogEnvelope))]
[JsonSerializable(typeof(DiagnosticRecord))]
[JsonSerializable(typeof(ObserverMessage))]
[JsonSerializable(typeof(ObserverTarget))]
[JsonSerializable(typeof(DiagnosticSummary))]
[JsonSerializable(typeof(ProcessIdentity[]))]
[JsonSerializable(typeof(DiagnosticClip[]))]
internal partial class DiagnosticJson : JsonSerializerContext { }
