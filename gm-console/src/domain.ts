export type Section =
  "query" | "inventory" | "mail" | "quests" | "gm" | "accounts" | "settings";
export type InventoryTab =
  | "bag"
  | "equipped"
  | "petbag"
  | "warehouse"
  | "account"
  | "pets"
  | "avatars"
  | "emblems";
export type Character = {
  id: string;
  accountId: string;
  name: string;
  job: string;
  level: number;
  online: boolean | null;
  jobCode?: string;
  growType?: number;
  gold: number;
  sp: number;
  tp: number;
};
export type Account = {
  id: string;
  name: string;
  points: number;
  tokens: number;
  status: "正常" | "限制登录";
  characterCount?: number;
  rawPoints?: string;
};
export type Item = {
  key: string;
  characterId: string;
  accountId?: string;
  tab: InventoryTab;
  slot: number;
  id: string;
  name: string;
  category: string;
  rarity: "普通" | "高级" | "稀有" | "神器" | "史诗" | "传说";
  count: number;
  level: number;
  enhance: number;
  amplify: number;
  forge: number;
  bound: boolean;
  version: number;
  remote?: {
    container: string;
    revision?: string;
    pvf?: string;
    options?: Record<string, any>;
    attributes?: Record<string, any>;
    relatedRevision?: string;
    expiresAt?: string | null;
    known: boolean;
    kind?: string;
    subtype?: string;
  };
};
export type Quest = {
  id: string;
  name: string;
  chapter: string;
  kind: string;
  level: number;
};
export type Attachment = {
  id: string;
  name: string;
  count: number;
  enhance: number;
};
export type Mail = {
  id: string;
  characterId: string;
  title: string;
  body: string;
  sender: string;
  gold: number;
  items: Attachment[];
  createdAt: string;
  claimed: boolean;
};
export type Receipt = {
  id: string;
  kind: string;
  target: string;
  summary: string;
  at: string;
  details?: Record<string, any>;
};
export type Benefit = {
  cera?: number;
  item?: Attachment;
  grantScope?: "character" | "account";
  id: string;
  name: string;
  gold: number;
  enabled: boolean;
  scope: "existing" | "future";
  recipients: string[];
  eligibleIds?: string[];
  revision?: number;
  sent?: number;
  scopeLabel?: string;
  rewardSummary?: string;
};
export type News = {
  id: string;
  title: string;
  body: string;
  published: boolean;
  originId?: string;
  sourceRevision?: string;
};
export type State = {
  api?: import("./api/model").LiveData;
  schema: 1;
  accounts: Account[];
  characters: Character[];
  items: Item[];
  mails: Mail[];
  questStates: Record<string, Record<string, string>>;
  benefits: Benefit[];
  receipts: Receipt[];
  news: News[];
  sources: { download: string; gameHost: string; gamePort: number };
};
export type Selection = { accounts: string[]; characters: string[] };
export type Command =
  | {
      kind: "item";
      key: string;
      version: number;
      patch: Pick<Item, "count" | "enhance" | "amplify" | "forge" | "bound">;
      expirationDays?: number;
    }
  | { kind: "delete-items"; keys: string[] }
  | { kind: "replace-item"; key: string; version: number; itemId: string }
  | { kind: "reset-skill-points"; characterId: string }
  | {
      kind: "mail";
      recipients: string[];
      title: string;
      body: string;
      gold: number;
      items: Attachment[];
    }
  | { kind: "delete-mail"; ids: string[] }
  | {
      kind: "quest";
      selectedTargets?: boolean;
      characterId: string;
      ids: string[];
      status: "completed" | "active";
    }
  | {
      kind: "growth";
      characterId: string;
      level: number;
      job: string;
      sp: number;
      tp: number;
    }
  | {
      kind: "currency";
      characterId: string;
      gold: number;
      points: number;
      pointsText?: string;
      tokens: number;
    }
  | {
      kind: "benefit-create";
      rewardKind?: "gold" | "cera" | "item";
      cera?: number;
      item?: Attachment;
      grantScope?: "character" | "account";
      name: string;
      gold: number;
      enabled: boolean;
      scope: "existing" | "future";
    }
  | { kind: "benefit-toggle"; id: string }
  | { kind: "task-control"; id: string; action: "resume" | "retry" | "pause" }
  | {
      kind: "news";
      id: string;
      title: string;
      body: string;
      published: boolean;
    }
  | {
      kind: "sources";
      download: string;
      gameHost: string;
      gamePort: number;
      source?: {
        id: string;
        name: string;
        endpoint: string;
        publicKey: string;
        certificateSha256: string;
        enabled: boolean;
      };
    };

export function resolveRecipients(
  state: State,
  selection: Selection,
): string[] {
  const accounts = new Set(selection.accounts),
    ids = new Set(selection.characters);
  return state.characters
    .filter((c) => accounts.has(c.accountId) || ids.has(c.id))
    .map((c) => c.id);
}
function integer(n: number, min = 0, max = 2147483647) {
  if (!Number.isSafeInteger(n) || n < min || n > max)
    throw new Error(
      `请输入 ${min.toLocaleString()}–${max.toLocaleString()} 之间的整数`,
    );
}
function offline(s: State, id: string) {
  const c = s.characters.find((c) => c.id === id);
  if (!c) throw new Error("角色不存在");
  if (c.online) throw new Error(`${c.name} 当前在线，请该角色退出后重试`);
  return c;
}
function requireCharacter(s: State, id: string) {
  const c = s.characters.find((c) => c.id === id);
  if (!c) throw new Error("角色不存在");
  return c;
}

export function benefitReward(command: Extract<Command, { kind: "benefit-create" }>) {
  if (!command.name.trim()) throw new Error("请填写规则名称");
  const kind = command.rewardKind || "gold";
  const grantScope = kind === "cera" ? "account" : command.grantScope || "character";
  const gold = kind === "gold" ? command.gold : 0;
  const cera = kind === "cera" ? command.cera || 0 : 0;
  const item = kind === "item" ? command.item : undefined;
  if (kind === "item") {
    if (!item || !/^[1-9]\d*$/.test(item.id)) throw new Error("请输入有效的物品 ID");
    integer(Number(item.id), 1);
    integer(item.count, 1);
  } else integer(kind === "cera" ? cera : gold, 1);
  const summary = item ? `${item.name || item.id}（${item.id}）× ${item.count.toLocaleString()}` :
    cera ? `点券 ${cera.toLocaleString()}` : `金币 ${gold.toLocaleString()}`;
  return { gold, cera, item, grantScope, summary };
}

export function applyCommand(
  input: State,
  command: Command,
  now = new Date().toISOString(),
  requestId: string = crypto.randomUUID(),
): State {
  if (input.receipts.some((r) => r.id === requestId)) return input;
  const s = structuredClone(input);
  let summary = "",
    target = "";
  const deliver = (
    recipients: string[],
    title: string,
    body: string,
    gold: number,
    items: Attachment[],
    batchId = requestId,
  ) => {
    integer(gold);
    if (!title.trim()) throw new Error("请填写邮件标题");
    if (recipients.length === 0) throw new Error("请先选择收件角色");
    for (const i of items) {
      integer(i.count, 1, 99999);
      integer(i.enhance, 0, 31);
      if (!i.id) throw new Error("请选择附件物品");
    }
    for (const id of [...new Set(recipients)]) {
      requireCharacter(s, id);
      s.mails.unshift({
        id: `${batchId}-${id}`,
        characterId: id,
        title,
        body,
        sender: "PurpleMagic",
        gold,
        items: structuredClone(items),
        createdAt: now,
        claimed: false,
      });
    }
  };
  switch (command.kind) {
    case "item": {
      const item = s.items.find((i) => i.key === command.key);
      if (!item) throw new Error("物品不存在");
      if (item.version !== command.version)
        throw new Error("物品已变更，请重新打开详情");
      if (item.tab === "account") {
        if (
          s.characters.some((c) => c.accountId === item.accountId && c.online)
        )
          throw new Error("账号仓库编辑需该账号角色全部离线");
      } else offline(s, item.characterId);
      integer(command.patch.count, 1, 99999);
      integer(command.patch.enhance, 0, 31);
      integer(command.patch.amplify, 0, 31);
      integer(command.patch.forge, 0, 8);
      Object.assign(item, command.patch, { version: item.version + 1 });
      target = item.name;
      summary = "已保存物品属性";
      break;
    }
    case "delete-items": {
      const items = s.items.filter((i) => command.keys.includes(i.key));
      if (!items.length) throw new Error("请选择物品");
      for (const i of items) {
        if (i.tab === "account") {
          if (s.characters.some((c) => c.accountId === i.accountId && c.online))
            throw new Error("账号仓库编辑需该账号角色全部离线");
        } else offline(s, i.characterId);
      }
      s.items = s.items.filter((i) => !command.keys.includes(i.key));
      target = `${items.length} 件物品`;
      summary = "已删除选中物品";
      break;
    }
    case "mail": {
      deliver(
        command.recipients,
        command.title,
        command.body,
        command.gold,
        command.items,
      );
      target = `${new Set(command.recipients).size} 个角色`;
      summary = `已发送「${command.title}」`;
      break;
    }
    case "delete-mail": {
      if (!command.ids.length) throw new Error("请选择邮件");
      s.mails = s.mails.filter((m) => !command.ids.includes(m.id));
      target = `${command.ids.length} 封邮件`;
      summary = "已删除选中邮件";
      break;
    }
    case "quest": {
      const c = offline(s, command.characterId);
      if (!command.ids.length) throw new Error("请选择任务");
      s.questStates[c.id] ??= {};
      for (const id of command.ids) s.questStates[c.id][id] = command.status;
      target = c.name;
      summary = `${command.status === "completed" ? "完成" : "重置"} ${command.ids.length} 个任务`;
      break;
    }
    case "growth": {
      const c = offline(s, command.characterId);
      integer(command.level, 1, 90);
      integer(command.sp, 0, 99999);
      integer(command.tp, 0, 999);
      if (!command.job.trim()) throw new Error("请选择职业");
      Object.assign(c, {
        level: command.level,
        job: command.job,
        sp: command.sp,
        tp: command.tp,
      });
      target = c.name;
      summary = "已保存角色成长";
      break;
    }
    case "currency": {
      const c = offline(s, command.characterId);
      const a = s.accounts.find((a) => a.id === c.accountId)!;
      integer(command.gold);
      integer(command.points);
      integer(command.tokens);
      c.gold = command.gold;
      a.points = command.points;
      a.tokens = command.tokens;
      target = c.name;
      summary = "已保存金币与账号点券";
      break;
    }
    case "benefit-create": {
      const reward = benefitReward(command);
      const b: Benefit = {
        id: requestId,
        name: command.name,
        gold: reward.gold,
        cera: reward.cera,
        item: reward.item,
        grantScope: reward.grantScope,
        scopeLabel: reward.grantScope === "account" ? "每账号一次" : "每角色一次",
        rewardSummary: reward.summary,
        enabled: command.enabled,
        scope: command.scope,
        recipients: [],
        eligibleIds: s.characters.map((c) => c.id),
      };
      s.benefits.unshift(b);
      target = b.name;
      summary = b.enabled ? "规则已启用，已自动发放" : "规则已保存";
      break;
    }
    case "benefit-toggle": {
      const b = s.benefits.find((b) => b.id === command.id);
      if (!b) throw new Error("规则不存在");
      b.enabled = !b.enabled;
      target = b.name;
      summary = b.enabled ? "规则已启用，已自动检查收件名单" : "规则已暂停";
      break;
    }
    case "news": {
      if (!command.title.trim() || !command.body.trim())
        throw new Error("请填写公告标题和正文");
      const n = s.news.find((n) => n.id === command.id);
      if (n)
        Object.assign(n, {
          title: command.title,
          body: command.body,
          published: command.published,
        });
      else
        s.news.unshift({
          id: requestId,
          title: command.title,
          body: command.body,
          published: command.published,
        });
      target = command.title;
      summary = command.published ? "公告已发布" : "公告草稿已保存";
      break;
    }
    case "sources": {
      let u: URL;
      try {
        u = new URL(command.download);
      } catch {
        throw new Error("请填写有效的下载源 URL");
      }
      if (!["https:", "http:"].includes(u.protocol))
        throw new Error("下载源必须使用 HTTP 或 HTTPS");
      if (!command.gameHost.trim() || /[\s/]/.test(command.gameHost))
        throw new Error("请填写有效的游戏地址");
      integer(command.gamePort, 1, 65535);
      s.sources = {
        download: u.href,
        gameHost: command.gameHost,
        gamePort: command.gamePort,
      };
      target = "公告与下载源";
      summary = "连接配置已保存";
      break;
    }
  }
  // The local fixture simulates the server scheduler when a rule is created/enabled.
  if (command.kind === "benefit-create" || command.kind === "benefit-toggle")
    for (const b of s.benefits.filter((b) => b.enabled)) {
      let pending = s.characters.filter(
        (c) =>
          !b.recipients.includes(c.id) &&
          (b.scope === "future" || b.eligibleIds?.includes(c.id)),
      );
      if (b.grantScope === "account") {
        const accounts = new Set(b.recipients.map((id) => s.characters.find((c) => c.id === id)?.accountId));
        pending = pending.filter((c) => {
          if (accounts.has(c.accountId)) return false;
          accounts.add(c.accountId);
          return true;
        });
      }
      if (pending.length) {
        if (b.cera) for (const c of pending) {
          const account = s.accounts.find((a) => a.id === c.accountId)!;
          account.points += b.cera;
        }
        deliver(
          pending.map((c) => c.id),
          b.name,
          b.cera ? `点券 ${b.cera} 已存入账号。` : "福利奖励已送达。",
          b.gold,
          b.item ? [b.item] : [],
          `${requestId}-${b.id}`,
        );
        b.recipients.push(...pending.map((c) => c.id));
      }
    }
  s.receipts.unshift({
    id: requestId,
    kind: command.kind,
    target,
    summary,
    at: now,
  });
  return s;
}
