"""Public synthetic regressions for evidence selection and schema compatibility."""
import copy
import json
from pathlib import Path
import tempfile
import unittest

from analyze_diagnostics import analyze, evidence_timeline, validate_v4

ROOT = Path(__file__).resolve().parents[1]


class AnalysisChecks(unittest.TestCase):
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


if __name__ == '__main__':
    unittest.main()
