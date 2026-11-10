"""Validate a PurpleMagic diagnostic log, Contract v1 bundle (.zip/.jsonl), or historical ZIP and emit a bounded evidence timeline."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import re
import zipfile

LIMIT = 10 * 1024 * 1024
CONTRACT_BUNDLE_LIMIT = 64 * 1024 * 1024
CONTRACT_UNCOMPRESSED_LIMIT = 180 * 1024 * 1024
CONTRACT_V1_NAME = 'purplemagic-anton-first-entry-diagnostics'

SENSITIVE = {'password', 'token', 'authorization', 'arguments', 'command_line', 'chat', 'account', 'install_path'}
CONTRACT_SENSITIVE = SENSITIVE | {'login_token', 'login_request_body', 'sensitive_command_line'}

REQUIRED_COMMON_FIELDS = {
    'schema_version',
    'event',
    'source',
    'client_session_id',
    'event_seq',
    'observed_utc',
    'mono_ticks',
    'mono_frequency_hz',
    'pid',
    'process_created_utc',
}

REQUIRED_MANIFEST_FIELDS = {
    'incident_id',
    'launcher_version',
    'client_file_sha256',
    'pvf_file_sha256',
    'protocol_profile',
    'server_endpoint',
    'sessions',
    'capabilities',
    'integrity',
}

REQUIRED_CAPABILITY_KEYS = {
    'launcher_sessions',
    'manual_observations',
    'game_socket_ownership',
    'client_side_packet_capture',
    'application_frames',
    'native_actor_events',
    'native_registry_events',
    'native_context_events',
    'native_render_events',
}

SERVER_EVENTS = {'context_changed', 'connection_changed', 'character_changed', 'settlement_changed', 'business_disconnect'}

CAPTURE_FAILURES = {'stack_unavailable', 'attach_failed', 'access_denied', 'observer_failed',
                    'observer_timeout', 'observer_disconnected', 'observer_dropped',
                    'exception_limit', 'continue_failed', 'elevation_cancelled', 'capture_failed',
                    'platform_unsupported', 'operations_unavailable', 'collector_error', 'record_budget'}


def event_row(row, **extra):
    return dict(extra, sequence=row['sequence'], utc=row['utc'], kind=row['type'],
                reason=row.get('reason'), fields=row.get('fields', {}), evidence=row.get('evidence'), context=row.get('context'),
                time_source='server_utc' if row.get('module') == 'server' and row.get('reason') in SERVER_EVENTS else 'client_utc')


def evidence_timeline(events, end_reason=None):
    # Retain every critical event. The input file/archive size bounds this list.
    # Ordinary context has a separate 100-event budget and explicit omission ranges.
    def critical(e):
        return (e.get('kind') == 'exception' or (e.get('evidence') or {}).get('kind') in {'native_exception', 'managed_exception'} or
                e.get('reason') in CAPTURE_FAILURES | {'native_exception', 'game_exit', 'business_disconnect', 'character_changed', 'connection_changed', 'server_event_gap'})
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
    disconnections = [e for e in events if e.get('reason') == 'business_disconnect']
    state = 'unhandled_crash' if terminal else 'business_disconnected' if disconnections else 'exited' if exits else 'recording' if end_reason == 'recording' else 'no_crash_observed'
    return dict(critical_events=[events[i] for i in important],
                timeline=[e for i, e in enumerate(events) if i in selected],
                timeline_omitted=len(events) - len(selected), omitted_event_ranges=omitted,
                outcome=dict(state=state, unhandled_exceptions=len(terminal), exits=len([e for e in exits if 'exit_code' in e.get('fields', {})]) or int(bool(exits))),
                interpretation='First-chance events are exception stages, not separate crashes. Later observer failures do not establish a cause. overwritten counts ring eviction, not missing export rows.')


def inspect_keys(value, forbidden=SENSITIVE):
    if isinstance(value, dict):
        for key, item in value.items():
            if key.lower() in forbidden:
                raise ValueError('Sensitive field: ' + key)
            inspect_keys(item, forbidden)
    elif isinstance(value, list):
        for item in value:
            inspect_keys(item, forbidden)


def analyze_log_bytes(raw):
    if len(raw) > LIMIT or not raw.endswith(b'\n'):
        raise ValueError('Log limit or incomplete line')
    rows = [json.loads(line) for line in raw.splitlines()]
    header, footer = rows[0], rows[-1]
    if header['type'] != 'log_header' or footer['type'] != 'log_summary' or header['schema_version'] not in (2, 3, 4, 5):
        raise ValueError('Missing log header/summary')
    if footer.get('schema_version') not in (2, 3, 4, 5) or footer['schema_version'] < header['schema_version'] or (footer['schema_version'] >= 4 and header['schema_version'] != footer['schema_version']):
        raise ValueError('Unsupported final log schema')
    if header['schema_version'] >= 4:
        validate_v4(rows, header['schema_version'])
    sid = header['log']['summary']['session_id']
    if footer['log']['summary']['session_id'] != sid:
        raise ValueError('Summary session mismatch')
    sequence, observed, base = 0, -1, None
    events, missing = [], set(footer['log']['summary']['missing'])
    for row in rows:
        inspect_keys(row)
    for row in rows[1:-1]:
        if (header['schema_version'] >= 4 and row.get('record_version') != header['schema_version'] - 1) or (header['schema_version'] < 4 and row.get('record_version', 2) >= 3):
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
    return {'schema_version': footer['schema_version'], 'verified': True, 'session_id': sid,
            'incident_id': footer['log']['summary'].get('incident_id'),
            'records': len(rows) - 2, 'missing': sorted(missing), 'context': footer['log']['summary'].get('context'),
            'support_summary': support_summary(footer['log'], events), **evidence_timeline(events, footer['log']['summary']['end_reason']),
            'truncated': footer['log']['summary']['truncated'], 'sha256': hashlib.sha256(raw).hexdigest()}


def analyze_log(path):
    if path.stat().st_size > LIMIT:
        raise ValueError('Log exceeds 10 MiB')
    return analyze_log_bytes(path.read_bytes())


def support_summary(log, events):
    context = log['summary'].get('context') or {}
    # Fault/disconnect context takes precedence over the latest healthy state.
    terminal = [e for e in events if e.get('reason') == 'native_exception' and
                e.get('fields', {}).get('first_chance', {}).get('value') == 0]
    incidents = terminal or [e for e in events if e.get('reason') == 'business_disconnect' or
                             (e.get('evidence') or {}).get('kind') == 'managed_exception']
    event = incidents[-1] if incidents else events[-1] if events else {}
    context = event.get('context') or context
    utc = event.get('utc') or log['ended_utc']
    try:
        normalized = re.sub(r'(\.\d{6})\d+', r'\1', utc).replace('Z', '+00:00')
        beijing = datetime.datetime.fromisoformat(normalized).astimezone(datetime.timezone(datetime.timedelta(hours=8))).isoformat()
    except ValueError:
        beijing = None
    fields = context.get('fields', {})
    return dict(event_utc=utc, event_beijing=beijing, time_basis=event.get('time_source', 'client_utc'),
                character=fields.get('character_name'), character_id=fields.get('character_id'),
                connection=fields.get('connection_id'), dungeon=fields.get('dungeon_id'),
                settlement_phase=fields.get('settlement_phase'), context_status=context.get('status', 'unavailable'),
                context_observed_at=context.get('observed_at'), correlation_id=context.get('correlation_id'),
                clock=context.get('clock'), private_player_context=bool(context))


def validate_v4(rows, version=4):
    from jsonschema import Draft202012Validator
    schemas = Path(__file__).resolve().parents[1] / 'docs' / 'diagnostics'
    record = Draft202012Validator(json.loads((schemas / f'record-v{version - 1}.schema.json').read_text(encoding='utf-8')))
    envelope = Draft202012Validator(json.loads((schemas / f'envelope-v{version}.schema.json').read_text(encoding='utf-8')))
    for i, row in enumerate(rows):
        validator = envelope if i in (0, len(rows) - 1) else record
        if next(validator.iter_errors(row), None) is not None:
            raise ValueError(f'Schema validation failed at line {i + 1}')


def _contract_validators():
    from jsonschema import Draft202012Validator
    schema_path = Path(__file__).resolve().parents[1] / 'docs' / 'diagnostics' / 'contract-v1.schema.json'
    schema = json.loads(schema_path.read_text(encoding='utf-8'))
    defs = schema['$defs']
    return (
        Draft202012Validator({'$schema': schema['$schema'], '$defs': defs, **defs['manifest']}),
        Draft202012Validator({'$schema': schema['$schema'], '$defs': defs, **defs['integrity']}),
        Draft202012Validator({'$schema': schema['$schema'], '$defs': defs, **defs['event_record']}),
    )


def validate_contract_jsonl_bytes(raw, expected_session_id=None, event_validator=None):
    if len(raw) > 33_554_432:
        raise ValueError('Session JSONL exceeds 32 MiB budget')
    if raw and not raw.endswith(b'\n'):
        raise ValueError('Incomplete JSONL line')
    rows = [json.loads(line) for line in raw.splitlines()] if raw else []
    prev_seqs = {}
    for idx, row in enumerate(rows, start=1):
        inspect_keys(row, CONTRACT_SENSITIVE)
        missing_common = REQUIRED_COMMON_FIELDS - set(row.keys())
        if missing_common:
            raise ValueError(f'Line {idx} missing common fields: {sorted(missing_common)}')
        if event_validator is not None:
            err = next(event_validator.iter_errors(row), None)
            if err is not None:
                raise ValueError(f'Contract v1 event schema failed at line {idx}: {err.message}')
        sid = row['client_session_id']
        if expected_session_id is not None and sid != expected_session_id:
            raise ValueError(f'Session mismatch in JSONL: expected {expected_session_id}, got {sid}')
        seq = row['event_seq']
        if seq <= prev_seqs.get(sid, 0):
            raise ValueError(f'Non-monotonic event_seq at line {idx}: {seq}')
        prev_seqs[sid] = seq
        if row['mono_frequency_hz'] <= 0 or not str(row['observed_utc']).endswith('Z'):
            raise ValueError(f'Invalid timestamp/QPC metadata at line {idx}')
    return rows


def analyze_contract_v1_jsonl(path):
    _, _, event_validator = _contract_validators()
    raw = path.read_bytes()
    rows = validate_contract_jsonl_bytes(raw, event_validator=event_validator)
    sessions = sorted({r['client_session_id'] for r in rows})
    observations = [r for r in rows if r['event'] == 'observation.reported']
    return {
        'contract_name': CONTRACT_V1_NAME,
        'schema_version': 1,
        'verified': True,
        'format': 'jsonl',
        'records': len(rows),
        'sessions': sessions,
        'observations': len(observations),
        'sha256': hashlib.sha256(raw).hexdigest(),
    }


def analyze_contract_v1_bundle(archive, entries, manifest):
    manifest_validator, integrity_validator, event_validator = _contract_validators()
    inspect_keys(manifest, CONTRACT_SENSITIVE)
    missing_manifest = REQUIRED_MANIFEST_FIELDS - set(manifest.keys())
    if missing_manifest:
        raise ValueError(f'Manifest missing required fields: {sorted(missing_manifest)}')
    err = next(manifest_validator.iter_errors(manifest), None)
    if err is not None:
        raise ValueError('Contract v1 manifest schema failed: ' + err.message)
    missing_caps = REQUIRED_CAPABILITY_KEYS - set(manifest['capabilities'].keys())
    if missing_caps:
        raise ValueError(f'Manifest missing capability keys: {sorted(missing_caps)}')
    for cap, supported in manifest['capabilities'].items():
        if not supported and not (manifest.get('capability_reasons') or {}).get(cap):
            raise ValueError(f'Unsupported capability {cap} must declare capability_reasons[{cap}]')

    names = [entry.filename for entry in entries]
    listed = [item['path'] for item in manifest['files']]
    if set(listed) != set(names) - {'manifest.json'} or len(listed) != len(set(listed)):
        raise ValueError('Manifest must cover every payload file exactly once')

    payloads = {}
    for item in manifest['files']:
        data = archive.read(item['path'])
        if len(data) != item['bytes'] or hashlib.sha256(data).hexdigest() != item['sha256']:
            raise ValueError('Manifest verification failed: ' + item['path'])
        payloads[item['path']] = data

    if 'integrity.json' not in payloads:
        raise ValueError('Missing integrity.json in Contract v1 bundle')
    integrity = json.loads(payloads['integrity.json'])
    inspect_keys(integrity, CONTRACT_SENSITIVE)
    err = next(integrity_validator.iter_errors(integrity), None)
    if err is not None:
        raise ValueError('Contract v1 integrity schema failed: ' + err.message)

    session_summaries = []
    enhanced_summaries = []
    declared_sessions = {s['client_session_id']: s for s in manifest['sessions']}

    for sid, descriptor in declared_sessions.items():
        events_path = f'sessions/{sid}/launcher-events.jsonl'
        obs_path = f'sessions/{sid}/observations.jsonl'
        if events_path not in payloads or obs_path not in payloads:
            raise ValueError(f'Missing required session streams for {sid}')
        events = validate_contract_jsonl_bytes(payloads[events_path], expected_session_id=sid, event_validator=event_validator)
        obs_rows = validate_contract_jsonl_bytes(payloads[obs_path], expected_session_id=sid, event_validator=event_validator)
        embedded_obs = [r for r in events if r['event'] == 'observation.reported']
        if [r['event_seq'] for r in obs_rows] != [r['event_seq'] for r in embedded_obs]:
            raise ValueError(f'observations.jsonl sequence mismatch with launcher-events.jsonl for {sid}')

        entries_seen = [
            {
                'entry_id': r['entry_id'],
                'parent_entry_id': r.get('parent_entry_id'),
                'scene_epoch': r.get('scene_epoch'),
                'entry_kind': r.get('entry_kind'),
                'entry_source': r.get('entry_source'),
                'observed_utc': r['observed_utc'],
            }
            for r in events if r['event'] == 'scene.entry'
        ]
        sockets_open = [r for r in events if r['event'] == 'connection.open']
        sockets_close = [r for r in events if r['event'] == 'connection.close']
        exits = [r for r in events if r['event'] == 'process.exited']
        session_summaries.append({
            'client_session_id': sid,
            'launch_instance_id': descriptor['launch_instance_id'],
            'pid': descriptor['pid'],
            'process_created_utc': descriptor['process_created_utc'],
            'receiver_character_id': descriptor.get('receiver_character_id'),
            'receiver_character_name': descriptor.get('receiver_character_name'),
            'association_source': descriptor.get('association_source'),
            'end_state': descriptor['end_state'],
            'events': len(events),
            'observations': [
                {
                    'event_seq': o['event_seq'],
                    'observed_utc': o['observed_utc'],
                    'entry_id': o.get('entry_id'),
                    'parent_entry_id': o.get('parent_entry_id'),
                    'scene_epoch': o.get('scene_epoch'),
                    'peer_scope': o.get('peer_scope'),
                    'peer_character_name': o.get('peer_character_name'),
                    'state': o['state'],
                    'estimated_onset_utc': o.get('estimated_onset_utc'),
                    'time_uncertainty_ms': o.get('time_uncertainty_ms'),
                }
                for o in obs_rows
            ],
            'scene_entries': entries_seen,
            'tcp_streams_opened': sum(1 for r in sockets_open if r.get('protocol') == 'tcp'),
            'udp_bindings_opened': sum(1 for r in sockets_open if r.get('protocol') == 'udp'),
            'connections_closed': len(sockets_close),
            'exit_classification': exits[-1].get('exit_classification') if exits else None,
        })

    for path_name, raw in sorted(payloads.items()):
        if path_name.startswith('enhanced/') and path_name.endswith('.log'):
            enhanced_summaries.append(analyze_log_bytes(raw))
        elif path_name in ('integrity.json',) or path_name.startswith('sessions/'):
            continue
        else:
            raise ValueError('Unexpected payload path in Contract v1 bundle: ' + path_name)

    return {
        'contract_name': CONTRACT_V1_NAME,
        'schema_version': manifest['schema_version'],
        'verified': True,
        'incident_id': manifest['incident_id'],
        'launcher_version': manifest['launcher_version'],
        'launcher_commit': manifest.get('launcher_commit'),
        'build_flavor': manifest.get('build_flavor'),
        'client_file_sha256': manifest['client_file_sha256'],
        'pvf_file_sha256': manifest['pvf_file_sha256'],
        'declared_manifest_digests': manifest.get('declared_manifest_digests'),
        'protocol_profile': manifest['protocol_profile'],
        'server_endpoint': manifest['server_endpoint'],
        'capabilities': manifest['capabilities'],
        'capability_reasons': manifest.get('capability_reasons'),
        'optional_payloads': manifest.get('optional_payloads'),
        'integrity': integrity,
        'sessions': session_summaries,
        'enhanced_logs': enhanced_summaries,
    }


def analyze(path):
    suffix = path.suffix.lower()
    if suffix == '.log':
        return analyze_log(path)
    if suffix == '.jsonl':
        return analyze_contract_v1_jsonl(path)
    size = path.stat().st_size
    if size > CONTRACT_BUNDLE_LIMIT:
        raise ValueError('ZIP exceeds archive limit')
    with zipfile.ZipFile(path) as archive:
        entries = archive.infolist()
        names = [entry.filename for entry in entries]
        if len(names) != len(set(names)) or len(entries) > 80:
            raise ValueError('Archive bounds or duplicate entry')
        if any(n.startswith(('/', '\\')) or '\\' in n or ':' in n or '..' in n.split('/') for n in names):
            raise ValueError('Unsafe archive entry')
        manifest = json.loads(archive.read('manifest.json'))
        if manifest.get('contract_name') == CONTRACT_V1_NAME:
            if sum(e.file_size for e in entries) > CONTRACT_UNCOMPRESSED_LIMIT:
                raise ValueError('Contract v1 bundle uncompressed size exceeds limit')
            return analyze_contract_v1_bundle(archive, entries, manifest)
        if size > LIMIT or len(entries) > 60 or sum(e.file_size for e in entries) > 40 * 1024 * 1024:
            raise ValueError('ZIP exceeds 10 MiB or legacy archive bounds')
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
