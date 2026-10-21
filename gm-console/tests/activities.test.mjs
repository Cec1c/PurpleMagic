import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { activityChanges, demoActivities } from '../src/activities.ts';
import { applyCommand } from '../src/domain.ts';
const { outputFiles } = await build({ entryPoints: ['src/api/console.ts'], bundle: true, platform: 'node', format: 'esm', write: false });
const { ConsoleAPI } = await import('data:text/javascript;base64,' + Buffer.from(outputFiles[0].text).toString('base64'));
const response = (data, status = 200) => new Response(JSON.stringify(status === 200 ? { data } : { error: { code: 'unavailable', message: 'fixture unavailable' } }), { status });
const selection = { accounts: [], characters: [] };
const command = (revision = 0) => ({ kind: 'activity-save', revision, activities: [{ id: 145, enabled: false }, { id: 2365, enabled: true }] });

function fixture() {
  const storage = new Map();
  globalThis.sessionStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) };
  let snapshot = structuredClone(demoActivities);
  const calls = [];
  const api = new ConsoleAPI(() => {});
  api.sectionId = 'activities';
  api.meta.authenticated = true;
  api.meta.features = { 'activities.read': true, 'activities.write': true };
  const save = body => {
    assert.equal(body.revision, snapshot.revision);
    for (const change of body.activities) {
      const row = snapshot.activities.find(v => v.id === change.id);
      row.enabled = change.enabled;
      row.pendingRestart = row.enabled !== row.effectiveEnabled;
    }
    snapshot.revision++;
    snapshot.restartRequired = snapshot.activities.some(v => v.pendingRestart);
  };
  const fetcher = async (path, init) => {
    assert.equal(path, '/api/v1/activities', 'global activities never read or write a character');
    calls.push({ method: init.method, body: init.body, key: init.headers['Idempotency-Key'] });
    if (init.method === 'POST') save(JSON.parse(init.body));
    return response(snapshot);
  };
  api.http.fetcher = fetcher;
  return { api, calls, storage, save, fetcher, get snapshot() { return snapshot; }, replace(value) { snapshot = value; } };
}

test('activity batch prepares exact changes without writing; one confirmation saves without a character', async () => {
  const f = fixture();
  const c = command();
  c.activities.push({ id: 331, enabled: true }); // Already enabled: excluded from the confirmed batch.
  await f.api.prepare(c, selection);
  assert.deepEqual(f.calls.map(c => c.method), ['GET']);
  assert.equal(f.api.meta.pending.steps.length, 1);
  assert.deepEqual(f.api.meta.pending.activityChanges.map(v => v.id), [145, 2365]);
  c.activities.length = 0; // The confirmation owns a frozen copy.
  await f.api.confirm();
  assert.equal(f.calls.filter(c => c.method === 'POST').length, 1);
  assert.equal(f.api.meta.pending.completed, true);
  assert.equal(f.api.meta.resources.activities.revision, 1);
  assert.equal(f.snapshot.activities.find(v => v.id === 145).enabled, false);
  assert.equal(f.snapshot.activities.find(v => v.id === 145).effectiveEnabled, true);
  assert.equal(f.snapshot.restartRequired, true);
  await f.api.confirm();
  assert.equal(f.calls.filter(c => c.method === 'POST').length, 1);
});

test('stale activity page refreshes the displayed snapshot without preparing a write', async () => {
  const f = fixture(); f.save({ revision: 0, activities: [{ id: 331, enabled: false }] });
  await assert.rejects(f.api.prepare(command(), selection), e => e.status === 409);
  assert.equal(f.api.meta.resources.activities.revision, 1);
  assert.equal(f.api.meta.pending, undefined);
  assert.ok(f.calls.every(c => c.method === 'GET'));
});

test('another operator changing activities after preview rejects the original confirmation', async () => {
  const f = fixture(); await f.api.prepare(command(), selection);
  f.save({ revision: 0, activities: [{ id: 331, enabled: false }] });
  await assert.rejects(f.api.confirm(), e => e.status === 409);
  assert.equal(f.api.meta.pending.rejected, true);
  assert.equal(f.api.meta.pending.steps[0].body.revision, 0);
  assert.ok(f.calls.every(c => c.method === 'GET'));
  assert.equal(f.snapshot.activities.find(v => v.id === 145).enabled, true);
});

test('saved activity batch with a lost response recovers by readback, without another POST', async () => {
  const f = fixture(); await f.api.prepare(command(), selection);
  f.api.http.fetcher = async (path, init) => {
    const result = await f.fetcher(path, init);
    if (init.method === 'POST') throw new Error('lost response');
    return result;
  };
  await f.api.confirm();
  assert.equal(f.api.meta.pending.completed, true);
  assert.equal(f.api.meta.pending.steps[0].attempted, true);
  assert.equal(f.api.meta.pending.steps[0].result.revision, 1);
  assert.equal(f.calls.filter(c => c.method === 'POST').length, 1);
});

test('unsaved activity failure keeps the original revision and idempotency key for retry', async () => {
  const f = fixture(); await f.api.prepare(command(), selection);
  let writes = 0; const bodies = [];
  f.api.http.fetcher = async (path, init) => {
    if (init.method === 'POST') {
      bodies.push({ body: init.body, key: init.headers['Idempotency-Key'] });
      if (++writes === 1) return response({}, 503);
    }
    return f.fetcher(path, init);
  };
  await assert.rejects(f.api.confirm(), e => e.status === 503);
  assert.equal(f.api.meta.pending.rejected, false);
  assert.equal(f.snapshot.revision, 0);
  await f.api.confirm();
  assert.equal(f.api.meta.pending.completed, true);
  assert.equal(f.snapshot.revision, 1);
  assert.deepEqual(bodies[0], bodies[1]);
});

test('restored activity operation recovers a saved batch after both write and read responses were lost', async () => {
  const f = fixture(); await f.api.prepare(command(), selection);
  let disconnected = false;
  f.api.http.fetcher = async (path, init) => {
    if (disconnected) throw new Error('fixture disconnected');
    const result = await f.fetcher(path, init);
    if (init.method === 'POST') { disconnected = true; throw new Error('lost response'); }
    return result;
  };
  await assert.rejects(f.api.confirm());
  assert.equal(JSON.parse(f.storage.get('purplemagic-pending-v1')).steps[0].attempted, true);
  const restored = new ConsoleAPI(() => {});
  restored.sectionId = 'activities'; restored.meta.authenticated = true;
  restored.http.fetcher = f.fetcher; restored.restore();
  await restored.confirm();
  assert.equal(restored.meta.pending.completed, true);
  assert.equal(f.calls.filter(c => c.method === 'POST').length, 1);
  assert.equal(f.snapshot.revision, 1);
});

test('failed write followed by a different configuration never rebases or overwrites the new revision', async () => {
  const f = fixture(); await f.api.prepare(command(), selection);
  let writes = 0;
  f.api.http.fetcher = async (path, init) => {
    if (init.method === 'POST') {
      writes++; f.save({ revision: 0, activities: [{ id: 331, enabled: false }] });
      return response({}, 409);
    }
    return f.fetcher(path, init);
  };
  await assert.rejects(f.api.confirm(), e => e.status === 409);
  assert.equal(f.api.meta.pending.rejected, true);
  await f.api.confirm(); assert.equal(writes, 1);
  assert.equal(f.api.meta.pending.steps[0].body.revision, 0);
  assert.equal(f.snapshot.activities.find(v => v.id === 145).enabled, true);
});

test('activity preparation requires the actual server write capability', async () => {
  const f = fixture(); f.api.meta.features = { 'activities.read': true };
  await assert.rejects(f.api.prepare(command(), selection), /尚未开放/);
  assert.equal(f.calls.length, 0);
});

test('activity batch validation rejects unknown, duplicate, malformed and no-op selections', () => {
  for (const changes of [[], [{ id: 2, enabled: true }], [{ id: 145, enabled: false }, { id: 145, enabled: true }], [{ id: 145, enabled: 'false' }], [{ id: 145, enabled: true }], Array(257).fill({ id: 145, enabled: false })]) {
    assert.throws(() => activityChanges(demoActivities, changes));
  }
  assert.throws(() => activityChanges({ ...demoActivities, revision: Number.MAX_SAFE_INTEGER + 1 }, command().activities));
});

test('demo activities preserve running state and pending explicit policy until restart', () => {
  const initial = { schema: 1, history: [], receipts: [] };
  const first = applyCommand(initial, command(), '2026-10-21T00:00:00Z', 'first');
  assert.equal(first.activities.revision, 1);
  assert.equal(first.activities.restartRequired, true);
  const second = applyCommand(first, { kind: 'activity-save', revision: 1, activities: [{ id: 145, enabled: true }, { id: 2365, enabled: false }] }, '2026-10-21T00:01:00Z', 'second');
  assert.equal(second.activities.revision, 2);
  assert.equal(second.activities.restartRequired, true);
  assert.equal(second.activities.activities.find(v => v.id === 145).effectiveEnabled, true);
  assert.equal(demoActivities.revision, 0);
});
