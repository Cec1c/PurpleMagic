import type {
  Account,
  Character,
  Command,
  InventoryTab,
  Item,
  Section,
  Selection,
  State,
} from "../domain";
import { HttpClient, ApiError, query, segment, safeNumber } from "./http";
import { emptyState, type DTO, type Pending } from "./model";
import { prepareOperation, executeOperation } from "./operations";

export const containers: Record<InventoryTab, string[]> = {
  bag: [
    "equipment",
    "consumables",
    "materials",
    "quest-items",
    "profession",
    "quickbar",
  ],
  equipped: ["equipped"],
  petbag: ["pet-equipment", "pet-consumables"],
  warehouse: ["warehouse"],
  account: ["account-cargo", "shared"],
  pets: ["pets"],
  avatars: ["avatars"],
  emblems: ["materials"],
};
const jobNames = [
  "鬼剑士",
  "格斗家",
  "神枪手",
  "魔法师",
  "圣职者",
  "女枪手",
  "暗夜使者",
  "男格斗家",
  "男魔法师",
  "黑暗武士",
  "缔造者",
  "女鬼剑士",
  "守护者",
  "魔枪士",
  "女圣职者",
];
export function characterDTO(v: DTO, previous?: Character): Character {
  return {
    ...previous,
    id: String(v.characterId),
    accountId: v.accountId,
    name: v.name,
    job: jobNames[Number(v.job)] || v.job,
    jobCode: v.job,
    growType: v.growType,
    level: v.level,
    online: previous?.online ?? null,
    gold: previous?.gold ?? NaN,
    sp: previous?.sp ?? NaN,
    tp: previous?.tp ?? NaN,
  };
}
export function accountDTO(v: DTO, previous?: Account): Account {
  return {
    ...previous,
    id: v.accountId,
    name: v.name,
    status: v.disabled ? "限制登录" : "正常",
    points: previous?.points ?? NaN,
    tokens: previous?.tokens ?? NaN,
    characterCount: v.characterCount,
  };
}
export function itemDTO(v: DTO, c: Character, tab: InventoryTab): Item {
  const a = v.attributes || {},
    rarities = ["普通", "高级", "稀有", "神器", "史诗", "传说"] as const;
  return {
    key: `${c.id}/${v.container}/${v.slot}`,
    characterId: c.id,
    ...(v.container === "account-cargo" || v.container === "shared"
      ? { accountId: c.accountId }
      : {}),
    tab,
    slot: v.slot,
    id: v.itemId,
    name: v.name || `物品 ${v.itemId}`,
    category:
      v.category ||
      (/avatar emblem/.test(v.tag || "")
        ? "徽章"
        : /avatar/.test(v.tag || "")
          ? "时装"
          : v.tag === "weapon"
            ? "武器"
            : /^(coat|pants|shoulder|waist|shoes)$/.test(v.tag || "")
              ? "防具"
              : /^(ring|amulet|wrist)$/.test(v.tag || "")
                ? "首饰"
                : /^(support|magic stone)$/.test(v.tag || "")
                  ? "辅助装备"
                  : v.tag === "creature"
                    ? "宠物"
                    : /^artifact/.test(v.tag || "")
                      ? "宠物装备"
                      : v.kind === "equipment"
                        ? "装备"
                        : v.container === "consumables"
                          ? "消耗品"
                          : "材料"),
    rarity: rarities[v.rarity] || "普通",
    count: safeNumber(v.count),
    level: v.minimumLevel ?? 0,
    enhance:
      /avatar/i.test(v.tag || "") || v.container === "avatars"
        ? NaN
        : safeNumber(a.reinforce),
    amplify:
      /avatar/i.test(v.tag || "") || v.container === "avatars"
        ? NaN
        : safeNumber(a.amplifyType),
    forge:
      /avatar/i.test(v.tag || "") || v.container === "avatars"
        ? NaN
        : safeNumber(a.forging),
    bound: v.bound,
    version: 0,
    remote: {
      container: v.container,
      revision: v.revisionScope === "summary" ? undefined : v.revision,
      pvf: v.pvfChecksum,
      attributes: a,
      known: v.definitionKnown,
      kind: v.kind,
      subtype: v.tag,
      expiresAt: v.expiresAt,
    },
  };
}
export function merge<T>(old: T[], next: T[], key: (x: T) => string): T[] {
  const m = new Map(old.map((x) => [key(x), x]));
  next.forEach((x) => m.set(key(x), x));
  return [...m.values()];
}

export class ConsoleAPI {
  state = emptyState();
  current = "";
  sectionId: Section = "query";
  http: HttpClient;
  private epoch = 0;
  private retries = new Set<() => Promise<void>>();
  private controllers = new Map<string, AbortController>();
  private selected: Selection = { accounts: [], characters: [] };
  constructor(
    private emit: (s: State) => void,
    http = new HttpClient(),
  ) {
    this.http = http;
  }
  get meta() {
    return this.state.api!;
  }
  publish() {
    this.emit({ ...this.state, api: { ...this.meta } });
  }
  patch(v: Partial<State>) {
    this.state = { ...this.state, ...v };
    this.publish();
  }
  error(e: unknown) {
    if ((e as Error).name === "AbortError") return;
    if (e instanceof ApiError && e.status === 401)
      this.meta.authenticated = false;
    this.meta.error = (e as Error).message;
    this.publish();
  }
  navigate(section: Section, force = false) {
    if (this.sectionId === section && !force) return;
    this.epoch++;
    this.sectionId = section;
    this.http.cancelReads();
    this.controllers.forEach((c) => c.abort());
    this.retries.clear();
    this.meta.reading = 0;
    this.meta.inventory.loading = false;
    this.meta.error = "";
    this.publish();
  }
  cancelScope(scope: string) {
    this.controllers.get(scope)?.abort();
  }
  async retry() {
    const jobs = [...this.retries];
    this.retries.clear();
    this.meta.error = "";
    this.http.clearCache();
    await Promise.all(jobs.map((job) => this.guard(job)));
  }
  async guard(f: () => Promise<void>, retry = f) {
    const epoch = this.epoch;
    this.meta.reading++;
    this.publish();
    try {
      await f();
    } catch (e) {
      if (epoch === this.epoch && (e as Error).name !== "AbortError") {
        this.retries.add(retry);
        this.error(e);
      }
    } finally {
      if (epoch === this.epoch) {
        this.meta.reading = Math.max(0, this.meta.reading - 1);
        this.publish();
      }
    }
  }
  private async partial(
    jobs: (() => Promise<void>)[],
    retry?: () => Promise<void>,
  ) {
    await Promise.all(jobs.map((job) => this.guard(job, retry)));
  }
  signal(scope: string) {
    this.controllers.get(scope)?.abort();
    const controller = new AbortController();
    this.controllers.set(scope, controller);
    return controller.signal;
  }
  async boot() {
    try {
      const cap = await this.http.capabilities();
      this.meta.features = {
        ...cap.features,
        backgroundMail: !!cap.backgroundMail,
      };
      this.meta.authenticated = true;
      this.meta.error = "";
      this.restore();
      this.publish();
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        this.meta.authenticated = false;
        this.publish();
      } else this.error(e);
    } finally {
      this.meta.authReady = true;
      this.publish();
    }
  }
  async login(key: string, remember = true) {
    const cap = await this.http.login(key, remember);
    this.meta.features = {
      ...cap.features,
      backgroundMail: !!cap.backgroundMail,
    };
    this.meta.authenticated = true;
    this.meta.authReady = true;
    this.meta.error = "";
    this.restore();
    this.publish();
  }
  async logout() {
    await this.http.raw("/api/auth/logout", "POST", {});
    this.navigate("query", true);
    this.http.clearCache();
    this.state = emptyState();
    this.meta.authReady = true;
    this.http.csrf = "";
    this.current = "";
    this.publish();
  }
  restore() {
    try {
      const raw = sessionStorage.getItem("purplemagic-pending-v1");
      if (raw) {
        const p = JSON.parse(raw);
        if (p.id && Array.isArray(p.steps)) this.meta.pending = p;
      }
    } catch {}
    this.publish();
  }
  persistPending() {
    if (this.meta.pending)
      sessionStorage.setItem(
        "purplemagic-pending-v1",
        JSON.stringify(this.meta.pending),
      );
    else sessionStorage.removeItem("purplemagic-pending-v1");
    this.publish();
  }
  async directory(
    mode: "accounts" | "characters",
    q: string,
    more = false,
    accountId = "",
    online = "all",
  ) {
    const signal = this.signal("directory");
    const old = this.meta.directory;
    const cursor =
      more &&
      old.mode === mode &&
      old.q === q &&
      (old.online || "all") === online
        ? old.next
        : "";
    this.meta.error = "";
    this.publish();
    {
      const r = await this.http.get<DTO[]>(
        accountId
          ? `/api/v1/accounts/${segment(accountId)}/characters`
          : query(`/api/v1/${mode}`, {
              q,
              limit: 20,
              cursor,
              ...(mode === "characters" && online !== "all" ? { online } : {}),
            }),
        signal,
      );
      let chars: Character[] = [],
        acc: Account[] = [];
      if (mode === "characters") {
        chars = (r.data || [])
          .filter(
            (v) =>
              !accountId ||
              `${v.name} ${v.characterId}`
                .toLowerCase()
                .includes(q.toLowerCase()),
          )
          .map((v) =>
            characterDTO(
              v,
              this.state.characters.find((c) => c.id === String(v.characterId)),
            ),
          );
        acc = [...new Set(chars.map((c) => c.accountId))].map(
          (id) =>
            this.state.accounts.find((a) => a.id === id) ||
            accountDTO({ accountId: id, name: id }),
        );
      } else {
        acc = (r.data || []).map((v) =>
          accountDTO(
            v,
            this.state.accounts.find((a) => a.id === v.accountId),
          ),
        );
      }
      if (signal.aborted) return;
      const keepChars = this.state.characters.filter(
        (c) =>
          c.id === this.current ||
          this.selected.characters.includes(c.id) ||
          this.selected.accounts.includes(c.accountId) ||
          (more && old.ids.includes(c.id)),
      );
      const allChars = merge(keepChars, chars, (c) => c.id);
      const keepAcc = this.state.accounts.filter(
        (a) =>
          a.id ===
            this.state.characters.find((c) => c.id === this.current)
              ?.accountId ||
          this.selected.accounts.includes(a.id) ||
          allChars.some((c) => c.accountId === a.id),
      );
      this.meta.directory = {
        mode,
        q,
        online,
        next: r.page?.nextCursor || "",
        ids: more
          ? [...new Set([...old.ids, ...chars.map((c) => c.id)])]
          : chars.map((c) => c.id),
        accountIds: more
          ? [...new Set([...old.accountIds, ...acc.map((a) => a.id)])]
          : acc.map((a) => a.id),
      };
      this.patch({
        characters: allChars,
        accounts: merge(keepAcc, acc, (a) => a.id),
      });
      await this.partial([
        ...(mode === "characters"
          ? acc
              .filter((a) => a.name === a.id)
              .map((a) => async () => {
                const { data } = await this.http.get(
                  `/api/v1/accounts/${segment(a.id)}`,
                  signal,
                );
                if (signal.aborted) return;
                this.patch({
                  accounts: merge(
                    this.state.accounts,
                    [accountDTO(data, a)],
                    (x) => x.id,
                  ),
                });
              })
          : []),
        // Counts come with the directory; only visible wallets need supplements.
        ...(mode === "accounts" && this.meta.features["currency.read"]
          ? acc.map((a) => async () => {
              const { data } = await this.http.get(
                `/api/v1/accounts/${segment(a.id)}/currency`,
                signal,
              );
              if (signal.aborted) return;
              this.patch({
                accounts: this.state.accounts.map((x) =>
                  x.id === a.id
                    ? {
                        ...x,
                        points: safeNumber(data.balance),
                        rawPoints: data.balance,
                      }
                    : x,
                ),
              });
            })
          : []),
        ...(mode === "characters" ? [() => this.presence(signal)] : []),
      ]);
    }
  }
  async resolveSelection(selection: Selection) {
    this.selected = selection;
    let chars = this.state.characters;
    for (const id of selection.accounts) {
      const { data } = await this.http.get<DTO[]>(
        `/api/v1/accounts/${segment(id)}/characters`,
      );
      chars = merge(
        chars,
        (data || []).map((v) =>
          characterDTO(
            v,
            chars.find((c) => c.id === String(v.characterId)),
          ),
        ),
        (c) => c.id,
      );
    }
    for (const id of selection.characters) {
      if (!chars.some((c) => c.id === id)) {
        const { data } = await this.http.get(
          `/api/v1/characters/${segment(id)}`,
        );
        chars.push(characterDTO(data.character));
      }
    }
    if (this.selected === selection)
      this.patch({
        characters: merge(this.state.characters, chars, (c) => c.id),
      });
    return chars.filter(
      (c) =>
        selection.accounts.includes(c.accountId) ||
        selection.characters.includes(c.id),
    );
  }
  async presence(signal?: AbortSignal) {
    try {
      const { data } = await this.http.get("/api/v1/presence", signal);
      const ids = new Set(data.characterIds);
      this.patch({
        characters: this.state.characters.map((c) => ({
          ...c,
          online: ids.has(c.id),
        })),
      });
    } catch (e) {
      if ((e as Error).name === "AbortError") throw e;
      this.patch({
        characters: this.state.characters.map((c) => ({ ...c, online: null })),
      });
    }
  }
  async select(id: string) {
    this.http.cancelReads();
    this.controllers.forEach((c) => c.abort());
    const signal = this.signal("selection");
    this.current = id;
    this.state.items = [];
    this.state.mails = [];
    this.meta.resources = {};
    this.meta.inventory = { tab: "bag", next: {}, pvf: "", loading: false };
    this.meta.quests = [];
    this.meta.error = "";
    this.publish();
    await this.character(id);
    if (!signal.aborted && this.current === id) await this.presence(signal);
  }
  async character(id = this.current) {
    if (!id) return;
    const signal = this.signal("character");
    const { data } = await this.http.get(
      `/api/v1/characters/${segment(id)}`,
      signal,
    );
    let c = characterDTO(
      data.character,
      this.state.characters.find((x) => x.id === id),
    );
    if (data.online !== "unknown")
      c.online = data.online === true || data.online === "online";
    let a =
      this.state.accounts.find((x) => x.id === c.accountId) ||
      accountDTO(
        (
          await this.http.get(
            `/api/v1/accounts/${segment(c.accountId)}`,
            signal,
          )
        ).data,
      );
    if (signal.aborted || id !== this.current) return;
    this.patch({
      characters: merge(this.state.characters, [c], (x) => x.id),
      accounts: merge(this.state.accounts, [a], (x) => x.id),
    });
    const reads: [string, string][] = [
      ["gold", `characters/${segment(id)}/gold`],
      ["currency", `accounts/${segment(c.accountId)}/currency`],
    ];
    const resources: Record<string, DTO> = {};
    await this.partial(
      reads.map(([name, path]) => async () => {
        if (this.meta.features[name + ".read"]) {
          const { data } = await this.http.get(`/api/v1/${path}`, signal);
          if (signal.aborted || id !== this.current) return;
          resources[name] = data;
          if (name === "gold") c.gold = safeNumber(data.balance);
          else
            a = {
              ...a,
              points: safeNumber(data.balance),
              rawPoints: data.balance,
            };
        }
      }),
      () => this.character(id),
    );
    if (signal.aborted || id !== this.current) return;
    const latest = this.state.characters.find((x) => x.id === id);
    c = {
      ...c,
      sp: latest?.sp ?? c.sp,
      tp: latest?.tp ?? c.tp,
      job:
        latest && latest.jobCode === c.jobCode && latest.growType === c.growType
          ? latest.job
          : c.job,
    };
    this.meta.resources = { ...this.meta.resources, ...resources };
    this.patch({
      characters: merge(this.state.characters, [c], (x) => x.id),
      accounts: merge(this.state.accounts, [a], (x) => x.id),
    });
  }
  async enter(section: Section, refresh = false) {
    this.sectionId = section;
    this.meta.error = "";
    this.publish();
    if (!this.meta.authenticated) return;
    if (section === "inventory")
      await this.inventory(
        refresh ? (this.meta.inventory.tab as InventoryTab) : "bag",
      );
    else if (refresh && section === "mail")
      await this.partial([() => this.mailbox(), () => this.tasks()]);
    else if (refresh && section === "quests")
      await this.quests(this.meta.questFilter.status, this.meta.questFilter.q);
    else if (refresh && section === "gm")
      await this.partial([
        () => this.growth(),
        () => this.tasks(), () => this.history(),
      ]);
    else if (refresh && section === "benefits") await this.benefits();
    else if (refresh && section === "settings") await this.hub();
  }
  async inventory(tab: InventoryTab, more = false) {
    if (!this.current) return;
    const id = this.current,
      c = this.state.characters.find((c) => c.id === id)!;
    if (!c) return;
    const signal = this.signal("inventory");
    const same = this.meta.inventory.tab === tab;
    const previous = more && same ? this.state.items : [];
    const cursors = more && same ? this.meta.inventory.next : {};
    this.meta.inventory = { tab, next: {}, pvf: "", loading: true };
    if (!more) this.state.items = [];
    this.publish();
    try {
      let items: Item[] = [...previous],
        next: Record<string, string> = {},
        pvf = "";
      await this.partial(
        containers[tab].map((container) => async () => {
          if (more && !cursors[container]) return;
          const { data, page } = await this.http.get(
            query(`/api/v1/characters/${segment(id)}/inventory`, {
              container,
              limit: 100,
              cursor: cursors[container],
            }),
            signal,
          );
          if (signal.aborted || id !== this.current) return;
          if (pvf && pvf !== data.pvfChecksum)
            throw new Error("PVF 已更新，请刷新背包");
          pvf = data.pvfChecksum;
          const rows = (data.items || []).filter(
            (v: DTO) =>
              tab !== "emblems" ||
              /emblem/i.test(v.tag || "") ||
              v.category === "徽章",
          );
          items = merge(
            items,
            rows.map((v: DTO) => itemDTO({ ...v, pvfChecksum: pvf }, c, tab)),
            (i) => i.key,
          );
          if (page?.nextCursor) next[container] = page.nextCursor;
          this.patch({ items: [...items] });
        }),
        () => this.inventory(tab),
      );
      if (signal.aborted || id !== this.current) return;
      this.meta.inventory = { tab, next, pvf, loading: false };
      this.patch({ items });
    } finally {
      if (!signal.aborted) {
        this.meta.inventory.loading = false;
        this.publish();
      }
    }
  }
  async item(key: string) {
    const i = this.state.items.find((i) => i.key === key);
    if (!i?.remote) return;
    const id = this.current;
    const signal = this.signal("item");
    const { data } = await this.http.get(
      `/api/v1/characters/${segment(id)}/inventory/${segment(i.remote.container)}/${i.slot}/options`,
      signal,
    );
    if (
      signal.aborted ||
      this.current !== id ||
      !this.state.items.some((x) => x.key === key)
    )
      return;
    const c = this.state.characters.find((c) => c.id === id)!;
    const item = itemDTO(data.item, c, i.tab);
    item.remote!.options = data;
    const equipment = data.configuration?.equipment;
    if (equipment) {
      item.level = equipment.minimumLevel;
      item.enhance = equipment.currentUpgradeLevel;
      item.amplify = equipment.currentAmplifyType;
      item.forge = equipment.currentForgingLevel;
    }
    item.remote!.relatedRevision = data.item.relatedRevision;
    item.version = i.version + 1;
    this.patch({
      items: this.state.items.map((x) => (x.key === key ? item : x)),
    });
  }
  async mailbox(more = false) {
    if (!this.current) return;
    const id = this.current,
      signal = this.signal("mailbox");
    const { data, page } = await this.http.get<DTO[]>(
      query(`/api/v1/characters/${segment(id)}/mailbox`, {
        limit: 30,
        cursor: more ? this.meta.mailNext : undefined,
      }),
      signal,
    );
    if (signal.aborted || id !== this.current) return;
    this.meta.mailNext = page?.nextCursor || "";
    this.patch({
      mails: merge(
        more ? this.state.mails : [],
        (data || []).map((v) => ({
          id: v.mailId,
          characterId: id,
          title: v.title,
          body: "",
          sender: v.sender,
          gold: safeNumber(v.gold),
          items: [],
          createdAt: v.createdAt,
          claimed: v.claimed,
        })),
        (m) => m.id,
      ),
    });
  }
  async mail(id: string) {
    const c = this.current;
    const { data } = await this.http.get(
      `/api/v1/characters/${segment(c)}/mailbox/${segment(id)}`,
    );
    if (this.current !== c) return;
    this.patch({
      mails: this.state.mails.map((m) =>
        m.id === id
          ? {
              ...m,
              body: data.body,
              items: (data.attachments || []).map((a: DTO) => ({
                id: a.itemId,
                name: a.name || a.itemId,
                count: safeNumber(a.count),
                enhance: 0,
              })),
            }
          : m,
      ),
    });
  }
  async quests(status = "active", q = "", more = false) {
    if (!this.current && status !== "catalog") return;
    this.meta.questFilter = { status, q };
    const id = this.current,
      signal = this.signal("quests"),
      path =
        status === "catalog"
          ? "/api/v1/quests"
          : `/api/v1/characters/${segment(id)}/quests`;
    const { data, page } = await this.http.get(
      query(path, {
        state: status === "catalog" ? undefined : status,
        q: status === "catalog" ? q : undefined,
        limit: 100,
        cursor: more ? this.meta.questNext : undefined,
      }),
      signal,
    );
    if (signal.aborted || id !== this.current) return;
    const states =
      more || status === "catalog"
        ? { ...this.state.questStates[id] }
        : ({} as Record<string, string>);
    const rows = (data.quests || []).map((v: DTO) => {
      if (status !== "catalog") states[v.questId] = v.status || "unstarted";
      return {
        id: v.questId,
        name: v.name || `任务 ${v.questId}`,
        chapter: v.grade || "其他",
        kind: v.grade || "任务",
        level: v.levelMin,
      };
    });
    this.meta.quests = merge(
      more ? this.meta.quests : [],
      rows,
      (q: any) => q.id,
    );
    this.meta.questNext = page?.nextCursor || "";
    this.meta.questPVF = data.pvfChecksum;
    this.patch({ questStates: { [id]: states } });
  }
  async catalog(q: string, more = false) {
    const signal = this.signal("catalog");
    const { data, page } = await this.http.get<DTO>(
      query("/api/v1/items", {
        q,
        limit: 30,
        cursor: more ? this.meta.catalogNext : undefined,
      }),
      signal,
    );
    if (signal.aborted) return;
    const c =
      this.state.characters.find((c) => c.id === this.current) ||
      ({ id: "", accountId: "" } as Character);
    this.meta.catalog = merge(
      more ? this.meta.catalog : [],
      (data.items || [])
        .filter((v: DTO) => !v.invalid)
        .map((v: DTO) =>
          itemDTO(
            {
              ...v,
              minimumLevel: v.minLevel,
              slot: 0,
              container: "catalog",
              itemId: v.itemId ?? v.id,
              count: "1",
            },
            c,
            "bag",
          ),
        ),
      (x) => x.id,
    );
    this.meta.catalogNext = page?.nextCursor || "";
    this.publish();
  }
  async library(q: string, kind: string, more = false) {
    const signal = this.signal("library");
    const previous = this.meta.resources.library;
    const { data, page } = await this.http.get(
      query("/api/v1/items", {
        q,
        kind,
        limit: 50,
        cursor: more ? previous?.next : undefined,
      }),
      signal,
    );
    if (signal.aborted) return;
    this.meta.resources.library = {
      ...data,
      q,
      kind,
      next: page?.nextCursor || "",
      items: merge(
        more ? previous?.items || [] : [],
        data.items || [],
        (v: DTO) => v.itemId,
      ),
    };
    this.publish();
  }
  async libraryItem(id: string) {
    const signal = this.signal("libraryItem");
    this.meta.resources.libraryItem = { loading: true, id };
    this.publish();
    try {
      const { data } = await this.http.get(
        `/api/v1/items/${segment(id)}`,
        signal,
      );
      if (signal.aborted) return;
      this.meta.resources.libraryItem = { ...data, id };
    } finally {
      if (!signal.aborted) {
        this.meta.resources.libraryItem.loading = false;
        this.publish();
      }
    }
  }
  async activities() {
    const signal = this.signal("activities");
    const { data } = await this.http.get("/api/v1/activities", signal);
    if (signal.aborted) return;
    this.meta.resources.activities = data;
    this.publish();
  }
  async pvfMode(mode?: "disk" | "memory") {
    const signal = this.signal("pvfMode");
    const data = mode
      ? await this.http.post("/api/v1/settings/pvf", { mode })
      : (await this.http.get("/api/v1/settings/pvf", signal)).data;
    if (signal.aborted) return;
    this.meta.resources.pvfMode = data;
    this.publish();
  }
  async growth() {
    if (!this.current) return;
    const id = this.current,
      signal = this.signal("growth");
    let c = this.state.characters.find((c) => c.id === id)!;
    await this.partial(
      [
        ["level", "level"],
        ["skillPoints", "skill-points"],
        ["profession", "profession"],
      ].map(([name, path]) => async () => {
        if (!this.meta.features[name + ".read"]) return;
        const { data } = await this.http.get(
          `/api/v1/characters/${segment(id)}/${path}`,
          signal,
        );
        if (signal.aborted || id !== this.current) return;
        this.meta.resources[name] = data;
        if (name === "skillPoints") {
          c = { ...c, sp: data.balance.bonusSp, tp: data.balance.bonusTp };
          this.patch({
            characters: this.state.characters.map((x) =>
              x.id === id ? { ...x, sp: c.sp, tp: c.tp } : x,
            ),
          });
        }
        if (name === "profession") {
          this.meta.jobsRaw = data.jobs;
          this.meta.jobs = (data.jobs || []).flatMap((j: DTO) =>
            (j.choices || []).map((g: DTO) => ({
              value: `${j.job}:${g.growType}`,
              label: g.name || j.name,
            })),
          );
          const choice = this.meta.jobs.find(
            (j) => j.value === `${data.balance.job}:${data.balance.growType}`,
          );
          c = {
            ...c,
            job: choice?.label || c.job,
            jobCode: String(data.balance.job),
            growType: data.balance.growType,
          };
        }
      }),
      () => this.growth(),
    );
    if (!signal.aborted && id === this.current)
      this.patch({
        characters: this.state.characters.map((x) =>
          x.id === id
            ? {
                ...x,
                sp: c.sp,
                tp: c.tp,
                job: c.job,
                jobCode: c.jobCode,
                growType: c.growType,
              }
            : x,
        ),
      });
  }
  async benefits(more = false) {
    const signal = this.signal("benefits");
    if (!this.meta.features["benefits.write"]) return;
    const { data, page } = await this.http.get<DTO[]>(
      query("/api/v1/benefits", {
        limit: 30,
        cursor: more ? this.meta.benefitNext : undefined,
      }),
      signal,
    );
    if (signal.aborted) return;
    this.meta.benefitNext = page?.nextCursor || "";
    this.patch({
      benefits: merge(
        more ? this.state.benefits : [],
        (data || []).map((v) => ({
          id: v.id,
          name: v.reward.title,
          gold: v.reward.gold,
          cera: v.reward.cera,
          grantScope: v.scope,
          enabled: v.enabled,
          scope: v.autoNewCharacters
            ? ("future" as const)
            : ("existing" as const),
          recipients: [],
          revision: v.revision,
          scopeLabel: v.scope === "account" ? "每账号一次" : "每角色一次",
          rewardSummary: [
            v.reward.cera
              ? `点券 ${Number(v.reward.cera).toLocaleString()}`
              : "",
            v.reward.gold
              ? `金币 ${Number(v.reward.gold).toLocaleString()}`
              : "",
            ...(v.reward.attachments || []).map(
              (a: DTO) => `${v.itemName || a.name || a.itemId} × ${a.count}`,
            ),
          ]
            .filter(Boolean)
            .join("；"),
        })),
        (b) => b.id,
      ),
    });
  }

  async tasks(more = false) {
    const signal = this.signal("tasks");
    if (!this.meta.features["tasks.read"]) return;
    const { data, page } = await this.http.get<DTO[]>(
      query("/api/v1/tasks", {
        limit: 30,
        cursor: more ? this.meta.taskNext : undefined,
      }),
      signal,
    );
    if (signal.aborted) return;
    this.meta.taskNext = page?.nextCursor || "";
    this.meta.resources.tasks = {
      rows: merge(
        more ? this.meta.resources.tasks?.rows || [] : [],
        data || [],
        (v: DTO) => v.id,
      ),
    };
    this.patch({
      receipts: merge(
        more
          ? this.state.receipts
          : this.state.receipts.filter((r) => r.kind !== "mail"),
        (data || []).map((v) => ({
          id: v.id,
          kind: "mail",
          target: `${v.total} 个角色`,
          summary: `${v.reward.title} · 已发 ${v.sent} / ${v.total}${v.failed ? ` · 失败 ${v.failed}` : ""} · ${v.state === "preview" ? "待确认" : v.state === "completed" ? "已完成" : v.state === "failed" ? "待重试" : "处理中"}`,
          at: v.createdAt,
          details: v,
        })),
        (r) => r.id,
      ),
    });
  }
  async history(more = false) {
    const id = this.current,
      c = this.state.characters.find((c) => c.id === id);
    if (!c) return;
    const signal = this.signal("history");
    const base = `/api/v1/characters/${segment(id)}`;
    const account = `/api/v1/accounts/${segment(c.accountId)}`;
    const routes = [
      ["inventory.write", `${base}/inventory-operations`, "物品"],
      ["inventory.write", `${account}/inventory-operations`, "账号物品"],
      ["gold.write", `${base}/gold-operations`, "金币"],
      ["currency.write", `${account}/currency-adjustments`, "点券"],
      ["level.write", `${base}/level-operations`, "等级"],
      ["profession.write", `${base}/profession-operations`, "职业"],
      ["skillPoints.write", `${base}/skill-point-operations`, "SP / TP"],
      ["quests.write", `${base}/quest-operations`, "任务"],
      ["mailbox.write", `${base}/mailbox-operations`, "邮箱"],
    ];
    const cursors: Record<string, string> = {};
    const rows: State["receipts"] = [];
    await this.partial(
      routes.map(([feature, path, label]) => async () => {
        if (
          !this.meta.features[feature] ||
          (more && !this.meta.historyNext[path])
        )
          return;
        const { data, page } = await this.http.get<DTO[]>(
          query(path, {
            limit: 20,
            cursor: more ? this.meta.historyNext[path] : undefined,
          }),
          signal,
        );
        if (page?.nextCursor) cursors[path] = page.nextCursor;
        for (const v of data || [])
          rows.push({
            id: `${label}:${v.requestId}`,
            kind: v.kind || label,
            target: label,
            summary: `${label} · ${v.reason || "管理操作"}${typeof v.before === "string" ? ` · ${v.before} → ${v.after}` : ""}`,
            at: v.createdAt,
            details: v,
          });
      }),
      () => this.history(),
    );
    if (signal.aborted || this.current !== id) return;
    this.meta.historyNext = cursors;
    this.patch({
      receipts: merge(
        more
          ? this.state.receipts
          : this.state.receipts.filter((r) => r.kind === "mail"),
        rows,
        (r) => r.id,
      ).sort((a, b) => b.at.localeCompare(a.at)),
    });
  }
  async hub(token?: string) {
    try {
      if (token)
        await this.http.raw("/admin/api/login", "POST", {
          token,
          remember: false,
        });
      const data = await this.http.raw<DTO>(
        "/admin/api/state",
        "GET",
        undefined,
        undefined,
        this.signal("hub"),
      );
      this.http.hubCsrf = data.csrf;
      this.meta.hub = data;
      this.meta.hubError = "";
      const sources = data.sources || [];
      this.patch({
        news: [
          ...JSON.parse(
            sessionStorage.getItem("purplemagic-news-drafts-v1") || "[]",
          ),
          ...(data.published?.news || []).map((n: DTO, i: number) => ({
            id: String(i),
            title: n.title,
            body: n.body,
            published: true,
          })),
        ],
        sources: {
          download: sources[0]?.endpoint || "",
          gameHost: data.published?.profile?.GameHost || "",
          gamePort: data.published?.profile?.BootstrapPort || 0,
        },
      });
    } catch (e) {
      if ((e as Error).name === "AbortError") return;
      this.meta.hubError = (e as Error).message;
      if (e instanceof ApiError && e.status === 401) this.meta.hub = undefined;
      this.publish();
    }
  }
  async prepare(command: Command, selection: Selection) {
    if (this.meta.pending?.confirmed)
      throw new Error("请先处理尚未取得回执的操作");
    this.meta.busy = true;
    this.meta.error = "";
    this.publish();
    try {
      this.meta.pending = await prepareOperation(this, command, selection);
      this.persistPending();
    } finally {
      this.meta.busy = false;
      this.publish();
    }
  }
  cancel() {
    if (
      this.meta.pending?.confirmed &&
      !this.meta.pending.completed &&
      !this.meta.pending.rejected
    )
      throw new Error("已提交的操作请继续查询回执");
    this.meta.pending = undefined;
    this.persistPending();
  }
  async confirm() {
    const p = this.meta.pending;
    if (!p || p.completed || p.rejected || this.meta.busy) return;
    this.meta.busy = true;
    p.confirmed = true;
    p.error = undefined;
    this.persistPending();
    try {
      await executeOperation(this, p);
      p.completed = true;
      if (p.draftId) {
        const drafts = JSON.parse(
          sessionStorage.getItem("purplemagic-news-drafts-v1") || "[]",
        );
        sessionStorage.setItem(
          "purplemagic-news-drafts-v1",
          JSON.stringify(drafts.filter((n: DTO) => n.id !== p.draftId)),
        );
      }
      this.persistPending();
    } catch (e) {
      p.error = (e as Error).message;
      p.rejected =
        e instanceof ApiError && [400, 403, 404, 409, 422].includes(e.status);
      this.meta.pending = p;
      this.persistPending();
      throw e;
    } finally {
      this.meta.busy = false;
      this.publish();
    }
    await this.guard(async () => {
      if (this.sectionId !== "benefits") await this.character();
      await this.enter(this.sectionId, true);
    });
  }
}
