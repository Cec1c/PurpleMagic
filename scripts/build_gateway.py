"""Build PlayerGateway, GMService and GMIndex without deploying them."""
import argparse
import os
import re
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('goos', choices=['windows','linux'])
    parser.add_argument('arch', choices=['amd64','arm64'], default='amd64', nargs='?')
    parser.add_argument('--source-version', help='Exact source commit when building an exported source archive')
    args = parser.parse_args()
    out = ROOT/'dist/gateway'/f'{args.goos}-{args.arch}'
    out.mkdir(parents=True, exist_ok=True)
    exe = out/('PlayerGateway.exe' if args.goos=='windows' else 'PlayerGateway')
    env = os.environ | {'GOOS':args.goos,'GOARCH':args.arch,'CGO_ENABLED':'0','GOWORK':'off'}
    version = args.source_version
    if version and not re.fullmatch(r'[a-f0-9]{40}(?:-dirty)?',version):
        parser.error('source-version must be a full commit hash, optionally followed by -dirty')
    if not version:
        try:
            version = subprocess.check_output(['git','rev-parse','HEAD'],cwd=ROOT,text=True,stderr=subprocess.DEVNULL).strip()
            if subprocess.check_output(['git','status','--porcelain'],cwd=ROOT,text=True).strip():
                version += '-dirty'
        except (subprocess.CalledProcessError, FileNotFoundError):
            version = 'source-archive'
    flags = '-ldflags=-s -w -X dnf90.local/gateway/internal/diagnostics.BuildVersion=' + version
    subprocess.run(['go','build','-buildvcs=false','-mod=readonly','-trimpath',flags,'-o',str(exe),'./cmd/playergateway'],cwd=ROOT/'gateway',env=env,check=True)
    print(exe)
    indexer=out/('GMIndex.exe' if args.goos=='windows' else 'GMIndex')
    subprocess.run(['go','build','-buildvcs=false','-mod=readonly','-trimpath',flags,'-o',str(indexer),'./cmd/gmindex'],cwd=ROOT/'gateway',env=env,check=True)
    print(indexer)
    service=out/('GMService.exe' if args.goos=='windows' else 'GMService')
    subprocess.run(['go','build','-buildvcs=false','-mod=readonly','-trimpath',flags,'-o',str(service),'./cmd/gmservice'],cwd=ROOT/'gateway',env=env,check=True)
    print(service)

if __name__=='__main__':main()
