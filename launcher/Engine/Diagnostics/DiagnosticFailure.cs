using System.ComponentModel;
using System.Diagnostics;
using System.Diagnostics.CodeAnalysis;
using System.Net.Sockets;

namespace Dnf90.Launcher.Diagnostics;

internal static class DiagnosticFailure
{
    // Exception messages, Data, filenames, URLs and argument values never enter the log.
    [UnconditionalSuppressMessage("Trimming", "IL2026", Justification = "NativeAOT may omit method metadata; null becomes unknown. No method is invoked through reflection and no file information is requested.")]
    public static DiagnosticEvidence Capture(Exception error)
    {
        var result = new List<DiagnosticException>();
        for (Exception? e = error; e is not null && result.Count < 6; e = e.InnerException)
        {
            var kind = e switch {
                SocketException => "socket", HttpRequestException => "http", TimeoutException => "timeout",
                OperationCanceledException => "cancelled", UnauthorizedAccessException => "access_denied",
                System.Security.Cryptography.CryptographicException => "signature",
                InvalidDataException => "invalid_data", IOException => "io", Win32Exception => "win32",
                InvalidOperationException => "invalid_operation", _ => "exception"
            };
            var frames = new StackTrace(e, false).GetFrames().Take(32).Select(f => {
                var method = f.GetMethod();
                return new DiagnosticStackFrame(SafeName(method?.Module.Assembly.GetName().Name),
                    (ulong)Math.Max(0, f.GetNativeOffset()), SafeName(method?.DeclaringType?.FullName + "." + method?.Name));
            }).ToArray();
            result.Add(new(kind, e.HResult, e is Win32Exception w ? w.NativeErrorCode : e is HttpRequestException h ? (int?)h.StatusCode : null, frames));
        }
        return new("managed_exception", [.. result]);
    }
    internal static string SafeName(string? value) => new((value ?? "unknown").Take(180)
        .Select(c => char.IsAsciiLetterOrDigit(c) || c is '.' or '_' or '+' or '<' or '>' or '`' ? c : '_').ToArray());
}
