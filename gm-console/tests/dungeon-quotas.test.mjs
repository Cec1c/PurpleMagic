import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';

const { outputFiles } = await build({
  entryPoints: ['src/api/console.ts'],
  bundle: true,
  platform: 'node',
  format: 'esm',
  write: false,
});
const { ConsoleAPI } = await import(
  'data:text/javascript;base64,' + Buffer.from(outputFiles[0].text).toString('base64')
);

const storage = new Map();
globalThis.sessionStorage = {
  getItem: (k) => storage.get(k) ?? null,
  setItem: (k, v) => storage.set(k, v),
  removeItem: (k) => storage.delete(k),
};

const selection = { accounts: [], characters: [] };

function makeQuotas(riftUsed = 2, echonUsed = 4, antonUsed = 2, fiendUsed = 0) {
  return [
    { key: 'pandemonium_rift', name: '魔界裂缝', category: 'daily', resetSchedule: '每日 06:00', used: riftUsed, limit: 3, canReset: riftUsed > 0, detail: '下次入场消耗 6 个精炼的时空石' },
    { key: 'echon', name: '艾肯（遗落的神殿 + 试炼）', category: 'daily', resetSchedule: '每日 06:00', used: echonUsed, limit: 4, canReset: echonUsed > 0, detail: '遗落的神殿 3/3（已通关 3/3） · 试炼 1/1' },
    { key: 'anton_raid', name: '安徒恩攻坚战', category: 'weekly', resetSchedule: '每周四 06:00', used: antonUsed, limit: 2, canReset: antonUsed > 0, detail: '团队与单人安徒恩共用角色每周攻坚次数' },
    { key: 'luke_raid', name: '卢克攻坚战', category: 'weekly', resetSchedule: '每周四 06:00', used: 1, limit: 2, canReset: true, detail: '团队与单人卢克共用角色每周攻坚次数' },
    { key: 'fiend_war', name: '超时空之战', category: 'weekly', resetSchedule: '每周四 06:00', used: fiendUsed, limit: 2, canReset: fiendUsed > 0, detail: '角色每周攻坚通关次数' },
  ];
}

function fixture() {
  storage.clear();
  const api = new ConsoleAPI(() => {});
  api.current = '10';
  api.sectionId = 'gm';
  api.state.characters = [
    { id: '10', accountId: 'a', name: '剑宗测试', level: 90, online: false, job: '女鬼剑士', gold: 0, sp: 0, tp: 0 },
  ];
  api.meta.features['dungeonQuotas.read'] = true;
  api.meta.features['dungeonQuotas.write'] = true;
  api.meta.resources.dungeonQuotas = {
    accountId: 'a',
    characterId: '10',
    characterLevel: 90,
    quotas: makeQuotas(),
    pvfChecksum: 'a'.repeat(64),
    definitionRevision: 'b'.repeat(64),
    revision: '1'.repeat(64),
  };
  return api;
}

test('two consecutive quota resets make only preview + commit each and update revision in place', async () => {
  const api = fixture();
  const calls = [];
  let stepIndex = 0;
  api.http.fetcher = async (path, init) => {
    assert.equal(init.method, 'POST');
    const body = JSON.parse(init.body);
    calls.push({ path, body, key: init.headers['Idempotency-Key'] });
    const isPreview = path.endsWith('/preview');
    const nextRev = stepIndex === 0 ? '2'.repeat(64) : '3'.repeat(64);
    const nextQuotas =
      body.quotaKey === 'echon'
        ? makeQuotas(2, 0, 2, 0)
        : makeQuotas(2, 0, 0, 0);
    const receipt = {
      kind: 'dungeon-quota-operation',
      accountId: 'a',
      characterId: '10',
      action: 'reset',
      quotaKey: body.quotaKey,
      quotaName: body.quotaKey === 'echon' ? '艾肯（遗落的神殿 + 试炼）' : '安徒恩攻坚战',
      before: { used: body.quotaKey === 'echon' ? 4 : 2, limit: body.quotaKey === 'echon' ? 4 : 2 },
      after: { used: 0, limit: body.quotaKey === 'echon' ? 4 : 2 },
      quotas: nextQuotas,
      pvfChecksum: 'a'.repeat(64),
      definitionRevision: 'b'.repeat(64),
      revision: isPreview ? body.revision : nextRev,
    };
    if (isPreview) {
      return new Response(JSON.stringify({ data: { receipt, definitionRevision: 'b'.repeat(64), planToken: 'signed-plan-' + body.quotaKey } }));
    }
    stepIndex++;
    return new Response(JSON.stringify({ data: receipt }));
  };

  await api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'echon' }, selection);
  assert.match(api.meta.pending.summary, /艾肯.*4\/4 → 0\/4/);
  await api.confirm();
  assert.equal(api.meta.resources.dungeonQuotas.revision, '2'.repeat(64));
  assert.equal(api.meta.resources.dungeonQuotas.quotas.find((q) => q.key === 'echon').used, 0);

  await api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'anton_raid' }, selection);
  assert.equal(calls[2].body.revision, '2'.repeat(64));
  await api.confirm();
  assert.equal(calls.length, 4);
  assert.equal(api.meta.resources.dungeonQuotas.revision, '3'.repeat(64));
  assert.equal(api.meta.resources.dungeonQuotas.quotas.find((q) => q.key === 'anton_raid').used, 0);
});

test('zero-used quota and disabled capability reject before any network call', async () => {
  for (const run of [
    (api) => api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'fiend_war' }, selection),
    (api) => {
      api.meta.features['dungeonQuotas.write'] = false;
      return api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'pandemonium_rift' }, selection);
    },
  ]) {
    const api = fixture();
    let calls = 0;
    api.http.fetcher = async () => {
      calls++;
      throw new Error('unexpected network call');
    };
    await assert.rejects(() => run(api));
    assert.equal(calls, 0);
  }
});

test('lost response retries with identical Idempotency-Key and updates quotas from receipt', async () => {
  const api = fixture();
  const keys = [];
  let failOnce = true;
  api.http.fetcher = async (path, init) => {
    if (path.endsWith('/preview')) {
      return new Response(JSON.stringify({ data: { receipt: {}, definitionRevision: 'b'.repeat(64), planToken: 'token-rift' } }));
    }
    keys.push(init.headers['Idempotency-Key']);
    if (failOnce) {
      failOnce = false;
      throw new Error('network interrupted');
    }
    return new Response(
      JSON.stringify({
        data: {
          kind: 'dungeon-quota-operation',
          accountId: 'a',
          characterId: '10',
          quotas: makeQuotas(0, 4, 2, 0),
          pvfChecksum: 'a'.repeat(64),
          definitionRevision: 'b'.repeat(64),
          revision: '9'.repeat(64),
        },
      })
    );
  };
  await api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'pandemonium_rift' }, selection);
  await assert.rejects(api.confirm());
  await api.confirm();
  assert.equal(keys.length, 2);
  assert.equal(keys[0], keys[1]);
  assert.equal(api.meta.resources.dungeonQuotas.quotas.find((q) => q.key === 'pandemonium_rift').used, 0);
});
