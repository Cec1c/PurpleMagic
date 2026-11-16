import { LoadingSkeleton } from "../Loading";
import { useState, useEffect } from "react";
import { Mail, ArrowUpRight, Users, UserRound, Search, Backpack, Shield, WandSparkles, Settings2, Clock3 } from "lucide-react";
import { useLive } from "../api/context";
import { LoadMore } from "../api/controls";
import { query } from "../api/http";
import { characterDTO } from "../api/console";
import { resolveRecipients } from "../domain";
import {
  readDirectoryMemory,
  writeDirectoryMemory,
  writeInventoryTabMemory,
} from "../navigation-state";
import { ResetPassword } from "./ResetPassword";
import type { State, Selection, Account, Character } from "../domain";
import {
  SearchField,
  Tabs,
  Pill,
  CheckBox,
  toggle,
  Empty,
  number,
  type PageProps,
} from "../ui";

export function PlayerPicker({
  state,
  selection,
  setSelection,
  onChoose,
  onManageAccount,
  pickOnly = false,
}: {
  state: State;
  selection: Selection;
  setSelection: (v: Selection) => void;
  onChoose?: (id: string) => void;
  onManageAccount?: (accountId: string, accountName?: string) => void;
  pickOnly?: boolean;
}) {
  const live = useLive();
  const [q, setQ] = useState("");
  const [quickResult, setQuickResult] = useState<{
    characters: Character[];
    accounts: Account[];
    qqMatches: { qq: string; accountId: string; accountName: string }[];
    accountRoles: Record<string, Character[]>;
  } | null>(null);
  const [searching, setSearching] = useState(false);

  useEffect(() => {
    if (!live) return;
    const trimmed = q.trim();
    if (!trimmed) {
      setQuickResult(null);
      setSearching(false);
      const timer = setTimeout(
        () => void live.guard(() => live.directory("characters", "")),
        120,
      );
      return () => {
        clearTimeout(timer);
        live.cancelScope("directory");
      };
    }
    const ctrl = new AbortController();
    setSearching(true);
    const timer = setTimeout(() => {
      void live
        .guard(async () => {
          const res = await live.quickSearch(trimmed, ctrl.signal);
          if (!ctrl.signal.aborted) setQuickResult(res);
        })
        .finally(() => {
          if (!ctrl.signal.aborted) setSearching(false);
        });
    }, 220);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [live, q]);

  const isQuickMode = !!live && !!q.trim();
  const rows = isQuickMode
    ? (() => {
        if (!quickResult) return [];
        const byId = new Map<string, Character>();
        for (const c of quickResult.characters) byId.set(c.id, c);
        for (const list of Object.values(quickResult.accountRoles)) {
          for (const c of list) if (!byId.has(c.id)) byId.set(c.id, c);
        }
        return [...byId.values()];
      })()
    : state.characters.filter((c) =>
        `${c.name} ${c.id} ${state.accounts.find((a) => a.id === c.accountId)?.name}`
          .toLowerCase()
          .includes(q.toLowerCase()),
      );

  const displayAccounts: Account[] = isQuickMode
    ? (() => {
        if (!quickResult) return [];
        const map = new Map<string, Account>();
        for (const a of quickResult.accounts) map.set(a.id, a);
        for (const c of rows) {
          if (!map.has(c.accountId)) {
            const known = state.accounts.find((a) => a.id === c.accountId);
            map.set(
              c.accountId,
              known || {
                id: c.accountId,
                name: c.accountId,
                points: 0,
                tokens: 0,
                status: "正常",
              },
            );
          }
        }
        return [...map.values()];
      })()
    : state.accounts.filter((a) =>
        q.trim()
          ? rows.some((c) => c.accountId === a.id) ||
            `${a.name} ${a.id}`.toLowerCase().includes(q.toLowerCase())
          : rows.some((c) => c.accountId === a.id),
      );

  const recent = state.api?.recentCharacters || [];

  return (
    <>
      <SearchField
        placeholder="同时搜索角色名、账号名、ID 或绑定 QQ 号"
        value={q}
        onChange={setQ}
      />
      {pickOnly && !q.trim() && recent.length > 0 && (
        <div className="picker-recent">
          <div className="picker-recent-head">
            <Clock3 size={13} />
            <span>最近访问角色</span>
          </div>
          <div className="recent-character-grid compact">
            {recent.map((rc) => (
              <button
                key={rc.id}
                type="button"
                className="recent-character-card"
                onClick={() => onChoose?.(rc.id)}
              >
                <strong>{rc.name}</strong>
                <small>
                  Lv.{rc.level} · {rc.job}
                </small>
              </button>
            ))}
          </div>
        </div>
      )}
      <div className="picker-list">
        {displayAccounts.map((a) => {
          const accountChars = rows.filter((c) => c.accountId === a.id);
          const qq = quickResult?.qqMatches.find((m) => m.accountId === a.id)?.qq;
          return (
            <section key={a.id}>
              <div className="picker-account">
                {!pickOnly && (
                  <CheckBox
                    label={`账号 ${a.name} 的全部现有角色`}
                    checked={selection.accounts.includes(a.id)}
                    onChange={() =>
                      setSelection({
                        ...selection,
                        accounts: toggle(selection.accounts, a.id),
                      })
                    }
                  />
                )}
                <Users size={15} />
                <strong>{a.name}</strong>
                {a.name !== a.id && <small className="mono">{a.id}</small>}
                {qq && <Pill tone="cyan">QQ {qq}</Pill>}
                {a.status === "限制登录" && <Pill tone="orange">限制登录</Pill>}
                <small>
                  {Number.isFinite(a.characterCount) ? a.characterCount : accountChars.length} 个角色
                </small>
                {onManageAccount && (
                  <button
                    type="button"
                    className="text-button picker-manage-account"
                    onClick={() => onManageAccount(a.id, a.name)}
                  >
                    <Settings2 size={13} />
                    管理账号
                  </button>
                )}
              </div>
              {accountChars.length === 0 ? (
                <div className="picker-empty-account muted">
                  该账号暂无角色{onManageAccount ? "，可点击右上「管理账号」调整点券、黑钻或重置密码" : ""}
                </div>
              ) : (
                accountChars.map((c) => (
                  <div className="picker-character" key={c.id}>
                    {!pickOnly && (
                      <CheckBox
                        label={`收件角色 ${c.name}`}
                        checked={
                          selection.accounts.includes(c.accountId) ||
                          selection.characters.includes(c.id)
                        }
                        onChange={() => {
                          if (selection.accounts.includes(c.accountId)) {
                            setSelection({
                              accounts: selection.accounts.filter(
                                (accId) => accId !== c.accountId,
                              ),
                              characters: [
                                ...new Set([
                                  ...selection.characters.filter(
                                    (id) => id !== c.id,
                                  ),
                                  ...state.characters
                                    .filter(
                                      (x) =>
                                        x.accountId === c.accountId &&
                                        x.id !== c.id,
                                    )
                                    .map((x) => x.id),
                                ]),
                              ],
                            });
                          } else
                            setSelection({
                              ...selection,
                              characters: toggle(selection.characters, c.id),
                            });
                        }}
                      />
                    )}
                    <UserRound size={17} />
                    <div>
                      <strong>{c.name}</strong>
                      <small>
                        Lv.{c.level} · {c.job} · ID {c.id}
                      </small>
                    </div>
                    <span className={`status ${c.online ? "online" : ""}`}>
                      <i />
                      {c.online === null
                        ? "状态未知"
                        : c.online
                          ? "在线"
                          : "离线"}
                    </span>
                    {pickOnly && (
                      <button
                        className="secondary"
                        onClick={() => onChoose?.(c.id)}
                      >
                        选择
                      </button>
                    )}
                  </div>
                ))
              )}
            </section>
          );
        })}
      </div>
      {searching && !displayAccounts.length && <LoadingSkeleton rows={3} />}
      {!searching && !displayAccounts.length && (
        <Empty title="没有匹配的角色或账号" detail="可输入角色名、账号名、ID 或绑定 QQ 号检索" />
      )}
      {!isQuickMode && (
        <LoadMore
          show={!!state.api?.directory.next}
          onClick={() =>
            void live?.guard(() => live.directory("characters", q, true))
          }
        />
      )}
      {!pickOnly && (
        <p className="muted">
          已选择 {resolveRecipients(state, selection).length}{" "}
          个角色；选择账号会包含该账号全部现有角色。
        </p>
      )}
    </>
  );
}
export function Players(props: PageProps & { accountMode?: boolean }) {
  const live = useLive();
  const {
    state,
    selection,
    setSelection,
    selectCharacter,
    go,
    notify,
    openAccount,
    accountMode = false,
  } = props;
  const [resetAccount, setResetAccount] = useState<{ id: string; name: string } | null>(null);
  const initialDir = readDirectoryMemory(live);
  const hasSavedDir = !!(
    initialDir &&
    (initialDir.initialized ||
      initialDir.q ||
      initialDir.accountId ||
      (initialDir.online && initialDir.online !== "all") ||
      initialDir.ids.length > 0 ||
      initialDir.accountIds.length > 0)
  );
  const [mode, setMode] = useState<"characters" | "accounts">(
      hasSavedDir ? initialDir!.mode : accountMode ? "accounts" : "characters",
    ),
    [q, setQ] = useState(hasSavedDir ? initialDir!.q : ""),
    [accountScope, setAccountScope] = useState(hasSavedDir ? initialDir!.accountId || "" : ""),
    [online, setOnline] = useState(hasSavedDir ? initialDir!.online || "all" : "all");
  const [crossCharacters, setCrossCharacters] = useState<Character[]>([]);

  useEffect(() => {
    writeDirectoryMemory(live, { mode, q, accountId: accountScope, online });
    if (!live) return;
    const timer = setTimeout(
      () => void live.guard(() => live.directory(mode, q, false, accountScope, online)),
      220,
    );
    return () => {
      clearTimeout(timer);
      live.cancelScope("directory");
    };
  }, [live, mode, q, accountScope, online]);

  useEffect(() => {
    const trimmed = q.trim();
    if (mode !== "accounts" || !trimmed) {
      setCrossCharacters([]);
      return;
    }
    if (!live) {
      setCrossCharacters(
        state.characters
          .filter((c) => `${c.name} ${c.id}`.toLowerCase().includes(trimmed.toLowerCase()))
          .slice(0, 8),
      );
      return;
    }
    const ctrl = new AbortController();
    const timer = setTimeout(() => {
      void live.http
        .get<any[]>(query("/api/v1/characters", { q: trimmed, limit: 8 }), ctrl.signal)
        .then(({ data }) => {
          if (!ctrl.signal.aborted) {
            setCrossCharacters(
              (data || []).map((v) =>
                characterDTO(
                  v,
                  state.characters.find((c) => c.id === String(v.characterId)),
                ),
              ),
            );
          }
        })
        .catch(() => {
          if (!ctrl.signal.aborted) setCrossCharacters([]);
        });
    }, 240);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [live, mode, q]);

  const rows = state.characters.filter(
    (c) =>
      (!live || state.api?.directory.ids.includes(c.id)) &&
      (!!live || !accountScope || c.accountId === accountScope) &&
      (online === "all" ||
        (c.online !== null && (online === "online") === c.online)) &&
      (!!live ||
        `${c.name} ${c.job} ${c.id} ${c.accountId} ${state.accounts.find((a) => a.id === c.accountId)?.name}`
          .toLowerCase()
          .includes(q.toLowerCase())),
  );
  const accounts = state.accounts.filter((a) =>
    live
      ? state.api?.directory.accountIds.includes(a.id)
      : q.trim()
        ? `${a.name} ${a.id}`.toLowerCase().includes(q.toLowerCase()) ||
          rows.some((c) => c.accountId === a.id)
        : rows.some((c) => c.accountId === a.id),
  );
  const selected = resolveRecipients(state, selection);
  function open(id: string) {
    selectCharacter(id);
    go("inventory");
  }
  function openEquipped(id: string) {
    writeInventoryTabMemory(live, "equipped");
    selectCharacter(id);
    go("inventory");
  }
  function openGm(id: string) {
    selectCharacter(id);
    go("gm");
  }
  const listIds =
      mode === "accounts" ? accounts.map((a) => a.id) : rows.map((c) => c.id),
    checkedIds =
      mode === "accounts" ? selection.accounts : selection.characters;
  return (
    <>
      <div className="workspace-intro">
        <div>
          <h2>{accountMode ? "账号与角色目录" : "查找一位冒险家"}</h2>
          <p>
            {accountMode
              ? "查看账号归属、角色信息与共享资产"
              : "按账号、角色名或 ID 查询，选定后进入角色工作区"}
          </p>
        </div>
        <Pill>
          {state.accounts.length} 个账号 · {state.characters.length} 个角色
        </Pill>
      </div>
      <div className="toolbar">
        <Tabs
          label="查询范围"
          items={[
            { id: "characters", label: "角色" },
            { id: "accounts", label: "账号" },
          ]}
          value={mode}
          onChange={(v) => {
            setAccountScope("");
            setMode(v);
            setOnline("all");
          }}
        />
        <SearchField
          placeholder="账号 / 角色名 / ID"
          value={q}
          onChange={setQ}
        />
        <select
          aria-label="在线状态"
          value={online}
          onChange={(e) => {
            setOnline(e.target.value);
            if (e.target.value !== "all") setMode("characters");
          }}
        >
          <option value="all">全部状态</option>
          <option value="online">在线</option>
          <option value="offline">离线</option>
        </select>
      </div>
      <div className="table-panel glass query-surface" aria-busy={!!state.api?.reading}>
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th className="check-cell">
                  <CheckBox
                    label="全选查询结果"
                    checked={
                      listIds.length > 0 &&
                      listIds.every((id) => checkedIds.includes(id))
                    }
                    onChange={() =>
                      setSelection({
                        ...selection,
                        [mode]: listIds.every((id) => checkedIds.includes(id))
                          ? []
                          : listIds,
                      })
                    }
                  />
                </th>
                <th>{mode === "accounts" ? "账号" : "角色"}</th>
                <th>{mode === "accounts" ? "角色数" : "等级 / 职业"}</th>
                <th>{mode === "accounts" ? "点券" : "账号"}</th>
                <th>{mode === "accounts" && !live ? "代币券" : "状态"}</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {mode === "accounts"
                ? accounts.map((a) => (
                    <tr key={a.id}>
                      <td>
                        <CheckBox
                          label={`选择账号 ${a.name}`}
                          checked={selection.accounts.includes(a.id)}
                          onChange={() =>
                            setSelection({
                              ...selection,
                              accounts: toggle(selection.accounts, a.id),
                            })
                          }
                        />
                      </td>
                      <td>
                        <strong>{a.name}</strong>
                        <small className="cell-meta">{a.id}</small>
                      </td>
                      <td>
                        {
                          (Number.isFinite(a.characterCount) ? a.characterCount : state.characters.filter((c) => c.accountId === a.id).length)
                        }{" "}
                        个
                      </td>
                      <td>{number(a.rawPoints || a.points)}</td>
                      <td>{live ? a.status : number(a.tokens)}</td>
                      <td>
                        <div className="inline-jump-actions">
                          {openAccount && (
                            <button
                              type="button"
                              className="text-button"
                              onClick={() => openAccount(a.id, a.name)}
                            >
                              <Settings2 size={14} />
                              管理账号
                            </button>
                          )}
                          <button
                            type="button"
                            className="text-button"
                            onClick={() => {
                              setQ(live ? "" : a.name);
                              setAccountScope(live ? a.id : "");
                              setMode("characters");
                            }}
                          >
                            查看角色
                            <ArrowUpRight size={14} />
                          </button>
                          {accountMode && live?.meta.features["accounts.password-reset"] && (
                            <button
                              type="button"
                              className="text-button"
                              onClick={() => setResetAccount({ id: a.id, name: a.name })}
                            >
                              重置密码
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))
                : rows.map((c) => (
                    <tr key={c.id}>
                      <td>
                        <CheckBox
                          label={`选择角色 ${c.name}`}
                          checked={selected.includes(c.id)}
                          onChange={() => {
                            if (selection.accounts.includes(c.accountId))
                              setSelection({
                                accounts: selection.accounts.filter(
                                  (id) => id !== c.accountId,
                                ),
                                characters: [
                                  ...new Set([
                                    ...selected.filter((id) => id !== c.id),
                                  ]),
                                ],
                              });
                            else
                              setSelection({
                                ...selection,
                                characters: toggle(selection.characters, c.id),
                              });
                          }}
                        />
                      </td>
                      <td>
                        <button
                          className="character-link"
                          onClick={() => open(c.id)}
                        >
                          <span className="character-symbol">
                            <UserRound size={18} />
                          </span>
                          <span>
                            <strong>{c.name}</strong>
                            <small>ID {c.id}</small>
                          </span>
                        </button>
                      </td>
                      <td>
                        <strong>Lv.{c.level}</strong>
                        <small className="cell-meta">{c.job}</small>
                      </td>
                      <td>
                        {openAccount ? (
                          <button
                            type="button"
                            className="text-button"
                            onClick={() =>
                              openAccount(
                                c.accountId,
                                state.accounts.find((a) => a.id === c.accountId)?.name,
                              )
                            }
                          >
                            {state.accounts.find((a) => a.id === c.accountId)?.name || c.accountId}
                          </button>
                        ) : (
                          state.accounts.find((a) => a.id === c.accountId)?.name || c.accountId
                        )}
                      </td>
                      <td>
                        <span className={`status ${c.online ? "online" : ""}`}>
                          <i />
                          {c.online === null
                            ? "状态未知"
                            : c.online
                              ? "在线"
                              : "离线"}
                        </span>
                      </td>
                      <td>
                        <div className="inline-jump-actions">
                          <button
                            type="button"
                            className="text-button"
                            onClick={() => open(c.id)}
                          >
                            <Backpack size={14} />
                            背包
                          </button>
                          <button
                            type="button"
                            className="text-button"
                            onClick={() => openEquipped(c.id)}
                          >
                            <Shield size={14} />
                            穿戴
                          </button>
                          <button
                            type="button"
                            className="text-button"
                            onClick={() => openGm(c.id)}
                          >
                            <WandSparkles size={14} />
                            GM
                          </button>
                          <button
                            type="button"
                            className="text-button"
                            onClick={() => {
                              selectCharacter(c.id);
                              notify(`已切换当前角色为 ${c.name}`);
                            }}
                          >
                            选定
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
            </tbody>
          </table>
        </div>
        {!!state.api?.reading && !listIds.length && <LoadingSkeleton />}
        {!state.api?.reading && mode === "accounts" && accounts.length === 0 && crossCharacters.length > 0 && (
          <div className="cross-match-banner">
            <div className="cross-match-head">
              <span>
                未找到匹配账号，但在角色目录中找到 <strong>{crossCharacters.length}</strong> 位匹配角色：
              </span>
              <button
                type="button"
                className="secondary"
                onClick={() => setMode("characters")}
              >
                切换到角色视图
              </button>
            </div>
            <div className="cross-match-list">
              {crossCharacters.map((c) => (
                <div key={c.id} className="cross-match-card">
                  <div>
                    <strong>{c.name}</strong>
                    <small>
                      Lv.{c.level} · {c.job} · 账号 {c.accountId}
                    </small>
                  </div>
                  <div className="inline-jump-actions">
                    <button
                      type="button"
                      className="text-button"
                      onClick={() => open(c.id)}
                    >
                      背包
                    </button>
                    <button
                      type="button"
                      className="text-button"
                      onClick={() => openGm(c.id)}
                    >
                      GM
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
        {!state.api?.reading &&
          (mode === "accounts"
            ? accounts.length === 0 && crossCharacters.length === 0
            : rows.length === 0) && (
            <Empty title="没有匹配结果" detail="尝试其他账号、名称或 ID" />
          )}
        <div className="table-footer">
          <span>
            <Search size={14} />
            {mode === "accounts" ? accounts.length : rows.length} 条结果
          </span>
          <span>已选 {selected.length} 个收件角色</span>
        </div>
      </div>
      <LoadMore
        show={!!state.api?.directory.next}
        onClick={() =>
          void live?.guard(() => live.directory(mode, q, true, accountScope, online))
        }
      />
      {resetAccount && <ResetPassword account={resetAccount} onClose={() => setResetAccount(null)} />}
      {accountScope && (
        <button className="text-button" onClick={() => setAccountScope("")}>
          查看全部角色
        </button>
      )}
      {selected.length > 0 && (
        <div className="selection-bar glass">
          <div>
            <CheckBox
              label="清空所选收件人"
              checked={true}
              onChange={() => setSelection({ accounts: [], characters: [] })}
            />
            <strong>已选择 {selected.length} 个角色</strong>
            <span className="muted">
              {state.characters
                .filter((c) => selected.includes(c.id))
                .map((c) => c.name)
                .join("、")}
            </span>
          </div>
          <button className="primary" onClick={() => go("mail")}>
            <Mail size={16} />
            批量发邮件
          </button>
        </div>
      )}
    </>
  );
}
