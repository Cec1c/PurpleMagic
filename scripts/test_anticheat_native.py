"""Exercise file leases and real Windows job lifetime with isolated game fixtures."""
import argparse
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--dotnet',type=Path,required=True)
    parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args();output=args.output.resolve();output.mkdir(parents=True,exist_ok=True)
    vswhere=Path(os.environ['ProgramFiles(x86)'])/'Microsoft Visual Studio/Installer/vswhere.exe'
    installs=json.loads(subprocess.check_output([str(vswhere),'-products','*','-requires','Microsoft.VisualStudio.Component.VC.Tools.x86.x64','-format','json','-utf8'],encoding='utf-8-sig'))
    vs=Path(installs[0]['installationPath'])
    subprocess.run([str(args.dotnet),'build',str(ROOT/'launcher/EngineTests'),'-c','Release','-p:AntiCheat=true','--nologo'],cwd=ROOT,check=True)
    env=dict(os.environ, DOTNET_ROOT=str(args.dotnet.resolve().parent))
    for arch,vcarch in [('x86','x86'),('x64','amd64')]:
        target=output/arch;target.mkdir(exist_ok=True)
        batch=target/'build.cmd'
        batch.write_text(f'@echo off\ncall "{vs / "VC/Auxiliary/Build/vcvarsall.bat"}" {vcarch} >nul\nif errorlevel 1 exit /b 1\ncl /nologo /O2 "{ROOT / "scripts/diagnostics/anticheat_fixture.cpp"}" /Fe:DNF.exe /Fo:fixture.obj /link /SUBSYSTEM:WINDOWS\n',encoding='utf-8')
        subprocess.run(['cmd','/d','/c',str(batch)],cwd=target,check=True)
        subprocess.run([str(ROOT/'launcher/EngineTests/bin-anticheat/Release/net8.0/Launcher.EngineTests.exe'),'--anticheat-check',str(target/'DNF.exe'),str(output/'checks'/arch)],env=env,cwd=ROOT,check=True)

if __name__=='__main__':main()
