"""Embed a verified complete launcher ZIP into a single NativeAOT portable entry."""
import argparse
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def prepare(source, target, version, icon):
    with zipfile.ZipFile(source) as archive:
        entries = [info for info in archive.infolist() if not info.is_dir()]
        if not 8 <= len(entries) <= 2000 or sum(i.file_size for i in entries) > 500 * 1024**2:
            raise ValueError("Invalid complete bundle size/count")
        prefix = PurePosixPath(entries[0].filename).parts[0] + "/"
        files = {}
        folded = set()
        for entry in entries:
            if not entry.filename.startswith(prefix):
                raise ValueError("A complete distribution ZIP with one root directory is required")
            name = entry.filename[len(prefix):]
            if name in files or name.lower() in folded or "\\" in name or ":" in name or any(p in ("", ".", "..") for p in name.split("/")):
                raise ValueError("Unsafe/duplicate bundle path")
            if not (name.startswith("app/") or name in ("DNF90Launcher.exe", "launcher.id", "SHA256SUMS.txt", "使用说明.txt")):
                raise ValueError("Unexpected runtime or credential file in bundle: " + name)
            files[name] = archive.read(entry)
            folded.add(name.lower())
    if files.get("launcher.id", b"").strip() != b"DNF90-Flutter-bundle-v1":
        raise ValueError("Missing approved install marker")
    sums = {}
    for line in files["SHA256SUMS.txt"].decode("utf-8").splitlines():
        digest, name = line.split("  ", 1)
        if name in sums or not re.fullmatch("[a-f0-9]{64}", digest):
            raise ValueError("Invalid checksums")
        sums[name] = digest
    if set(sums) != set(files) - {"SHA256SUMS.txt"}:
        raise ValueError("Complete bundle has unlisted files")
    for name, digest in sums.items():
        if sha(files[name]) != digest:
            raise ValueError("Bundle checksum failed: " + name)
    for name in ("DNF90Launcher.exe", "app/DNF90.exe", "app/DNF90.Core.exe", "app/flutter_windows.dll"):
        if not files[name].startswith(b"MZ"):
            raise ValueError("Missing Windows program: " + name)
    stream = io.BytesIO()
    with zipfile.ZipFile(stream, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as payload:
        for name, raw in sorted(files.items()):
            entry = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
            entry.compress_type = zipfile.ZIP_DEFLATED
            payload.writestr(entry, raw, compresslevel=9)
    raw = stream.getvalue()
    metadata = {"version": version, "size": len(raw), "sha256": sha(raw), "files": {name: {"size": len(value), "sha256": sha(value)} for name, value in files.items()}}
    target.mkdir(parents=True, exist_ok=True)
    (target / "payload.zip").write_bytes(raw)
    (target / "payload.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2), encoding="utf-8")
    shutil.copy2(icon, target / "app_icon.ico")
    return metadata


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bundle", required=True, type=Path)
    parser.add_argument("--output", type=Path, default=ROOT / "dist/PurpleMagic.exe")
    parser.add_argument("--version", default="1.0.3")
    parser.add_argument("--icon", type=Path, default=ROOT / "launcher/Flutter/windows/runner/resources/app_icon.ico")
    parser.add_argument("--dotnet", type=Path, default=ROOT / ".cache/dotnet8/dotnet.exe")
    args = parser.parse_args()
    output = args.output.resolve()
    checksum = output.with_name(output.name + ".sha256")
    if output.exists() or checksum.exists():
        raise SystemExit("Output exists; choose a new path to preserve it")
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", args.version):
        raise SystemExit("Version must be x.y.z and match the complete bundle")
    inputs = ROOT / "runtime/flutter-build/portable-input"
    metadata = prepare(args.bundle.resolve(), inputs, args.version, args.icon.resolve())
    build = ROOT / "runtime/flutter-build/portable"
    subprocess.run([str(args.dotnet), "publish", str(ROOT / "launcher/Portable/Launcher.Portable.csproj"), "-c", "Release", "-r", "win-x64", "-p:PortableInput=" + str(inputs), "-p:Version=" + args.version, "-o", str(build)], cwd=ROOT, check=True)
    output.parent.mkdir(parents=True, exist_ok=True)
    # Exclusive creation also protects an output created while compilation runs.
    with output.open("xb") as destination, (build / "PurpleMagic.exe").open("rb") as source:
        shutil.copyfileobj(source, destination)
    digest = sha(output.read_bytes())
    with checksum.open("x", encoding="utf-8") as handle:
        handle.write(digest + "  " + output.name + "\n")
    receipt = {"version": args.version, "file": str(output), "bytes": output.stat().st_size, "sha256": digest, "payload_sha256": metadata["sha256"], "payload_bytes": metadata["size"], "expanded_bytes": sum(f["size"] for f in metadata["files"].values()), "files": len(metadata["files"]), "source_zip_sha256": sha(args.bundle.read_bytes()), "gui_started": False}
    (ROOT / "runtime/flutter-build/portable-receipt.json").write_text(json.dumps(receipt, indent=2), encoding="utf-8")
    print(json.dumps(receipt, indent=2))


if __name__ == "__main__":
    main()
