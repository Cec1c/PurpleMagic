"""Public synthetic regressions for evidence selection, v2-v5 log compatibility, and Anton Contract v1 bundles."""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

from analyze_diagnostics import analyze, event_row, evidence_timeline, support_summary, validate_v4

ROOT = Path(__file__).resolve().parents[1]

CONTRACT_EXAMPLE_JSONL = """\
{"schema_version": 1, "source": "launcher", "client_session_id": "EXAMPLE-client-session-A", "incident_id": "EXAMPLE-incident-1", "pid": 12345, "process_created_utc": "2026-11-10T16:40:00.000Z", "mono_frequency_hz": 10000000, "event": "character.associated", "event_seq": 1, "observed_utc": "2026-11-10T16:40:15.100Z", "mono_ticks": 151000000, "receiver_character_id": 3, "receiver_character_name": "test1", "association_source": "user_selected"}
{"schema_version": 1, "source": "launcher", "client_session_id": "EXAMPLE-client-session-A", "incident_id": "EXAMPLE-incident-1", "pid": 12345, "process_created_utc": "2026-11-10T16:40:00.000Z", "mono_frequency_hz": 10000000, "event": "observation.reported", "event_seq": 2, "observed_utc": "2026-11-10T16:41:02.200Z", "mono_ticks": 622000000, "entry_id": "EXAMPLE-entry-1", "receiver_character_id": 3, "receiver_character_name": "test1", "peer_character_id": 1, "peer_character_name": "312da", "state": "missing", "observation_source": "user", "estimated_onset_utc": null, "time_uncertainty_ms": null}
{"schema_version": 1, "source": "launcher", "client_session_id": "EXAMPLE-client-session-A", "incident_id": "EXAMPLE-incident-1", "pid": 12345, "process_created_utc": "2026-11-10T16:40:00.000Z", "mono_frequency_hz": 10000000, "event": "observation.reported", "event_seq": 3, "observed_utc": "2026-11-10T16:41:05.700Z", "mono_ticks": 657000000, "entry_id": "EXAMPLE-entry-1", "receiver_character_id": 3, "receiver_character_name": "test1", "peer_character_id": 1, "peer_character_name": "312da", "state": "appeared", "observation_source": "user", "estimated_onset_utc": null, "time_uncertainty_ms": null}
{"schema_version": 1, "source": "launcher", "client_session_id": "EXAMPLE-client-session-A", "incident_id": "EXAMPLE-incident-1", "pid": 12345, "process_created_utc": "2026-11-10T16:40:00.000Z", "mono_frequency_hz": 10000000, "event": "trace.integrity", "event_seq": 4, "observed_utc": "2026-11-10T16:41:06.000Z", "mono_ticks": 660000000, "queue_dropped": 0, "critical_events_lost": 0, "native_events_capability": false, "native_events_unknown_reason": "launcher_only_no_client_event_provider"}
"""


def _build_contract_bundle(target_zip: Path, extra_secret_field=False, tamper_payload=False, bad_seq=False):
    sid = 'cs-fixture-01'
    events = [json.loads(line) for line in CONTRACT_EXAMPLE_JSONL.splitlines()]
    for e in events:
        e['client_session_id'] = sid
    if extra_secret_field:
        events[0]['login_token'] = 'forbidden'
    if bad_seq:
        events[2]['event_seq'] = 2
    obs = [e for e in events if e['event'] == 'observation.reported']
    events_raw = ('\n'.join(json.dumps(e) for e in events) + '\n').encode('utf-8')
    obs_raw = ('\n'.join(json.dumps(e) for e in obs) + '\n').encode('utf-8')
    integrity = {
        'queue_dropped': 0,
        'critical_events_lost': 0,
        'truncated_records': 0,
        'ring_evicted_records': 0,
        'capture_dropped_packets': None,
        'capture_dropped_packets_unknown_reason': 'client_side_packet_capture_not_enabled',
        'native_events_capability': False,
        'native_events_unknown_reason': 'launcher_only_no_client_event_provider',
        'per_session': [],
    }
    integrity_raw = (json.dumps(integrity, indent=2) + '\n').encode('utf-8')
    payload_files = {
        'integrity.json': integrity_raw,
        f'sessions/{sid}/launcher-events.jsonl': events_raw,
        f'sessions/{sid}/observations.jsonl': obs_raw,
    }
    manifest = {
        'schema_version': 1,
        'contract_name': 'purplemagic-anton-first-entry-diagnostics',
        'exported_utc': '2026-11-11T00:00:00.000Z',
        'incident_id': 'EXAMPLE-incident-1',
        'launcher_version': '1.0.11',
        'launcher_commit': 'abcdef123456',
        'diagnostic_module_version': 'pm-contract-v1.0',
        'build_flavor': 'anticheat',
        'client_file_sha256': 'a' * 64,
        'client_file_bytes': 1024,
        'client_file_unknown_reason': None,
        'pvf_file_sha256': 'b' * 64,
        'pvf_file_bytes': 2048,
        'pvf_file_unknown_reason': None,
        'declared_manifest_digests': {
            'manifest_client_sha256': 'a' * 64,
            'manifest_pvf_sha256': 'b' * 64,
            'publication_sequence': 198,
            'content_version': 'V141',
            'source_id': 'tianyi',
        },
        'server_version': None,
        'server_version_unknown_reason': 'not_exposed_to_public_launcher',
        'protocol_profile': 's5a3-bootstrap-v1',
        'server_endpoint': {
            'game_host': '198.51.100.10',
            'bootstrap_port': 7001,
            'channel_server_id': 11,
            'previous_channel_server_id': None,
            'api_origin': 'https://purplemagic.example.test',
        },
        'time_policy': {
            'utc_for_cross_machine_alignment': True,
            'qpc_for_local_intervals': True,
            'compare_qpc_across_hosts': False,
            'raw_qpc_frequency_required': True,
            'host_mono_frequency_hz': 10000000,
        },
        'limits': {
            'pre_trigger_seconds': 120,
            'post_trigger_seconds': 60,
            'max_bytes_per_session': 33554432,
            'max_incidents': 5,
        },
        'capabilities': {
            'launcher_sessions': True,
            'manual_observations': True,
            'game_socket_ownership': True,
            'client_side_packet_capture': False,
            'application_frames': False,
            'native_actor_events': False,
            'native_registry_events': False,
            'native_context_events': False,
            'native_render_events': False,
        },
        'capability_reasons': {
            'launcher_sessions': None,
            'manual_observations': None,
            'game_socket_ownership': None,
            'client_side_packet_capture': 'client_side_packet_capture_not_enabled',
            'application_frames': 'application_frame_decoder_not_installed',
            'native_actor_events': 'launcher_only_no_client_event_provider',
            'native_registry_events': 'launcher_only_no_client_event_provider',
            'native_context_events': 'launcher_only_no_client_event_provider',
            'native_render_events': 'launcher_only_no_client_event_provider',
        },
        'optional_payloads': {
            'packets_pcapng': {'present': False, 'unknown_reason': 'client_side_packet_capture_not_enabled'},
        },
        'sessions': [
            {
                'client_session_id': sid,
                'launch_instance_id': 'slot-1#launch-1',
                'launch_ordinal': 1,
                'active_slot': 1,
                'handoff_mode': 'anticheat_guard_child',
                'pid': 12345,
                'process_created_utc': '2026-11-10T16:40:00.000Z',
                'executable_path': '%USERPROFILE%/Games/DNF.exe',
                'executable_path_sha256': 'c' * 64,
                'started_utc': '2026-11-10T16:40:00.000Z',
                'ended_utc': '2026-11-10T16:42:00.000Z',
                'end_state': 'normal_exit_zero',
                'receiver_character_id': 3,
                'receiver_character_name': 'test1',
                'association_source': 'user_selected',
                'enhanced_session_id': None,
                'entries': [],
            }
        ],
        'integrity': integrity,
        'files': [
            {'path': p, 'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest()}
            for p, raw in sorted(payload_files.items())
        ],
    }
    if tamper_payload:
        payload_files[f'sessions/{sid}/observations.jsonl'] += b' '
    with zipfile.ZipFile(target_zip, 'w', zipfile.ZIP_DEFLATED) as zf:
        zf.writestr('manifest.json', json.dumps(manifest, indent=2) + '\n')
        for p, raw in sorted(payload_files.items()):
            zf.writestr(p, raw)


class AnalysisChecks(unittest.TestCase):
    def test_context_time_and_failure_priority(self):
        context = dict(status='last_known', observed_at='2026-11-01T01:00:00Z', fields={
            'character_name': dict(value='Fixture B', status='last_known', reason='')})
        row = dict(sequence=1, utc='2026-11-01T01:00:02Z', type='event', module='server',
                   reason='server_context_unavailable', fields={}, context=context)
        self.assertEqual(event_row(row)['time_source'], 'client_utc')
        row.update(sequence=2, reason='business_disconnect', utc=context['observed_at'])
        disconnect = event_row(row)
        self.assertEqual(disconnect['time_source'], 'server_utc')
        row.update(sequence=3, reason='native_exception', module='process', fields={'first_chance': {'value': 1}})
        summary = support_summary(dict(summary={}, ended_utc=row['utc']), [disconnect, event_row(row)])
        self.assertEqual(summary['time_basis'], 'server_utc')
        self.assertEqual(summary['event_beijing'], '2026-11-01T09:00:00+08:00')
        self.assertEqual(summary['character']['value'], 'Fixture B')
        row['fields']['first_chance']['value'] = 0
        summary = support_summary(dict(summary={}, ended_utc=row['utc']), [event_row(row), disconnect])
        self.assertEqual(summary['time_basis'], 'client_utc')

    def test_tail_and_stages(self):
        events = [dict(sequence=i + 1, utc='fixture', kind='event', reason='player_operations', fields={}) for i in range(269)]
        for index, first in ((260, 1), (261, 0)):
            events[index].update(kind='exception', reason='native_exception', fields={'first_chance': {'value': first}, 'thread_id': {'value': 1234}})
        events[262].update(reason='observer_failed')
        events[-1].update(reason='game_exit', fields={'exit_code': {'value': -1073741819}})
        result = evidence_timeline(events, 'game_exit')
        self.assertEqual(result['outcome'], dict(state='unhandled_crash', unhandled_exceptions=1, exits=1))
        self.assertEqual([e['sequence'] for e in result['critical_events']], [261, 262, 263, 269])
        self.assertEqual(sum(r['count'] for r in result['omitted_event_ranges']), result['timeline_omitted'])
        self.assertTrue(all(e in result['timeline'] for e in result['critical_events']))
        self.assertEqual(evidence_timeline(events[:20], 'recording')['outcome']['state'], 'recording')

    def test_existing_formats(self):
        for name in ('diagnostics-synthetic.log', 'diagnostics-synthetic.zip', 'diagnostics-native-synthetic.log'):
            self.assertTrue(analyze(ROOT / 'docs/diagnostics' / name)['verified'])

    def test_strict_new_schema_and_precision(self):
        old = [json.loads(line) for line in (ROOT / 'docs/diagnostics/diagnostics-synthetic.log').read_text().splitlines()]
        rows = copy.deepcopy(old)
        for row in (rows[0], rows[-1]):
            row['schema_version'] = 4
            row['log']['schema_version'] = 4
            summary = row['log']['summary']
            summary.update(server_events_connected=False, server_clock_offset_ms=None, server_clock_uncertainty_ms=None,
                           observation_kind='client_polling', counter_scope='session_cumulative', correlation_id=summary['session_id'], expected_pvf_sha256='', release_channel='standard')
        for row in rows[1:-1]:
            row['record_version'] = 3
        row = rows[1]
        row['evidence'] = dict(kind='native_exception', exceptions=None, stack=[dict(module='unmapped', offset='0x0000000000000000', method=None, address='0xFFFFFFFFFFFFFFFF', module_id=None)], native=None, module=None)
        validate_v4(rows)
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'synthetic.log'
            path.write_text('\n'.join(json.dumps(r) for r in rows) + '\n', encoding='utf-8')
            self.assertTrue(analyze(path)['verified'])
            self.assertEqual(json.loads(path.read_text().splitlines()[1])['evidence']['stack'][0]['address'], '0xFFFFFFFFFFFFFFFF')
            row['evidence']['unexpected'] = 'not allowed'
            with self.assertRaises(ValueError):
                validate_v4(rows)
            del row['evidence']['unexpected']
            row['evidence']['stack'][0]['address'] = 18446744073709551615
            with self.assertRaises(ValueError):
                validate_v4(rows)
            rows[0]['schema_version'] = 3
            path.write_text('\n'.join(json.dumps(r) for r in rows) + '\n', encoding='utf-8')
            with self.assertRaises(ValueError):
                analyze(path)

    def test_contract_v1_example_jsonl_and_bundle_verification(self):
        with tempfile.TemporaryDirectory() as temp:
            tmp = Path(temp)
            jsonl_path = tmp / 'launcher-events-EXAMPLE.jsonl'
            jsonl_path.write_text(CONTRACT_EXAMPLE_JSONL, encoding='utf-8')
            parsed = analyze(jsonl_path)
            self.assertTrue(parsed['verified'])
            self.assertEqual(parsed['records'], 4)
            self.assertEqual(parsed['observations'], 2)

            valid_zip = tmp / 'valid-bundle.zip'
            _build_contract_bundle(valid_zip)
            report = analyze(valid_zip)
            self.assertTrue(report['verified'])
            self.assertEqual(report['contract_name'], 'purplemagic-anton-first-entry-diagnostics')
            self.assertEqual(report['incident_id'], 'EXAMPLE-incident-1')
            self.assertEqual(len(report['sessions']), 1)
            self.assertEqual(len(report['sessions'][0]['observations']), 2)

            for label, kwargs in (
                ('secret', dict(extra_secret_field=True)),
                ('tamper', dict(tamper_payload=True)),
                ('bad_seq', dict(bad_seq=True)),
            ):
                bad_zip = tmp / f'bad-{label}.zip'
                _build_contract_bundle(bad_zip, **kwargs)
                with self.assertRaises(ValueError):
                    analyze(bad_zip)


if __name__ == '__main__':
    unittest.main()
