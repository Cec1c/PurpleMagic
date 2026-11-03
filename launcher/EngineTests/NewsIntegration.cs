using System.Net;
using System.Text.Json;
using Dnf90.Launcher;
using ServerS5A3;

static class NewsIntegration
{
    public static async Task Run(string receipt)
    {
        try
        {
            using var doc = JsonDocument.Parse(File.ReadAllBytes(receipt)); var data = doc.RootElement;
            var root = JsonSerializer.Deserialize(File.ReadAllBytes(data.GetProperty("profile").GetString()!), CoreJson.Default.PlayerProfile)!;
            var state = data.GetProperty("state").GetString()!;
            if (!IPAddress.TryParse(new Uri(root.ApiUrl).Host, out var ip) || !IPAddress.IsLoopback(ip) || !Path.GetFullPath(state).StartsWith(Path.GetTempPath(), StringComparison.OrdinalIgnoreCase)) throw new Exception("isolated news fixture required");
            using (var engine = new LauncherEngine(root, state))
            {
                await engine.SavePreferences(new Preferences(AutoCheck:false));
                await engine.Check();
                if (engine.State.News.Length != 1 || engine.State.News[0] != new News("跨语言公告", "## 第一行\n\n**第二行**", "指南", "10 / 15") { Format = "markdown" }) throw new Exception("signed Hub news did not reach launcher state");
            }
            using var restored = new LauncherEngine(root, state);
            if (restored.State.News.Length != 1 || restored.State.News[0].Title != "跨语言公告" || restored.State.News[0].Format != "markdown") throw new Exception("cached signed news lost after restart");
            Console.WriteLine("PASS existing Hub news API -> signed bootstrap -> pinned TLS -> launcher state -> verified cached news after restart");
        }
        finally { File.WriteAllText(receipt + ".done", "done"); }
    }
}
