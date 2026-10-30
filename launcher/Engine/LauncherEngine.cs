using System.Diagnostics;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Microsoft.Win32;
using ServerS5A3;
using Dnf90.Launcher.Diagnostics;
#if ANTICHEAT
using Dnf90.Launcher.AntiCheat;
#endif

namespace Dnf90.Launcher;

public sealed class LauncherEngine : IDisposable
{
    public const string VersionText = "1.0.6";
    private readonly PlayerProfile root;
    private readonly string stateRoot;
    private readonly string? previousUpdateFailure;
    private readonly BundleOutcome? previousUpdateOutcome;
    private readonly SemaphoreSlim operation = new(1, 1);
    private CancellationTokenSource? download;
    private Bootstrap? bootstrap;
    private byte[]? envelope;
    private Preferences preferences = new();
    private ContentFile[] plan = [];
    private string clientVersion = "";
    private readonly List<(string Account, Process Process)> games = [];
#if ANTICHEAT
    private readonly List<AntiCheatSession> protectedGames = [];
    private readonly object guardLock = new();
    private readonly CancellationTokenSource guardLifetime = new();
    private bool guardClosing;
#endif
    private readonly DiagnosticService diagnostics;
    public event Action<State>? Changed;
    private State state = new("ready", "正在读取本机设置…", 0, 0, 0, false, false, false, "本服批准源", "", new(), []);
    public State State { get => state with { Diagnostics = diagnostics?.Status }; private set => state = value; }
    private PlayerProfile Profile => bootstrap?.Profile ?? root;
    public LauncherEngine(PlayerProfile trusted, string dataDirectory)
    {
        root = trusted; stateRoot = dataDirectory; SafeFiles.NoLinks(stateRoot); Directory.CreateDirectory(stateRoot);
        root.AntiCheat?.Validate();
#if ANTICHEAT
        LegacyPreferences.Import(stateRoot);
#endif
        var file = Path.Combine(stateRoot, "preferences.json");
        if (File.Exists(file)) preferences = JsonSerializer.Deserialize(File.ReadAllBytes(file), WireJson.Default.Preferences) ?? new();
        if (preferences.Diagnostics is { Enabled: true, HasConsent: false } legacy) {
            preferences = preferences with { Diagnostics = legacy with { Enabled = false } };
            SafeFiles.Atomic(file, JsonSerializer.SerializeToUtf8Bytes(preferences, WireJson.Default.Preferences));
        }
        var detected = ClientDirectoryDiscovery.Find(preferences.ClientDirectory, root.DefaultClientDirectory,
            AppContext.BaseDirectory, Environment.GetEnvironmentVariable(ClientDirectoryDiscovery.LaunchDirectoryVariable), Environment.CurrentDirectory);
        var discovered = detected != "" && detected != preferences.ClientDirectory;
        if (discovered)
        {
            preferences = preferences with { ClientDirectory = detected };
            SafeFiles.Atomic(file, JsonSerializer.SerializeToUtf8Bytes(preferences, WireJson.Default.Preferences));
        }
        var cached = Path.Combine(stateRoot, "bootstrap.json");
        if (File.Exists(cached)) { envelope = File.ReadAllBytes(cached); bootstrap = Trust.Verify(envelope, root); }
        RefreshClientVersion();
        var install = Path.GetDirectoryName(Path.GetFullPath(AppContext.BaseDirectory).TrimEnd(Path.DirectorySeparatorChar))!;
        previousUpdateFailure = BundleInstaller.PreviousFailure(install);
        previousUpdateOutcome = BundleInstaller.ReadOutcome(install);
        diagnostics = new(stateRoot);
        diagnostics.Changed += () => Changed?.Invoke(State);
        diagnostics.Configure(preferences.Diagnostics ?? new(), preferences.ClientDirectory, Profile.PvfSha256, bootstrap?.Sequence ?? 0);
        Emit("ready", previousUpdateFailure ?? (discovered ? "已自动识别客户端目录。" : preferences.ClientDirectory == "" ? "请选择客户端目录，检查更新后登录。" : "客户端目录已就绪。"));
    }
    private void Emit(string phase, string message, long current = 0, long total = 0)
    {
        var busy = phase is "checking" or "downloading" or "installing" or "logging-in" or "resetting";
        State = new(phase, message, total == 0 ? 0 : Math.Clamp((double)current / total, 0, 1), current, total,
            !busy && phase != "paused" && plan.Length == 0 && preferences.ClientDirectory != "" && SupportsContent(Profile), plan.Length != 0,
            bootstrap?.Launcher is { } b && b.BuildFlavor == LauncherBuild.Flavor && System.Version.Parse(b.Version) > System.Version.Parse(VersionText),
            bootstrap?.Content?.SourceId ?? "本服批准源", bootstrap?.Content?.Version ?? "", preferences, bootstrap?.News ?? []) { ClientVersion = clientVersion, QQBindingEnabled = root.EnableQQBinding, RequireQQBinding = Profile.RequireQQBinding, AntiCheatEnabled = LauncherBuild.AntiCheat && Profile.AntiCheat is { Enabled: true } };
        Changed?.Invoke(State);
    }
    private void RefreshClientVersion() => clientVersion = InstalledClientVersion.Read(preferences.ClientDirectory, root, bootstrap);
    public void Pause() => download?.Cancel();
    internal static bool SupportsContent(PlayerProfile profile) =>
        (string.IsNullOrEmpty(profile.MinimumLauncherVersion) ||
         System.Version.Parse(VersionText) >= System.Version.Parse(profile.MinimumLauncherVersion)) &&
        NativeChannelHistory.SupportsProfile(profile);
    internal static void RequireContentSupport(PlayerProfile profile)
    {
        if (!SupportsContent(profile)) throw new InvalidOperationException("请先更新登录器，再安装资源或登录游戏。");
    }
    public void ReportHealthy(string path)
    {
        var install = Path.GetDirectoryName(Path.GetFullPath(AppContext.BaseDirectory).TrimEnd(Path.DirectorySeparatorChar))!;
        var full = Path.GetFullPath(path); var work = Path.GetDirectoryName(full)!;
        if (Path.GetFileName(full) != "ready" || Path.GetDirectoryName(work) != Path.Combine(install, ".updates") || !Guid.TryParseExact(Path.GetFileName(work), "N", out _)) throw new InvalidDataException("就绪回执路径无效。");
        SafeFiles.NoLinks(full);
        var job = JsonSerializer.Deserialize(File.ReadAllBytes(Path.Combine(install, ".launcher-update.json")), WireJson.Default.BundleJob);
        if (job?.Work != work || job.Phase != "candidate-installed") throw new InvalidDataException("就绪回执与更新事务不匹配。");
        SafeFiles.Atomic(full, "ready"u8.ToArray());
    }
    public async Task<bool> UpdateLauncher(int uiPid)
    {
        if (!await operation.WaitAsync(0)) throw new InvalidOperationException("已有操作正在进行。");
        try { return await UpdateLauncherCore(uiPid); }
        catch (OperationCanceledException) { Emit("ready", "登录器更新已取消，可手动重试。"); return false; }
        catch { Emit("error", "登录器更新未完成，请查看提示。"); throw; }
        finally { download = null; operation.Release(); }
    }
    private async Task<bool> UpdateLauncherCore(int uiPid)
    {
        RequireNoGames();
        var release = bootstrap?.Launcher ?? throw new InvalidOperationException("尚未发布登录器更新。");
        LauncherBuild.RequireFlavor(release.BuildFlavor);
        if (System.Version.Parse(release.Version) <= System.Version.Parse(VersionText)) throw new InvalidOperationException("当前登录器已是最新版本。");
        var app = Path.GetFullPath(AppContext.BaseDirectory).TrimEnd(Path.DirectorySeparatorChar);
        var install = Path.GetDirectoryName(app)!; BundleInstaller.ValidateRoot(install);
        if (Path.GetFileName(app) != "app") throw new InvalidDataException("请从完整安装目录启动登录器。");
        using var ui = Process.GetProcessById(uiPid);
        if (!Path.GetFullPath(ui.MainModule!.FileName).Equals(Path.Combine(app, "DNF90.exe"), StringComparison.OrdinalIgnoreCase)) throw new InvalidDataException("登录器父进程不匹配。");
        var work = Path.Combine(install, ".updates", Guid.NewGuid().ToString("N")); SafeFiles.NoLinks(work); Directory.CreateDirectory(work);
        var package = Path.Combine(work, "bundle.zip"); using var api = new PlayerApi(root); api.Http.Timeout = Timeout.InfiniteTimeSpan;
        using var timeout = new CancellationTokenSource(TimeSpan.FromMinutes(30)); download = timeout;
        Emit("downloading", "正在下载登录器完整更新包…", 0, release.Size);
        using var delivery = new LauncherBundleDownloader(root);
        var cachedPackage = await delivery.Download(release, Path.Combine(stateRoot, "bundle-downloads"), count => Emit("downloading", "正在下载登录器完整更新包…", count, release.Size), timeout.Token);
        File.Copy(cachedPackage, package, false);
        download = null;
        RequireNoGames();
        using var own = Process.GetCurrentProcess();
        var job = new BundleJob(install, package, Convert.ToBase64String(envelope!), ui.Id, ui.StartTime.ToUniversalTime().Ticks, own.Id, own.StartTime.ToUniversalTime().Ticks, work, "downloaded");
        var jobFile = Path.Combine(work, "job.json"); SafeFiles.Atomic(jobFile, JsonSerializer.SerializeToUtf8Bytes(job, WireJson.Default.BundleJob));
        var helper = Path.Combine(install, "update-helper.exe"); SafeFiles.NoLinks(helper); File.Copy(Environment.ProcessPath!, helper, true);
        var start = new ProcessStartInfo(helper) { UseShellExecute = false, CreateNoWindow = true, WorkingDirectory = install };
        start.ArgumentList.Add("--apply-bundle"); start.ArgumentList.Add(jobFile);
        using var helperProcess = Process.Start(start) ?? throw new InvalidOperationException("更新助手启动失败。");
        Emit("installing", "登录器更新已准备完成，正在重启…");
        return true;
    }
    public async Task<bool> AutomaticUpdate(int uiPid)
    {
        if (!await operation.WaitAsync(0)) throw new InvalidOperationException("已有操作正在进行。");
        try
        {
            // Keep the existing AutoCheck preference so an explicit opt-out survives upgrades.
            if (!preferences.AutoCheck) return false;
            RequireNoGames();
            await RefreshMetadata();
            if (State.HasLauncherUpdate)
            {
                // A failed replacement must not cause an automatic restart/rollback loop.
                if (BundleInstaller.SuppressAutomaticRetry(previousUpdateOutcome, bootstrap!.Launcher!.Version)) { Emit("ready", previousUpdateFailure!); return false; }
                return await UpdateLauncherCore(uiPid);
            }
            await PlanContent();
            await InstallContent();
            return false;
        }
        catch (OperationCanceledException)
        {
            Emit(State.HasUpdate && !State.HasLauncherUpdate ? "paused" : "ready", "自动更新已暂停，可手动重试。", State.Downloaded, State.Total);
            return false;
        }
        catch { Emit("error", "自动更新未完成，可手动重试。", State.Downloaded, State.Total); throw; }
        finally { download = null; operation.Release(); }
    }
    public async Task SavePreferences(Preferences value)
    {
        if (!await operation.WaitAsync(0)) throw new InvalidOperationException("当前操作完成后再修改设置。");
        try
        {
            if (value.Diagnostics?.ProblemType is not (null or "auto" or "quests" or "interaction" or "connection" or "performance")) throw new InvalidDataException("诊断问题类型无效。");
            if (value.Diagnostics is { Enabled: true, HasConsent: false }) throw new InvalidOperationException("请先阅读并同意异常栈与游戏操作采集说明。");
            if (value.Account.Length > 64 || value.ClientDirectory.Length > 1000) throw new InvalidDataException("设置内容过长。");
            var directoryChanged = value.ClientDirectory != preferences.ClientDirectory;
            preferences = value with { Account = value.RememberAccount ? value.Account : "" };
            SafeFiles.Atomic(Path.Combine(stateRoot, "preferences.json"), JsonSerializer.SerializeToUtf8Bytes(preferences, WireJson.Default.Preferences));
            diagnostics.Configure(preferences.Diagnostics ?? new(), preferences.ClientDirectory, Profile.PvfSha256, bootstrap?.Sequence ?? 0);
            if (directoryChanged) { plan = []; await Task.Run(RefreshClientVersion); }
            Emit("ready", "设置已保存。");
        }
        finally { operation.Release(); }
    }
    public Task<ConfigurationReset> ResetGameConfiguration(bool confirmed) =>
        ResetGameConfiguration(confirmed, () => GameConfiguration.Reset(confirmed));

    internal async Task<ConfigurationReset> ResetGameConfiguration(bool confirmed, Func<ConfigurationReset> reset)
    {
        if (!confirmed) throw new InvalidOperationException("请先确认重置本机游戏配置。");
        if (!await operation.WaitAsync(0)) throw new InvalidOperationException("当前操作完成后再重置游戏配置。");
        try
        {
            Emit("resetting", "正在备份并重置本机游戏配置…");
            var result = reset();
            Emit("ready", result.Reset ? "本机游戏配置已备份并重置，请重新登录。" : "未发现本机游戏配置，无需重置。");
            return result;
        }
        catch { Emit("error", "游戏配置未重置，请查看提示。"); throw; }
        finally { operation.Release(); }
    }

    public async Task Check()
    {
        if (!await operation.WaitAsync(0)) throw new InvalidOperationException("已有操作正在进行。");
        try { await CheckCore(); }
        catch { Emit("error", "检查未完成，可重试或查看错误提示。"); throw; }
        finally { operation.Release(); }
    }
    private async Task CheckCore()
    {
        await RefreshMetadata();
        await PlanContent();
    }
    private async Task RefreshMetadata()
    {
        var metadataClock = Stopwatch.StartNew();
        Emit("checking", "正在读取本服签名发布信息…");
        using var api = new PlayerApi(root);
        using var metadataTimeout = new CancellationTokenSource(TimeSpan.FromSeconds(25));
        using var response = await api.Http.GetAsync(root.ApiUrl.TrimEnd('/') + LauncherBuild.ChannelPath + "bootstrap.json", HttpCompletionOption.ResponseHeadersRead, metadataTimeout.Token);
        if (response.StatusCode == System.Net.HttpStatusCode.NotFound && bootstrap is null)
        {
            plan = []; Emit("ready", "后台更新服务尚未部署；登录使用现有本服兼容配置。"); return;
        }
        response.EnsureSuccessStatusCode();
        await using var input = await response.Content.ReadAsStreamAsync(metadataTimeout.Token); using var output = new MemoryStream(); var buffer = new byte[8192]; int read;
        while ((read = await input.ReadAsync(buffer, metadataTimeout.Token)) != 0) { if (output.Length + read > Trust.MaxMetadata) throw new InvalidDataException("发布数据超过限制。"); output.Write(buffer, 0, read); }
        var raw = output.ToArray(); var next = Trust.Verify(raw, root);
        if (bootstrap is not null && envelope is not null) Trust.CheckSequence(next, raw, bootstrap, envelope);
        SafeFiles.Atomic(Path.Combine(stateRoot, "bootstrap.json"), raw); bootstrap = next; envelope = raw;
        diagnostics.Configure(preferences.Diagnostics ?? new(), preferences.ClientDirectory, Profile.PvfSha256, bootstrap?.Sequence ?? 0);
        diagnostics.Event("metadata_verified", metadataClock.Elapsed.TotalMilliseconds, bootstrap?.Sequence);
        Emit("checking", "发布信息已验证。");
    }
    private async Task PlanContent()
    {
        if (bootstrap is null) return;
        if (!SupportsContent(Profile)) { plan = []; Emit("ready", "请先更新登录器，再安装资源或登录游戏。"); return; }
        if (preferences.ClientDirectory == "") { Emit("ready", "发布信息已更新，请先选择客户端目录。"); return; }
        RequireNoGames();
        using var transaction = new FileTransaction(preferences.ClientDirectory); transaction.Recover();
        await Task.Run(RefreshClientVersion);
        if (bootstrap.Content is { } content)
        {
            Emit("checking", "正在校验客户端资源…"); plan = await ContentUpdater.Plan(preferences.ClientDirectory, content, CancellationToken.None, Path.Combine(stateRoot, "content-check.txt"));
            Emit("ready", plan.Length == 0 ? "客户端资源与本服批准版本一致。" : $"发现 {plan.Length} 个待更新文件，请点击安装更新。");
        }
        else { plan = []; await GameContract.VerifyClient(preferences.ClientDirectory, Profile); Emit("ready", "客户端兼容校验通过；后台尚未发布资源更新。"); }
    }
    public async Task UpdateContent()
    {
        if (!await operation.WaitAsync(0)) throw new InvalidOperationException("已有操作正在进行。");
        try
        {
            RequireNoGames(); await CheckCore(); await InstallContent();
        }
        catch (OperationCanceledException) { Emit("paused", "下载已暂停，点击继续会保留已下载的断点。", State.Downloaded, State.Total); }
        catch (Exception error) { diagnostics.Failure("update_error", error); Emit("error", "更新未完成，已保留当前版本与可恢复记录。", State.Downloaded, State.Total); throw; }
        finally { download = null; operation.Release(); }
    }
    private async Task InstallContent()
    {
        var updateClock = Stopwatch.StartNew();
        RequireContentSupport(Profile);
        if (bootstrap?.Content is not { } content || plan.Length == 0) return;
        RequireNoGames();
        using var transaction = new FileTransaction(preferences.ClientDirectory); transaction.Recover();
        using var client = ContentUpdater.Client(content); using var cancel = new CancellationTokenSource(TimeSpan.FromHours(6)); download = cancel;
            using var delivery = new LauncherBundleDownloader(root with { ApiUrl = content.Endpoint, CertificateSha256 = content.CertificateSha256 });
        var total = plan.Sum(ContentUpdater.TransferSize); long finished = 0;
        for (int i = 0; i < plan.Length; i++)
        {
            var file = plan[i]; var lastUpdate = Stopwatch.StartNew();
            Emit("downloading", "正在下载 " + file.Path, finished, total);
            await ContentUpdater.PrepareFile(client, content, file, Path.Combine(stateRoot, "downloads"), transaction.Stage(i), bytes =>
            { if (lastUpdate.ElapsedMilliseconds >= 80) { Emit("downloading", "正在下载 " + file.Path, finished + bytes, total); lastUpdate.Restart(); } }, cancel.Token, delivery);
            finished += ContentUpdater.TransferSize(file);
        }
        cancel.Token.ThrowIfCancellationRequested(); download = null; RequireNoGames();
        clientVersion = "";
        Emit("installing", "正在提交更新，保留恢复记录…", total, total);
        transaction.Commit(plan.Select(f => f.Path).ToArray(), envelope!); plan = [];
        await Task.Run(RefreshClientVersion);
        diagnostics.Event("update_complete", updateClock.Elapsed.TotalMilliseconds);
        Emit("ready", "更新完成，客户端资源已通过双重摘要校验。", total, total);
    }
    public async Task<LoginReply> AccountSecurity(string action, PasswordChange input)
    {
        if (action is not ("binding-code" or "password")) throw new ArgumentException(nameof(action));
        if (action == "binding-code" && !root.EnableQQBinding) throw new InvalidOperationException("此登录器未启用 QQ 绑定功能。");
        if (!await operation.WaitAsync(0)) throw new InvalidOperationException("已有操作正在进行。");
        try {
            ValidateCredentials(new(input.Account, input.Password));
            if (action == "password") ValidateCredentials(new(input.Account, input.NewPassword));
            using var api = new PlayerApi(Profile);
            return await api.Submit(action, input.Account.Trim(), input.Password, Device(), input.NewPassword);
        } finally { operation.Release(); }
    }
    public async Task Register(Credentials input)
    {
        if (!await operation.WaitAsync(0)) throw new InvalidOperationException("已有操作正在进行。");
        try { ValidateCredentials(input); Emit("logging-in", "正在注册本服账号…"); using var api = new PlayerApi(Profile); await api.Submit("register", input.Account.Trim(), input.Password, Device()); Emit("ready", "注册成功，请登录。"); }
        catch { Emit("error", "注册未完成，请查看提示。"); throw; }
        finally { operation.Release(); }
    }
    public async Task Login(Credentials input)
    {
        if (!OperatingSystem.IsWindows()) throw new PlatformNotSupportedException();
        if (!await operation.WaitAsync(0)) throw new InvalidOperationException("已有操作正在进行。");
#if ANTICHEAT
        AntiCheatSession? guard = null;
#endif
        try
        {
            diagnostics.Event("login_started");
            var stageClock = Stopwatch.StartNew();
            RequireContentSupport(Profile);
            ValidateCredentials(input); Emit("checking", "正在验证客户端与本服版本…");
            using var clientLock = new FileTransaction(preferences.ClientDirectory);
            if (File.Exists(Path.Combine(preferences.ClientDirectory, ".dnf90-update", "journal.json"))) RequireNoGames();
            clientLock.Recover();
            if (bootstrap?.Content is { } content)
            {
                plan = await ContentUpdater.Plan(preferences.ClientDirectory, content, CancellationToken.None, Path.Combine(stateRoot, "content-check.txt"));
                if (plan.Length != 0) throw new InvalidOperationException("请先完成客户端资源更新。");
            }
            await GameContract.VerifyClient(preferences.ClientDirectory, Profile);
            diagnostics.Event("client_verified", stageClock.Elapsed.TotalMilliseconds); stageClock.Restart();
            if (NativeGuildCompatibility.Prepare(preferences.ClientDirectory, Profile))
                Emit("checking", "公会兼容补丁已应用，原版客户端已备份。");
            games.RemoveAll(g => g.Process.HasExited);
            if (games.Any(g => g.Account.Equals(input.Account.Trim(), StringComparison.OrdinalIgnoreCase))) throw new InvalidOperationException("该账号已经启动，请使用另一个账号。");
            if (Profile.AntiCheat is { Enabled: true }) {
#if ANTICHEAT
                lock (guardLock) {
                    if (guardClosing) throw new InvalidOperationException("登录器已关闭。");
                    foreach (var finished in protectedGames.Where(g => g.Completed).ToArray()) { finished.Dispose(); protectedGames.Remove(finished); }
                }
                Emit("checking", "正在请求管理员授权以启动反作弊保护；退出登录器将结束本次游戏…");
                guard = await AntiCheatSession.Start(preferences.ClientDirectory, envelope, stateRoot, message => Emit("error", message), guardLifetime.Token);
                lock (guardLock) {
                    if (guardClosing) throw new InvalidOperationException("登录器已关闭。");
                    protectedGames.Add(guard);
                }
#else
                throw new InvalidOperationException("AC00：本服已要求反作弊保护，请使用 anticheat 发行包。");
#endif
            }
            var channels = await ChannelProbe.Check(Profile, m => Emit("checking", m));
            diagnostics.Event("channel_verified", stageClock.Elapsed.TotalMilliseconds, channels.ServerId); stageClock.Restart();
            Emit("logging-in", "正在验证账号…"); using var api = new PlayerApi(Profile, status => diagnostics.Event("account_http_response", number: status));
            var reply = await api.Submit("login", input.Account.Trim(), input.Password, Device());
            diagnostics.Event("account_verified", stageClock.Elapsed.TotalMilliseconds); stageClock.Restart();
            NativeChannelHistory.Prepare(Profile, channels);
            var executable = Path.Combine(preferences.ClientDirectory, "DNF.exe");
            var running = Process.GetProcessesByName("DNF");
            try
            {
                if (running.Length >= 2) throw new InvalidOperationException("最多同时运行两个游戏客户端。");
                if (running.Length == 1) NativeClientGate.PrepareSecondClient(running[0], executable, Profile.ExeSha256);
            }
            finally { foreach (var process in running) process.Dispose(); }
            var start = new ProcessStartInfo(executable) { UseShellExecute = false, WorkingDirectory = preferences.ClientDirectory, Arguments = GameContract.Arguments(reply.Token ?? "", Profile.GameHost, Profile.BootstrapPort) };
            Process game;
#if ANTICHEAT
            game = guard is null ? Process.Start(start) ?? throw new InvalidOperationException("游戏进程创建失败。") : await guard.Launch(executable, start.Arguments);
#else
            game = Process.Start(start) ?? throw new InvalidOperationException("游戏进程创建失败。");
#endif
            games.Add((input.Account.Trim(), game));
            diagnostics.Track(game, executable);
            diagnostics.Event("process_started", stageClock.Elapsed.TotalMilliseconds);
            preferences = preferences with { Account = preferences.RememberAccount ? input.Account.Trim() : "" };
            SafeFiles.Atomic(Path.Combine(stateRoot, "preferences.json"), JsonSerializer.SerializeToUtf8Bytes(preferences, WireJson.Default.Preferences));
            Emit("logging-in", "游戏进程已创建，正在等待客户端启动…");
            using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(8));
            try { await game.WaitForExitAsync(timeout.Token); } catch (OperationCanceledException) when (timeout.IsCancellationRequested) { }
            if (game.HasExited) throw new InvalidOperationException($"客户端提前退出（代码 {game.ExitCode}），请等待约 20 秒后重试。");
#if ANTICHEAT
            guard = null; // Ownership remains with the launcher until game/launcher exit.
#endif
            Emit("ready", channels.Message + " 客户端正在运行。");
        }
        catch (Exception error) { diagnostics.Failure("login_error", error); Emit("error", "登录未完成，请查看提示。"); throw; }
        finally {
#if ANTICHEAT
            guard?.Dispose();
#endif
            operation.Release();
        }
    }
    private static void ValidateCredentials(Credentials input)
    { if (input.Account.Trim().Length is < 1 or > 64 || input.Password.Length is < 1 or > 64) throw new InvalidDataException("账号或密码长度无效。"); }
    private static string Device()
    {
        if (!OperatingSystem.IsWindows()) throw new PlatformNotSupportedException();
        using var machine = Registry.LocalMachine.OpenSubKey(@"SOFTWARE\Microsoft\Cryptography");
        var identity = machine?.GetValue("MachineGuid") as string ?? Environment.MachineName;
        return Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes("ServerS5A3:" + identity))).ToLowerInvariant();
    }
    public static void RequireNoGames()
    {
        var processes = Process.GetProcessesByName("DNF");
        try { if (processes.Length > 0) throw new InvalidOperationException("更新或恢复文件前请退出所有游戏客户端。"); }
        finally { foreach (var p in processes) p.Dispose(); }
    }
    public Task<DiagnosticExport> ExportDiagnostics(string sessionId, string target) => diagnostics.Export(sessionId, target);
    public Task ElevateDiagnostics(string sessionId) => diagnostics.Elevate(sessionId);
    public void RecordDiagnosticError(Exception error) => diagnostics.Failure("operation_error", error);
    public void StopProtectedGames()
    {
#if ANTICHEAT
        lock (guardLock) { guardClosing = true; guardLifetime.Cancel(); foreach (var session in protectedGames) session.Dispose(); protectedGames.Clear(); }
#endif
    }
    public void Dispose() { StopProtectedGames(); download?.Cancel(); diagnostics.Dispose(); foreach (var (_, p) in games) p.Dispose(); operation.Dispose(); }
}
