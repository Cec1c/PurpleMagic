"""Validate a PurpleMagic diagnostic log (or historical ZIP) and emit a bounded evidence timeline."""
import argparse
import hashlib
import json
from pathlib import Path
import zipfile

LIMIT = 10 * 1024 * 1024
SENSITIVE = {'password', 'token', 'authorization', 'arguments', 'command_line', 'chat', 'account', 'install_path'}

CAPTURE_FAILURES = {'stack_unavailable', 'attach_failed', 'access_denied', 'observer_failed',
                    'observer_timeout', 'observer_disconnected', 'observer_dropped',
                    'exception_limit', 'continue_failed', 'elevation_cancelled', 'capture_failed',
                    'platform_unsupported', 'operations_unavailable', 'collector_error', 'record_budget'}


def event_row(row, **extra):
    return dict(extra, sequence=row['sequence'], utc=row['utc'], kind=row['type'],
                reason=row.get('reason'), fields=row.get('fields', {}), evidence=row.get('evidence'))


def evidence_timeline(events, end_reason=None):
    # Retain every critical event. The input file/archive size bounds this list.
    # Ordinary context has a separate 100-event budget and explicit omission ranges.
    def critical(e):
        return (e.get('kind') == 'exception' or (e.get('evidence') or {}).get('kind') in {'native_exception', 'managed_exception'} or
                e.get('reason') in CAPTURE_FAILURES | {'native_exception', 'game_exit'})
    important = [i for i, e in enumerate(events) if critical(e)]
    ordinary = {i for i in range(min(15, len(events)))}
    ordinary.update(range(max(0, len(events) - 15), len(events)))
    for i in reversed(important):
        for j in range(max(0, i - 4), min(len(events), i + 5)):
            if len(ordinary) < 100:
                ordinary.add(j)
    selected = ordinary | set(important)
    omitted = []
    for i, e in enumerate(events):
        if i in selected:
            continue
        if omitted and omitted[-1]['last_index'] == i - 1:
            omitted[-1].update(last_index=i, last_sequence=e['sequence'], count=omitted[-1]['count'] + 1)
        else:
            omitted.append(dict(first_index=i, last_index=i, first_sequence=e['sequence'], last_sequence=e['sequence'], count=1))
    terminal = [e for e in events if e.get('reason') == 'native_exception' and
                e.get('fields', {}).get('first_chance', {}).get('value') == 0]
    exits = [e for e in events if e.get('reason') == 'game_exit']
    state = 'unhandled_crash' if terminal else 'exited' if exits else 'recording' if end_reason == 'recording' else 'no_crash_observed'
    return dict(critical_events=[events[i] for i in important],
                timeline=[e for i, e in enumerate(events) if i in selected],
                timeline_omitted=len(events) - len(selected), omitted_event_ranges=omitted,
                outcome=dict(state=state, unhandled_exceptions=len(terminal), exits=len([e for e in exits if 'exit_code' in e.get('fields', {})]) or int(bool(exits))),
                interpretation='First-chance events are exception stages, not separate crashes. Later observer failures do not establish a cause. overwritten counts ring eviction, not missing export rows.')


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
    if path.stat().st_size > LIMIT:
        raise ValueError('Log exceeds 10 MiB')
    raw = path.read_bytes()
    if len(raw) > LIMIT or not raw.endswith(b'\n'):
        raise ValueError('Log limit or incomplete line')
    rows = [json.loads(line) for line in raw.splitlines()]
    header, footer = rows[0], rows[-1]
    if header['type'] != 'log_header' or footer['type'] != 'log_summary' or header['schema_version'] not in (2, 3, 4):
        raise ValueError('Missing log header/summary')
    if footer.get('schema_version') not in (2, 3, 4) or footer['schema_version'] < header['schema_version'] or (footer['schema_version'] == 4 and header['schema_version'] != 4):
        raise ValueError('Unsupported final log schema')
    if header['schema_version'] == 4:
        validate_v4(rows)
    sid = header['log']['summary']['session_id']
    if footer['log']['summary']['session_id'] != sid:
        raise ValueError('Summary session mismatch')
    sequence, observed, base = 0, -1, None
    events, missing = [], set(footer['log']['summary']['missing'])
    for row in rows:
        inspect_keys(row)
    for row in rows[1:-1]:
        if (header['schema_version'] == 4) != (row.get('record_version') == 3):
            raise ValueError('Record/envelope version mismatch')
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
        if row['type'] in ('event', 'exception'):
            events.append(event_row(row))
    if len(rows) - 2 != footer['log']['summary']['records']:
        raise ValueError('Record count mismatch')
    return {'schema_version': footer['schema_version'], 'verified': True, 'session_id': sid, 'records': len(rows) - 2,
            'missing': sorted(missing), **evidence_timeline(events, footer['log']['summary']['end_reason']),
            'truncated': footer['log']['summary']['truncated'], 'sha256': hashlib.sha256(raw).hexdigest()}


def validate_v4(rows):
    from jsonschema import Draft202012Validator
    schemas = Path(__file__).resolve().parents[1] / 'docs' / 'diagnostics'
    record = Draft202012Validator(json.loads((schemas / 'record-v3.schema.json').read_text(encoding='utf-8')))
    envelope = Draft202012Validator(json.loads((schemas / 'envelope-v4.schema.json').read_text(encoding='utf-8')))
    for i, row in enumerate(rows):
        validator = envelope if i in (0, len(rows) - 1) else record
        if next(validator.iter_errors(row), None) is not None:
            raise ValueError(f'Schema validation failed at line {i + 1}')


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
                elif row['type'] in ('event', 'exception'):
                    events.append(event_row(row, incident=incident))
            if incident != 'session':
                incidents[incident] = incidents.get(incident, 0) + len(rows)
        events.sort(key=lambda row: row['sequence'])
        return {'schema_version': 1, 'verified': True, 'session_id': manifest['session_id'],
                'incidents': incidents, 'missing': sorted(missing), **evidence_timeline(events),
                'observation_kind': 'client_polling'}


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
