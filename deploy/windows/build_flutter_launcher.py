"""Build a complete Flutter/native-core bundle without changing installed launchers."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def run(args, cwd=ROOT):
    subprocess.run([str(x) for x in args], cwd=cwd, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--flutter", type=Path, default=Path(os.environ.get("FLUTTER_SDK", "D:/SDKs/flutter")))
    parser.add_argument("--dotnet", type=Path, default=ROOT / ".cache/dotnet8/dotnet.exe")
    parser.add_argument("--profile-dir", type=Path, default=ROOT / "runtime/launcher-input")
    parser.add_argument("--anticheat", action="store_true", help="Compile optional UAC anti-cheat support; policy remains opt-in")
    parser.add_argument("--output", type=Path, default=ROOT / "dist/DNF90-Flutter-1.0.9")
    args = parser.parse_args()
    flavor = 'anticheat' if args.anticheat else 'standard'
    if args.anticheat and args.output == ROOT / "dist/DNF90-Flutter-1.0.9":
        args.output = args.output.with_name(args.output.name + '-anticheat')
    target = args.output.resolve()
    if target.exists():
        raise SystemExit("Output already exists; choose a new --output to preserve it")
    profile = args.profile_dir.resolve()
    if not (profile / "profile.json").is_file():
        raise SystemExit("Missing approved private profile.json")
    policy = json.loads((profile / 'profile.json').read_text(encoding='utf-8-sig')).get('AntiCheat') or {}
    if policy.get('Enabled') and not args.anticheat:
        raise SystemExit('AntiCheat.Enabled requires --anticheat')
    project = ROOT / "launcher/Flutter"
    for name in ("background.webp", "announcement.webp"):
        if not (project / "assets" / name).is_file():
            raise SystemExit("Missing approved artwork: " + name)
    if not (project / "windows/runner/resources/app_icon.ico").is_file():
        raise SystemExit("Missing approved Windows app_icon.ico")
    if not (project / "brand/witch.png").is_file():
        raise SystemExit("Missing PurpleMagic brand/witch.png")
    vswhere = Path(os.environ["ProgramFiles(x86)"]) / "Microsoft Visual Studio/Installer/vswhere.exe"
    installations = json.loads(subprocess.check_output([str(vswhere), "-products", "*", "-requires", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64", "-format", "json", "-utf8"], encoding="utf-8-sig"))
    vs = next((Path(x["installationPath"]) for x in installations if (Path(x["installationPath"]) / "VC/Tools/MSVC").is_dir()), None)
    if vs is None:
        raise SystemExit("Visual Studio C++ build tools are required")
    cmake = vs / "Common7/IDE/CommonExtensions/Microsoft/CMake/CMake/bin/cmake.exe"
    core = ROOT / "runtime/flutter-build" / flavor / "core"
    bootstrap = ROOT / "runtime/flutter-build/bootstrap"
    run([args.dotnet, "publish", "launcher/Host/Launcher.Host.csproj", "-c", "Release", "-r", "win-x64", "-p:AntiCheat=" + str(args.anticheat).lower(), "-p:LauncherInput=" + str(profile), "-o", core])
    if subprocess.check_output([str(core / 'DNF90.Core.exe'), '--build-flavor'], text=True).strip() != flavor:
        raise RuntimeError('Compiled core flavor mismatch')
    flutter = args.flutter / "bin/flutter.bat"
    run([flutter, "--no-version-check", "--suppress-analytics", "pub", "get", "--offline"], project)
    run([flutter, "--no-version-check", "--suppress-analytics", "build", "windows", "--release", "--no-pub"], project)
    run([cmake, "-S", ROOT / "launcher/Bootstrap", "-B", bootstrap, "-G", "Visual Studio 17 2022", "-A", "x64", "-DCMAKE_GENERATOR_INSTANCE=" + str(vs)])
    run([cmake, "--build", bootstrap, "--config", "Release"])
    target.mkdir(parents=True)
    shutil.copytree(project / "build/windows/x64/runner/Release", target / "app")
    shutil.copy2(core / "DNF90.Core.exe", target / "app/DNF90.Core.exe")
    (target / 'app/build-flavor.json').write_text(json.dumps({'format': 1, 'flavor': flavor}), encoding='utf-8')
    shutil.copy2(bootstrap / "Release/DNF90Launcher.exe", target / "DNF90Launcher.exe")
    crt = sorted((vs / "VC/Redist/MSVC").glob("*/x64/Microsoft.VC143.CRT"))[-1]
    for name in ("msvcp140.dll", "vcruntime140.dll", "vcruntime140_1.dll"):
        shutil.copy2(crt / name, target / "app" / name)
    (target / "launcher.id").write_text("DNF90-Flutter-bundle-v1\n", encoding="utf-8")
    (target / "使用说明.txt").write_text("PurpleMagic 런처 1.0.9\n\nDNF90Launcher.exe를 실행하세요. 전체 폴더를 보관해야 합니다. app 폴더 안의 실행 파일을 따로 옮기지 마세요.\nDNF.exe와 Script.pvf가 들어 있는 호환 클라이언트 폴더를 선택하세요. 계정과 비밀번호는 이 서버로 전송되며 비밀번호는 저장하지 않습니다.\n클라이언트 업데이트와 공지를 사용하려면 전용 게이트웨이를 배포해야 합니다. 게이트웨이가 없으면 안내가 표시되며, 로그인은 런처에 포함된 이 서버용 설정을 사용합니다.\n업데이트하기 전에 게임을 종료하세요. 런처 업데이트는 app 폴더 전체를 교체하며, 새 버전이 시작되지 않으면 이전 버전 복구를 시도합니다.\n계정, 폴더, 화면 설정은 현재 Windows 사용자 LocalAppData/DNF90Launcher에 저장됩니다.\n설정 → 진단 로그: 기본값은 꺼짐입니다. 켜면 게임을 시작할 때마다 기록하고 최근 5회분을 보관합니다. 각 기록의 내보내기를 눌러 .log 파일로 저장할 수 있습니다.\n채널 설정 용량 문제가 있으면 설정에서 확인한 뒤 로컬 DNF.cfg를 백업하고 초기화할 수 있습니다. 게임에 저장된 계정과 개인 설정은 초기화될 수 있지만 서버 캐릭터와 장비에는 영향이 없습니다.\n", encoding="utf-8-sig")
    if args.anticheat:
        help_path = target / '使用说明.txt'
        help_path.write_text(help_path.read_text(encoding='utf-8-sig').replace('LocalAppData/DNF90Launcher。', 'LocalAppData/DNF90Launcher-anticheat。'), encoding='utf-8-sig')
        with (target / '使用说明.txt').open('a', encoding='utf-8') as help_file:
            help_file.write('\n反作弊可选版：默认关闭。服主通过签名 AntiCheat.Enabled 配置开启后，普通启动时保护助手请求 Windows 管理员授权；以管理员身份启动登录器时直接启动保护助手，游戏沿用管理员权限。保护期间锁定资源并检查配置的进程和模块规则；退出登录器会结束其启动的游戏。本机记录不自动上传。\n')
    files = {}
    for path in sorted(target.rglob("*")):
        if path.is_file():
            files[path.relative_to(target).as_posix()] = hashlib.sha256(path.read_bytes()).hexdigest()
    (target / "SHA256SUMS.txt").write_text("".join(f"{sha}  {path}\n" for path, sha in files.items()), encoding="utf-8")
    archive = target.with_name(target.name + ".zip")
    update = target.with_name(target.name + "-app-update.zip")
    if archive.exists() or update.exists():
        raise SystemExit("Archive already exists; preserving it")
    for path, base in ((archive, target.parent), (update, target / "app")):
        entries = target.rglob("*") if path == archive else (target / "app").rglob("*")
        with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as z:
            for entry in sorted(entries):
                if entry.is_file():
                    z.write(entry, entry.relative_to(base))
        with zipfile.ZipFile(path) as z:
            if z.testzip() is not None:
                raise RuntimeError("ZIP integrity check failed")
    receipt = {"version": "1.0.9", "directory": str(target), "complete_bytes": sum(x.stat().st_size for x in target.rglob("*") if x.is_file()), "zip_bytes": archive.stat().st_size, "zip_sha256": hashlib.sha256(archive.read_bytes()).hexdigest(), "update_bytes": update.stat().st_size, "files": files}
    receipt['buildFlavor'] = flavor
    (ROOT / "runtime/flutter-build" / flavor / "build-receipt.json").write_text(json.dumps(receipt, ensure_ascii=False, indent=2), encoding="utf-8")
    (ROOT / "runtime/flutter-build/build-receipt.json").write_text(json.dumps(receipt, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({k: v for k, v in receipt.items() if k != "files"}, ensure_ascii=False))


if __name__ == "__main__":
    main()
