using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text.Json;
using Dnf90.Portable;

var prepareOnly = args.Length > 0 && args[0] == "--prepare-only";
try
{
    if (args.Length != 0 && !(prepareOnly && (args.Length == 1 || args.Length == 3 && args[1] == "--cache-root")))
        throw new InvalidOperationException("不支持此启动参数。");
    var cache = prepareOnly && args.Length == 3 ? args[2] : Path.Combine(
        Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "DNF90Launcher", "portable");
    using var metadata = typeof(Program).Assembly.GetManifestResourceStream("payload.json")!;
    var payload = JsonSerializer.Deserialize(metadata, PortableJson.Default.Payload) ?? throw new InvalidDataException();
    if (payload.BuildFlavor is not ("standard" or "anticheat")) throw new InvalidDataException("发行包类型无效。");
    if (payload.BuildFlavor == "anticheat") cache = Path.Combine(cache, "anticheat");
    using var archive = typeof(Program).Assembly.GetManifestResourceStream("payload.zip")!;
    var result = PortableCache.Prepare(archive, payload, cache);
    if (prepareOnly)
    {
        using var output = Console.OpenStandardOutput();
        output.Write(JsonSerializer.SerializeToUtf8Bytes(result, PortableJson.Default.Prepared));
        output.WriteByte((byte)'\n');
    }
    else
    {
        var start = new ProcessStartInfo(Path.Combine(result.Directory, "DNF90Launcher.exe"))
        { UseShellExecute = false, CreateNoWindow = true, WorkingDirectory = result.Directory };
        // Preserve the portable EXE's location through bootstrap, Flutter and core;
        // their working directories belong to the extracted app cache.
        start.Environment["DNF90_LAUNCH_DIRECTORY"] = AppContext.BaseDirectory;
        using var child = Process.Start(start);
        if (child is null) throw new InvalidOperationException("登录器启动失败。");
    }
    return 0;
}
catch (Exception error)
{
    var message = error is InvalidDataException or InvalidOperationException ? error.Message :
        "运行文件无法准备，请检查本机目录权限、剩余空间及是否正在更新。原有文件已保留。";
    if (prepareOnly)
    {
        using var stderr = Console.OpenStandardError();
        stderr.Write(System.Text.Encoding.UTF8.GetBytes(error.GetType().Name + ": " + message + "\n"));
    }
    else Native.MessageBox(IntPtr.Zero, message, "PurpleMagic", 0x10);
    return 2;
}

internal static partial class Native
{
    [LibraryImport("user32.dll", EntryPoint = "MessageBoxW", StringMarshalling = StringMarshalling.Utf16)]
    internal static partial int MessageBox(IntPtr window, string text, string caption, uint flags);
}
