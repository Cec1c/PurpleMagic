import { useState, useEffect } from "react";
import { Mail, ArrowUpRight, Users, UserRound, Search } from "lucide-react";
import { useLive } from "../api/context";
import { LoadMore } from "../api/controls";
import { resolveRecipients } from "../domain";
import type { State, Selection } from "../domain";
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
  pickOnly = false,
}: {
  state: State;
  selection: Selection;
  setSelection: (v: Selection) => void;
  onChoose?: (id: string) => void;
  pickOnly?: boolean;
}) {
  const live = useLive();
  const [q, setQ] = useState("");
  useEffect(() => {
    if (!live) return;
    const timer = setTimeout(
      () => void live.guard(() => live.directory("characters", q)),
      250,
    );
    return () => { clearTimeout(timer); live.cancelScope("directory"); };
  }, [live, q]);
  const rows = state.characters.filter((c) =>
    `${c.name} ${c.id} ${state.accounts.find((a) => a.id === c.accountId)?.name}`
      .toLowerCase()
      .includes(q.toLowerCase()),
  );
  return (
    <>
      <SearchField
        placeholder="搜索账号、角色名或 ID"
        value={q}
        onChange={setQ}
      />
      <div className="picker-list">
        {state.accounts
          .filter((a) => rows.some((c) => c.accountId === a.id))
          .map((a) => (
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
                <small>
                  {state.characters.filter((c) => c.accountId === a.id).length}{" "}
                  个角色
                </small>
              </div>
              {rows
                .filter((c) => c.accountId === a.id)
                .map((c) => (
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
                                (a) => a !== c.accountId,
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
                        Lv.{c.level} · {c.job}
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
                ))}
            </section>
          ))}
      </div>
      {!rows.length && <Empty title="没有匹配的角色" />}
      <LoadMore
        show={!!state.api?.directory.next}
        onClick={() =>
          void live?.guard(() => live.directory("characters", q, true))
        }
      />
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
    accountMode = false,
  } = props;
  const [mode, setMode] = useState<"characters" | "accounts">(
      accountMode ? "accounts" : "characters",
    ),
    [q, setQ] = useState(""),
    [accountScope, setAccountScope] = useState(""),
    [online, setOnline] = useState("all");
  useEffect(() => {
    if (!live) return;
    const timer = setTimeout(
      () => void live.guard(() => live.directory(mode, q, false, accountScope, online)),
      250,
    );
    return () => { clearTimeout(timer); live.cancelScope("directory"); };
  }, [live, mode, q, accountScope, online]);
  const rows = state.characters.filter(
    (c) =>
      (!live || state.api?.directory.ids.includes(c.id)) &&
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
      : rows.some((c) => c.accountId === a.id),
  );
  const selected = resolveRecipients(state, selection);
  function open(id: string) {
    go("inventory");
    selectCharacter(id);
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
          onChange={(e) => { setOnline(e.target.value); if (e.target.value !== "all") setMode("characters"); }}
        >
          <option value="all">全部状态</option>
          <option value="online">在线</option>
          <option value="offline">离线</option>
        </select>
      </div>
      <div className="table-panel glass">
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
                          (a.characterCount ?? state.characters.filter((c) => c.accountId === a.id).length)
                        }{" "}
                        个
                      </td>
                      <td>{number(a.rawPoints ?? a.points)}</td>
                      <td>{live ? a.status : number(a.tokens)}</td>
                      <td>
                        <button
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
                        {state.accounts.find((a) => a.id === c.accountId)?.name}
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
                        <button
                          className="text-button"
                          onClick={() => open(c.id)}
                        >
                          打开工作区
                          <ArrowUpRight size={14} />
                        </button>
                      </td>
                    </tr>
                  ))}
            </tbody>
          </table>
        </div>
        {(mode === "accounts" ? accounts.length : rows.length) === 0 && (
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
