using System.Globalization;
using System.Net;
using System.Net.Http.Headers;
using ServerS5A3;

namespace Dnf90.Launcher;

/// <summary>Transport for already verified launcher bundles and immutable game blobs; never account requests.</summary>
public sealed class LauncherBundleDownloader : IDisposable
{
    public const string CapabilityHeader = "X-DNF90-Bundle-Transport";
    public const string Capability = "baidu-v1";
    private const int MaxRedirects = 4;
    private readonly Uri origin;
    private readonly HttpClient http;

    public LauncherBundleDownloader(PlayerProfile root) : this(root, CreateHandler(root)) { }

    // Tests own an isolated handler; production never reuses an account HttpClient or its headers.
    internal LauncherBundleDownloader(PlayerProfile root, HttpMessageHandler handler)
    {
        SignedUpdate.RequireHttps(root.ApiUrl);
        origin = new Uri(root.ApiUrl);
        http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
    }

    internal static HttpClientHandler CreateHandler(PlayerProfile root) => new()
    {
        AllowAutoRedirect = false, UseCookies = false, UseProxy = false,
        UseDefaultCredentials = false, Credentials = null,
        AutomaticDecompression = DecompressionMethods.None,
        ServerCertificateCustomValidationCallback = (request, cert, _, errors) =>
            request.RequestUri is { } uri && (root.CertificateSha256 == "" ? errors == System.Net.Security.SslPolicyErrors.None : PlayerApi.CertificateAllowed(uri, cert, errors, root))
    };

    /// <summary>Returns a verified immutable cache file; caller still performs installer/signature checks.</summary>
    public async Task<string> Download(BundleRelease release, string cacheDirectory, Action<long>? progress, CancellationToken cancel)
    {
        var url = ValidateRelease(release);
        var cache = Path.GetFullPath(cacheDirectory);
        SafeFiles.NoLinks(cache); Directory.CreateDirectory(cache);
        var key = release.Sha256.ToLowerInvariant() + "-" + release.Size.ToString(CultureInfo.InvariantCulture);
        var complete = Path.Combine(cache, key + ".zip");
        var partial = Path.Combine(cache, key + ".part");
        var lockPath = Path.Combine(cache, key + ".lock");
        SafeFiles.NoLinks(lockPath); SafeFiles.NoLinks(complete); SafeFiles.NoLinks(partial);
        // Leave the lock file in place: deleting it after close permits two different lock handles.
        using var exclusive = new FileStream(lockPath, FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None);
        using var deadline = CancellationTokenSource.CreateLinkedTokenSource(cancel);
        deadline.CancelAfter(TimeSpan.FromMinutes(30));
        var token = deadline.Token;
        try
        {
            token.ThrowIfCancellationRequested();
            if (await SafeFiles.Matches(complete, release.Size, release.Sha256, token))
            { progress?.Invoke(release.Size); return complete; }
            File.Delete(complete);
            if (await SafeFiles.Matches(partial, release.Size, release.Sha256, token))
            { File.Move(partial, complete); progress?.Invoke(release.Size); return complete; }
            if (File.Exists(partial) && new FileInfo(partial).Length >= release.Size) File.Delete(partial);

            // Re-resolve once on transient/expired links, then request legacy bytes with no capability.
            // All three attempts start at the same signed immutable URL; no fallback URL is supplied by a CDN.
            for (var attempt = 0; attempt < 3; attempt++)
            {
                token.ThrowIfCancellationRequested();
                try
                {
                    await Transfer(url, release.Size, partial, attempt < 2, progress, token, attempt == 1);
                    if (!await SafeFiles.Matches(partial, release.Size, release.Sha256, token))
                        throw new InvalidDataException("登录器更新摘要不匹配。");
                    SafeFiles.NoLinks(complete); File.Move(partial, complete, true);
                    return complete;
                }
                catch (HttpRequestException) when (attempt < 2) { }
                catch (IOException) when (attempt < 2) { }
            }
            throw new IOException("登录器下载未完成，可重试。");
        }
        catch (InvalidDataException)
        {
            // Integrity/policy failures are terminal, not a reason to accept an alternative payload.
            SafeFiles.NoLinks(partial); File.Delete(partial);
            throw new InvalidDataException("登录器更新响应未通过校验，已丢弃临时文件。");
        }
        catch (OperationCanceledException) when (cancel.IsCancellationRequested)
        { throw new OperationCanceledException("登录器下载已取消，可继续下载。", cancel); }
        catch (OperationCanceledException)
        { throw new IOException("登录器下载超时，可继续下载。"); }
        // Do not expose signed CDN query strings through HTTP exception messages or inner exceptions.
        catch (HttpRequestException) { throw new IOException("登录器下载连接失败，可继续下载。"); }
        catch (IOException) { throw new IOException("登录器下载未完成，可继续下载。"); }
    }

    // Uses the existing content partial-file name, so a 0.2.8 download can resume
    // through Baidu after upgrading. Only the signed blob identity is accepted.
    public async Task DownloadContent(ContentRelease release, ContentFile file, string partial, Action<long>? progress, CancellationToken cancel)
    {
        var size = ContentUpdater.TransferSize(file);
        var hash = file.Compression == "gzip" ? file.BlobSha256 : file.Sha256;
        SignedUpdate.RequireHttps(release.Endpoint);
        var source = new Uri(release.Endpoint);
        if (source.Host != origin.Host || source.Port != origin.Port || source.AbsolutePath != "/" || source.Query != "" || source.Fragment != "" ||
            !Trust.Hash(hash) || size < 1 || size > 8L * 1024 * 1024 * 1024 || !file.Downloadable ||
            !release.Files.Contains(file) || file.Compression is not ("" or "none" or "gzip"))
            throw new InvalidDataException("资源下载身份无效。");
        var entry = new Uri(source, "/blobs/" + hash.ToLowerInvariant());
        SafeFiles.NoLinks(partial); Directory.CreateDirectory(Path.GetDirectoryName(Path.GetFullPath(partial))!);
        SafeFiles.NoLinks(partial + ".lock");
        using var exclusive = new FileStream(partial + ".lock", FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None);
        try
        {
            cancel.ThrowIfCancellationRequested();
            if (await SafeFiles.Matches(partial, size, hash, cancel)) { progress?.Invoke(size); return; }
            for (int attempt = 0; attempt < 3; attempt++)
            {
                try
                {
                    await Transfer(entry, size, partial, attempt < 2, progress, cancel, attempt == 1);
                    if (!await SafeFiles.Matches(partial, size, hash, cancel)) throw new InvalidDataException("资源摘要不匹配。");
                    return;
                }
                catch (HttpRequestException) when (attempt < 2) { }
                catch (IOException) when (attempt < 2) { }
            }
        }
        catch (InvalidDataException) { SafeFiles.NoLinks(partial); File.Delete(partial); throw new InvalidDataException("资源下载未通过校验，已丢弃临时文件。"); }
        catch (HttpRequestException) { throw new IOException("资源下载连接失败，可继续下载。"); }
        catch (IOException) { throw new IOException("资源下载中断，可继续下载。"); }
    }

    private Uri ValidateRelease(BundleRelease release)
    {
        if (!Trust.Hash(release.Sha256) || release.Size is < 1 or > SignedUpdate.MaxPackageBytes || !Version.TryParse(release.Version, out _))
            throw new InvalidDataException("登录器包信息无效。");
        SignedUpdate.RequireHttps(release.Url);
        var uri = new Uri(release.Url);
        if (uri.Host != origin.Host || uri.Port != origin.Port || uri.Query != "" || uri.Fragment != "" ||
            uri.AbsolutePath != LauncherBuild.ChannelPath + "bundles/" + release.Sha256.ToLowerInvariant() + ".zip")
            throw new InvalidDataException("登录器包必须使用受信网关的内容寻址地址。");
        return uri;
    }

    private static void RequireProvider(Uri uri)
    {
        var host = uri.IdnHost;
        if (!uri.IsAbsoluteUri || uri.Scheme != "https" || uri.Port != 443 || uri.UserInfo != "" || uri.Fragment != "" ||
            !(host == "pan.baidu.com" || host == "d.pcs.baidu.com" || host == "baidupcs.com" || host.EndsWith(".baidupcs.com", StringComparison.Ordinal)))
            throw new InvalidDataException("登录器下载跳转地址不允许。");
    }

    private async Task Transfer(Uri entry, long size, string partial, bool negotiate, Action<long>? progress, CancellationToken cancel, bool fresh = false)
    {
        SafeFiles.NoLinks(partial);
        long offset = File.Exists(partial) ? new FileInfo(partial).Length : 0;
        if (offset >= size) { File.Delete(partial); offset = 0; }
        var current = entry;
        var visited = new HashSet<string>(StringComparer.Ordinal);
        for (var hop = 0; ; hop++)
        {
            if (!visited.Add(current.AbsoluteUri)) throw new InvalidDataException("登录器下载跳转循环。");
            using var request = new HttpRequestMessage(HttpMethod.Get, current);
            request.Headers.AcceptEncoding.Add(new StringWithQualityHeaderValue("identity"));
            if (offset > 0) request.Headers.Range = new RangeHeaderValue(offset, null);
            if (hop == 0 && negotiate) { request.Headers.Add(CapabilityHeader, Capability); if (fresh) request.Headers.Add("X-DNF90-Refresh-Link", "1"); }
            if (hop != 0) request.Headers.UserAgent.ParseAdd("pan.baidu.com");
            using var response = await http.SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancel);
            if (response.StatusCode is HttpStatusCode.MovedPermanently or HttpStatusCode.Redirect or HttpStatusCode.SeeOther or HttpStatusCode.TemporaryRedirect or HttpStatusCode.PermanentRedirect)
            {
                if (!negotiate || hop >= MaxRedirects || response.Headers.Location is not { } location || !Uri.TryCreate(current, location, out var next))
                    throw new InvalidDataException("登录器下载跳转无效或超过限制。");
                RequireProvider(next); current = next; continue;
            }
            if (response.StatusCode == HttpStatusCode.PartialContent)
            {
                var range = response.Content.Headers.ContentRange;
                if (range is null || range.Unit != "bytes" || range.From != offset || range.To != size - 1 || range.Length != size)
                    throw new InvalidDataException("登录器下载断点范围不匹配。");
            }
            else if (response.StatusCode == HttpStatusCode.OK)
            {
                offset = 0;
                if (response.Content.Headers.ContentRange is not null) throw new InvalidDataException("完整响应包含断点范围。");
            }
            else throw new IOException("登录器下载源暂不可用。");
            if (response.Content.Headers.ContentEncoding.Any(e => !e.Equals("identity", StringComparison.OrdinalIgnoreCase)) ||
                response.Content.Headers.ContentLength is { } length && length != size - offset)
                throw new InvalidDataException("登录器下载编码或长度不匹配。");
            await Save(response.Content, partial, offset, size, progress, cancel);
            return;
        }
    }

    private static async Task Save(HttpContent content, string partial, long offset, long size, Action<long>? progress, CancellationToken cancel, bool fresh = false)
    {
        SafeFiles.NoLinks(partial);
        await using var input = await content.ReadAsStreamAsync(cancel);
        await using var output = new FileStream(partial, offset == 0 ? FileMode.Create : FileMode.Append, FileAccess.Write, FileShare.None, 65536, true);
        var buffer = new byte[65536]; var total = offset;
        while (true)
        {
            cancel.ThrowIfCancellationRequested();
            var read = await input.ReadAsync(buffer, cancel);
            if (read == 0) break;
            if (read > size - total) throw new InvalidDataException("登录器更新负载超过签名大小。");
            await output.WriteAsync(buffer.AsMemory(0, read), cancel);
            total += read; progress?.Invoke(total);
        }
        await output.FlushAsync(cancel); output.Flush(true);
        if (total != size) throw new IOException("登录器下载中断。");
    }

    public void Dispose() => http.Dispose();
}
