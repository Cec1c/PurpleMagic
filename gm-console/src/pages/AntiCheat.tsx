import { DelayedSpinner, LoadingSkeleton } from "../Loading";
import { useEffect, useRef, useState } from "react";
import {
  ShieldCheck,
  Timer,
  Search,
  RefreshCw,
  ArrowUpDown,
  ArrowUp,
  ArrowDown,
  Zap,
  Ban as BanIcon,
  UnlockKeyhole,
  LoaderCircle,
} from "lucide-react";
import { useLive } from "../api/context";
import { ApiError, query } from "../api/http";
import { Empty, Field, Modal, Tabs, type PageProps } from "../ui";
import {
  qqActionLabel,
  banState,
  duration,
  fatigue,
  nextSort,
  type Sort,
  pendingBanKey,
  readPendingBan,
  suspicious,
  type Ban,
  type BanChange,
  type ClearStats,
  type AccountClearStats,
  type ModerationAccount,
  type Page,
  type PendingBan,
} from "../moderation";
import "../moderation.css";
import { QQBindingPage } from "./QQBinding";
import { IPRangeBans } from "./IPRanges";
import {
  readAntiCheatMemory,
  writeAntiCheatMemory,
  writeDirectoryMemory,
  writeInventoryTabMemory,
} from "../navigation-state";

const stamp = (raw: string) => new Date(raw).toLocaleString();
const dateInput = (date: Date) =>
  new Date(date.getTime() - date.getTimezoneOffset() * 60000)
    .toISOString()
    .slice(0, 16);
type ModerationTab = "account-stats" | "clears" | "accounts" | "bans";
type AllAntiCheatTab = ModerationTab | "qq";

const defaultSorts = (): Record<ModerationTab, Sort> => ({
  "account-stats": { sortBy: "count", sortOrder: "desc" },
  clears: { sortBy: "count", sortOrder: "desc" },
  accounts: { sortBy: "id", sortOrder: "asc" },
  bans: { sortBy: "updatedAt", sortOrder: "desc" },
});

export function SortHeader({
  fields,
  sort,
  onSort,
  disabled,
}: {
  fields: [string, string][];
  sort: Sort;
  onSort: (field: string) => void;
  disabled: boolean;
}) {
  const active = fields.some(([key]) => key === sort.sortBy);
  return (
    <th
      aria-sort={
        active
          ? sort.sortOrder === "asc"
            ? "ascending"
            : "descending"
          : "none"
      }
    >
      <div className="moderation-sort-fields">
        {fields.map(([key, label]) => (
          <button
            key={key}
            className="moderation-sort"
            disabled={disabled}
            onClick={() => onSort(key)}
            title={`按${label}${sort.sortBy === key && sort.sortOrder === "asc" ? "降序" : "升序"}排列`}
          >
            {label}
            {sort.sortBy === key ? (
              sort.sortOrder === "asc" ? (
                <ArrowUp size={12} />
              ) : (
                <ArrowDown size={12} />
              )
            ) : (
              <ArrowUpDown size={12} />
            )}
          </button>
        ))}
      </div>
    </th>
  );
}

const readInitialPendingBan = (): PendingBan | null =>
  typeof sessionStorage === "undefined" ? null : readPendingBan(sessionStorage);

export function AntiCheatPage(props: PageProps) {
  const live = useLive();
  const saved = readAntiCheatMemory(live);
  const [tab, setTab] = useState<AllAntiCheatTab>(
    (saved.tab as AllAntiCheatTab) || "account-stats",
  );
  const [accountFocus, setAccountFocus] = useState<string>(
    saved.accountFocus || "",
  );
  const [qqFocus, setQqFocus] = useState<{ search: string; tick: number }>({
    search: "",
    tick: 0,
  });
  const [resolvingAccount, setResolvingAccount] = useState("");

  const [pending, setPending] = useState<PendingBan | null>(
      readInitialPendingBan,
    ),
    [show, setShow] = useState(() => !!readInitialPendingBan()),
    [busy, setBusy] = useState(false),
    [message, setMessage] = useState(""),
    [reason, setReason] = useState(""),
    [hours, setHours] = useState("24"),
    [writeError, setWriteError] = useState(""),
    [banRevision, setBanRevision] = useState(0);

  const canWrite = !!live && !!props.state.api?.features["moderation.write"];
  const canBan = canWrite && !pending?.confirmed;

  useEffect(() => {
    writeAntiCheatMemory(live, { tab, accountFocus });
  }, [live, tab, accountFocus]);

  function inspectCharacterEquipped(characterId: string) {
    if (!characterId) return;
    writeInventoryTabMemory(live, "equipped");
    props.selectCharacter(characterId);
    props.go("inventory");
  }

  function inspectCharacterGm(characterId: string) {
    if (!characterId) return;
    props.selectCharacter(characterId);
    props.go("gm");
  }

  async function inspectAccountRoles(accountId: string, accountName?: string) {
    if (!accountId || resolvingAccount) return;
    if (!live) {
      const chars = props.state.characters.filter(
        (c) => c.accountId === accountId,
      );
      if (chars.length === 1) {
        inspectCharacterEquipped(chars[0].id);
      } else if (chars.length > 1) {
        writeDirectoryMemory(null, {
          mode: "characters",
          q: "",
          next: "",
          ids: chars.map((c) => c.id),
          accountIds: [accountId],
          accountId,
          online: "all",
        });
        props.go("accounts");
      } else {
        props.openAccount?.(accountId, accountName);
        props.notify("该账号暂无角色，已打开账号管理面板");
      }
      return;
    }
    setResolvingAccount(accountId);
    try {
      await live.guard(async () => {
        const chars = await live.accountCharacters(accountId);
        if (chars.length === 1) {
          writeInventoryTabMemory(live, "equipped");
          props.selectCharacter(chars[0].id);
          props.go("inventory");
        } else if (chars.length > 1) {
          writeDirectoryMemory(live, {
            mode: "characters",
            q: "",
            next: "",
            ids: chars.map((c) => c.id),
            accountIds: [accountId],
            accountId,
            online: "all",
          });
          props.go("accounts");
        } else {
          props.openAccount?.(accountId, accountName);
          props.notify("该账号暂无角色，已打开账号管理面板");
        }
      });
    } finally {
      setResolvingAccount("");
    }
  }

  function focusClears(accountId: string) {
    setAccountFocus(accountId);
    writeAntiCheatMemory(live, {
      tab: "clears",
      accountFocus: accountId,
      q: "",
      offset: 0,
      forceReload: Date.now(),
    });
    setTab("clears");
  }

  function focusQQ(accountId: string) {
    const tick = Date.now();
    setAccountFocus(accountId);
    setQqFocus({ search: accountId, tick });
    writeAntiCheatMemory(live, {
      tab: "qq",
      accountFocus: accountId,
    });
    setTab("qq");
  }

  async function prepareBan(
    kind: "account" | "ip",
    target: string,
    label: string,
    revoke = false,
    existing?: Ban,
  ) {
    if (!live || pending?.confirmed || busy) return;
    setWriteError("");
    setMessage("");
    setReason("");
    setHours("24");
    await live.guard(async () => {
      const result = existing
        ? { rows: [existing] }
        : await live.moderation("bans", { search: target, limit: 100 });
      const prior = (result.rows as Ban[]).find(
        (v) => v.kind === kind && (kind === "ip" || v.target === target),
      );
      const targets =
        kind === "account" && !revoke
          ? await live.banTargets(target)
          : undefined;
      setPending({
        targets,
        change: {
          requestId: crypto.randomUUID(),
          kind,
          target: prior?.target || target,
          reason: "",
          expiresAt: null,
          revoke,
          revision: prior?.revision || 0,
        },
        label,
        confirmed: false,
      });
      setShow(true);
    });
  }

  async function confirmBan() {
    if (!pending || !live || busy) return;
    let operation = pending;
    if (!operation.confirmed) {
      const change: BanChange = {
        ...operation.change,
        reason: reason.trim(),
        expiresAt:
          !operation.change.revoke && hours !== "0"
            ? new Date(Date.now() + Number(hours) * 3600000).toISOString()
            : null,
      };
      if (!change.reason) {
        setWriteError("请填写操作原因");
        return;
      }
      operation = { ...operation, change, confirmed: true };
      sessionStorage.setItem(pendingBanKey, JSON.stringify(operation));
      setPending(operation);
    }
    setBusy(true);
    setWriteError("");
    try {
      const result = await live.changeBan(operation.change);
      sessionStorage.removeItem(pendingBanKey);
      setPending(null);
      setShow(false);
      setMessage(
        [
          `${result.label}：${banState(result)}`,
          ...(result.linkedIps || []).map((ip) => `IP ${ip}：已封禁`),
          ...(result.qqActions || []).map(qqActionLabel),
        ].join("；"),
      );
      writeAntiCheatMemory(live, { cachedModeration: null });
      setBanRevision((v) => v + 1);
    } catch (e) {
      if (e instanceof ApiError && (e.status === 400 || e.status === 409)) {
        sessionStorage.removeItem(pendingBanKey);
        setPending(null);
        setShow(false);
        setMessage(e.message);
        writeAntiCheatMemory(live, { cachedModeration: null, offset: 0 });
        setBanRevision((v) => v + 1);
      } else {
        setWriteError((e as Error).message);
        if (e instanceof ApiError && e.status === 401) live.error(e);
      }
    } finally {
      setBusy(false);
    }
  }

  function closeBan() {
    if (busy) return;
    setShow(false);
    if (!pending?.confirmed) setPending(null);
  }

  return (
    <div className="moderation-page">
      <Tabs
        label="反作弊功能"
        items={[
          { id: "account-stats", label: "账号统计" },
          { id: "clears", label: "通关统计" },
          { id: "accounts", label: "账号封禁" },
          { id: "bans", label: "IP 与封禁记录" },
          { id: "qq", label: "账号与 QQ 号" },
        ]}
        value={tab}
        onChange={(v) => {
          setAccountFocus("");
          writeAntiCheatMemory(live, {
            tab: v,
            accountFocus: "",
          });
          setTab(v as AllAntiCheatTab);
        }}
      />
      {pending?.confirmed && !show && (
        <div className="moderation-notice glass">
          有一笔操作等待回执
          <button className="secondary" onClick={() => setShow(true)}>
            继续查询
          </button>
        </div>
      )}
      {message && (
        <div role="status" className="moderation-notice glass">
          {message}
        </div>
      )}
      {tab === "qq" ? (
        <QQBindingPage
          {...props}
          focusSearch={qqFocus.search}
          focusTick={qqFocus.tick}
          onInspectAccount={(id, name) => void inspectAccountRoles(id, name)}
          onFocusClears={focusClears}
          onBanAccount={(id, label) =>
            void prepareBan("account", id, label || id)
          }
          canBan={canBan}
        />
      ) : (
        <ModerationPage
          {...props}
          tab={tab}
          accountFocus={accountFocus}
          banRevision={banRevision}
          canWrite={canWrite}
          hasConfirmedBan={!!pending?.confirmed}
          resolvingAccount={resolvingAccount}
          onViewAccount={focusClears}
          onFocusQQ={focusQQ}
          onInspectCharacterEquipped={inspectCharacterEquipped}
          onInspectCharacterGm={inspectCharacterGm}
          onInspectAccount={(id, name) => void inspectAccountRoles(id, name)}
          onPrepareBan={prepareBan}
        />
      )}
      {pending && show && (
        <Modal
          title={pending.change.revoke ? "解除封禁" : "确认封禁"}
          onClose={closeBan}
          dismissOnBackdrop={!busy}
          footer={
            <>
              <button className="secondary" disabled={busy} onClick={closeBan}>
                {pending.confirmed ? "稍后继续" : "取消"}
              </button>
              <button
                className="primary"
                disabled={busy || (!pending.confirmed && !reason.trim())}
                onClick={() => void confirmBan()}
              >
                {busy ? <LoaderCircle size={16} className="spin" /> : null}
                {pending.confirmed
                  ? "使用原请求重试"
                  : pending.change.revoke
                    ? "确认解除"
                    : "确认封禁"}
              </button>
            </>
          }
        >
          <div className="moderation-confirm">
            <strong>{pending.label}</strong>
            <p>
              {pending.change.kind === "ip" ? "IP" : "账号"}：
              {pending.change.target}
            </p>
            <p>
              {pending.change.revoke
                ? "解除本条封禁记录。"
                : pending.change.kind === "ip"
                  ? "阻止此 IP 注册、登录和进入游戏，并断开匹配的游戏连接。"
                  : "阻止此账号登录和进入游戏，并断开当前游戏连接。"}
            </p>
            {pending.confirmed ? (
              <>
                <p>原因：{pending.change.reason}</p>
                <p>
                  期限：
                  {pending.change.expiresAt
                    ? stamp(pending.change.expiresAt)
                    : "永久"}
                </p>
              </>
            ) : (
              <>
                <Field label="原因">
                  <textarea
                    maxLength={160}
                    value={reason}
                    onChange={(e) => setReason(e.target.value)}
                    autoFocus
                  />
                </Field>
                {!pending.change.revoke && (
                  <Field label="封禁期限">
                    <select
                      value={hours}
                      onChange={(e) => setHours(e.target.value)}
                    >
                      <option value="1">1 小时</option>
                      <option value="24">1 天</option>
                      <option value="168">7 天</option>
                      <option value="720">30 天</option>
                      <option value="0">永久</option>
                    </select>
                  </Field>
                )}
              </>
            )}
            {pending.targets && !pending.change.revoke && (
              <div className="moderation-linked">
                <strong>同时处理</strong>
                {(
                  [
                    ["lastLoginIp", "最近登录 IP"],
                    ["registrationIp", "注册 IP"],
                  ] as const
                ).map(([key, label]) => (
                  <label key={key}>
                    <input
                      type="checkbox"
                      disabled={
                        busy || pending.confirmed || !pending.targets?.[key]
                      }
                      checked={!!pending.change.linked?.[key]}
                      onChange={(e) =>
                        setPending({
                          ...pending,
                          change: {
                            ...pending.change,
                            linked: {
                              ...pending.change.linked,
                              [key]: e.target.checked
                                ? pending.targets?.[key]
                                : undefined,
                            },
                          },
                        })
                      }
                    />
                    {label}：{pending.targets?.[key] || "无记录"}
                  </label>
                ))}
                <label>
                  <input
                    type="checkbox"
                    disabled={
                      busy ||
                      pending.confirmed ||
                      !pending.targets.qq ||
                      !pending.targets.groups?.length
                    }
                    checked={!!pending.change.linked?.qq}
                    onChange={(e) =>
                      setPending({
                        ...pending,
                        change: {
                          ...pending.change,
                          linked: {
                            ...pending.change.linked,
                            qq: e.target.checked
                              ? pending.targets?.qq
                              : undefined,
                            groups: e.target.checked
                              ? pending.targets?.groups
                              : undefined,
                            settingsRevision: e.target.checked
                              ? pending.targets?.settingsRevision
                              : undefined,
                          },
                        },
                      })
                    }
                  />
                  踢出群聊并加入黑名单：{pending.targets.qq || "未绑定 QQ"}
                </label>
                {pending.targets.qq && (
                  <small>
                    群：{pending.targets.groups?.join("、") || "未配置"}。QQ
                    黑名单持续生效；解除账号封禁后仍保留。
                  </small>
                )}
              </div>
            )}
            {writeError && <p role="alert">{writeError}</p>}
          </div>
        </Modal>
      )}
    </div>
  );
}

function ModerationPage({
  state,
  openAccount,
  tab,
  accountFocus,
  banRevision,
  canWrite,
  hasConfirmedBan,
  resolvingAccount,
  onViewAccount,
  onFocusQQ,
  onInspectCharacterEquipped,
  onInspectCharacterGm,
  onInspectAccount,
  onPrepareBan,
}: PageProps & {
  tab: ModerationTab;
  accountFocus: string;
  banRevision: number;
  canWrite: boolean;
  hasConfirmedBan: boolean;
  resolvingAccount: string;
  onViewAccount: (id: string) => void;
  onFocusQQ: (id: string) => void;
  onInspectCharacterEquipped: (characterId: string) => void;
  onInspectCharacterGm: (characterId: string) => void;
  onInspectAccount: (accountId: string, accountName?: string) => void;
  onPrepareBan: (
    kind: "account" | "ip",
    target: string,
    label: string,
    revoke?: boolean,
    existing?: Ban,
  ) => Promise<void>;
}) {
  const statistics = tab === "clears" || tab === "account-stats";
  const live = useLive();
  const saved = readAntiCheatMemory(live);

  const [hideBanned, setHideBanned] = useState<boolean>(
    saved.hideBanned ?? true,
  );
  const [q, setQ] = useState<string>(saved.q ?? ""),
    [from, setFrom] = useState<string>(
      () => saved.from || dateInput(new Date(Date.now() - 86400000)),
    ),
    [to, setTo] = useState<string>(() => saved.to || dateInput(new Date()));
  const [seconds, setSeconds] = useState<number>(saved.seconds ?? 30),
    [hourly, setHourly] = useState<number>(saved.hourly ?? 60),
    [filter, setFilter] = useState<string>(saved.filter ?? "all");
  const [sorts, setSorts] = useState<Record<ModerationTab, Sort>>(
    () => ({ ...defaultSorts(), ...(saved.sorts || {}) }),
  );
  const [ip, setIP] = useState("");
  const read = useRef<AbortController | null>(null);
  const modRefreshRev = state.api?.resources.moderationRefresh?.revision || 0;
  const hideRangeMembers =
    tab === "bans" && !!state.api?.features["ipRanges.write"];

  function buildKey(
    which: ModerationTab,
    nextOffset: number,
    sorting: Sort,
    searchText = q,
    focus = accountFocus,
    bannedFlag = hideBanned,
    fromVal = from,
    toVal = to,
    filterVal = filter,
    secVal = seconds,
    hrVal = hourly,
  ) {
    return JSON.stringify({
      which,
      offset: nextOffset,
      sort: sorting,
      q: searchText,
      accountFocus: which === "clears" || which === "account-stats" ? focus : "",
      hideBanned:
        which === "clears" || which === "account-stats" ? bannedFlag : false,
      from: which === "clears" || which === "account-stats" ? fromVal : "",
      to: which === "clears" || which === "account-stats" ? toVal : "",
      filter: which === "clears" || which === "account-stats" ? filterVal : "",
      seconds:
        (which === "clears" || which === "account-stats") &&
        filterVal === "fast"
          ? secVal
          : 0,
      hourly:
        (which === "clears" || which === "account-stats") &&
        filterVal === "frequent"
          ? hrVal
          : 0,
      hideRange: which === "bans" && !!state.api?.features["ipRanges.write"],
      modRefreshRev,
      banRevision,
    });
  }

  const initialOffset =
    saved.cachedModeration?.tab === tab && !saved.forceReload
      ? saved.cachedModeration.offset || 0
      : saved.offset || 0;
  const initialKey = buildKey(tab, initialOffset, sorts[tab]);
  const hasMatchingCache =
    !saved.forceReload && saved.cachedModeration?.key === initialKey;

  const [page, setPage] = useState<Page<any>>(() =>
      hasMatchingCache
        ? saved.cachedModeration.page
        : { rows: [], hasMore: false },
    ),
    [offset, setOffset] = useState<number>(initialOffset),
    [loaded, setLoaded] = useState<boolean>(hasMatchingCache),
    [loading, setLoading] = useState(false);

  useEffect(() => {
    writeAntiCheatMemory(live, {
      q,
      from,
      to,
      seconds,
      hourly,
      filter,
      hideBanned,
      sorts,
    });
  }, [live, q, from, to, seconds, hourly, filter, hideBanned, sorts]);

  async function load(next = 0, which = tab, sorting = sorts[which]) {
    read.current?.abort();
    const controller = new AbortController();
    read.current = controller;
    const key = buildKey(which, next, sorting);
    if (!live) {
      setOffset(next);
      setLoaded(true);
      writeAntiCheatMemory(null, {
        offset: next,
        forceReload: undefined,
        cachedModeration: {
          tab: which,
          key,
          offset: next,
          page: { rows: [], hasMore: false },
        },
      });
      return;
    }
    setLoading(true);
    await live.guard(async () => {
      try {
        const params: Record<string, string | number | undefined> = {
          search: q,
          offset: next,
          limit: 50,
          ...sorting,
        };
        if (which === "clears" || which === "account-stats")
          Object.assign(params, {
            accountId: accountFocus || undefined,
            hideBanned: String(hideBanned),
            from: new Date(from).toISOString(),
            to: new Date(to).toISOString(),
            maxDurationMs: filter === "fast" ? seconds * 1000 : 0,
            minHourlyPeak: filter === "frequent" ? hourly : 0,
          });
        if (which === "bans" && state.api?.features["ipRanges.write"])
          params.hideRangeMembers = "true";
        await live.http.observe<Page<any>>(
          query(`/api/v1/anti-cheat/${which}`, params),
          controller.signal,
          ({ data }) => {
            if (!controller.signal.aborted) {
              setPage(data);
              setOffset(next);
              setLoaded(true);
              writeAntiCheatMemory(live, {
                offset: next,
                forceReload: undefined,
                cachedModeration: {
                  tab: which,
                  key,
                  offset: next,
                  page: data,
                },
              });
            }
          },
        );
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    });
  }

  useEffect(() => {
    const mem = readAntiCheatMemory(live);
    let nextFilter = filter;
    let nextQ = q;
    if (mem.forceReload) {
      if (typeof mem.filter === "string" && mem.filter !== filter) {
        nextFilter = mem.filter;
        setFilter(mem.filter);
      }
      if (typeof mem.q === "string" && mem.q !== q) {
        nextQ = mem.q;
        setQ(mem.q);
      }
    }
    const targetOffset =
      !mem.forceReload && mem.cachedModeration?.tab === tab
        ? mem.cachedModeration.offset || 0
        : 0;
    const expectedKey = buildKey(
      tab,
      targetOffset,
      sorts[tab],
      nextQ,
      accountFocus,
      hideBanned,
      from,
      to,
      nextFilter,
      seconds,
      hourly,
    );
    if (!mem.forceReload && mem.cachedModeration?.key === expectedKey) {
      setPage(mem.cachedModeration.page);
      setOffset(targetOffset);
      setLoaded(true);
      return () => read.current?.abort();
    }
    setPage({ rows: [], hasMore: false });
    setLoaded(false);
    void load(targetOffset, tab, sorts[tab]);
    return () => read.current?.abort();
  }, [
    tab,
    live,
    accountFocus,
    hideBanned,
    hideRangeMembers,
    modRefreshRev,
    banRevision,
  ]);

  function changeSort(field: string) {
    const next = nextSort(sorts[tab], field);
    setSorts((previous) => ({ ...previous, [tab]: next }));
    void load(0, tab, next);
  }
  const header = (fields: [string, string][]) => (
    <SortHeader
      fields={fields}
      sort={sorts[tab]}
      onSort={changeSort}
      disabled={loading}
    />
  );
  const rows = page.rows as ClearStats[];
  const sampled = rows.reduce((n, c) => n + (c.fatigueSamples || 0), 0);
  const spent = rows.reduce((n, c) => n + (c.fatigueSpent || 0), 0);
  return (
    <div className="motion-section moderation-page">
      <div className="moderation-filters glass">
        <Field
          label={
            tab === "account-stats"
              ? "账号 / 账号 ID"
              : tab === "clears"
                ? "角色 / 账号 ID / 地下城"
                : "账号 / ID / IP"
          }
        >
          <input
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="输入关键词"
            onKeyDown={(e) => {
              if (e.key === "Enter") void load(0);
            }}
          />
        </Field>
        {statistics && (
          <>
            <Field label="开始时间">
              <input
                type="datetime-local"
                value={from}
                onChange={(e) => setFrom(e.target.value)}
              />
            </Field>
            <Field label="结束时间">
              <input
                type="datetime-local"
                value={to}
                onChange={(e) => setTo(e.target.value)}
              />
            </Field>
            <Field label="筛选">
              <select
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
              >
                <option value="all">全部通关</option>
                <option value="fast">低于耗时阈值</option>
                <option value="frequent">高于频率阈值</option>
              </select>
            </Field>
            <Field label="耗时阈值（秒）">
              <input
                type="number"
                min="1"
                max="86400"
                value={seconds}
                onChange={(e) => setSeconds(Number(e.target.value))}
              />
            </Field>
            <Field label="单小时次数阈值">
              <input
                type="number"
                min="1"
                max="100000"
                value={hourly}
                onChange={(e) => setHourly(Number(e.target.value))}
              />
            </Field>
          </>
        )}
        {statistics && (
          <label className="moderation-hide-banned">
            <input
              type="checkbox"
              checked={hideBanned}
              onChange={(e) => setHideBanned(e.target.checked)}
            />
            隐藏已封禁账号
          </label>
        )}
        {tab === "clears" && accountFocus && (
          <span>
            账号：{accountFocus}
            <button className="text-button" onClick={() => onViewAccount("")}>
              清除筛选
            </button>
          </span>
        )}
        <button
          className="primary"
          disabled={loading}
          onClick={() => void load(0)}
        >
          {loading ? <DelayedSpinner active size={16} /> : <Search size={16} />}
          查询
        </button>
      </div>
      {tab === "account-stats" ? (
        <>
          <p className="moderation-caption">
            合并账号下所有角色的成功通关。单小时峰值按同一整小时汇总；每秒疲劳消耗使用已采集且耗时有效的局次总疲劳
            ÷ 总秒数，未采集显示“—”。
          </p>
          <div
            className="table-panel glass moderation-table query-surface"
            aria-busy={loading}
          >
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    {header([
                      ["account", "账号名"],
                      ["accountId", "账号 ID"],
                    ])}
                    {header([["count", "次数"]])}
                    {header([
                      ["fastestMs", "最快"],
                      ["averageMs", "平均"],
                    ])}
                    {header([["fatigueSpent", "消耗疲劳"]])}
                    {header([["fatiguePerSecond", "平均每秒疲劳消耗"]])}
                    {header([["peakHourly", "单小时峰值"]])}
                    {header([["lastClearAt", "最近通关时间"]])}
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {(page.rows as AccountClearStats[]).map((a) => (
                    <tr key={a.accountId}>
                      <td>
                        <button
                          type="button"
                          className="link-strong"
                          onClick={() =>
                            onInspectAccount(
                              a.accountId,
                              a.account || a.accountId,
                            )
                          }
                        >
                          {a.account || a.accountId}
                        </button>
                        <small>{a.accountId}</small>
                      </td>
                      <td>{a.count}</td>
                      <td>
                        {duration(a.fastestMs)}
                        <small>{duration(a.averageMs)}</small>
                      </td>
                      <td>
                        {fatigue(a.fatigueSpent)}
                        <small>
                          {a.fatigueSamples}/{a.count} 局已采集
                        </small>
                      </td>
                      <td>
                        {a.fatiguePerSecond == null
                          ? "—"
                          : `${a.fatiguePerSecond.toFixed(3)} / 秒`}
                      </td>
                      <td
                        className={
                          a.peakHourly >= hourly ? "moderation-flag" : ""
                        }
                      >
                        {a.peakHourly} 次
                      </td>
                      <td>{stamp(a.lastClearAt)}</td>
                      <td>
                        <div className="moderation-actions">
                          <button
                            className="text-button"
                            onClick={() => {
                              setQ("");
                              onViewAccount(a.accountId);
                            }}
                          >
                            查看通关统计
                          </button>
                          <button
                            className="text-button"
                            disabled={resolvingAccount === a.accountId}
                            onClick={() =>
                              onInspectAccount(
                                a.accountId,
                                a.account || a.accountId,
                              )
                            }
                          >
                            查角色
                          </button>
                          <button
                            className="text-button"
                            onClick={() => onFocusQQ(a.accountId)}
                          >
                            查QQ
                          </button>
                          <button
                            className="text-button"
                            disabled={!canWrite || hasConfirmedBan}
                            onClick={() =>
                              void onPrepareBan(
                                "account",
                                a.accountId,
                                a.account || a.accountId,
                              )
                            }
                          >
                            封禁账号
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {loading && !loaded && <LoadingSkeleton />}
            {loaded && !page.rows.length && (
              <Empty title="当前条件下没有账号通关记录" />
            )}
          </div>
        </>
      ) : tab === "clears" ? (
        <>
          <div className="moderation-overview glass">
            <div>
              <ShieldCheck size={19} />
              <span>
                当前页角色 × 地下城<strong>{page.rows.length}</strong>
              </span>
            </div>
            <div>
              <Timer size={19} />
              <span>
                当前页通关
                <strong>{rows.reduce((n, c) => n + c.count, 0)}</strong>
              </span>
            </div>
            <div>
              <BanIcon size={19} />
              <span>
                达到阈值
                <strong>
                  {rows.filter((c) => suspicious(c, seconds, hourly)).length}
                </strong>
              </span>
            </div>
            <div>
              <Zap size={19} />
              <span>
                当前页已采集疲劳<strong>{sampled ? spent : "—"}</strong>
              </span>
            </div>
          </div>
          <p className="moderation-caption">
            统计采集启用后的成功结算。耗时从服务器接受入场到通关；疲劳按成功通关局内的实际扣除累计，平均值只计算已采集局次，未采集显示“—”。频率为每个整小时的通关峰值。阈值用于筛选，封禁由管理员确认。
          </p>
          <div
            className="table-panel glass moderation-table query-surface"
            aria-busy={loading}
          >
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    {header([
                      ["characterName", "角色"],
                      ["characterId", "角色 ID"],
                      ["accountId", "账号"],
                    ])}
                    {header([
                      ["dungeonName", "地下城"],
                      ["dungeonId", "ID"],
                      ["difficulty", "难度"],
                    ])}
                    {header([["count", "次数"]])}
                    {header([
                      ["fastestMs", "最快"],
                      ["averageMs", "平均"],
                    ])}
                    {header([
                      ["fatigueSpent", "消耗疲劳"],
                      ["averageFatigue", "平均每次"],
                    ])}
                    {header([["peakHourly", "单小时峰值"]])}
                    {header([["lastClearAt", "最近通关"]])}
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((c) => (
                    <tr key={`${c.characterId}/${c.dungeonId}/${c.difficulty}`}>
                      <td>
                        <button
                          type="button"
                          className="link-strong"
                          onClick={() =>
                            onInspectCharacterEquipped(c.characterId)
                          }
                        >
                          {c.characterName || c.characterId}
                        </button>
                        <small>
                          角色 {c.characterId} · {c.accountId}
                        </small>
                      </td>
                      <td>
                        {c.dungeonName || c.dungeonId}
                        <small>
                          ID {c.dungeonId} · 难度 {c.difficulty}
                        </small>
                      </td>
                      <td>{c.count}</td>
                      <td
                        className={
                          c.fastestMs > 0 && c.fastestMs <= seconds * 1000
                            ? "moderation-flag"
                            : ""
                        }
                      >
                        {duration(c.fastestMs)}
                        <small>{duration(c.averageMs)}</small>
                      </td>
                      <td>
                        {fatigue(c.fatigueSpent)}
                        <small>
                          平均 {fatigue(c.averageFatigue, true)} ·{" "}
                          {c.fatigueSamples || 0}/{c.count} 局已采集
                        </small>
                      </td>
                      <td
                        className={
                          c.peakHourly >= hourly ? "moderation-flag" : ""
                        }
                      >
                        {c.peakHourly} 次
                      </td>
                      <td>{stamp(c.lastClearAt)}</td>
                      <td>
                        <div className="moderation-actions">
                          <button
                            className="text-button"
                            onClick={() =>
                              onInspectCharacterEquipped(c.characterId)
                            }
                          >
                            查穿戴
                          </button>
                          <button
                            className="text-button"
                            onClick={() => onInspectCharacterGm(c.characterId)}
                          >
                            GM
                          </button>
                          <button
                            className="text-button"
                            disabled={!canWrite || hasConfirmedBan}
                            onClick={() =>
                              void onPrepareBan(
                                "account",
                                c.accountId,
                                c.accountId,
                              )
                            }
                          >
                            封禁账号
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {loading && !loaded && <LoadingSkeleton />}
            {loaded && !rows.length && (
              <Empty title="当前条件下没有通关记录" />
            )}
          </div>
        </>
      ) : tab === "accounts" ? (
        <div
          className="table-panel glass moderation-table query-surface"
          aria-busy={loading}
        >
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  {header([
                    ["name", "账号"],
                    ["id", "ID"],
                  ])}
                  {header([["lastLoginIp", "最近登录 IP"]])}
                  {header([["registrationIp", "注册 IP"]])}
                  {header([["disabled", "登录限制"]])}
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {(page.rows as ModerationAccount[]).map((a) => (
                  <tr key={a.id}>
                    <td>
                      <button
                        type="button"
                        className="link-strong"
                        onClick={() => onInspectAccount(a.id, a.name)}
                      >
                        {a.name}
                      </button>
                      <small>{a.id}</small>
                    </td>
                    <td>{a.lastLoginIp || "—"}</td>
                    <td>{a.registrationIp || "—"}</td>
                    <td>{a.disabled ? "已限制" : "正常"}</td>
                    <td>
                      <div className="moderation-actions">
                        <button
                          className="text-button"
                          disabled={resolvingAccount === a.id}
                          onClick={() => onInspectAccount(a.id, a.name)}
                        >
                          查角色
                        </button>
                        {openAccount && (
                          <button
                            className="text-button"
                            onClick={() => openAccount(a.id, a.name)}
                          >
                            管理账号
                          </button>
                        )}
                        <button
                          className="text-button"
                          onClick={() => onFocusQQ(a.id)}
                        >
                          查QQ
                        </button>
                        <button
                          className="text-button"
                          disabled={!canWrite || hasConfirmedBan}
                          onClick={() =>
                            void onPrepareBan("account", a.id, a.name)
                          }
                        >
                          封禁账号
                        </button>
                        {a.lastLoginIp && (
                          <button
                            className="text-button"
                            disabled={!canWrite || hasConfirmedBan}
                            onClick={() =>
                              void onPrepareBan(
                                "ip",
                                a.lastLoginIp!,
                                a.lastLoginIp!,
                              )
                            }
                          >
                            封禁登录 IP
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {loading && !loaded && <LoadingSkeleton />}
          {loaded && !page.rows.length && <Empty title="没有匹配的账号" />}
        </div>
      ) : (
        <>
          {state.api?.features["ipRanges.write"] && <IPRangeBans />}
          <div className="moderation-ip glass">
            <Field label="封禁 IP">
              <input
                value={ip}
                onChange={(e) => setIP(e.target.value)}
                placeholder="IPv4 或 IPv6 地址"
              />
            </Field>
            <button
              className="secondary"
              disabled={!canWrite || !ip.trim() || hasConfirmedBan}
              onClick={() => void onPrepareBan("ip", ip.trim(), ip.trim())}
            >
              <BanIcon size={15} />
              封禁 IP
            </button>
          </div>
          <div
            className="table-panel glass moderation-table query-surface"
            aria-busy={loading}
          >
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    {header([
                      ["label", "目标"],
                      ["kind", "类型"],
                      ["target", "ID / IP"],
                    ])}
                    {header([["state", "状态"]])}
                    {header([["reason", "原因"]])}
                    {header([["expiresAt", "到期时间"]])}
                    {header([["updatedAt", "更新时间"]])}
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {(page.rows as Ban[]).map((b) => (
                    <tr key={`${b.kind}/${b.target}`}>
                      <td>
                        {b.kind === "account" ? (
                          <button
                            type="button"
                            className="link-strong"
                            onClick={() => onInspectAccount(b.target, b.label)}
                          >
                            {b.label}
                          </button>
                        ) : (
                          <strong>{b.label}</strong>
                        )}
                        <small>
                          {b.kind === "ip" ? "IP 地址" : "账号"} · {b.target}
                        </small>
                      </td>
                      <td>{banState(b)}</td>
                      <td>
                        {b.reason}
                        {b.qqActions?.map((a) => (
                          <small key={a.id}>{qqActionLabel(a)}</small>
                        ))}
                      </td>
                      <td>{b.expiresAt ? stamp(b.expiresAt) : "永久"}</td>
                      <td>{stamp(b.updatedAt)}</td>
                      <td>
                        <div className="moderation-actions">
                          {b.kind === "account" && (
                            <button
                              className="text-button"
                              disabled={resolvingAccount === b.target}
                              onClick={() =>
                                onInspectAccount(b.target, b.label)
                              }
                            >
                              查角色
                            </button>
                          )}
                          <button
                            className="text-button"
                            disabled={!canWrite || hasConfirmedBan}
                            onClick={() =>
                              void onPrepareBan(
                                b.kind,
                                b.target,
                                b.label,
                                banState(b) === "封禁中",
                                b,
                              )
                            }
                          >
                            {banState(b) === "封禁中" ? (
                              <>
                                <UnlockKeyhole size={14} />
                                解除封禁
                              </>
                            ) : (
                              "重新封禁"
                            )}
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {loading && !loaded && <LoadingSkeleton />}
            {loaded && !page.rows.length && <Empty title="没有封禁记录" />}
          </div>
        </>
      )}
      <div className="moderation-pagination">
        <span>
          第 {offset / 50 + 1} 页 · 当前 {page.rows.length} 条
        </span>
        <button
          className="secondary"
          disabled={loading || offset === 0}
          onClick={() => void load(Math.max(0, offset - 50))}
        >
          上一页
        </button>
        <button
          className="secondary"
          disabled={loading || !page.hasMore}
          onClick={() => void load(offset + 50)}
        >
          下一页
        </button>
        <button
          className="text-button"
          disabled={loading}
          onClick={() => {
            live?.http.invalidate(["moderation"]);
            void load(offset);
          }}
        >
          <RefreshCw size={14} />
          刷新
        </button>
      </div>
    </div>
  );
}
