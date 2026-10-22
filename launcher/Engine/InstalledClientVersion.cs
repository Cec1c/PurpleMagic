using System.Security.Cryptography;
using System.Text.Json;
using System.Text.RegularExpressions;
using ServerS5A3;

namespace Dnf90.Launcher;

internal static class InstalledClientVersion
{
    // V numbers belong to signed content releases, not the EXE's fixed Windows
    // file version. Match the installed bytes before showing a release label.
    public static string Read(string directory, PlayerProfile root, Bootstrap? current)
    {
        if (string.IsNullOrWhiteSpace(directory)) return "";
        try
        {
            var path = Path.Combine(directory, "DNF.exe");
            SafeFiles.NoLinks(path);
            if (!File.Exists(path)) return "";
            var candidates = new List<ContentRelease>();
            if (current?.Content is { } latest) candidates.Add(latest);
            var receipt = Path.Combine(directory, ".dnf90-update", "installed.json");
            try
            {
                SafeFiles.NoLinks(receipt);
                if (File.Exists(receipt) && new FileInfo(receipt).Length <= Trust.MaxMetadata &&
                    Trust.Verify(File.ReadAllBytes(receipt), root).Content is { } installed)
                    candidates.Add(installed);
            }
            catch (Exception e) when (e is IOException or InvalidDataException or UnauthorizedAccessException or JsonException or CryptographicException or FormatException or ArgumentException or InvalidOperationException) { }

            using var file = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read);
            var matches = candidates.Select(c => (Label: Label(c.Version), Exe: c.Files.FirstOrDefault(f => f.Path.Equals("DNF.exe", StringComparison.OrdinalIgnoreCase))))
                .Where(c => c.Label != "" && c.Exe?.Length == file.Length).ToArray();
            if (matches.Length == 0) return "";
            var hash = Convert.ToHexString(SHA256.HashData(file));
            return matches.FirstOrDefault(c => c.Exe!.Sha256.Equals(hash, StringComparison.OrdinalIgnoreCase)).Label ?? "";
        }
        catch (Exception e) when (e is IOException or InvalidDataException or UnauthorizedAccessException or ArgumentException or NotSupportedException) { return ""; }
    }

    private static string Label(string? version)
    {
        var match = Regex.Match(version ?? "", @"^(?:client[-_])?[vV]([1-9][0-9]{0,5})(?:$|[-_])", RegexOptions.CultureInvariant);
        return match.Success ? "V" + match.Groups[1].Value : "";
    }
}
