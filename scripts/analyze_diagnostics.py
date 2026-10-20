"""Validate a PurpleMagic diagnostic log (or historical ZIP) and emit a bounded evidence timeline."""
import argparse
import hashlib
import json
from pathlib import Path
import zipfile

LIMIT = 10 * 1024 * 1024
SENSITIVE = {'password', 'token', 'authorization', 'arguments', 'command_line', 'chat', 'account', 'install_path'}


def inspect_keys(value):
    if isinstance(value, dict):
        for key, item in value.items():
            if key.lower() in SENSITIVE:
                raise ValueError('Sensitive field: ' + key)
            inspect_keys(item)
    elif isinstance(value, list):
        for item in value:
            inspect_keys(item)


def analyze_log(path):
    raw = path.read_bytes()
    if len(raw) > LIMIT or not raw.endswith(b'\n'):
        raise ValueError('Log limit or incomplete line')
    rows = [json.loads(line) for line in raw.splitlines()]
    header, footer = rows[0], rows[-1]
    if header['type'] != 'log_header' or footer['type'] != 'log_summary' or header['schema_version'] != 2:
        raise ValueError('Missing log header/summary')
    sid = header['log']['summary']['session_id']
    if footer['log']['summary']['session_id'] != sid:
        raise ValueError('Summary session mismatch')
    sequence, observed, base = 0, -1, None
    events, missing = [], set(footer['log']['summary']['missing'])
    for row in rows:
        inspect_keys(row)
    for row in rows[1:-1]:
        if row['session_id'] != sid or row['sequence'] <= sequence or row['monotonic_ms'] < observed:
            raise ValueError('Session/sequence/time mismatch')
        sequence, observed = row['sequence'], row['monotonic_ms']
        if row['type'] == 'sample':
            if row['reason'] == 'full':
                base = sequence
            elif base is None or row['base_sequence'] != base:
                raise ValueError('Delta without retained full snapshot')
        for key, field in row['fields'].items():
            if field['status'] not in {'ok', 'not_applicable', 'unsupported', 'read_failed', 'unstable'}:
                raise ValueError('Unknown field quality')
            if field['status'] != 'ok' and field['value'] is not None:
                raise ValueError('Unavailable field must be null')
            if field['status'] in {'unsupported', 'read_failed'}:
                missing.add(key + ':' + field['status'])
        if row['type'] == 'event':
            events.append({'sequence': sequence, 'utc': row['utc'], 'reason': row['reason']})
    if len(rows) - 2 != footer['log']['summary']['records']:
        raise ValueError('Record count mismatch')
    return {'schema_version': 2, 'verified': True, 'session_id': sid, 'records': len(rows) - 2,
            'missing': sorted(missing), 'timeline': events[:100], 'timeline_omitted': max(0, len(events) - 100),
            'truncated': footer['log']['summary']['truncated'], 'sha256': hashlib.sha256(raw).hexdigest()}


def analyze(path):
    if path.suffix.lower() == ".log":
        return analyze_log(path)
    if path.stat().st_size > LIMIT:
        raise ValueError('ZIP exceeds 10 MiB')
    with zipfile.ZipFile(path) as archive:
        entries = archive.infolist()
        names = [entry.filename for entry in entries]
        if len(names) != len(set(names)) or len(entries) > 60 or sum(e.file_size for e in entries) > 40 * 1024 * 1024:
            raise ValueError('Archive bounds or duplicate entry')
        if any(n.startswith(('/', '\\')) or '\\' in n or ':' in n or '..' in n.split('/') for n in names):
            raise ValueError('Unsafe archive entry')
        manifest = json.loads(archive.read('manifest.json'))
        if manifest['schema_version'] != 1:
            raise ValueError('Unsupported schema version')
        listed = [item['path'] for item in manifest['files']]
        if set(listed) != set(names) - {'manifest.json'} or len(listed) != len(set(listed)):
            raise ValueError('Manifest must cover every payload exactly once')
        incidents, missing, events = {}, set(), []
        for item in manifest['files']:
            data = archive.read(item['path'])
            if len(data) != item['bytes'] or hashlib.sha256(data).hexdigest() != item['sha256']:
                raise ValueError('Manifest verification failed: ' + item['path'])
            if item['path'].endswith('.json'):
                value = json.loads(data)
                inspect_keys(value)
                if isinstance(value, dict):
                    missing.update(value.get('missing', []))
                continue
            if not item['path'].endswith('.jsonl'):
                raise ValueError('Unexpected payload type')
            if data and not data.endswith(b'\n'):
                raise ValueError('Incomplete JSONL line')
            rows = [json.loads(line) for line in data.splitlines()]
            previous_sequence = 0
            previous_time = -1
            state, base = {}, None
            incident = item['path'].split('/')[1] if item['path'].startswith('incidents/') else 'session'
            for row in rows:
                inspect_keys(row)
                if row['session_id'] != manifest['session_id'] or row['sequence'] <= previous_sequence or row['monotonic_ms'] < previous_time:
                    raise ValueError('Session or sequence mismatch')
                previous_sequence, previous_time = row['sequence'], row['monotonic_ms']
                if row['type'] == 'sample':
                    if row['reason'] == 'full':
                        state = dict(row['fields'])
                        base = row['sequence']
                    elif row.get('base_sequence') != base or base is None:
                        raise ValueError('Delta without retained full snapshot')
                    else:
                        state.update(row['fields'])
                    for key, value in row['fields'].items():
                        if value['status'] not in {'ok', 'not_applicable', 'unsupported', 'read_failed', 'unstable'}:
                            raise ValueError('Unknown field quality')
                        if value['status'] != 'ok' and value['value'] is not None:
                            raise ValueError('Unavailable field must be null')
                        if value['status'] in {'unsupported', 'read_failed'}:
                            missing.add(key + ':' + value['status'])
                    changes = {k: v for k, v in row['fields'].items() if k in {'actor.ride', 'actor.action', 'descriptor.state', 'logical4.0.fresh', 'vehicle.0.rider', 'vehicle.0.distance_xy'}}
                    if changes and row['reason'] != 'full':
                        events.append({'incident': incident, 'sequence': row['sequence'], 'utc': row['utc'], 'kind': 'observed_state_change', 'fields': changes})
                elif row['type'] == 'event':
                    events.append({'incident': incident, 'sequence': row['sequence'], 'utc': row['utc'], 'kind': 'event', 'reason': row.get('reason')})
            if incident != 'session':
                incidents[incident] = incidents.get(incident, 0) + len(rows)
        events.sort(key=lambda row: row['sequence'])
        return {'schema_version': 1, 'verified': True, 'session_id': manifest['session_id'],
                'incidents': incidents, 'missing': sorted(missing), 'timeline': events[:100],
                'timeline_omitted': max(0, len(events) - 100), 'observation_kind': 'client_polling'}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('zip', type=Path)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    result = json.dumps(analyze(args.zip), ensure_ascii=False, indent=2)
    if args.output:
        args.output.write_text(result + '\n', encoding='utf-8')
    else:
        print(result)


if __name__ == '__main__':
    main()
