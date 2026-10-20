"""Exercise the built native Host's diagnostic RPCs using isolated local state."""
import argparse
import json
from pathlib import Path
import queue
import shutil
import subprocess
import threading
import time

from analyze_diagnostics import analyze


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--exe', required=True, type=Path)
    parser.add_argument('--output', required=True, type=Path)
    parser.add_argument('--fixture-exe', required=True, type=Path, help='EngineTests apphost, for native process association')
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    exports = output / 'exports'
    exports.mkdir(exist_ok=True)
    state_root = output / 'state'
    if state_root.exists():
        raise ValueError('Use a new state directory')
    fixture = None
    game_directory = ''
    if args.fixture_exe:
        game = output / 'game'
        game.mkdir()
        for file in args.fixture_exe.resolve().parent.iterdir():
            if file.is_file():
                shutil.copy2(file, game / file.name)
        shutil.copy2(args.fixture_exe, game / 'DNF.exe')
        fixture = subprocess.Popen([str(game / 'DNF.exe'), '--diagnostic-memory-child'],
                                   stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True,
                                   creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0))
        assert fixture.stdout.readline().strip() == '536870912'
        game_directory = str(game)
    process = subprocess.Popen([str(args.exe.resolve()), '--state-directory', str(state_root)],
                               stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                               text=True, encoding='utf-8', creationflags=getattr(subprocess, 'CREATE_NO_WINDOW', 0))
    inbox = queue.Queue()
    def read():
        for line in process.stdout:
            inbox.put(json.loads(line))
    threading.Thread(target=read, daemon=True).start()
    sequence = 0
    def call(method, payload=None):
        nonlocal sequence
        sequence += 1
        process.stdin.write(json.dumps({'id': sequence, 'method': method, 'args': payload or {}}) + '\n')
        process.stdin.flush()
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            reply = inbox.get(timeout=20)
            if reply.get('id') == sequence:
                if not reply.get('ok'):
                    raise AssertionError('RPC failed: ' + method)
                return reply
        raise TimeoutError(method)
    def diagnostic_when(predicate):
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            diagnostic = call('initialize')['state']['diagnostics']
            if predicate(diagnostic):
                return diagnostic
            time.sleep(.1)
        raise TimeoutError('diagnostic state')
    try:
        initial = call('initialize')['state']
        assert not initial['diagnostics']['enabled']
        prefs = {'clientDirectory': game_directory, 'account': '', 'rememberAccount': False,
                 'autoCheck': False, 'reducedEffects': True,
                 'diagnostics': {'enabled': True, 'problemType': 'connection'}}
        call('preferences', prefs)
        expected = 1
        status = diagnostic_when(lambda d: d['enabled'] and len(d['instances']) == expected and all(i['selected'] for i in d['instances']))
        instance = next((i for i in status['instances'] if i['pid'] != 0), status['instances'][0])
        if fixture:
            assert instance['pid'] == fixture.pid and instance['exeVersion'] == 'unsupported'
        session = instance['id']
        status = diagnostic_when(lambda d: len(d['clips']) == 1)
        result = call('diagnosticExport', {'sessionId': session, 'path': str(exports / 'active.log')})['diagnosticExport']
        report = analyze(Path(result['path']))
        assert report['verified'] and report['records'] > 0
        assert Path(result['path']).suffix == '.log'
        if fixture:
            fixture.stdin.write('exit\n')
            fixture.stdin.flush()
            assert fixture.wait(timeout=10) == 259
            diagnostic_when(lambda d: all(i['pid'] != fixture.pid for i in d['instances']))
        result = call('diagnosticExport', {'sessionId': session, 'path': str(exports / 'closed.log')})['diagnosticExport']
        assert analyze(Path(result['path']))['verified']
        prefs['diagnostics']['enabled'] = False
        call('preferences', prefs)
        diagnostic_when(lambda d: not d['enabled'])
        process.stdin.close()
        assert process.wait(timeout=30) == 0
        receipt = {'native_aot_host': True, 'launcher_version': initial['launcherVersion'], 'default_off': True,
                   'automatic_launch_log': True, 'active_log_export': True, 'disable_and_eof_flush': True,
                   'plain_log_verified': True, 'production_network_calls': 0,
                   'native_process_binding_and_exit': fixture is not None,
                   'native_gameplay': False, 'native_save_as_picker': 'widget mocked; manual acceptance pending'}
        (output / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n', encoding='utf-8')
        print(json.dumps(receipt, indent=2))
    finally:
        if process.poll() is None:
            process.stdin.close()
            try:
                process.wait(timeout=30)
            except subprocess.TimeoutExpired:
                process.kill()
        if fixture and fixture.poll() is None:
            fixture.stdin.write('exit\n')
            fixture.stdin.flush()
            fixture.wait(timeout=10)


if __name__ == '__main__':
    main()
