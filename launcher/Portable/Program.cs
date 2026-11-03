using System.Diagnostics;
using System.Globalization;
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
    var korean = CultureInfo.CurrentUICulture.TwoLetterISOLanguageName == "ko";
    var message = korean
        ? PortableMessages.Korean.GetValueOrDefault(error.Message, "실행 파일을 준비하지 못했습니다. 이 PC의 폴더 권한과 남은 공간을 확인한 뒤 다시 시도하세요. 기존 파일은 보존했습니다.")
        : error is InvalidDataException or InvalidOperationException ? error.Message :
            "运行文件无法准备，请检查本机目录权限、剩余空间及是否正在更新。原有文件已保留。";
    if (prepareOnly)
    {
        using var stderr = Console.OpenStandardError();
        stderr.Write(System.Text.Encoding.UTF8.GetBytes(error.GetType().Name + ": " + message + "\n"));
    }
    else Native.MessageBox(IntPtr.Zero, message, "PurpleMagic", 0x10);
    return 2;
}

internal static class PortableMessages
{
    internal static readonly IReadOnlyDictionary<string, string> Korean = new Dictionary<string, string>
    {
        ["不支持此启动参数。"] = "지원하지 않는 실행 옵션입니다.",
        ["发行包类型无效。"] = "배포 패키지 유형이 올바르지 않습니다.",
        ["登录器启动失败。"] = "런처를 시작하지 못했습니다.",
        ["便携包清单无效。"] = "포터블 패키지 목록이 올바르지 않습니다.",
        ["便携包包含不支持的文件。"] = "포터블 패키지에 지원하지 않는 파일이 들어 있습니다.",
        ["便携包缺少运行文件。"] = "포터블 패키지에 필요한 실행 파일이 없습니다.",
        ["便携入口文件损坏，请重新下载完整便携包。"] = "포터블 실행 파일이 손상되었습니다. 패키지 전체를 다시 다운로드하세요.",
        ["该目录不是此便携包创建的，已保留原文件。"] = "이 폴더는 포터블 패키지가 만든 폴더가 아닙니다. 기존 파일을 보존했습니다.",
        ["已释放的运行文件不完整，已停止启动并保留现有更新数据。"] = "압축을 푼 실행 파일이 완전하지 않아 실행을 중단했습니다. 기존 업데이트 데이터는 보존했습니다.",
        ["便携目录被其他文件占用。"] = "포터블 폴더에 다른 파일이 있어 사용할 수 없습니다.",
        ["便携包长度不符。"] = "포터블 패키지 크기가 올바르지 않습니다.",
        ["便携包校验失败，请重新下载。"] = "포터블 패키지 검증에 실패했습니다. 다시 다운로드하세요.",
        ["便携包文件清单不符。"] = "포터블 패키지의 파일 목록이 일치하지 않습니다.",
        ["便携包文件条目无效。"] = "포터블 패키지의 파일 항목이 올바르지 않습니다.",
        ["便携包文件大小超出清单。"] = "포터블 패키지의 파일 크기가 목록에 지정된 값을 초과합니다.",
        ["便携包的安装标识无效。"] = "포터블 패키지 설치 정보가 올바르지 않습니다."
    };
}

internal static partial class Native
{
    [LibraryImport("user32.dll", EntryPoint = "MessageBoxW", StringMarshalling = StringMarshalling.Utf16)]
    internal static partial int MessageBox(IntPtr window, string text, string caption, uint flags);
}
