using System.Security.Cryptography;
using ServerS5A3;

namespace Dnf90.Launcher.AntiCheat;

internal sealed class ClientFileLease : IDisposable
{
    private readonly List<FileStream> files = [];
    internal string ExeSha256 { get; }
    internal ClientFileLease(string directory, PlayerProfile profile, CancellationToken token = default)
    {
        try {
            ExeSha256 = Lock(directory, "DNF.exe", profile.ExeSha256, token, profile);
            Lock(directory, "Script.pvf", profile.PvfSha256, token);
            foreach (var file in profile.AntiCheat?.RequiredFiles ?? []) Lock(directory, file.Name, file.Sha256, token);
        } catch { Dispose(); throw; }
    }
    private string Lock(string directory, string name, string digest, CancellationToken token, PlayerProfile? executableProfile = null)
    {
        var path = SafeFiles.Target(directory, name);
        var file = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read, 65536, FileOptions.SequentialScan);
        files.Add(file);
        using var hash = IncrementalHash.CreateHash(HashAlgorithmName.SHA256);
        var buffer = new byte[65536]; int read;
        token.ThrowIfCancellationRequested();
        while ((read = file.Read(buffer)) != 0) { token.ThrowIfCancellationRequested(); hash.AppendData(buffer, 0, read); }
        var actual = Convert.ToHexString(hash.GetHashAndReset());
        if (!GuardNative.FinalPath(file.SafeFileHandle).Equals(path, StringComparison.OrdinalIgnoreCase) ||
            !(actual.Equals(digest, StringComparison.OrdinalIgnoreCase) || executableProfile is not null && NativeGuildCompatibility.AcceptsDerivative(executableProfile, actual)))
            throw new InvalidDataException("保护文件校验失败。");
        return actual;
    }
    public void Dispose() { foreach (var file in files) file.Dispose(); files.Clear(); }
}
