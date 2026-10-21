import "../activities.css";
import { useEffect, useState } from "react";
import { CalendarDays, Check, Clock3, Pause, Play, RefreshCw } from "lucide-react";
import { useLive } from "../api/context";
import { demoActivities, type ActivitySnapshot } from "../activities";
import { CheckBox, Empty, Modal, Pill, SearchField, type PageProps } from "../ui";

export function ActivitiesPage({ state, request }: PageProps) {
  const live = useLive();
  const data = (live ? state.api?.resources.activities : state.activities || demoActivities) as ActivitySnapshot | undefined;
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState("all");
  const [selected, setSelected] = useState<number[]>([]);
  const [detail, setDetail] = useState<number>();
  useEffect(() => {
    if (!live) return;
    void live.guard(() => live.activities());
    return () => live.cancelScope("activities");
  }, [live]);
  useEffect(() => {
    if (data) setSelected((ids) => ids.filter((id) => data.activities.some((v) => v.id === id)));
  }, [data]);
  const all = data?.activities || [];
  const rows = all.filter((v) => {
    const matches = `${v.id} ${v.name} ${v.description}`.toLowerCase().includes(q.trim().toLowerCase());
    return matches && (filter === "all" || filter === "enabled" && v.enabled || filter === "disabled" && !v.enabled || filter === "pending" && v.pendingRestart);
  });
  const canWrite = !live || !!state.api?.features["activities.write"];
  const busy = !!state.api?.busy;
  const focused = all.find((v) => v.id === detail);
  function change(ids: number[], enabled: boolean) {
    if (!data || busy) return;
    request({ kind: "activity-save", revision: data.revision, activities: ids.map((id) => ({ id, enabled })) });
  }
  const visibleSelected = rows.length > 0 && rows.every((v) => selected.includes(v.id));
  return <div className="motion-section activities-page">
    <div className="activity-overview glass" aria-label="活动概览">
      <div><CalendarDays size={19} /><span>全部活动<strong>{data ? all.length : "—"}</strong></span></div>
      <div><Play size={18} /><span>配置开启<strong>{data ? all.filter((v) => v.enabled).length : "—"}</strong></span></div>
      <div><Check size={18} /><span>实际运行<strong>{data ? all.filter((v) => v.effectiveEnabled).length : "—"}</strong></span></div>
      <div><Clock3 size={18} /><span>待重启<strong>{data ? all.filter((v) => v.pendingRestart).length : "—"}</strong></span></div>
    </div>
    <div className="toolbar activity-toolbar">
      <SearchField placeholder="搜索活动名称、ID 或说明" value={q} onChange={setQ} />
      <select aria-label="活动状态筛选" value={filter} onChange={(e) => setFilter(e.target.value)}>
        <option value="all">全部状态</option><option value="enabled">配置开启</option>
        <option value="disabled">配置关闭</option><option value="pending">等待重启</option>
      </select>
      {live && <button className="secondary" disabled={busy} onClick={() => void live.guard(() => live.activities())}><RefreshCw size={15} />刷新</button>}
    </div>
    <div className="activity-action-bar">
      <span>{selected.length ? `已选 ${selected.length} 个活动` : "选择活动后可批量调整"}</span>
      {selected.length > 0 && <button className="text-button" onClick={() => setSelected([])}>清空选择</button>}
      {canWrite && <div className="activity-actions">
        <button className="secondary" disabled={busy || !all.some((v) => selected.includes(v.id) && !v.enabled)} onClick={() => change(selected, true)}><Play size={14} />批量开启</button>
        <button className="secondary" disabled={busy || !all.some((v) => selected.includes(v.id) && v.enabled)} onClick={() => change(selected, false)}><Pause size={14} />批量关闭</button>
      </div>}
    </div>
    <div className="table-panel glass activity-table">
      <div className="table-scroll"><table>
        <thead><tr>
          <th className="activity-select"><CheckBox label="选择当前筛选中的全部活动" checked={visibleSelected} onChange={() => setSelected(visibleSelected ? selected.filter((id) => !rows.some((v) => v.id === id)) : [...new Set([...selected, ...rows.map((v) => v.id)])])} /></th>
          <th>游戏活动</th><th>配置状态</th><th>实际状态</th><th>生效情况</th>{canWrite && <th>操作</th>}
        </tr></thead>
        <tbody>{rows.map((v) => <tr key={v.id} className={selected.includes(v.id) ? "selected" : undefined}>
          <td><CheckBox label={`选择 ${v.name}`} checked={selected.includes(v.id)} onChange={() => setSelected(selected.includes(v.id) ? selected.filter((id) => id !== v.id) : [...selected, v.id])} /></td>
          <td className="activity-name"><button className="text-button" onClick={() => setDetail(v.id)}>{v.name}</button><small className="cell-meta">#{v.id}</small><p>{v.description}</p></td>
          <td><Pill tone={v.enabled ? "purple" : "neutral"}>{v.enabled ? "开启" : "关闭"}</Pill></td>
          <td><Pill tone={v.effectiveEnabled ? "green" : "neutral"}>{v.effectiveEnabled ? "运行中" : "未运行"}</Pill></td>
          <td>{v.pendingRestart ? <Pill tone="orange">等待重启</Pill> : <span className="muted">已生效</span>}</td>
          {canWrite && <td><button className="secondary" disabled={busy} onClick={() => change([v.id], !v.enabled)}>{v.enabled ? <Pause size={14} /> : <Play size={14} />}{v.enabled ? "关闭" : "开启"}</button></td>}
        </tr>)}</tbody>
      </table></div>
      {data && !rows.length && <Empty title={all.length ? "没有符合条件的活动" : "当前游戏服务没有可管理的活动"} />}
    </div>
    <div className="activity-footnote"><Clock3 size={15} /><span>开关保存后，在游戏服务下次重启时生效。{data?.restartRequired ? "当前有待生效的配置。" : ""}</span>{data && <small>配置版本 {data.revision}</small>}</div>
    {focused && <Modal title={focused.name} onClose={() => setDetail(undefined)} footer={<>
      <button className="secondary" onClick={() => setDetail(undefined)}>关闭详情</button>
      {canWrite && <button className="primary" disabled={busy} onClick={() => { setDetail(undefined); change([focused.id], !focused.enabled); }}>{focused.enabled ? "关闭此活动" : "开启此活动"}</button>}
    </>}>
      <div className="activity-detail">
        <p>{focused.description}</p>
        <dl><dt>活动 ID</dt><dd>{focused.id}</dd><dt>默认配置</dt><dd>{focused.defaultEnabled ? "开启" : "关闭"}</dd><dt>当前配置</dt><dd>{focused.enabled ? "开启" : "关闭"}</dd><dt>实际状态</dt><dd>{focused.effectiveEnabled ? "运行中" : "未运行"}</dd><dt>生效情况</dt><dd>{focused.pendingRestart ? "等待游戏服务重启" : "已生效"}</dd></dl>
        {focused.source && <details><summary>活动来源</summary><p className="muted">{focused.source}</p></details>}
      </div>
    </Modal>}
  </div>;
}
