import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { build } from "esbuild";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  computeCeraAdjustment,
  MAX_CERA,
  isDirectCommand,
} from "../src/domain.ts";
import {
  appendAttachments,
  canEnhanceAttachment,
  canForgeAttachment,
} from "../src/item-picker.ts";
import {
  readAntiCheatMemory,
  readDirectoryMemory,
  resetNavigationMemoryForTests,
  writeAntiCheatMemory,
  writeDirectoryMemory,
} from "../src/navigation-state.ts";

const apiBundle = await build({
  entryPoints: ["src/api/console.ts"],
  bundle: true,
  platform: "node",
  format: "esm",
  write: false,
});
const { ConsoleAPI } = await import(
  "data:text/javascript;base64," +
    Buffer.from(apiBundle.outputFiles[0].text).toString("base64")
);

const uiBundle = await build({
  stdin: {
    contents: `
      export { LiveContext } from "./src/api/context";
      export { Identity } from "./src/ui";
      export { GmPage } from "./src/pages/Gm";
      export { MailPage } from "./src/pages/Mail";
      export { AntiCheatPage } from "./src/pages/AntiCheat";
      export { AccountManagerModal } from "./src/AccountManagerModal";
    `,
    resolveDir: process.cwd(),
    loader: "tsx",
  },
  bundle: true,
  platform: "node",
  format: "cjs",
  packages: "external",
  loader: { ".css": "empty" },
  write: false,
});
const uiMod = { exports: {} };
new Function("require", "module", "exports", uiBundle.outputFiles[0].text)(
  createRequire(import.meta.url),
  uiMod,
  uiMod.exports,
);

const storage = new Map();
globalThis.sessionStorage = {
  getItem: (k) => storage.get(k) ?? null,
  setItem: (k, v) => storage.set(k, v),
  removeItem: (k) => storage.delete(k),
};
globalThis.localStorage = {
  getItem: () => null,
  setItem() {},
};

const ok = (data) => new Response(JSON.stringify({ data }), { status: 200 });
const fail = (code, message, status = 409) =>
  new Response(JSON.stringify({ error: { code, message } }), { status });
const emptySelection = { accounts: [], characters: [] };

test("computeCeraAdjustment handles add, sub, set with exact 64-bit BigInt bounds", () => {
  const large = "9007199254740993";
  const added = computeCeraAdjustment(large, "add", "1000");
  assert.equal(added.valid, true);
  assert.equal(added.after, 9007199254741993n);
  assert.equal(added.delta, 1000n);

  const subbed = computeCeraAdjustment("50000", "sub", "12500");
  assert.equal(subbed.valid, true);
  assert.equal(subbed.after, 37500n);
  assert.equal(subbed.delta, -12500n);

  const setRes = computeCeraAdjustment("50000", "set", "88000");
  assert.equal(setRes.valid, true);
  assert.equal(setRes.after, 88000n);
  assert.equal(setRes.delta, 38000n);

  assert.equal(computeCeraAdjustment("100", "add", "0").valid, false);
  assert.equal(computeCeraAdjustment("100", "set", "100").valid, false);
  assert.equal(computeCeraAdjustment("100", "sub", "101").valid, false);
  assert.equal(
    computeCeraAdjustment(String(MAX_CERA), "add", "1").valid,
    false,
  );
});

test("standalone 0-character account panel executes Cera, Black Diamond and Adventure rename with empty character selection", async () => {
  storage.clear();
  const api = new ConsoleAPI(() => {});
  api.meta.authenticated = true;
  api.meta.features = {
    "currency.read": true,
    "currency.write": true,
    "premium.read": true,
    "premium.write": true,
    "adventure.read": true,
    "adventure.rename": true,
  };
  assert.equal(api.current, "");
  assert.equal(api.state.characters.length, 0);

  const calls = [];
  api.http.fetcher = async (path, init = {}) => {
    calls.push([init.method || "GET", path]);
    if (path === "/api/v1/accounts/empty-acc")
      return ok({
        accountId: "empty-acc",
        name: "零角色账号",
        disabled: false,
        characterCount: 0,
      });
    if (path === "/api/v1/accounts/empty-acc/currency")
      return ok({
        accountId: "empty-acc",
        currency: "cera",
        balance: "2000",
        revision: "rev-1",
      });
    if (path === "/api/v1/accounts/empty-acc/premium")
      return ok({ accountId: "empty-acc", active: false, expireAt: 0 });
    if (path === "/api/v1/accounts/empty-acc/adventure-name")
      return ok({ accountId: "empty-acc", name: "空号冒险团" });
    if (path === "/api/v1/accounts/empty-acc/currency-adjustments/preview") {
      const body = JSON.parse(init.body);
      assert.deepEqual(body, { amount: "7000", revision: "rev-1" });
      return ok({
        receipt: {
          kind: "currency-adjustment",
          accountId: "empty-acc",
          before: "2000",
          after: "7000",
          delta: "5000",
          revision: "rev-2",
        },
      });
    }
    if (path === "/api/v1/accounts/empty-acc/currency-adjustments") {
      return ok({
        kind: "currency-adjustment",
        accountId: "empty-acc",
        currency: "cera",
        before: "2000",
        after: "7000",
        delta: "5000",
        revision: "rev-2",
      });
    }
    if (path === "/api/v1/accounts/empty-acc/premium-grants/preview") {
      return ok({
        receipt: {
          kind: "premium-grant",
          accountId: "empty-acc",
          active: true,
          addedDays: 30,
          expireAt: 1900000000,
        },
      });
    }
    if (path === "/api/v1/accounts/empty-acc/premium-grants") {
      return ok({
        kind: "premium-grant",
        accountId: "empty-acc",
        active: true,
        addedDays: 30,
        expireAt: 1900000000,
      });
    }
    if (
      path === "/api/v1/accounts/empty-acc/adventure-rename-operations/preview"
    ) {
      return ok({
        receipt: {
          kind: "adventure-rename-operation",
          accountId: "empty-acc",
          before: "空号冒险团",
          after: "兰紫冒险团",
        },
      });
    }
    if (path === "/api/v1/accounts/empty-acc/adventure-rename-operations") {
      return ok({
        kind: "adventure-rename-operation",
        accountId: "empty-acc",
        before: "空号冒险团",
        after: "兰紫冒险团",
      });
    }
    throw new Error(`unexpected request: ${init.method} ${path}`);
  };

  await api.openAccountPanel("empty-acc");
  assert.equal(api.meta.accountPanel.loading, false);
  assert.equal(api.meta.accountPanel.currency.balance, "2000");

  const toasts = [];
  api.onToast = (msg) => toasts.push(msg);

  await api.prepare(
    {
      kind: "account-currency",
      accountId: "empty-acc",
      mode: "add",
      amountText: "5000",
      standalone: true,
    },
    emptySelection,
  );
  assert.match(api.meta.pending.summary, /\+5,000/);
  await api.confirm();
  assert.equal(api.meta.pending.completed, true);
  assert.equal(api.meta.pending.hidden, true);
  assert.equal(api.meta.accountPanel.currency.balance, "7000");
  assert.equal(api.meta.accountPanel.currency.revision, "rev-2");
  assert.equal(
    api.state.accounts.find((a) => a.id === "empty-acc").rawPoints,
    "7000",
  );
  assert.equal(toasts.length, 1);
  assert.equal(api.operationReceipts().length, 1);

  await api.prepare(
    {
      kind: "premium-grant",
      accountId: "empty-acc",
      days: 30,
      standalone: true,
    },
    emptySelection,
  );
  await api.confirm();
  assert.equal(api.meta.accountPanel.premium.active, true);

  await api.prepare(
    {
      kind: "rename-adventure",
      accountId: "empty-acc",
      expectedName: "空号冒险团",
      newName: "兰紫冒险团",
      standalone: true,
    },
    emptySelection,
  );
  await api.confirm();
  assert.equal(api.meta.accountPanel.adventureName.name, "兰紫冒险团");
  assert.equal(
    api.state.accounts.find((a) => a.id === "empty-acc").adventureName,
    "兰紫冒险团",
  );
});

test("select preserves inventory and GM sub-tabs, records MRU recentCharacters, restores on F5 boot, and clears on logout", async () => {
  storage.clear();
  const makeFetcher = () => async (path) => {
    if (path === "/api/v1/capabilities")
      return ok({ features: {}, csrfToken: "csrf-1" });
    if (path === "/api/auth/logout")
      return new Response(JSON.stringify({ success: true }), { status: 200 });
    if (path === "/api/v1/characters/10")
      return ok({
        character: {
          characterId: 10,
          accountId: "acc-1",
          name: "红眼大号",
          job: 0,
          growType: 2,
          level: 90,
        },
        online: true,
      });
    if (path === "/api/v1/characters/11")
      return ok({
        character: {
          characterId: 11,
          accountId: "acc-1",
          name: "剑魂小号",
          job: 0,
          growType: 1,
          level: 86,
        },
        online: false,
      });
    if (path === "/api/v1/accounts/acc-1")
      return ok({ accountId: "acc-1", name: "player_one", disabled: false });
    if (path === "/api/v1/presence") return ok({ characterIds: ["10"] });
    return ok({});
  };

  const api = new ConsoleAPI(() => {});
  api.http.fetcher = makeFetcher();
  api.setInventoryTab("equipped");
  api.setGmTab("quotas");

  await api.select("10");
  assert.equal(api.meta.inventory.tab, "equipped");
  assert.equal(api.meta.gmTab, "quotas");

  await api.select("11");
  assert.equal(api.meta.inventory.tab, "equipped");
  assert.equal(api.meta.gmTab, "quotas");
  assert.deepEqual(
    api.meta.recentCharacters.map((r) => r.id),
    ["11", "10"],
  );

  // Simulate F5 browser refresh
  const reloaded = new ConsoleAPI(() => {});
  reloaded.http.fetcher = makeFetcher();
  await reloaded.boot();
  assert.equal(reloaded.current, "11");
  assert.deepEqual(
    reloaded.meta.recentCharacters.map((r) => r.name),
    ["剑魂小号", "红眼大号"],
  );

  await reloaded.logout();
  assert.equal(reloaded.current, "");
  assert.equal(reloaded.meta.recentCharacters.length, 0);
  assert.equal(storage.has("purplemagic-character-context-v1"), false);
});

test("quickSearch queries characters, accounts and numeric QQ bindings in parallel and expands matched accounts", async () => {
  storage.clear();
  const api = new ConsoleAPI(() => {});
  api.meta.features = { "qqBinding.read": true };
  api.http.fetcher = async (path) => {
    if (path.startsWith("/api/v1/characters?")) return ok([]);
    if (path.startsWith("/api/v1/accounts?"))
      return ok([
        {
          accountId: "empty-2",
          name: "88812345_empty",
          disabled: false,
          characterCount: 0,
        },
      ]);
    if (path === "/api/v1/presence") return ok({ characterIds: ["201"] });
    if (path.startsWith("/api/v1/anti-cheat/qq/bindings?"))
      return ok({
        rows: [{ accountId: "qq-acc", account: "qq_owner", qq: "88812345" }],
        hasMore: false,
        total: 1,
      });
    if (path === "/api/v1/accounts/qq-acc/characters")
      return ok([
        {
          characterId: 201,
          accountId: "qq-acc",
          name: "元素师",
          job: 3,
          growType: 1,
          level: 90,
        },
      ]);
    throw new Error(`unexpected path: ${path}`);
  };

  const res = await api.quickSearch("88812345");
  assert.equal(res.qqMatches.length, 1);
  assert.equal(res.qqMatches[0].qq, "88812345");
  assert.equal(res.qqMatches[0].accountId, "qq-acc");
  assert.equal(res.accountRoles["qq-acc"].length, 1);
  assert.equal(res.accountRoles["qq-acc"][0].name, "元素师");
  assert.equal(res.accountRoles["qq-acc"][0].online, true);
  assert.ok(res.accounts.some((a) => a.id === "empty-2"));
});

test("character_online 409 enables carbonMode in place and retries preview without losing operation draft", async () => {
  storage.clear();
  const api = new ConsoleAPI(() => {});
  api.current = "10";
  api.meta.authenticated = true;
  api.meta.features = {
    "system.settings": true,
    "gold.write": true,
  };
  api.meta.systemSettings = { carbonMode: false };
  api.meta.resources.gold = {
    characterId: "10",
    balance: "1000",
    revision: "g-rev-1",
  };
  api.state.characters = [
    { id: "10", accountId: "acc-1", name: "在线狂战", gold: 1000, online: true },
  ];
  api.state.accounts = [{ id: "acc-1", name: "acc-1", points: 0, tokens: 0, status: "正常" }];

  let carbon = false;
  let previewCalls = 0;
  api.http.fetcher = async (path, init = {}) => {
    if (path === "/api/v1/settings/system" && init.method === "POST") {
      carbon = JSON.parse(init.body).carbonMode;
      return ok({ carbonMode: carbon });
    }
    if (path === "/api/v1/characters/10/gold-operations/preview") {
      previewCalls++;
      if (!carbon)
        return fail("character_online", "角色当前在线，请先开启碳铀模式", 409);
      return ok({
        receipt: {
          kind: "gold-operation",
          characterId: "10",
          before: "1000",
          after: "50000",
        },
      });
    }
    throw new Error(`unexpected path: ${path}`);
  };

  await api.guard(() =>
    api.prepare({ kind: "character-gold", characterId: "10", gold: 50000 }, emptySelection),
  );
  assert.equal(api.meta.errorCode, "character_online");
  assert.equal(api.canEnableCarbonAndRetry(), true);

  await api.enableCarbonAndRetry();
  assert.equal(api.meta.systemSettings.carbonMode, true);
  assert.equal(previewCalls, 2);
  assert.ok(api.meta.pending);
  assert.equal(api.meta.pending.confirmed, false);
  assert.match(api.meta.pending.summary, /50,000/);
});

test("tiered confirmation runs local drafts directly and keeps async task modal open while auto-closing sync writes", async () => {
  const draftState = {
    news: [{ id: "d1", title: "草稿", published: false }],
  };
  const pubState = {
    news: [{ id: "p1", title: "已发布公告", published: true }],
  };
  assert.equal(
    isDirectCommand({ kind: "news-move", id: "d1", direction: "up" }, draftState),
    true,
  );
  assert.equal(
    isDirectCommand(
      {
        kind: "news",
        id: "d1",
        title: "草稿",
        category: "更新",
        date: "2026-11-16",
        published: false,
        body: "正文",
      },
      draftState,
    ),
    true,
  );
  assert.equal(
    isDirectCommand(
      {
        kind: "news",
        id: "p1",
        title: "发布",
        category: "更新",
        date: "2026-11-16",
        published: true,
        body: "正文",
      },
      pubState,
    ),
    false,
  );

  // Sync single-target write auto-closes modal (hidden = true) and emits toast while recording receipt
  storage.clear();
  const syncApi = new ConsoleAPI(() => {});
  syncApi.character = async () => {};
  syncApi.enter = async () => {};
  syncApi.meta.pending = {
    id: "gold-op-1",
    title: "确认修改金币",
    target: "红眼大号",
    summary: "金币 → 50,000",
    confirmed: false,
    steps: [
      {
        id: "gold-op-1-0",
        label: "金币 → 50,000",
        path: "/api/v1/characters/10/gold-operations",
        body: { amount: "50000" },
      },
    ],
  };
  syncApi.http.fetcher = async () =>
    ok({ receipt: { kind: "gold-operation", after: "50000" } });
  const toasts = [];
  syncApi.onToast = (msg) => toasts.push(msg);
  await syncApi.confirm();
  assert.equal(syncApi.meta.pending.completed, true);
  assert.equal(syncApi.meta.pending.hidden, true);
  assert.equal(toasts.length, 1);
  assert.equal(syncApi.operationReceipts().length, 1);

  // Async batch mail task stays open (!p.hidden) so operator sees task progress
  storage.clear();
  const api = new ConsoleAPI(() => {});
  api.character = async () => {};
  api.enter = async () => {};
  api.meta.pending = {
    id: "mail-task-1",
    title: "发送邮件",
    target: "2 个角色",
    summary: "发放测试奖励",
    confirmed: false,
    steps: [
      {
        id: "mail-task-1-0",
        kind: "task",
        label: "批量发信",
        path: "/api/v1/tasks/t1/run",
        body: { revision: 1 },
      },
    ],
  };
  api.http.fetcher = async () =>
    ok({ id: "t1", status: "completed", succeeded: 2, failed: 0 });
  let toastCount = 0;
  api.onToast = () => toastCount++;
  await api.confirm();
  assert.equal(api.meta.pending.completed, true);
  assert.equal(Boolean(api.meta.pending.hidden), false);
  assert.equal(toastCount, 0);
});

test("mail attachment equipment filtering and preview payload forward reinforce and forgingLevel accurately", async () => {
  assert.equal(canEnhanceAttachment({ subtype: "weapon" }), true);
  assert.equal(canEnhanceAttachment({ subtype: "coat" }), true);
  assert.equal(canEnhanceAttachment({ subtype: "support weapon" }), true);
  assert.equal(canEnhanceAttachment({ subtype: "title" }), false);
  assert.equal(canEnhanceAttachment({ subtype: "avatar" }), false);
  assert.equal(canForgeAttachment({ subtype: "weapon" }), true);
  assert.equal(canForgeAttachment({ subtype: "coat" }), false);

  const merged = appendAttachments(
    [],
    [
      {
        id: "1001",
        name: "荒古遗尘巨剑",
        category: "武器",
        remote: { kind: "equipment", subtype: "weapon" },
      },
      {
        id: "2001",
        name: "魔界抗疲劳秘药",
        category: "消耗品",
        remote: { kind: "stackable", subtype: "consumable" },
      },
    ],
    5,
  );
  assert.equal(merged[0].count, 1);
  assert.equal(merged[0].forge, 0);
  assert.equal(merged[1].count, 5);

  storage.clear();
  const api = new ConsoleAPI(() => {});
  api.state.characters = [
    { id: "10", accountId: "acc-1", name: "剑宗", job: "女鬼剑士", level: 90 },
  ];
  let previewBody = null;
  api.http.fetcher = async (path, init = {}) => {
    if (path === "/api/v1/items/1001/grant-options") {
      return ok({
        itemId: "1001",
        name: "荒古遗尘巨剑",
        kind: "equipment",
        tag: "weapon",
        pvfChecksum: "f".repeat(64),
      });
    }
    if (path === "/api/v1/mail/preview") {
      previewBody = JSON.parse(init.body);
      return ok({ id: "task-99", revision: 1 });
    }
    if (path === "/api/v1/tasks/task-99/confirmation") {
      return ok({
        recipients: [{ characterId: "10", name: "剑宗", accountId: "acc-1" }],
      });
    }
    throw new Error(`unexpected path: ${path}`);
  };

  await api.prepare(
    {
      kind: "mail",
      title: "武器发放",
      body: "请查收",
      gold: 0,
      items: [
        {
          id: "1001",
          name: "荒古遗尘巨剑",
          count: 1,
          enhance: 13,
          forge: 8,
          subtype: "weapon",
        },
      ],
    },
    { accounts: [], characters: ["10"] },
  );
  assert.equal(previewBody.options.reinforce, 13);
  assert.equal(previewBody.options.forgingLevel, 8);
  assert.match(api.meta.pending.steps[0].label, /\+13/);
  assert.match(api.meta.pending.steps[0].label, /锻造 8/);
  api.cancel();

  await assert.rejects(
    api.prepare(
      {
        kind: "mail",
        title: "超限测试",
        body: "正文",
        gold: 0,
        items: [
          {
            id: "1001",
            name: "荒古遗尘巨剑",
            count: 1,
            enhance: 32,
            forge: 0,
            subtype: "weapon",
          },
        ],
      },
      { accounts: [], characters: ["10"] },
    ),
    /0–31/,
  );
});

test("SSR renders Identity quick actions, split Cera calculator, 0-role AccountManagerModal, Mail forge inputs and AntiCheat closed-loop actions", () => {
  resetNavigationMemoryForTests();
  const api = new ConsoleAPI(() => {});
  api.meta.authenticated = true;
  api.meta.gmTab = "currency";
  api.meta.features = {
    "currency.read": true,
    "currency.write": true,
    "gold.read": true,
    "gold.write": true,
    "premium.read": true,
    "premium.write": true,
    "adventure.read": true,
    "adventure.rename": true,
    "accounts.password-reset": true,
    "moderation.write": true,
  };
  api.state.accounts = [
    {
      id: "acc-1",
      name: "orchid_admin",
      points: 12800,
      rawPoints: "12800",
      tokens: 0,
      status: "限制登录",
      adventureName: "兰紫团",
    },
  ];
  const role = {
    id: "10",
    accountId: "acc-1",
    name: "月蚀魔道",
    job: "魔法师",
    level: 90,
    online: true,
    gold: 500000,
    sp: 100,
    tp: 20,
  };
  api.state.characters = [role];
  api.meta.resources.currency = {
    accountId: "acc-1",
    balance: "12800",
    revision: "r1",
  };
  api.meta.resources.gold = {
    characterId: "10",
    balance: "500000",
    revision: "g1",
  };

  const identityHtml = renderToStaticMarkup(
    createElement(
      uiMod.exports.LiveContext.Provider,
      { value: api },
      createElement(uiMod.exports.Identity, {
        state: { ...api.state, api: api.meta },
        character: role,
        onChoose() {},
        onSelectCharacter() {},
        onManageAccount() {},
        onQuickMail() {},
      }),
    ),
  );
  assert.match(identityHtml, /同账号角色/);
  assert.match(identityHtml, /发邮件/);
  assert.match(identityHtml, /限制登录/);

  const gmHtml = renderToStaticMarkup(
    createElement(
      uiMod.exports.LiveContext.Provider,
      { value: api },
      createElement(uiMod.exports.GmPage, {
        state: { ...api.state, api: api.meta },
        character: role,
        selection: emptySelection,
        setSelection() {},
        selectCharacter() {},
        go() {},
        request() {},
        notify() {},
      }),
    ),
  );
  assert.match(gmHtml, /角色金币/);
  assert.match(gmHtml, /账号点券/);
  assert.match(gmHtml, /增加 \(\+\)/);
  assert.match(gmHtml, /扣除 \(-\)/);
  assert.match(gmHtml, /设为 \(=\)/);
  assert.match(gmHtml, /\+10,000/);

  api.meta.accountPanel = {
    accountId: "empty-acc",
    loading: false,
    error: "",
    account: {
      id: "empty-acc",
      name: "空号玩家",
      points: 600,
      rawPoints: "600",
      tokens: 0,
      status: "正常",
      adventureName: "新星团",
    },
    currency: { accountId: "empty-acc", balance: "600", revision: "c1" },
    premium: { accountId: "empty-acc", active: false, expireAt: 0 },
    adventureName: { accountId: "empty-acc", name: "新星团" },
  };
  const modalHtml = renderToStaticMarkup(
    createElement(
      uiMod.exports.LiveContext.Provider,
      { value: api },
      createElement(uiMod.exports.AccountManagerModal, {
        accountId: "empty-acc",
        state: { ...api.state, api: api.meta },
        request() {},
        onClose() {},
      }),
    ),
  );
  assert.match(modalHtml, /暂无角色 · 空账号仍可直接管理点券、黑钻、冒险团与密码/);
  assert.match(modalHtml, /黑钻续期/);
  assert.match(modalHtml, /冒险团改名/);
  assert.match(modalHtml, /登录密码重置/);

  // Navigation memory helpers
  writeDirectoryMemory(api, { mode: "characters", online: "online" });
  assert.equal(readDirectoryMemory(api).online, "online");
  assert.equal(readDirectoryMemory(api).initialized, true);

  writeAntiCheatMemory(api, {
    tab: "clears",
    filter: "fast",
    cachedModeration: {
      tab: "clears",
      key: JSON.stringify({
        which: "clears",
        offset: 100,
        sort: { sortBy: "count", sortOrder: "desc" },
        q: "",
        accountFocus: "",
        hideBanned: true,
        from: "2026-11-15T00:00",
        to: "2026-11-16T00:00",
        filter: "fast",
        seconds: 30,
        hourly: 0,
        hideRange: false,
        modRefreshRev: 0,
        banRevision: 0,
      }),
      offset: 100,
      page: {
        rows: [
          {
            characterId: "10",
            characterName: "月蚀魔道",
            accountId: "acc-1",
            dungeonId: 101,
            dungeonName: "时空裂缝",
            difficulty: 3,
            count: 25,
            fastestMs: 8500,
            averageMs: 11000,
            fatigueSpent: 150,
            averageFatigue: 6,
            fatigueSamples: 25,
            peakHourly: 25,
            lastClearAt: "2026-11-16T01:00:00Z",
          },
        ],
        hasMore: true,
      },
    },
    from: "2026-11-15T00:00",
    to: "2026-11-16T00:00",
    offset: 100,
  });
  assert.equal(readAntiCheatMemory(api).offset, 100);
  const acHtml = renderToStaticMarkup(
    createElement(
      uiMod.exports.LiveContext.Provider,
      { value: api },
      createElement(uiMod.exports.AntiCheatPage, {
        state: { ...api.state, api: api.meta },
        character: role,
        selection: emptySelection,
        setSelection() {},
        selectCharacter() {},
        go() {},
        request() {},
        notify() {},
      }),
    ),
  );
  assert.match(acHtml, /第 3 页 · 当前 1 条/);
  assert.match(acHtml, /查穿戴/);
  assert.match(acHtml, /封禁账号/);
});
