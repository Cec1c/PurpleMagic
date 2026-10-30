"""Interactive Windows UAC check: operator rejects, then accepts the real secure-desktop prompt."""
import argparse
import ctypes
import hashlib
import json
from pathlib import Path
import subprocess
import time


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--harness', type=Path, required=True)
    p.add_argument('--fixture', type=Path, required=True)
    p.add_argument('--output', type=Path, required=True)
    p.add_argument('--action', choices=['cancel', 'approve'], required=True)
    args = p.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    fixture = args.fixture.resolve()
    if any((output/name).exists() for name in ['ready.json', 'cancel.json', 'receipt.json']):
        raise SystemExit('Choose a fresh output directory; existing receipts are preserved')
    harness = args.harness.resolve()
    guard_exit = harness.parent/'uac-guard-exit.txt'
    if guard_exit.exists():
        guard_exit.unlink()
    profile = dict(ApiUrl='https://fixture.test', CertificateSha256='a'*64,
                   UpdatePublicKey='', SettingsUrl='', GameHost='192.0.2.1', BootstrapPort=7001,
                   DefaultClientDirectory='', AntiCheat=dict(Enabled=True, Mode='audit'))
    profile.update(ExeSha256=hashlib.sha256((fixture/'DNF.exe').read_bytes()).hexdigest(),
                   PvfSha256=hashlib.sha256((fixture/'Script.pvf').read_bytes()).hexdigest())
    (harness.parent/'uac-fixture.json').write_text(json.dumps(profile), encoding='utf-8')
    with (output/'runner.log').open('w', encoding='utf-8') as log:
        child = subprocess.Popen([str(harness), '--anticheat-uac-check', str(fixture), str(output), args.action], stdout=log, stderr=subprocess.STDOUT)
        try:
            if args.action == 'cancel':
                assert child.wait(timeout=150) == 0, (output/'runner.log').read_text(encoding='utf-8')
                receipt = json.loads((output/'cancel.json').read_text())
                assert receipt['passed']
            else:
                deadline = time.monotonic() + 150
                while not (output/'ready.json').exists():
                    if child.poll() is not None:
                        raise RuntimeError((output/'runner.log').read_text(encoding='utf-8'))
                    if time.monotonic() > deadline:
                        raise TimeoutError('No UAC fixture readiness receipt')
                    time.sleep(.1)
                receipt = json.loads((output/'ready.json').read_text())
                assert receipt['parentPid'] == child.pid
                kernel = ctypes.WinDLL('kernel32', use_last_error=True)
                kernel.OpenProcess.restype = ctypes.c_void_p
                kernel.WaitForSingleObject.argtypes = [ctypes.c_void_p, ctypes.c_uint32]
                kernel.CloseHandle.argtypes = [ctypes.c_void_p]
                game = kernel.OpenProcess(0x100000, False, receipt['gamePid'])
                if not game:
                    raise ctypes.WinError(ctypes.get_last_error())
                try:
                    assert kernel.WaitForSingleObject(game, 0) == 258, 'Game already exited before crash test'
                    child.kill()
                    child.wait(timeout=10)
                    assert kernel.WaitForSingleObject(game, 10000) == 0, 'Fixture survived its parent'
                finally:
                    kernel.CloseHandle(game)
                deadline = time.monotonic() + 10
                while True:
                    try:
                        with (fixture/'Script.pvf').open('r+b'):
                            pass
                        break
                    except PermissionError:
                        if time.monotonic() > deadline:
                            raise
                        time.sleep(.1)
                deadline = time.monotonic() + 10
                while not guard_exit.exists():
                    if time.monotonic() > deadline:
                        raise TimeoutError('Guard did not exit after parent death')
                    time.sleep(.1)
                assert guard_exit.read_text() == '0', 'Guard returned an error after parent death'
                receipt.update(passed=True, parentCrashEndsGame=True, guardReleasedPvf=True, guardExitedCleanly=True)
            (output/'receipt.json').write_text(json.dumps(receipt, indent=2), encoding='utf-8')
            print(json.dumps(receipt))
        finally:
            if child.poll() is None:
                child.kill()
                child.wait(timeout=10)


if __name__ == '__main__':
    main()
