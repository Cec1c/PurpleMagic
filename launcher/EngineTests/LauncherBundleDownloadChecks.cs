using System.Net;
using System.Net.Http.Headers;
using System.Text;
using Dnf90.Launcher;
using ServerS5A3;

internal static class LauncherBundleDownloadChecks
{
    internal static async Task Run(string temp, PlayerProfile root, Action<bool, string> check)
    {
        var bytes = Encoding.UTF8.GetBytes("immutable launcher app fixture");
        var hash = Trust.Digest(bytes);
        var release = new BundleRelease("0.2.6", root.ApiUrl + LauncherBuild.ChannelPath + "bundles/" + hash + ".zip", bytes.Length, hash);
        var parent = Path.Combine(temp, "bundle-transport"); Directory.CreateDirectory(parent);
        string Cache() => Path.Combine(parent, Guid.NewGuid().ToString("N"));
        bool IsOrigin(HttpRequestMessage r) => r.RequestUri!.Host == new Uri(root.ApiUrl).Host;
        bool Negotiates(HttpRequestMessage r) => r.Headers.TryGetValues(LauncherBundleDownloader.CapabilityHeader, out var v) && v.Single() == LauncherBundleDownloader.Capability;
        static HttpResponseMessage Redirect(string url)
        {
            var r = new HttpResponseMessage(HttpStatusCode.Redirect); r.Headers.Location = new Uri(url); return r;
        }
        HttpResponseMessage Payload(HttpRequestMessage r)
        {
            var offset = (int)(r.Headers.Range?.Ranges.Single().From ?? 0);
            var response = new HttpResponseMessage(offset == 0 ? HttpStatusCode.OK : HttpStatusCode.PartialContent) { Content = new ByteArrayContent(bytes[offset..]) };
            if (offset != 0) response.Content.Headers.ContentRange = new ContentRangeHeaderValue(offset, bytes.Length - 1, bytes.Length);
            return response;
        }
        async Task Reject(Func<Task> action, string name)
        {
            try { await action(); }
            catch (InvalidDataException) { check(true, name); return; }
            throw new Exception("Accepted: " + name);
        }
        LauncherBundleDownloader Client(Func<HttpRequestMessage, HttpResponseMessage> handle) => new(root, new Handler((r, _) => Task.FromResult(handle(r))));

        using (var handler = LauncherBundleDownloader.CreateHandler(root))
            check(!handler.AllowAutoRedirect && !handler.UseCookies && !handler.UseDefaultCredentials && handler.Credentials is null && !handler.UseProxy && handler.AutomaticDecompression == DecompressionMethods.None,
                "bundle HTTP client disables automatic redirects, cookies, credentials and decompression");

        using (var client = Client(_ => throw new Exception("Cross-channel request reached network")))
        {
            var other = LauncherBuild.AntiCheat ? "/launcher/v2/" : "/launcher/anticheat/v2/";
            await Reject(() => client.Download(release with { Url = root.ApiUrl + other + "bundles/" + hash + ".zip" }, Cache(), null, default),
                "other flavor channel rejected before network access");
        }

        var cache = Cache(); var calls = 0;
        using (var client = Client(r => { calls++; check(Negotiates(r), "only explicit capable client asks for Baidu mode"); return Payload(r); }))
        {
            var path = await client.Download(release, cache, null, default);
            check(File.ReadAllBytes(path).SequenceEqual(bytes), "ordinary gateway 200 stays compatible with new transport");
            check(await client.Download(release, cache, null, default) == path && calls == 1, "verified immutable app cache avoids duplicate download");
            File.WriteAllBytes(path, new byte[bytes.Length]);
            await client.Download(release, cache, null, default);
            check(calls == 2 && File.ReadAllBytes(path).SequenceEqual(bytes), "corrupt complete cache is verified and replaced");
        }

        cache = Cache(); Directory.CreateDirectory(cache);
        var finishedPartial = Path.Combine(cache, hash + "-" + bytes.Length + ".part"); File.WriteAllBytes(finishedPartial, bytes);
        using (var client = Client(_ => throw new Exception("complete prefix should not contact network")))
            check(File.ReadAllBytes(await client.Download(release, cache, null, default)).SequenceEqual(bytes) && !File.Exists(finishedPartial),
                "fully written app prefix is verified and promoted after process interruption");
        using (var cancellation = new CancellationTokenSource())
        using (var client = Client(_ => throw new Exception("cancelled operation reached network")))
        {
            cancellation.Cancel();
            try { await client.Download(release, Cache(), null, cancellation.Token); throw new Exception("Pre-cancel ignored"); }
            catch (OperationCanceledException) { check(true, "already cancelled app request makes no network request"); }
        }

        // No server activation is implied: the unchanged legacy byte downloader still works with this protocol's default response.
        using (var legacy = new HttpClient(new Handler((r, _) =>
        {
            check(!Negotiates(r), "legacy downloader sends no transport capability");
            return Task.FromResult(Payload(r));
        })))
        {
            var path = Path.Combine(parent, "legacy.part"); File.WriteAllBytes(path, bytes[..5]);
            await ContentUpdater.Download(legacy, new Uri(release.Url), bytes.Length, hash, path, null, default);
            check(File.ReadAllBytes(path).SequenceEqual(bytes), "legacy default 206 bytes retain existing hash and resume behavior");
        }

        calls = 0;
        using (var client = Client(r =>
        {
            calls++;
            check(r.Headers.Authorization is null && !r.Headers.Contains("Cookie") && r.Headers.Referrer is null && r.Content is null,
                "fresh bundle request carries no account authorization, cookie, referrer or body");
            if (IsOrigin(r))
            {
                var response = Redirect("https://d.pcs.baidu.com/file?fixture=one");
                response.Headers.Add("Set-Cookie", "account=must-not-forward; Secure"); return response;
            }
            check(!Negotiates(r) && r.Headers.UserAgent.ToString() == "pan.baidu.com", "provider receives only scoped UA and no gateway capability");
            return r.RequestUri!.Host == "d.pcs.baidu.com" ? Redirect("https://cdn.baidupcs.com/file?fixture=two") : Payload(r);
        }))
        {
            check(File.ReadAllBytes(await client.Download(release, Cache(), null, default)).SequenceEqual(bytes) && calls == 3,
                "controlled HTTPS provider chain verifies the signed app identity");
        }

        // Persisted bytes survive cancellation and a new downloader instance; server ignoring Range restarts cleanly.
        foreach (var ignoreRange in new[] { false, true })
        {
            cache = Cache(); using var cancellation = new CancellationTokenSource();
            using (var client = Client(_ => new(HttpStatusCode.OK) { Content = new StreamContent(new ChunkStream(bytes, 5)) }))
            {
                try { await client.Download(release, cache, _ => cancellation.Cancel(), cancellation.Token); throw new Exception("Cancellation ignored"); }
                catch (OperationCanceledException) { check(true, "app interruption preserves cancellation semantics"); }
            }
            check(new FileInfo(Directory.GetFiles(cache, "*.part").Single()).Length == 5, "cancelled app prefix remains for a later invocation");
            using (var client = Client(r =>
            {
                check(r.Headers.Range?.Ranges.Single().From == 5, "new invocation resumes app by immutable hash and size");
                return ignoreRange ? new(HttpStatusCode.OK) { Content = new ByteArrayContent(bytes) } : Payload(r);
            })) check(File.ReadAllBytes(await client.Download(release, cache, null, default)).SequenceEqual(bytes),
                ignoreRange ? "200 ignoring Range replaces prefix instead of appending" : "206 resumes across downloader instances");
        }

        var origins = 0; var providerCalls = 0;
        using (var client = Client(r =>
        {
            if (IsOrigin(r)) return Redirect("https://cdn.baidupcs.com/file?generation=" + ++origins);
            providerCalls++; return origins == 1 ? new(HttpStatusCode.Forbidden) : Payload(r);
        }))
        {
            await client.Download(release, Cache(), null, default);
            check(origins == 2 && providerCalls == 2, "expired provider link re-resolves once at signed gateway URL");
        }

        origins = 0; var fallback = false;
        using (var client = Client(r =>
        {
            if (IsOrigin(r))
            {
                origins++;
                if (Negotiates(r)) return Redirect("https://cdn.baidupcs.com/file?attempt=" + origins);
                fallback = true; check(r.RequestUri!.AbsoluteUri == release.Url && r.Headers.Range?.Ranges.Single().From == 5,
                    "legacy fallback uses the identical signed URL and retained offset");
                return Payload(r);
            }
            if (origins > 1) return new(HttpStatusCode.ServiceUnavailable);
            var response = new HttpResponseMessage(HttpStatusCode.OK) { Content = new StreamContent(new ChunkStream(bytes, 5, failAfterFirst: true)) };
            response.Content.Headers.ContentLength = bytes.Length; return response;
        }))
        {
            check(File.ReadAllBytes(await client.Download(release, Cache(), null, default)).SequenceEqual(bytes) && origins == 3 && fallback,
                "interruption and unavailable provider fall back to default bytes with final hash check");
        }

        foreach (var bad in new[] { "http://cdn.baidupcs.com/file", "https://cdn.baidupcs.com.evil.test/file", "https://evilbaidupcs.com/file", "https://evil.test/file", "https://user:password@cdn.baidupcs.com/file", "https://cdn.baidupcs.com:444/file", "https://cdn.baidupcs.com/file#fragment", root.ApiUrl + "/api/account/login" })
        {
            calls = 0;
            using var client = Client(_ => { calls++; return Redirect(bad); });
            await Reject(() => client.Download(release, Cache(), null, default), "unsafe redirect rejected before following: " + new Uri(bad).Host);
            check(calls == 1, "unsafe redirect performs no external request or retry");
        }
        using (var client = Client(_ => Redirect("https://cdn.baidupcs.com/loop")))
            await Reject(() => client.Download(release, Cache(), null, default), "repeated redirect URL rejected");
        calls = 0;
        using (var client = Client(_ => Redirect("https://cdn.baidupcs.com/hop/" + ++calls)))
        {
            await Reject(() => client.Download(release, Cache(), null, default), "distinct redirect chain has a fixed bound");
            check(calls == 5, "maximum four redirects plus original request");
        }
        using (var client = Client(_ => new(HttpStatusCode.Redirect)))
            await Reject(() => client.Download(release, Cache(), null, default), "redirect without Location rejected");

        foreach (var kind in new[] { "range", "length", "encoding", "hash", "oversize", "200-range" })
        {
            cache = Cache(); calls = 0;
            using var client = Client(_ =>
            {
                calls++;
                var response = new HttpResponseMessage(HttpStatusCode.OK) { Content = new ByteArrayContent(bytes) };
                switch (kind)
                {
                    case "range": response.StatusCode = HttpStatusCode.PartialContent; response.Content.Headers.ContentRange = new ContentRangeHeaderValue(1, bytes.Length - 1, bytes.Length); break;
                    case "length": response.Content.Headers.ContentLength = bytes.Length + 1; break;
                    case "encoding": response.Content.Headers.ContentEncoding.Add("gzip"); break;
                    case "hash": response.Content = new ByteArrayContent(new byte[bytes.Length]); break;
                    case "oversize": response.Content = new StreamContent(new ChunkStream([.. bytes, 1], 3)); break;
                    case "200-range": response.Content.Headers.ContentRange = new ContentRangeHeaderValue(0, bytes.Length - 1, bytes.Length); break;
                }
                return response;
            });
            await Reject(() => client.Download(release, cache, null, default), "malformed or corrupt payload rejected: " + kind);
            check(calls == 1 && Directory.GetFiles(cache, "*.part").Length == 0 && Directory.GetFiles(cache, "*.zip").Length == 0,
                "integrity rejection is terminal and removes untrusted cache");
        }

        foreach (var invalid in new[] { release with { Url = "http://accounts.example.test/file" }, release with { Url = root.ApiUrl + "/api/account/login" },
            release with { Url = release.Url + "?alternate=1" }, release with { Url = release.Url.Replace(new Uri(root.ApiUrl).Host, "other.example.test") },
            release with { Sha256 = "bad" }, release with { Size = 0 }, release with { Size = SignedUpdate.MaxPackageBytes + 1 }, release with { Version = "bad" } })
        {
            using var client = Client(_ => throw new Exception("invalid release reached network"));
            await Reject(() => client.Download(invalid, Cache(), null, default), "invalid bundle identity or noncanonical URL rejected before network");
        }

        calls = 0;
        using (var client = Client(_ => { calls++; throw new HttpRequestException("https://cdn.baidupcs.com/?secret=never-log-this"); }))
        {
            try { await client.Download(release, Cache(), null, default); throw new Exception("Network failure ignored"); }
            catch (IOException e) { check(calls == 3 && e.InnerException is null && !e.ToString().Contains("never-log-this"), "bounded retries strip sensitive URLs from public errors"); }
        }
        using (var client = Client(r => Negotiates(r) ? new(HttpStatusCode.ServiceUnavailable) : Redirect("https://cdn.baidupcs.com/file")))
            await Reject(() => client.Download(release, Cache(), null, default), "default byte fallback refuses redirects");

        cache = Cache();
        var entered = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var unblock = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        using (var client = new LauncherBundleDownloader(root, new Handler(async (r, token) => { entered.SetResult(); await unblock.Task.WaitAsync(token); return Payload(r); })))
        {
            var first = client.Download(release, cache, null, default); await entered.Task;
            try
            {
                using var second = Client(_ => throw new Exception("concurrent writer reached network"));
                try { await second.Download(release, cache, null, default); throw new Exception("Concurrent writer accepted"); }
                catch (IOException) { check(true, "same app cache serializes different downloader instances"); }
            }
            finally { unblock.SetResult(); }
            await first;
        }
    }

    private sealed class Handler(Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> handle) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) => handle(request, cancellationToken);
    }

    // A non-seekable stream exercises unknown content length and read interruption, not ByteArrayContent shortcuts.
    private sealed class ChunkStream(byte[] bytes, int chunk, bool failAfterFirst = false) : Stream
    {
        private int position;
        public override bool CanRead => true;
        public override bool CanSeek => false;
        public override bool CanWrite => false;
        public override long Length => throw new NotSupportedException();
        public override long Position { get => position; set => throw new NotSupportedException(); }
        public override int Read(byte[] buffer, int offset, int count)
        {
            if (failAfterFirst && position > 0) throw new IOException("fixture interrupted");
            var length = Math.Min(Math.Min(chunk, count), bytes.Length - position);
            bytes.AsSpan(position, length).CopyTo(buffer.AsSpan(offset)); position += length; return length;
        }
        public override ValueTask<int> ReadAsync(Memory<byte> buffer, CancellationToken cancellationToken = default)
        {
            cancellationToken.ThrowIfCancellationRequested();
            var temporary = new byte[buffer.Length]; var count = Read(temporary, 0, temporary.Length);
            temporary.AsMemory(0, count).CopyTo(buffer); return ValueTask.FromResult(count);
        }
        public override void Flush() => throw new NotSupportedException();
        public override long Seek(long offset, SeekOrigin origin) => throw new NotSupportedException();
        public override void SetLength(long value) => throw new NotSupportedException();
        public override void Write(byte[] buffer, int offset, int count) => throw new NotSupportedException();
    }
}
