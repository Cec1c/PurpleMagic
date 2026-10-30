using System.Text.Json;
using ServerS5A3;
using Dnf90.Launcher;
using Dnf90.Launcher.Diagnostics;

if (args.Length > 0 && args[0] == "--diagnostic-observer") { Environment.Exit(await DiagnosticObserver.Run(args)); return; }

Console.InputEncoding = System.Text.Encoding.UTF8;
Console.OutputEncoding = new System.Text.UTF8Encoding(false);

using var profileStream = typeof(Program).Assembly.GetManifestResourceStream("profile.json")!;
var profile = JsonSerializer.Deserialize(profileStream, CoreJson.Default.PlayerProfile)!;
if (args.Length == 1 && args[0] == "--build-flavor") {
    profile.AntiCheat?.Validate();
#if !ANTICHEAT
    if (profile.AntiCheat is { Enabled: true }) throw new InvalidDataException("Enabled AntiCheat policy requires --anticheat build.");
#endif
    Console.WriteLine(LauncherBuild.Flavor); return;
}
#if ANTICHEAT
if (args.Length > 0 && args[0] == "--anticheat-guard") { Environment.Exit(await Dnf90.Launcher.AntiCheat.AntiCheatGuard.Run(args, profile)); return; }
#endif
if (args.Length > 0 && args[0] == "--anticheat-guard") { Environment.Exit(2); return; }
if (args.Length == 2 && args[0] == "--apply-bundle")
{
    var job = JsonSerializer.Deserialize(File.ReadAllBytes(args[1]), WireJson.Default.BundleJob) ?? throw new InvalidDataException();
    Environment.Exit(await BundleInstaller.Apply(job, profile)); return;
}
if (args.Length == 2 && args[0] == "--recover-bundle") { BundleInstaller.Recover(Path.GetFullPath(args[1])); return; }
// No HTTP listener or credentials in command-line arguments. Flutter is the sole
// parent and sends bounded newline JSON through redirected process pipes.
var data = args.Length == 2 && args[0] == "--state-directory"
    ? Path.GetFullPath(args[1])
    : Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "DNF90Launcher" + (LauncherBuild.AntiCheat ? "-anticheat" : ""));
SafeFiles.NoLinks(data);
Directory.CreateDirectory(data);
using var instance = new FileStream(Path.Combine(data, "instance.lock"), FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None);
using var engine = new LauncherEngine(profile, data);
var output = new object();
void Respond(Response response) { lock (output) Console.WriteLine(JsonSerializer.Serialize(response, WireJson.Default.Response)); }
engine.Changed += state => { lock (output) Console.WriteLine(JsonSerializer.Serialize(new Event("state", state), WireJson.Default.Event)); };
var jobs = new List<Task>();
async Task Dispatch(Request request)
{
    try
    {
        switch (request.Method)
        {
            case "initialize": break;
            case "health": engine.ReportHealthy(request.Args.GetProperty("path").GetString()!); break;
            case "check": await engine.Check(); break;
            case "autoUpdate":
                var restart = await engine.AutomaticUpdate(request.Args.GetProperty("uiPid").GetInt32());
                Respond(new(request.Id, true, State: engine.State, Restart: restart)); return;
            case "update": await engine.UpdateContent(); break;
            case "pause": engine.Pause(); break;
            case "preferences": await engine.SavePreferences(request.Args.Deserialize(WireJson.Default.Preferences) ?? throw new InvalidDataException()); break;
            case "register": await engine.Register(request.Args.Deserialize(WireJson.Default.Credentials) ?? throw new InvalidDataException()); break;
            case "login": await engine.Login(request.Args.Deserialize(WireJson.Default.Credentials) ?? throw new InvalidDataException()); break;
            case "bindingCode":
            case "changePassword":
                var accountResult = await engine.AccountSecurity(request.Method == "bindingCode" ? "binding-code" : "password", request.Args.Deserialize(WireJson.Default.PasswordChange) ?? throw new InvalidDataException());
                Respond(new(request.Id, true, State: engine.State, AccountResult: accountResult)); return;
            case "diagnosticExport":
                var export = await engine.ExportDiagnostics(request.Args.GetProperty("sessionId").GetString()!, request.Args.GetProperty("path").GetString()!);
                Respond(new(request.Id, true, State: engine.State, DiagnosticExport: export)); return;
            case "diagnosticElevate": await engine.ElevateDiagnostics(request.Args.GetProperty("sessionId").GetString()!); break;
            case "resetGameConfiguration":
                var confirmed = request.Args.TryGetProperty("confirmed", out var confirmation) && confirmation.ValueKind == JsonValueKind.True;
                var reset = await engine.ResetGameConfiguration(confirmed);
                Respond(new(request.Id, true, State: engine.State, ConfigurationReset: reset)); return;
            case "updateLauncher":
                var launcherRestart = await engine.UpdateLauncher(request.Args.GetProperty("uiPid").GetInt32());
                Respond(new(request.Id, true, State: engine.State, Restart: launcherRestart)); return;
            default: throw new InvalidDataException("未知登录器操作。");
        }
        Respond(new(request.Id, true, State: engine.State));
    }
    catch (Exception error)
    {
        engine.RecordDiagnosticError(error);
        var message = error switch {
            HttpRequestException => "无法连接服务器或证书验证失败。请检查网络和后台配置。",
            IOException when error is not InvalidDataException => "文件无法读写，请检查目录权限、剩余空间及游戏是否已退出。",
            InvalidDataException or InvalidOperationException => error.Message,
            OperationCanceledException => "操作已取消或超时。",
            _ => "操作未完成，请重试并联系管理员。"
        };
        Respond(new(request.Id, false, message, engine.State, ErrorCode: (error as AccountApiException)?.Code, RetryAfter: (error as AccountApiException)?.RetryAfter ?? 0));
    }
}
while (await Console.In.ReadLineAsync() is { } line)
{
    if (line.Length > 16384) { Respond(new(0, false, "请求过大。")); continue; }
    try
    {
        var request = JsonSerializer.Deserialize(line, WireJson.Default.Request) ?? throw new InvalidDataException();
        jobs.RemoveAll(t => t.IsCompleted); jobs.Add(Dispatch(request));
    }
    catch (Exception error) when (error is JsonException or InvalidDataException) { Respond(new(0, false, "请求格式无效。")); }
}
engine.Pause();
engine.StopProtectedGames();
await Task.WhenAll(jobs);
