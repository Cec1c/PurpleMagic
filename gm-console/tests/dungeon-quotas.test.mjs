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

function makeQuotaSnapshot(revision = '1'.repeat(64), quotas = makeQuotas()) {
  return {
    accountId: 'a',
    characterId: '10',
    characterLevel: 90,
    quotas,
    pvfChecksum: 'a'.repeat(64),
    definitionRevision: 'b'.repeat(64),
    revision,
  };
}

function fixture() {
  storage.clear();
  const api = new ConsoleAPI(() => {});
  api.current = '10';
  api.sectionId = 'gm';
  api.state.accounts = [{ id: 'a', name: 'account-a', points: 0, tokens: 0, rawPoints: '0' }];
  api.state.characters = [
    { id: '10', accountId: 'a', name: '剑宗测试', level: 90, online: false, job: '女鬼剑士', jobCode: 11, growType: 1, gold: 0, sp: 0, tp: 0 },
  ];
  api.meta.features['dungeonQuotas.read'] = true;
  api.meta.features['dungeonQuotas.write'] = true;
  api.meta.resources.dungeonQuotas = makeQuotaSnapshot();
  return api;
}

test('two consecutive quota resets fetch fresh quotas before preview and update revision in place', async () => {
  const api = fixture();
  const postCalls = [];
  let serverQuotas = makeQuotas();
  let serverRevision = '1'.repeat(64);
  let stepIndex = 0;

  api.http.fetcher = async (path, init) => {
    if (init.method === 'GET' && path === '/api/v1/characters/10/dungeon-quotas') {
      return new Response(JSON.stringify({ data: makeQuotaSnapshot(serverRevision, serverQuotas) }));
    }
    assert.equal(init.method, 'POST');
    const body = JSON.parse(init.body);
    postCalls.push({ path, body, key: init.headers['Idempotency-Key'] });
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
    serverRevision = nextRev;
    serverQuotas = nextQuotas;
    return new Response(JSON.stringify({ data: receipt }));
  };

  await api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'echon' }, selection);
  assert.match(api.meta.pending.summary, /艾肯.*4\/4 → 0\/4/);
  await api.confirm();
  assert.equal(api.meta.resources.dungeonQuotas.revision, '2'.repeat(64));
  assert.equal(api.meta.resources.dungeonQuotas.quotas.find((q) => q.key === 'echon').used, 0);

  await api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'anton_raid' }, selection);
  assert.equal(postCalls[2].body.revision, '2'.repeat(64));
  await api.confirm();
  assert.equal(postCalls.length, 4);
  assert.equal(api.meta.resources.dungeonQuotas.revision, '3'.repeat(64));
  assert.equal(api.meta.resources.dungeonQuotas.quotas.find((q) => q.key === 'anton_raid').used, 0);
});

test('zero-used quota rejects before preview POST and disabled capability rejects before any network call', async () => {
  {
    const api = fixture();
    let postCalls = 0;
    api.http.fetcher = async (path, init) => {
      if (init.method === 'GET' && path === '/api/v1/characters/10/dungeon-quotas') {
        return new Response(JSON.stringify({ data: makeQuotaSnapshot() }));
      }
      postCalls++;
      throw new Error('unexpected POST call');
    };
    await assert.rejects(() => api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'fiend_war' }, selection));
    assert.equal(postCalls, 0);
  }
  {
    const api = fixture();
    api.meta.features['dungeonQuotas.write'] = false;
    let calls = 0;
    api.http.fetcher = async () => {
      calls++;
      throw new Error('unexpected network call');
    };
    await assert.rejects(() => api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'pandemonium_rift' }, selection));
    assert.equal(calls, 0);
  }
});

test('lost response retries with identical Idempotency-Key and updates quotas from receipt', async () => {
  const api = fixture();
  const keys = [];
  let failOnce = true;
  api.http.fetcher = async (path, init) => {
    if (init.method === 'GET' && path === '/api/v1/characters/10/dungeon-quotas') {
      return new Response(JSON.stringify({ data: makeQuotaSnapshot() }));
    }
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

test('refreshGmTab("quotas") refreshes both character online status and latest dungeon quotas', async () => {
  const api = fixture();
  assert.equal(api.state.characters[0].online, false);
  assert.equal(api.meta.resources.dungeonQuotas.revision, '1'.repeat(64));

  const seenPaths = [];
  api.http.fetcher = async (path, init) => {
    assert.equal(init.method, 'GET');
    seenPaths.push(path);
    if (path === '/api/v1/characters/10') {
      return new Response(JSON.stringify({
        data: {
          character: { characterId: '10', accountId: 'a', name: '剑宗测试', level: 90, job: 11, growType: 1 },
          online: 'unknown',
        },
      }));
    }
    if (path === '/api/v1/presence') {
      return new Response(JSON.stringify({ data: { characterIds: ['10'] } }));
    }
    if (path === '/api/v1/characters/10/dungeon-quotas') {
      return new Response(JSON.stringify({ data: makeQuotaSnapshot('5'.repeat(64), makeQuotas(3, 4, 2, 1)) }));
    }
    throw new Error('unexpected path: ' + path);
  };

  await api.refreshGmTab('quotas');
  assert.equal(api.state.characters.find((c) => c.id === '10').online, true);
  assert.equal(api.meta.resources.dungeonQuotas.revision, '5'.repeat(64));
  assert.equal(api.meta.resources.dungeonQuotas.quotas.find((q) => q.key === 'pandemonium_rift').used, 3);
  assert.ok(seenPaths.includes('/api/v1/presence'));
  assert.ok(seenPaths.includes('/api/v1/characters/10/dungeon-quotas'));
});

test('reset-dungeon-quota preview ignores stale cached revision and retries once on 409 conflict', async () => {
  const api = fixture();
  // Seed stale cached revision '1'x64 while server is already at '4'x64 and races once to '5'x64.
  api.meta.resources.dungeonQuotas = makeQuotaSnapshot('1'.repeat(64), makeQuotas(1, 4, 2, 0));

  let getCount = 0;
  const previewBodies = [];
  api.http.fetcher = async (path, init) => {
    if (init.method === 'GET' && path === '/api/v1/characters/10/dungeon-quotas') {
      getCount++;
      const rev = getCount === 1 ? '4'.repeat(64) : '5'.repeat(64);
      const used = getCount === 1 ? 2 : 3;
      return new Response(JSON.stringify({ data: makeQuotaSnapshot(rev, makeQuotas(used, 4, 2, 0)) }));
    }
    if (init.method === 'POST' && path === '/api/v1/characters/10/dungeon-quota-operations/preview') {
      const body = JSON.parse(init.body);
      previewBodies.push(body);
      if (previewBodies.length === 1) {
        return new Response(
          JSON.stringify({ error: { code: 'revision_conflict', message: '角色副本额度已变更，请刷新后重新预览', retryable: false } }),
          { status: 409 }
        );
      }
      return new Response(JSON.stringify({
        data: {
          receipt: { quotas: makeQuotas(0, 4, 2, 0) },
          definitionRevision: 'b'.repeat(64),
          planToken: 'plan-after-409-retry',
        },
      }));
    }
    throw new Error('unexpected call: ' + init.method + ' ' + path);
  };

  await api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'pandemonium_rift' }, selection);
  assert.equal(getCount, 2);
  assert.equal(previewBodies.length, 2);
  assert.equal(previewBodies[0].revision, '4'.repeat(64));
  assert.equal(previewBodies[1].revision, '5'.repeat(64));
  assert.equal(api.meta.pending.steps[0].body.planToken, 'plan-after-409-retry');
  assert.match(api.meta.pending.summary, /魔界裂缝.*3\/3 → 0\/3/);
});

test('repreviewDungeonQuotas() recovers failed quota reset in modal and after dismissing to top banner', async () => {
  const api = fixture();
  let phase = 'initial';

  api.http.fetcher = async (path, init) => {
    if (init.method === 'GET' && path === '/api/v1/characters/10/dungeon-quotas') {
      const rev = phase === 'initial' ? '2'.repeat(64) : '6'.repeat(64);
      const riftUsed = phase === 'initial' ? 2 : 3;
      return new Response(JSON.stringify({ data: makeQuotaSnapshot(rev, makeQuotas(riftUsed, 4, 2, 0)) }));
    }
    if (init.method === 'POST' && path.endsWith('/preview')) {
      const body = JSON.parse(init.body);
      return new Response(JSON.stringify({
        data: {
          receipt: { revision: body.revision },
          definitionRevision: 'b'.repeat(64),
          planToken: 'plan-' + body.revision.slice(0, 4),
        },
      }));
    }
    if (init.method === 'POST' && path.endsWith('/dungeon-quota-operations')) {
      const body = JSON.parse(init.body);
      if (body.revision !== '6'.repeat(64)) {
        return new Response(
          JSON.stringify({ error: { code: 'revision_conflict', message: '角色副本额度已变更，请刷新后重新预览', retryable: false } }),
          { status: 409 }
        );
      }
      return new Response(JSON.stringify({
        data: {
          kind: 'dungeon-quota-operation',
          accountId: 'a',
          characterId: '10',
          quotas: makeQuotas(0, 4, 2, 0),
          pvfChecksum: 'a'.repeat(64),
          definitionRevision: 'b'.repeat(64),
          revision: '7'.repeat(64),
        },
      }));
    }
    throw new Error('unexpected call: ' + path);
  };

  await api.prepare({ kind: 'reset-dungeon-quota', characterId: '10', quotaKey: 'pandemonium_rift' }, selection);
  await api.guard(() => api.confirm(api.meta.pending.id));
  assert.equal(api.meta.pending.rejected, true);
  assert.equal(api.meta.pending.errorCode, 'revision_conflict');
  assert.equal(api.canRepreviewDungeonQuotas(), true);

  // Dismiss modal ("结束此操作"): top banner error still offers repreviewableDungeonQuotaPending()
  api.dismissPending();
  assert.equal(api.meta.pending, undefined);
  const bannerTarget = api.repreviewableDungeonQuotaPending();
  assert.ok(bannerTarget);

  phase = 'drifted';
  await api.repreviewDungeonQuotas(bannerTarget.id);
  assert.equal(api.meta.error, '');
  assert.ok(api.meta.pending);
  assert.equal(api.meta.pending.rejected, false);
  assert.equal(api.meta.pending.steps[0].body.revision, '6'.repeat(64));
  assert.match(api.meta.pending.summary, /3\/3 → 0\/3/);

  await api.confirm(api.meta.pending.id);
  assert.equal(api.meta.pending.completed, true);
  assert.equal(api.meta.resources.dungeonQuotas.revision, '7'.repeat(64));
  assert.equal(api.meta.resources.dungeonQuotas.quotas.find((q) => q.key === 'pandemonium_rift').used, 0);
});
