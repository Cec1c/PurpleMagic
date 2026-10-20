"""Verify an actual portable EXE in an isolated cache, without UI or network."""
import argparse
import hashlib
import json
from pathlib import Path
import struct
import shutil
import subprocess
import uuid


def run(executable, metadata_file, evidence_root):
    executable = executable.resolve()
    evidence_root.mkdir(parents=True, exist_ok=True)
    work = evidence_root.resolve() / ("fixture-" + uuid.uuid4().hex)
    work.mkdir()
    args = [str(executable), "--prepare-only", "--cache-root", str(work / "cache")]
    processes = [subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                 creationflags=subprocess.CREATE_NO_WINDOW) for _ in range(2)]
    results = []
    for process in processes:
        output, error = process.communicate(timeout=60)
        assert process.returncode == 0, (process.returncode, error.decode("utf-8", "replace"))
        assert not error
        results.append(json.loads(output))
    assert sorted(r["reused"] for r in results) == [False, True]
    install = Path(results[0]["directory"])
    assert install.is_relative_to(work)
    assert all(Path(r["directory"]) == install for r in results)
    metadata = json.loads(metadata_file.read_text(encoding="utf-8"))
    for name, entry in metadata["files"].items():
        content = (install / name).read_bytes()
        assert len(content) == entry["size"] and hashlib.sha256(content).hexdigest() == entry["sha256"], name
    core = subprocess.run([str(install / "app/DNF90.Core.exe"), "--state-directory", str(work / "core-state")],
                          input=b'{"id":1,"method":"initialize","args":{}}\n', capture_output=True,
                          timeout=30, creationflags=subprocess.CREATE_NO_WINDOW)
    assert core.returncode == 0 and not core.stderr
    responses = [json.loads(line) for line in core.stdout.splitlines()]
    assert next(row for row in responses if row.get("id") == 1)["ok"] is True
    # Modify only this isolated fixture to simulate a newer installed app. The
    # portable seed must not undo it; this test never runs the modified GUI.
    app = install / "app/DNF90.exe"
    newer = app.read_bytes() + b"isolated-newer-app-fixture"
    app.write_bytes(newer)
    result = subprocess.run(args, capture_output=True, timeout=30, creationflags=subprocess.CREATE_NO_WINDOW)
    assert result.returncode == 0 and json.loads(result.stdout)["reused"]
    assert app.read_bytes() == newer
    # Outer executable names share the same payload/cache and preserve installed updates.
    renamed = work / "DNF90-Portable-legacy.exe"
    shutil.copy2(executable, renamed)
    legacy_args = [str(renamed), "--prepare-only", "--cache-root", str(work / "cache")]
    legacy = subprocess.run(legacy_args, capture_output=True, timeout=30, creationflags=subprocess.CREATE_NO_WINDOW)
    assert legacy.returncode == 0 and json.loads(legacy.stdout)["reused"]
    assert Path(json.loads(legacy.stdout)["directory"]) == install and app.read_bytes() == newer
    bootstrap = install / "DNF90Launcher.exe"
    bootstrap.write_bytes(b"isolated-corrupt-root")
    result = subprocess.run(args, capture_output=True, timeout=30, creationflags=subprocess.CREATE_NO_WINDOW)
    assert result.returncode == 2 and bootstrap.read_bytes() == b"isolated-corrupt-root"
    assert b"InvalidDataException" in result.stderr
    raw = executable.read_bytes()
    pe = struct.unpack_from("<I", raw, 0x3C)[0]
    assert struct.unpack_from("<H", raw, pe + 24 + 68)[0] == 2, "GUI subsystem required"
    receipt = {"passed": True, "portable_sha256": hashlib.sha256(raw).hexdigest(), "bytes": len(raw),
               "checks": ["two native processes, one extraction", "all embedded file hashes", "real extracted NativeAOT core initialize",
                          "repeated entry preserves updated app", "old and PurpleMagic filenames share update cache", "corrupt bootstrap rejected", "GUI PE subsystem", "redirected UTF-8 diagnostics"],
               "gui_started": False, "game_started": False, "network_called": False,
               "production_state_touched": False, "fixture_root": str(work)}
    (evidence_root / "receipt.json").write_text(json.dumps(receipt, indent=2), encoding="utf-8")
    print(json.dumps({k: v for k, v in receipt.items() if k != "fixture_root"}, indent=2))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("executable", type=Path)
    parser.add_argument("metadata", type=Path)
    parser.add_argument("evidence", type=Path)
    options = parser.parse_args()
    run(options.executable, options.metadata, options.evidence)
