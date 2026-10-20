using System.Diagnostics;
using System.Text.Json;
using Dnf90.Launcher.Diagnostics;

internal static class DiagnosticBenchmark
{
    internal static async Task Run(string output)
    {
        Directory.CreateDirectory(output);
        var memory = DiagnosticChecks.Fixture(); var modules = new[] { new At5tModule(memory, 0x400000), new At5tModule(memory, 0x400000) };
        var rings = new[] { new DiagnosticRing(2 * 1024 * 1024), new DiagnosticRing(2 * 1024 * 1024) };
        var previous = new Dictionary<string, DiagnosticField>[] { [], [] };
        var sequence = new int[2]; var fullSequence = new int[2];
        var ids = new[] { Guid.NewGuid().ToString("N"), Guid.NewGuid().ToString("N") };
        var clock = Stopwatch.StartNew(); using var process = Process.GetCurrentProcess(); var cpu = process.TotalProcessorTime;
        var allocated = GC.GetTotalAllocatedBytes(); long samples = 0; var durations = new List<double>(); int peakRing = 0;
        // Two-hour logical timeline, two instances, 30-second bursts every five minutes.
        for (var time = 0; time < 7200000; time += 5)
        {
            var specialized = time % 300000 < 30000;
            if (!specialized && time % 1000 != 0) continue;
            for (var i = 0; i < 2; i++)
            {
                var before = Stopwatch.GetTimestamp();
                var snapshot = modules[i].Sample(time, specialized);
                durations.Add(Stopwatch.GetElapsedTime(before).TotalMilliseconds);
                var full = previous[i].Count == 0 || time % 1000 == 0;
                var fields = full ? snapshot : snapshot.Where(f => !previous[i].TryGetValue(f.Key, out var old) || f.Value != old).ToDictionary();
                if (full) fullSequence[i] = sequence[i] + 1;
                fields["timing.interval_ms"] = new(specialized ? 5 : 1000);
                var line = DiagnosticChecks.Line(ids[i], ++sequence[i], time, full, specialized ? "sample" : "scene", fields, fullSequence[i]);
                rings[i].Add(line); previous[i] = snapshot; samples++;
            }
            peakRing = Math.Max(peakRing, rings.Sum(r => r.Bytes));
        }
        var seconds = clock.Elapsed.TotalSeconds; var cpuSeconds = (process.TotalProcessorTime - cpu).TotalSeconds;
        durations.Sort();
        var fixture = new {
            evidence = "accelerated logical-time fixture, not two hours of native gameplay", logicalHours = 2, instances = 2, samples,
            elapsedSeconds = seconds, collectorCpuSeconds = cpuSeconds, allocatedBytes = GC.GetTotalAllocatedBytes() - allocated,
            readP50Ms = durations[durations.Count / 2], readP95Ms = durations[(int)(durations.Count * .95)], readP99Ms = durations[(int)(durations.Count * .99)],
            peakRingAccountedBytes = peakRing, ringBudgetBytes = 4 * 1024 * 1024,
            worstCaseAllRetainedBuffersBytes = (19 * 1024 * 1024) + 768 * 1024,
            cacheBudgetBytes = 32 * 1024 * 1024, processWorkingSetBytes = process.WorkingSet64,
            actualTwoHourGameSoak = "pending", frameTimeComparison = "pending native client instrumentation"
        };
        File.WriteAllText(Path.Combine(output, "logical-two-hour.json"), JsonSerializer.Serialize(fixture, new JsonSerializerOptions { WriteIndented = true }));
        var storage = new DiagnosticStorage(Path.Combine(output, "sample-local"));
        var session = Guid.NewGuid().ToString("N"); var job = DiagnosticChecks.Job(session);
        var fullFields = new At5tModule(DiagnosticChecks.Fixture(), 0x400000).Sample(0, true);
        var lines = new[] { job.Lines[0], DiagnosticChecks.Line(session, 2, 10, true, "sample", fullFields), DiagnosticChecks.Line(session, 3, 15, false, "sample", new() { ["descriptor.state"] = new(1), ["logical4.0.fresh"] = new(0) }, 2) };
        // The example is explicitly synthetic and has no real installation paths.
        storage.Save(job with { Lines = lines }); var exported = storage.Export(session, [job.Summary.IncidentId], output);
        File.Move(exported.Path, Path.Combine(output, "diagnostics-synthetic.zip"), true);
        if (OperatingSystem.IsWindows()) await Native(output);
        Console.WriteLine("Diagnostics benchmark and synthetic ZIP: " + output);
    }
    private static async Task Native(string output)
    {
        using var child = Process.Start(new ProcessStartInfo(Environment.ProcessPath!) { UseShellExecute = false, RedirectStandardOutput = true, RedirectStandardInput = true, ArgumentList = { "--diagnostic-at5t-child" } })!;
        try {
            await child.StandardOutput.ReadLineAsync();
            using var read = new ReadOnlyGameProcess(child.Id, Environment.ProcessPath!); var module = new At5tModule(read, 0x400000);
            var results = new List<object>();
            foreach (var mode in new[] { "off", "basic", "specialty" }) {
                using var collector = Process.GetCurrentProcess();
                var collectorCpu = collector.TotalProcessorTime; var targetCpu = child.TotalProcessorTime;
                var ring = new DiagnosticRing(2 * 1024 * 1024);
                Dictionary<string, DiagnosticField> previous = []; long fullSequence = 0; double lastFull = -1000; var session = Guid.NewGuid().ToString("N");
                var clock = Stopwatch.StartNew(); var gaps = new List<double>(); var reads = new List<double>(); double last = 0; long count = 0;
                using var pacer = new DiagnosticPacer();
                while (clock.Elapsed.TotalSeconds < (mode == "specialty" ? 30 : 10)) {
                    var now = clock.Elapsed.TotalMilliseconds;
                    if (mode != "off") {
                        var before = Stopwatch.GetTimestamp();
                        if (mode == "specialty") {
                            var sample = module.Sample(now, true); var full = previous.Count == 0 || now - lastFull >= 1000;
                            var delta = full ? new Dictionary<string, DiagnosticField>(sample) : sample.Where(f => !previous.TryGetValue(f.Key, out var old) || old != f.Value).ToDictionary();
                            if (full) { fullSequence = count + 1; lastFull = now; }
                            delta["timing.interval_ms"] = new(last == 0 ? 0 : now - last);
                            ring.Add(DiagnosticChecks.Line(session, (int)count + 1, now, full, "sample", delta, fullSequence)); previous = sample;
                        } else {
                            ring.Add(DiagnosticChecks.Line(session, (int)count + 1, now, true, "performance", read.Performance()));
                        }
                        reads.Add(Stopwatch.GetElapsedTime(before).TotalMilliseconds);
                        if (last > 0) gaps.Add(now - last); last = now; count++;
                    }
                    pacer.Wait(mode == "specialty" ? 5 : 1000);
                }
                collector.Refresh(); child.Refresh(); gaps.Sort(); reads.Sort();
                double? P(List<double> xs, double p) => xs.Count == 0 ? null : xs[(int)((xs.Count - 1) * p)];
                results.Add(new { mode, evidence = "native Windows ReadProcessMemory plus serialization and ring against controlled child fixture", elapsedSeconds = clock.Elapsed.TotalSeconds, samples = count,
                    collectorCpuSeconds = (collector.TotalProcessorTime - collectorCpu).TotalSeconds,
                    targetCpuSeconds = (child.TotalProcessorTime - targetCpu).TotalSeconds,
                    collectorWorkingSetBytes = collector.WorkingSet64, targetWorkingSetBytes = child.WorkingSet64,
                    intervalP50Ms = P(gaps, .5), intervalP95Ms = P(gaps, .95), readP50Ms = P(reads, .5), readP95Ms = P(reads, .95), frameTimeMs = (double?)null });
            }
            File.WriteAllText(Path.Combine(output, "native-performance.json"), JsonSerializer.Serialize(results, new JsonSerializerOptions { WriteIndented = true }));
        } finally { if (!child.HasExited) { child.StandardInput.WriteLine("exit"); await child.WaitForExitAsync(); } }
    }
}
