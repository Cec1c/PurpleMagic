import test from "node:test";
import assert from "node:assert/strict";
import { build } from "esbuild";

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

const httpBundle = await build({
  entryPoints: ["src/api/http.ts"],
  bundle: true,
  platform: "node",
  format: "esm",
  write: false,
});
const { shouldRefreshOnVisibility, VISIBILITY_REFRESH_COOLDOWN_MS } =
  await import(
    "data:text/javascript;base64," +
      Buffer.from(httpBundle.outputFiles[0].text).toString("base64")
  );

const storage = new Map();
globalThis.sessionStorage = {
  getItem: (k) => storage.get(k) ?? null,
  setItem: (k, v) => storage.set(k, v),
  removeItem: (k) => storage.delete(k),
};

const ok = (data, page) =>
  new Response(JSON.stringify(page ? { data, page } : { data }), {
    status: 200,
  });

test("character directory and select populate account cache from accountName/accountBanned with zero /accounts/{id} requests", async () => {
  storage.clear();
  const api = new ConsoleAPI(() => {});
  const paths = [];
  api.http.fetcher = async (path) => {
    paths.push(path);
    if (path.startsWith("/api/v1/characters?")) {
      return ok(
        Array.from({ length: 20 }, (_, i) => ({
          characterId: String(100 + i),
          accountId: i === 0 ? "same_as_login" : `acc-${i}`,
          accountName: i === 0 ? "same_as_login" : `player_${i}`,
          accountBanned: i % 5 === 0,
          name: `角色${i}`,
          job: "0",
          growType: 1,
          level: 90,
        })),
      );
    }
    if (path === "/api/v1/characters/299") {
      return ok({
        character: {
          characterId: "299",
          accountId: "acc-direct",
          accountName: "direct_owner",
          accountBanned: true,
          name: "直选角色",
          job: "2",
          growType: 2,
          level: 90,
        },
        online: false,
      });
    }
    if (path === "/api/v1/presence") {
      return ok({ characterIds: ["100"] });
    }
    throw new Error(`unexpected N+1 request: ${path}`);
  };

  await api.directory("characters", "");
  assert.equal(api.state.characters.length, 20);
  assert.equal(api.state.accounts.length, 20);
  assert.equal(
    paths.filter((p) => p.startsWith("/api/v1/accounts/")).length,
    0,
  );
  const sameAcc = api.state.accounts.find((a) => a.id === "same_as_login");
  assert.equal(sameAcc.name, "same_as_login");
  assert.equal(sameAcc.status, "限制登录");
  const normalAcc = api.state.accounts.find((a) => a.id === "acc-1");
  assert.equal(normalAcc.name, "player_1");
  assert.equal(normalAcc.status, "正常");

  paths.length = 0;
  await api.select("299");
  assert.equal(
    paths.filter((p) => p.startsWith("/api/v1/accounts/")).length,
    0,
  );
  const directAcc = api.state.accounts.find((a) => a.id === "acc-direct");
  assert.equal(directAcc.name, "direct_owner");
  assert.equal(directAcc.status, "限制登录");
  assert.equal(api.meta.recentCharacters[0].accountName, "direct_owner");
});

test("inventory('bag') issues a single inventory-batch request for all 6 containers and falls back per-container on LoadMore", async () => {
  storage.clear();
  const api = new ConsoleAPI(() => {});
  api.current = "10";
  api.meta.features = { "inventory.read": true, "inventory.batch": true };
  api.state.characters = [
    { id: "10", accountId: "acc-1", name: "测试剑魂", job: "鬼剑士", level: 90 },
  ];

  const paths = [];
  api.http.fetcher = async (path) => {
    paths.push(path);
    if (path.startsWith("/api/v1/characters/10/inventory-batch?")) {
      return ok({
        characterId: "10",
        accountId: "acc-1",
        pvfChecksum: "a".repeat(64),
        containers: {
          equipment: {
            container: "equipment",
            items: [
              {
                slot: 9,
                container: "equipment",
                itemId: "1001",
                name: "荒古遗尘光剑",
                kind: "equipment",
                tag: "weapon",
                rarity: 4,
                count: "1",
                bound: true,
                revision: "sum-eq-9",
                revisionScope: "summary",
                attributes: { reinforce: 12 },
              },
            ],
            page: { limit: 100 },
          },
          consumables: {
            container: "consumables",
            items: [
              {
                slot: 57,
                container: "consumables",
                itemId: "2001",
                name: "斗神之吼秘药",
                kind: "stackable",
                rarity: 2,
                count: "50",
                bound: false,
                revision: "sum-con-57",
                revisionScope: "summary",
              },
            ],
            page: { limit: 100 },
          },
          materials: {
            container: "materials",
            items: [
              {
                slot: 105,
                container: "materials",
                itemId: "3001",
                name: "深渊派对邀请函",
                kind: "stackable",
                rarity: 3,
                count: "1500",
                bound: true,
                revision: "sum-mat-105",
                revisionScope: "summary",
              },
            ],
            page: { limit: 100, nextCursor: "cursor-mat-page2" },
          },
          "quest-items": {
            container: "quest-items",
            items: [
              {
                slot: 153,
                container: "quest-items",
                itemId: "4001",
                name: "任务道具",
                kind: "stackable",
                rarity: 0,
                count: "3",
                bound: true,
                revision: "sum-q-153",
                revisionScope: "summary",
              },
            ],
            page: { limit: 100 },
          },
          profession: {
            container: "profession",
            items: [
              {
                slot: 201,
                container: "profession",
                itemId: "5001",
                name: "金色小晶块",
                kind: "stackable",
                rarity: 1,
                count: "200",
                bound: false,
                revision: "sum-prof-201",
                revisionScope: "summary",
              },
            ],
            page: { limit: 100 },
          },
          quickbar: {
            container: "quickbar",
            items: [
              {
                slot: 3,
                container: "quickbar",
                itemId: "6001",
                name: "雷米迪亚的祝福",
                kind: "stackable",
                rarity: 2,
                count: "99",
                bound: true,
                revision: "sum-qb-3",
                revisionScope: "summary",
              },
            ],
            page: { limit: 100 },
          },
        },
      });
    }
    if (
      path ===
      "/api/v1/characters/10/inventory?container=materials&limit=100&cursor=cursor-mat-page2"
    ) {
      return ok(
        [
          {
            slot: 106,
            container: "materials",
            itemId: "3002",
            name: "时空石",
            kind: "stackable",
            rarity: 2,
            count: "80",
            bound: true,
            revision: "sum-mat-106",
            revisionScope: "summary",
          },
        ],
        { limit: 100 },
      );
    }
    // Single-container endpoint mixes data object shape:
    throw new Error(`unexpected request: ${path}`);
  };

  // Correct single-container shape for LoadMore page 2:
  const baseFetcher = api.http.fetcher;
  api.http.fetcher = async (path) => {
    if (
      path ===
      "/api/v1/characters/10/inventory?container=materials&limit=100&cursor=cursor-mat-page2"
    ) {
      paths.push(path);
      return ok(
        {
          characterId: "10",
          accountId: "acc-1",
          container: "materials",
          pvfChecksum: "a".repeat(64),
          items: [
            {
              slot: 106,
              container: "materials",
              itemId: "3002",
              name: "时空石",
              kind: "stackable",
              rarity: 2,
              count: "80",
              bound: true,
              revision: "sum-mat-106",
              revisionScope: "summary",
            },
          ],
        },
        { limit: 100 },
      );
    }
    return baseFetcher(path);
  };

  await api.inventory("bag");
  assert.equal(paths.length, 1);
  assert.match(paths[0], /^\/api\/v1\/characters\/10\/inventory-batch\?/);
  assert.equal(api.state.items.length, 6);
  assert.deepEqual(api.meta.inventory.next, { materials: "cursor-mat-page2" });
  assert.equal(
    api.state.items.find((i) => i.id === "1001").enhance,
    12,
  );

  // LoadMore only fetches the paginated container ('materials')
  paths.length = 0;
  await api.inventory("bag", true);
  assert.deepEqual(paths, [
    "/api/v1/characters/10/inventory?container=materials&limit=100&cursor=cursor-mat-page2",
  ]);
  assert.equal(api.state.items.length, 7);
  assert.deepEqual(api.meta.inventory.next, {});
});

test("visibilitychange refresh cooldown suppresses invalidation within 30s of recent read or refresh", () => {
  assert.equal(VISIBILITY_REFRESH_COOLDOWN_MS >= 30_000, true);
  // Just fetched data 4s ago despite opening tab 10 minutes ago -> skip refresh
  assert.equal(shouldRefreshOnVisibility(600_000, 0, 596_000), false);
  // Refreshed on visibility 10s ago -> skip refresh
  assert.equal(shouldRefreshOnVisibility(610_000, 600_000, 500_000), false);
  // Both last read and last visibility refresh >= 30s ago -> allow refresh
  assert.equal(shouldRefreshOnVisibility(631_000, 600_000, 596_000), true);
});
