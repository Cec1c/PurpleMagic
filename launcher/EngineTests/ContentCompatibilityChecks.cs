using System.Security.Cryptography;
using System.Text.Json;
using Dnf90.Launcher;
using ServerS5A3;

static class ContentCompatibilityChecks
{
    internal static async Task Run(string temp, Action<bool,string> check)
    {
        using var key=RSA.Create(2048);
        var root=new PlayerProfile("https://accounts.example.test",new string('a',64),key.ExportSubjectPublicKeyInfoPem(),"","192.0.2.1",7001,new string('b',64),new string('c',64),"");
        var approved=root with { MinimumLauncherVersion=LauncherEngine.VersionText, ChannelHistory=new(936,"YWNjb3VudA=="){Adapter=NativeChannelHistory.AdapterId} };
        Bootstrap Release(PlayerProfile profile,long sequence)=>new("DNF90-Flutter",1,sequence,profile,[],new("content-"+sequence,"fixture","stable",sequence,new string('d',64),root.ApiUrl,"",[new("DNF.exe",2,profile.ExeSha256,true,"","",0),new("Script.pvf",2,profile.PvfSha256,true,"","",0)]),new(LauncherEngine.VersionText,root.ApiUrl+"/app.zip",2,new string('e',64)) { BuildFlavor=LauncherBuild.Flavor });
        byte[] Sign(Bootstrap value)
        {
            var p=JsonSerializer.SerializeToUtf8Bytes(value,WireJson.Default.Bootstrap);
            return JsonSerializer.SerializeToUtf8Bytes(new SignedEnvelope(Convert.ToBase64String(p),Convert.ToBase64String(key.SignData(p,HashAlgorithmName.SHA256,RSASignaturePadding.Pss))),CoreJson.Default.SignedEnvelope);
        }
        foreach(var p in new[]{approved,approved with{PvfSha256=new string('f',64)},approved with{ExeSha256=new string('9',64)}})
        {
            var bytes=Sign(Release(p,2));var verified=Trust.Verify(bytes,root);
            check(verified.Profile.ChannelHistory!.Adapter==NativeChannelHistory.AdapterId && verified.Profile.MinimumLauncherVersion==LauncherEngine.VersionText,"signed adapter and minimum version survive AOT metadata");
            check(LauncherEngine.SupportsContent(verified.Profile),"new signed EXE/PVF pair reuses installed adapter");
            var state=Path.Combine(temp,"content-contract-"+Guid.NewGuid().ToString("N"));Directory.CreateDirectory(state);File.WriteAllBytes(Path.Combine(state,"bootstrap.json"),bytes);
            using var engine=new LauncherEngine(root,state);
            check(!engine.State.HasLauncherUpdate,"resource identity changes do not require same-version launcher replacement");
        }
        var future=approved with{MinimumLauncherVersion="99.0.0"};
        var raw=Sign(Release(future,3) with{Launcher=new("99.0.0",root.ApiUrl+"/app.zip",2,new string('e',64)) { BuildFlavor=LauncherBuild.Flavor }});
        Trust.Verify(raw,root); // Future metadata must load so the old app can self-update.
        var futureState=Path.Combine(temp,"future-contract");Directory.CreateDirectory(futureState);File.WriteAllBytes(Path.Combine(futureState,"bootstrap.json"),raw);
        using var old=new LauncherEngine(root,futureState);
        check(old.State.HasLauncherUpdate&&!old.State.CanLogin,"new required code blocks login while exposing self-update");
        try {await old.Login(new("fixture","password"));throw new Exception("minimum ignored");}
        catch(InvalidOperationException error){check(error.Message.Contains("请先更新登录器"),"minimum version enforced before account network or game launch");}
        check(!LauncherEngine.SupportsContent(approved with{ChannelHistory=approved.ChannelHistory! with{Adapter="future-adapter-v2"}}),"unknown adapter requires program support");
        check(!NativeChannelHistory.SupportsProfile(approved with{ChannelHistory=approved.ChannelHistory! with{Adapter=""}}),"unsigned legacy pair table is not implicitly extended");
        var invalid=Release(approved with{MinimumLauncherVersion="latest"},4);
        try {Trust.Verify(Sign(invalid),root);throw new Exception("invalid minimum accepted");}catch(InvalidDataException){check(true,"invalid minimum version rejected");}
    }
}
