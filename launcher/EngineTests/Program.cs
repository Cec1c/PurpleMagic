using System.IO.Compression;
using System.Net;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Dnf90.Launcher;
using ServerS5A3;

if (args.Length > 0 && args[0] == "--diagnostic-observer") { Environment.Exit(await Dnf90.Launcher.Diagnostics.DiagnosticObserver.Run(args)); return; }
if (args.Length == 2 && args[0] == "--diagnostic-crash-check") {
    Directory.CreateDirectory(args[1]);
    await CrashDiagnosticsChecks.Run(args[1], (ok, name) => { if (!ok) throw new Exception(name); Console.WriteLine("PASS " + name); }); return;
}

if (args.Length == 2 && args[0] == "--interop") { await Integration.Run(args[1]); return; }
if (args.Length == 2 && args[0] == "--news-interop") { await NewsIntegration.Run(args[1]); return; }
if (args.Length == 2 && args[0] == "--bundle-lifecycle") { await BundleLifecycleChecks.Run(args[1]); return; }
if (args.Length == 2 && args[0] == "--bundle-exit") { await BundleExitChecks.Run(args[1]); return; }

if (args.Length == 2 && args[0] == "--diagnostic-benchmark") { await DiagnosticBenchmark.Run(args[1]); return; }
if (args.Length == 1 && args[0] == "--diagnostic-at5t-child") { DiagnosticChecks.MemoryChild(true); return; }
if (args.Length == 1 && args[0] == "--diagnostic-memory-child") { DiagnosticChecks.MemoryChild(); return; }

var passed = 0;
void Check(bool condition, string name) { if (!condition) throw new Exception(name); passed++; Console.WriteLine("PASS " + name); }
void Reject(Action action, string name) { try { action(); } catch (Exception error) when (error is InvalidDataException or CryptographicException or IOException) { Check(true, name); return; } throw new Exception("Accepted: " + name); }
async Task RejectAsync(Func<Task> action, string name) { try { await action(); } catch (Exception error) when (error is InvalidDataException or IOException or InvalidOperationException) { Check(true, name); return; } throw new Exception("Accepted: " + name); }
var temp = Path.Combine(Path.GetTempPath(), "dnf90-engine-tests-" + Guid.NewGuid().ToString("N")); Directory.CreateDirectory(temp);
try
{
    await DiagnosticChecks.Run(temp, Check);
    using var key = RSA.Create(2048);
    var root = new PlayerProfile("https://accounts.example.test", new string('a', 64), key.ExportSubjectPublicKeyInfoPem(), "https://accounts.example.test/old-wpf-settings", "192.0.2.1", 7001, new string('b', 64), new string('c', 64), "");
    await CrashDiagnosticsChecks.Consent(temp, root, Check);
    await ClientDirectoryChecks.Run(temp, root, Check);
    await LauncherBundleDownloadChecks.Run(temp, root, Check);
    await ContentDirectDownloadChecks.Run(temp, root, Check);
    await GameConfigurationChecks.Run(temp, root, Check, RejectAsync);
    await AutomaticUpdateChecks.Run(temp, root, Check);
    BundleFailureChecks.Run(temp, Check);
    await ContentCompatibilityChecks.Run(temp, Check);
    var content = new ContentRelease("1", "author", "stable", 5, new string('d', 64), "https://content.example.test", "", [new("DNF.exe", 10, root.ExeSha256, true, "", "", 0), new("Script.pvf", 20, root.PvfSha256, true, "", "", 0)]);
    var bootstrap = new Bootstrap("DNF90-Flutter", 1, 10, root, [], content, null);
    byte[] Sign(Bootstrap value, RSASignaturePadding? padding = null)
    {
        var payload = JsonSerializer.SerializeToUtf8Bytes(value, WireJson.Default.Bootstrap);
        return JsonSerializer.SerializeToUtf8Bytes(new SignedEnvelope(Convert.ToBase64String(payload), Convert.ToBase64String(key.SignData(payload, HashAlgorithmName.SHA256, padding ?? RSASignaturePadding.Pss))), CoreJson.Default.SignedEnvelope);
    }
    var envelope = Sign(bootstrap);
    foreach (var required in new[] { false, true })
    {
        var policyDir = Path.Combine(temp, "binding-policy-" + required);
        Directory.CreateDirectory(policyDir);
        File.WriteAllBytes(Path.Combine(policyDir, "bootstrap.json"), Sign(bootstrap with { Profile = root with { RequireQQBinding = required } }));
        using var policyEngine = new LauncherEngine(root with { RequireQQBinding = !required }, policyDir);
        Check(policyEngine.State.RequireQQBinding == required, "verified server binding policy overrides embedded default: " + required);
    }

    Check(Trust.Verify(envelope, root).Content!.Files.Length == 2, "own PSS bootstrap and content contract");
    var transition = bootstrap with { Profile = root with { ChannelServerId = 1, PreviousChannelServerId = 0 } };
    var migrated = Trust.Verify(Sign(transition), root).Profile;
    Check(migrated.ChannelServerId == 1 && migrated.PreviousChannelServerId == 0, "signed directory transition survives AOT bootstrap JSON");
    Check(Trust.Verify(envelope, root).Profile.ChannelServerId == 0 && Trust.Verify(envelope, root).Profile.PreviousChannelServerId is null, "legacy signed directory defaults remain zero only");
    foreach (var profile in new[] { root with { ChannelServerId = -1 }, root with { ChannelServerId = 250 }, root with { PreviousChannelServerId = 0 }, root with { PreviousChannelServerId = -1 }, root with { PreviousChannelServerId = 250 } })
        Reject(() => Trust.Verify(Sign(bootstrap with { Profile = profile }), root), "signed invalid directory transition rejected before use");
    Reject(() => Trust.Verify(Sign(bootstrap, RSASignaturePadding.Pkcs1), root), "author PKCS1v15 cannot replace own PSS trust");
    using (var foreign = RSA.Create(2048)) Reject(() => Trust.Verify(envelope, root with { UpdatePublicKey = foreign.ExportSubjectPublicKeyInfoPem() }), "foreign signer");
    Reject(() => Trust.Verify(Sign(bootstrap with { Product = "other" }), root), "wrong product");
    Reject(() => Trust.Verify(Sign(bootstrap with { Profile = root with { ApiUrl = "https://content.example.test" } }), root), "account origin cannot move to author source");
    Reject(() => Trust.Verify(Sign(bootstrap with { Content = content with { Files = [content.Files[0] with { Path = "YKCN90Launcher.exe" }, content.Files[1]] } }), root), "foreign launcher cannot enter managed game content");
    Reject(() => Trust.CheckSequence(bootstrap with { Sequence = 9 }, envelope, bootstrap, envelope), "bootstrap rollback rejected");
    var changed = bootstrap with { News = [new("changed", "body", "更新", "today")] };
    Reject(() => Trust.CheckSequence(changed, Sign(changed), bootstrap, envelope), "same-sequence equivocation rejected");
    var sourceRollback = bootstrap with { Sequence = 11, Content = content with { SourceSequence = 4 } };
    Reject(() => Trust.CheckSequence(sourceRollback, Sign(sourceRollback), bootstrap, envelope), "source channel rollback rejected");
    foreach (var path in new[] { "../file", "x/../file", "/absolute", "C:/x", "x\\file", "NUL.txt", "x/COM1", "x. /file", ".dnf90-update/installed.json", "a\u0000b" })
        Reject(() => SafeFiles.Target(temp, path), "unsafe path " + path.Replace('\0', '?'));
    foreach (var step in new[] { "prepared", "backed-up", "installed", "receipt", "committed" })
    {
        var dir = Path.Combine(temp, step); Directory.CreateDirectory(dir); File.WriteAllText(Path.Combine(dir, "DNF.exe"), "old"); File.WriteAllText(Path.Combine(dir, "user.ini"), "keep");
        Directory.CreateDirectory(Path.Combine(dir, ".dnf90-update")); File.WriteAllText(Path.Combine(dir, ".dnf90-update", "installed.json"), "old-receipt");
        using (var transaction = new FileTransaction(dir))
        {
            File.WriteAllText(transaction.Stage(0), "new"); File.WriteAllText(transaction.Stage(1), "new-file");
            transaction.FaultPoint = (point, _) => { if (point == step) throw new SimulatedPowerLoss(); };
            try { transaction.Commit(["DNF.exe", "dstr.dat"], "new-receipt"u8.ToArray()); } catch (SimulatedPowerLoss) { }
        }
        using (var recovery = new FileTransaction(dir)) { recovery.Recover(); recovery.Recover(); }
        var committed = step == "committed";
        Check(File.ReadAllText(Path.Combine(dir, "DNF.exe")) == (committed ? "new" : "old"), "file recovery " + step);
        Check(File.Exists(Path.Combine(dir, "dstr.dat")) == committed, "new-file recovery " + step);
        Check(File.ReadAllText(Path.Combine(dir, ".dnf90-update", "installed.json")) == (committed ? "new-receipt" : "old-receipt"), "receipt recovery " + step);
        Check(File.ReadAllText(Path.Combine(dir, "user.ini")) == "keep", "unmanaged data retained " + step);
    }
    using (var locked = new FileTransaction(Path.Combine(temp, "exclusive"))) Reject(() => new FileTransaction(Path.Combine(temp, "exclusive")).Dispose(), "concurrent updater lock");
    var bytes = Encoding.UTF8.GetBytes("verified payload"); var hash = Trust.Digest(bytes); var partial = Path.Combine(temp, "download.part"); var url = new Uri("https://content.example.test/blobs/" + hash);
    File.WriteAllBytes(partial, bytes[..5]);
    using (var client = new HttpClient(new FakeHandler(request =>
    {
        Check(request.Headers.Range!.Ranges.Single().From == 5, "Range uses compressed/file offset");
        Check(request.Headers.Authorization is null && request.Content is null && !request.Headers.Contains("Cookie"), "author never receives account credentials");
        var response = new HttpResponseMessage(HttpStatusCode.PartialContent) { Content = new ByteArrayContent(bytes[5..]) }; response.Content.Headers.ContentRange = new ContentRangeHeaderValue(5, bytes.Length - 1, bytes.Length); return response;
    }))) await ContentUpdater.Download(client, url, bytes.Length, hash, partial, null, default);
    Check(File.ReadAllBytes(partial).SequenceEqual(bytes), "Range continuation verified");
    File.WriteAllBytes(partial, bytes[..5]);
    using (var client = new HttpClient(new FakeHandler(_ => new(HttpStatusCode.OK) { Content = new ByteArrayContent(bytes) }))) await ContentUpdater.Download(client, url, bytes.Length, hash, partial, null, default);
    Check(File.ReadAllBytes(partial).SequenceEqual(bytes), "server ignoring Range restarts instead of appending");
    File.WriteAllBytes(partial, bytes[..5]);
    using (var client = new HttpClient(new FakeHandler(_ => { var r = new HttpResponseMessage(HttpStatusCode.PartialContent) { Content = new ByteArrayContent(bytes) }; r.Content.Headers.ContentRange = new ContentRangeHeaderValue(0, bytes.Length - 1, bytes.Length); return r; })))
        await RejectAsync(() => ContentUpdater.Download(client, url, bytes.Length, hash, partial, null, default), "wrong Content-Range rejected");
    File.Delete(partial);
    using (var client = new HttpClient(new FakeHandler(_ => new(HttpStatusCode.OK) { Content = new ByteArrayContent(new byte[bytes.Length]) })))
        await RejectAsync(() => ContentUpdater.Download(client, url, bytes.Length, hash, partial, null, default), "bad payload hash rejected");
    Check(!File.Exists(partial), "bad payload is not reused");
    byte[] gzip;
    using (var compressed = new MemoryStream()) { using (var stream = new GZipStream(compressed, CompressionMode.Compress, true)) stream.Write(bytes); gzip = compressed.ToArray(); }
    var compressedFile = new ContentFile("dstr.dat", bytes.Length, hash, true, "gzip", Trust.Digest(gzip), gzip.Length);
    using (var client = new HttpClient(new FakeHandler(_ => new(HttpStatusCode.OK) { Content = new ByteArrayContent(gzip) })))
    {
        var stage = Path.Combine(temp, "gzip-stage");
        await ContentUpdater.PrepareFile(client, content, compressedFile, Path.Combine(temp, "cache"), stage, null, default);
        Check(File.ReadAllBytes(stage).SequenceEqual(bytes), "gzip transfer and final digest");
        await RejectAsync(() => ContentUpdater.PrepareFile(client, content, compressedFile with { Length = 1 }, Path.Combine(temp, "cache"), stage, null, default), "decompression bound");
        Check(!File.Exists(stage), "invalid decompressed stage removed");
    }
    await RejectAsync(() => ContentUpdater.Plan(temp, content with { Files = [content.Files[0] with { Downloadable = false }] }, default), "missing baseline is not silently skipped");
    var zipPath = Path.Combine(temp, "app.zip");
    using (var zip = ZipFile.Open(zipPath, ZipArchiveMode.Create)) foreach (var file in new[] { "DNF90.exe", "DNF90.Core.exe", "flutter_windows.dll", "data/app.so", "data/icudtl.dat" }) { using var writer = new StreamWriter(zip.CreateEntry(file).Open()); writer.Write("MZfixture"); }
    BundleInstaller.Extract(zipPath, Path.Combine(temp, "extract")); Check(File.Exists(Path.Combine(temp, "extract", "data", "app.so")), "complete Flutter bundle extracted");
    using (var zip = ZipFile.Open(zipPath, ZipArchiveMode.Update)) { using var writer = new StreamWriter(zip.CreateEntry("../outside.exe").Open()); writer.Write("MZbad"); }
    Reject(() => BundleInstaller.Extract(zipPath, Path.Combine(temp, "reject-extract")), "bundle path traversal rejected");
    var installation = Path.Combine(temp, "install"); Directory.CreateDirectory(installation); File.WriteAllText(Path.Combine(installation, "launcher.id"), BundleInstaller.Marker);
    var work = Path.Combine(installation, ".updates", "fixture"); Directory.CreateDirectory(Path.Combine(work, "previous")); File.WriteAllText(Path.Combine(work, "previous", "old"), "old"); Directory.CreateDirectory(Path.Combine(installation, "app")); File.WriteAllText(Path.Combine(installation, "app", "candidate"), "new");
    var job = new BundleJob(installation, "", "", 0, 0, 0, 0, work, "candidate-installed"); File.WriteAllBytes(Path.Combine(installation, ".launcher-update.json"), JsonSerializer.SerializeToUtf8Bytes(job, WireJson.Default.BundleJob));
    BundleInstaller.Recover(installation); Check(File.Exists(Path.Combine(installation, "app", "old")) && !File.Exists(Path.Combine(installation, "app", "candidate")), "whole-bundle crash rollback");
    Console.WriteLine($"{passed} engine checks passed; isolated files only, no production network, no game or GUI.");
}
finally { Directory.Delete(temp, true); }

sealed class FakeHandler(Func<HttpRequestMessage, HttpResponseMessage> handle) : HttpMessageHandler
{ protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) => Task.FromResult(handle(request)); }
