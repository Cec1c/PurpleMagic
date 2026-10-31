"""Compile isolated x86/x64 crash children and exercise the real Windows observer."""
import argparse
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--dotnet', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    vswhere = Path(os.environ['ProgramFiles(x86)']) / 'Microsoft Visual Studio/Installer/vswhere.exe'
    installs = json.loads(subprocess.check_output([str(vswhere), '-products', '*', '-requires', 'Microsoft.VisualStudio.Component.VC.Tools.x86.x64', '-format', 'json', '-utf8'], encoding='utf-8-sig'))
    vs = Path(installs[0]['installationPath'])
    env = dict(os.environ)
    for arch, vcarch in [('x86', 'x86'), ('x64', 'amd64')]:
        target = output / arch
        target.mkdir(exist_ok=True)
        batch = target / 'build.cmd'
        batch.write_text(f'@echo off\ncall "{vs / "VC/Auxiliary/Build/vcvarsall.bat"}" {vcarch} >nul\nif errorlevel 1 exit /b 1\ncl /nologo /Od /Oy- /Zi /EHsc "{ROOT / "scripts/diagnostics/native_crash_fixture.cpp"}" /Fe:DNF.exe /Fo:fixture.obj /Fd:fixture.pdb /link /DEBUG /PDB:DNF.pdb\n', encoding='utf-8')
        subprocess.run(['cmd', '/d', '/c', str(batch)], cwd=target, check=True)
        layout = json.loads(subprocess.check_output([str(target / 'DNF.exe'), '--layout']))
        expected = dict(debug_event=176, union=16, record=152, code=0, flags=4, chain=8, address=16, count=24, parameters=32, first_chance=168) if arch == 'x64' else dict(debug_event=96, union=12, record=80, code=0, flags=4, chain=8, address=12, count=16, parameters=20, first_chance=92)
        assert layout == expected, (arch, layout, expected)
        (target / 'layout.json').write_text(json.dumps(layout, indent=2) + '\n')
        env['PURPLEMAGIC_CRASH_FIXTURE_' + arch.upper()] = str(target / 'DNF.exe')
    subprocess.run([str(args.dotnet), 'run', '--project', str(ROOT / 'launcher/EngineTests'), '-c', 'Release', '--', '--diagnostic-crash-check', str(output / 'checks')], env=env, cwd=ROOT, check=True)


if __name__ == '__main__':
    main()
