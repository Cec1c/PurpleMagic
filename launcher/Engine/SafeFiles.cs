using System.Security.Cryptography;
using System.Text.RegularExpressions;

namespace Dnf90.Launcher;

public static class SafeFiles
{
    public static void ValidateRelative(string path)
    {
        if (string.IsNullOrWhiteSpace(path) || path.Length > 240 || path.StartsWith('/') || path.Contains('\\') || path.Contains(':') || path.Any(char.IsControl)) throw new InvalidDataException("更新路径无效。");
        foreach (var part in path.Split('/'))
        {
            if (part is "" or "." or ".." || part.TrimEnd(' ', '.') != part || part.IndexOfAny(['<', '>', '"', '|', '?', '*']) >= 0 ||
                Regex.IsMatch(part.Split('.')[0], "^(CON|PRN|AUX|NUL|COM[0-9]|LPT[0-9])$", RegexOptions.IgnoreCase) || part.StartsWith(".dnf90", StringComparison.OrdinalIgnoreCase)) throw new InvalidDataException("更新路径不允许。");
        }
    }
    public static string Target(string root, string relative)
    {
        ValidateRelative(relative);
        root = Path.GetFullPath(root).TrimEnd(Path.DirectorySeparatorChar) + Path.DirectorySeparatorChar;
        var full = Path.GetFullPath(Path.Combine(root, relative.Replace('/', Path.DirectorySeparatorChar)));
        if (!full.StartsWith(root, StringComparison.OrdinalIgnoreCase)) throw new InvalidDataException("路径超出客户端目录。");
        NoLinks(full);
        return full;
    }
    public static void NoLinks(string path)
    {
        for (var p = Path.GetFullPath(path); !string.IsNullOrEmpty(p); p = Path.GetDirectoryName(p))
        {
            try { if ((File.GetAttributes(p) & FileAttributes.ReparsePoint) != 0) throw new InvalidDataException("更新目录包含链接，已停止写入。"); }
            catch (FileNotFoundException) { }
            catch (DirectoryNotFoundException) { }
        }
    }
    public static void Atomic(string path, byte[] data, bool flushToDisk = true)
    {
        NoLinks(path); Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        var temporary = path + "." + Guid.NewGuid().ToString("N") + ".tmp";
        try
        {
            using (var stream = new FileStream(temporary, FileMode.CreateNew, FileAccess.Write, FileShare.None))
            {
                stream.Write(data);
                if (flushToDisk) stream.Flush(true);
            }
            File.Move(temporary, path, true);
        }
        finally { File.Delete(temporary); }
    }
    public static async Task<bool> Matches(string path, long size, string hash, CancellationToken cancel = default)
    {
        NoLinks(path); if (!File.Exists(path)) return false;
        await using var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read, 65536, true);
        return stream.Length == size && Convert.ToHexString(await SHA256.HashDataAsync(stream, cancel)).Equals(hash, StringComparison.OrdinalIgnoreCase);
    }
}
