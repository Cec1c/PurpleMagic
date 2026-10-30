import { useEffect, useState } from "react";
import { useLive } from "../api/context";
import { LoadMore } from "../api/controls";
import { Players } from "./Players";
import { Tabs, SearchField, Pill, Empty, type PageProps } from "../ui";

export function QueryPage(props: PageProps) {
  const live = useLive();
  const [tab, setTab] = useState<"players" | "items">("players");
  return (
    <>
      {live && (
        <div className="section-tabs">
          <Tabs
            label="查询范围"
            items={[
              { id: "players", label: "账号与角色" },
              { id: "items", label: "PVF 物品库" },
            ]}
            value={tab}
            onChange={(value) => {
              live?.navigate("query", true);
              setTab(value);
            }}
          />
        </div>
      )}
      {tab === "players" ? (
        <Players {...props} />
      ) : (
        <ItemLibrary {...props} />
      )}
    </>
  );
}
export function ItemLibrary({ state }: PageProps) {
  const live = useLive()!;
  const [q, setQ] = useState(""),
    [kind, setKind] = useState(""),
    [focus, setFocus] = useState("");
  const library = state.api?.resources.library;
  const rows = library?.items || [];
  const detail = state.api?.resources.libraryItem;
  useEffect(() => {
    const timer = setTimeout(
      () => void live.guard(() => live.library(q, kind)),
      250,
    );
    return () => {
      clearTimeout(timer);
      live.cancelScope("library");
    };
  }, [live, q, kind]);
  useEffect(() => {
    if (!focus) return;
    void live.guard(() => live.libraryItem(focus));
    return () => live.cancelScope("libraryItem");
  }, [live, focus]);
  return (
    <>
      <div className="toolbar">
        <SearchField
          placeholder="搜索 PVF 物品名称或 ID"
          value={q}
          onChange={setQ}
        />
        <select
          aria-label="物品类型"
          value={kind}
          onChange={(e) => setKind(e.target.value)}
        >
          <option value="">全部类型</option>
          <option value="equipment">装备</option>
          <option value="stackable">可堆叠物品</option>
        </select>
        <span className="muted">{library?.total ?? 0} 个物品</span>
        <button
          className="secondary"
          onClick={() => {
            live.http.clearCache();
            void live.guard(() => live.library(q, kind));
          }}
        >
          刷新
        </button>
      </div>
      <div className="table-panel glass">
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>物品</th>
                <th>类型</th>
                <th>品级</th>
                <th>等级</th>
                <th>叠加上限</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((v: any) => (
                <tr
                  key={v.itemId}
                  className={focus === v.itemId ? "selected" : ""}
                >
                  <td>
                    <button
                      className="text-button"
                      onClick={() => setFocus(v.itemId)}
                    >
                      {v.name || v.itemId}
                    </button>
                    <small className="cell-meta">
                      #{v.itemId}
                      {v.invalid ? " · 定义不可用" : ""}
                    </small>
                  </td>
                  <td>
                    {v.category ||
                      (v.kind === "equipment" ? "装备" : "可堆叠物品")}
                  </td>
                  <td>
                    {["普通", "高级", "稀有", "神器", "史诗", "传说"][
                      v.rarity
                    ] || "—"}
                  </td>
                  <td>{v.minLevel}</td>
                  <td>{v.stackLimit}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {!rows.length && <Empty title="没有符合条件的物品" />}
      </div>
      <LoadMore
        show={!!library?.next && library?.q === q && library?.kind === kind}
        onClick={() => void live.guard(() => live.library(q, kind, true))}
      />
      {focus && detail?.id === focus && (
        <section className="glass content-panel" aria-label="PVF 物品详情">
          <h3>{detail.item?.name || `物品 ${focus}`}</h3>
          {detail.loading ? (
            <p role="status">正在读取物品详情</p>
          ) : (
            <>
              {(detail.texts || []).map((v: any, i: number) => (
                <div key={i}>
                  <h4>{v.title}</h4>
                  <p style={{ whiteSpace: "pre-wrap" }}>{v.text}</p>
                </div>
              ))}
              {(detail.attributes || []).map((v: any, i: number) => (
                <p key={i}>
                  {v.label || attributeNames[v.name] || v.name}：
                  {v.display ?? v.value ?? (v.values || []).join(" / ")}
                </p>
              ))}
              <details>
                <summary>物品属性与条件</summary>
                <Sections rows={detail.sections || []} />
              </details>
              <small className="muted">PVF {detail.pvfChecksum}</small>
            </>
          )}
        </section>
      )}
    </>
  );
}
const attributeNames: Record<string, string> = {
  physical_attack: "物理攻击力",
  magical_attack: "魔法攻击力",
  independent_attack: "独立攻击力",
  physical_defense: "物理防御力",
  magical_defense: "魔法防御力",
  strength: "力量",
  intelligence: "智力",
  vitality: "体力",
  spirit: "精神",
  hp_max: "HP 上限",
  mp_max: "MP 上限",
  attack_speed: "攻击速度",
  move_speed: "移动速度",
  cast_speed: "施放速度",
  physical_critical: "物理暴击",
  magical_critical: "魔法暴击",
};
function Sections({ rows }: { rows: any[] }) {
  return (
    <div>
      {rows.map((v, i) => (
        <div key={i}>
          <strong>{v.label || v.name}</strong>
          <p style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>
            {(v.values || []).join(" · ")}
          </p>
          {v.children?.length > 0 && <Sections rows={v.children} />}
        </div>
      ))}
    </div>
  );
}
