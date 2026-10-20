import test from "node:test";
import assert from "node:assert/strict";
import { initialState } from "../src/fixtures.ts";
import { applyCommand, resolveRecipients } from "../src/domain.ts";
const date = "2026-10-19T03:00:00.000Z";

test('cera benefits credit each account once across siblings and pause/resume',()=>{
 const s=initialState();s.benefits=[];
 const cmd={kind:'benefit-create',name:'点券',gold:0,rewardKind:'cera',cera:500,scope:'future',enabled:true};
 let next=applyCommand(s,cmd,date,'cera-rule');
 for(const a of s.accounts) assert.equal(next.accounts.find(v=>v.id===a.id).points,a.points+500);
 assert.equal(next.benefits[0].recipients.length,s.accounts.length);
 next=applyCommand(next,{kind:'benefit-toggle',id:'cera-rule'},date,'pause');
 next=applyCommand(next,{kind:'benefit-toggle',id:'cera-rule'},date,'resume');
 for(const a of s.accounts) assert.equal(next.accounts.find(v=>v.id===a.id).points,a.points+500);
});

test('item benefit keeps selected ID and quantity in delivered attachments',()=>{
 const s=initialState();s.benefits=[];s.mails=[];
 const next=applyCommand(s,{kind:'benefit-create',name:'补给',gold:0,rewardKind:'item',item:{id:'7002',name:'药水',count:12,enhance:0},scope:'existing',enabled:true},date,'item-rule');
 assert.equal(next.mails.length,s.characters.length);
 for(const m of next.mails) assert.deepEqual(m.items,[{id:'7002',name:'药水',count:12,enhance:0}]);
});
test("account selection includes every current character and deduplicates explicit roles", () => {
  assert.deepEqual(
    resolveRecipients(initialState(), {
      accounts: ["a1", "a2"],
      characters: ["1", "3", "5", "missing"],
    }),
    ["1", "2", "3", "4", "5"],
  );
});
test("role edit is allowed when a sibling is online; target online blocks it", () => {
  const s = initialState();
  const item = s.items[0];
  const cmd = {
    kind: "item",
    key: item.key,
    version: 1,
    patch: { count: 1, enhance: 13, amplify: 0, forge: 8, bound: true },
  };
  const next = applyCommand(s, cmd, date, "edit");
  assert.equal(next.items[0].enhance, 13);
  assert.equal(s.items[0].enhance, 12);
  const onlineItem = s.items.find((i) => i.characterId === "2");
  assert.throws(
    () => applyCommand(s, { ...cmd, key: onlineItem.key }, date, "edit2"),
    /流心 当前在线/,
  );
});
test("account cargo edit checks all sibling sessions", () => {
  const s = initialState(),
    item = s.items.find((i) => i.tab === "account" && i.accountId === "a1");
  assert.throws(
    () =>
      applyCommand(
        s,
        {
          kind: "item",
          key: item.key,
          version: 1,
          patch: { count: 500, enhance: 0, amplify: 0, forge: 0, bound: false },
        },
        date,
        "cargo",
      ),
    /全部离线/,
  );
});
test("stale item revision and invalid values leave all state unchanged", () => {
  const s = initialState(),
    snapshot = structuredClone(s),
    item = s.items[0];
  const cmd = {
    kind: "item",
    key: item.key,
    version: 0,
    patch: { count: 1, enhance: 12, amplify: 0, forge: 0, bound: true },
  };
  assert.throws(() => applyCommand(s, cmd, date, "stale"), /已变更/);
  assert.throws(
    () =>
      applyCommand(
        s,
        { ...cmd, version: 1, patch: { ...cmd.patch, count: -1 } },
        date,
        "bad",
      ),
    /整数/,
  );
  assert.deepEqual(s, snapshot);
});
test("batch mail is atomic and idempotent, including online recipients", () => {
  const s = initialState();
  const cmd = {
    kind: "mail",
    recipients: ["1", "2", "1"],
    title: "礼包",
    body: "补给",
    gold: 200,
    items: [{ id: "3037", name: "无色小晶块", count: 100, enhance: 0 }],
  };
  const next = applyCommand(s, cmd, date, "batch");
  assert.equal(next.mails.length, s.mails.length + 2);
  assert.equal(next.receipts.length, 1);
  assert.deepEqual(applyCommand(next, cmd, date, "batch"), next);
  assert.throws(
    () =>
      applyCommand(s, { ...cmd, recipients: ["1", "missing"] }, date, "bad"),
    /不存在/,
  );
  assert.equal(s.mails.length, 2);
});
test("new enabled benefit delivers immediately and does not resend after pause/resume", () => {
  const s = initialState();
  const created = applyCommand(
    s,
    {
      kind: "benefit-create",
      name: "福利",
      gold: 2000,
      enabled: true,
      scope: "future",
    },
    date,
    "rule",
  );
  assert.equal(created.benefits[0].recipients.length, 6);
  assert.equal(created.mails.length, 8);
  const paused = applyCommand(
    created,
    { kind: "benefit-toggle", id: "rule" },
    date,
    "pause",
  );
  const resumed = applyCommand(
    paused,
    { kind: "benefit-toggle", id: "rule" },
    date,
    "resume",
  );
  assert.equal(resumed.mails.length, 8);
});
test("existing-only benefit keeps fixed targets and future rule admits later roles", () => {
  let s = applyCommand(
    initialState(),
    {
      kind: "benefit-create",
      name: "现有",
      gold: 10,
      enabled: false,
      scope: "existing",
    },
    date,
    "fixed",
  );
  s.characters.push({ ...s.characters[0], id: "99", name: "新角色" });
  s = applyCommand(s, { kind: "benefit-toggle", id: "fixed" }, date, "on");
  assert.equal(s.benefits[0].recipients.length, 6);
  assert.ok(!s.benefits[0].recipients.includes("99"));
  s = applyCommand(s, { kind: "benefit-toggle", id: "b1" }, date, "future");
  assert.equal(s.benefits.find((b) => b.id === "b1").recipients.length, 7);
  assert.equal(new Set(s.mails.map((m) => m.id)).size, s.mails.length);
});
test("quest changes target only one character; invalid source URLs are rejected", () => {
  const s = initialState(),
    next = applyCommand(
      s,
      { kind: "quest", characterId: "1", ids: ["122"], status: "completed" },
      date,
      "quest",
    );
  assert.equal(next.questStates["1"]["122"], "completed");
  assert.equal(next.questStates["2"]["122"], "active");
  assert.throws(
    () =>
      applyCommand(
        s,
        {
          kind: "sources",
          download: "javascript:alert(1)",
          gameHost: "example.com",
          gamePort: 7000,
        },
        date,
        "source",
      ),
    /HTTP/,
  );
});
