import { t } from "./i18n";
import { useEffect, useRef, useState, useMemo } from "react";
import { Search, Plus, LoaderCircle, Check } from "lucide-react";
import { useLive } from "./api/context";
import type { LiveData } from "./api/model";
import { catalog as demoCatalog } from "./fixtures";
import { itemExpirationLabel } from "./item-expiration";
import { catalogBatchSizes, type CatalogItem } from "./item-picker";
import { CheckBox, Field } from "./ui";
import "./item-picker.css";

export function ItemPicker({ api, multiple = false, selectedId, addedIds = [], onChoose }: {
  api?: LiveData; multiple?: boolean; selectedId?: string; addedIds?: string[];
  onChoose: (items: CatalogItem[], count: number) => void;
}) {
  const live = useLive();
  const [query, setQuery] = useState("");
  const [size, setSize] = useState(300);
  const [count, setCount] = useState(1);
  const [selected, setSelected] = useState<CatalogItem[]>([]);
  const [error, setError] = useState("");
  const results = useRef<HTMLDivElement>(null);
  useEffect(() => { if (results.current) results.current.scrollTop = 0; }, [query, size]);
  useEffect(() => {
    if (!live) return;
    const timer = setTimeout(() => void live.guard(() => live.catalog(query.trim(), false, size)), 250);
    return () => { clearTimeout(timer); live.cancelScope("catalog"); };
  }, [live, query, size]);
  const currentQuery = !live || api?.catalogQuery === query.trim();
  const rows: CatalogItem[] = live ? (currentQuery ? api?.catalog || [] : [])
    : demoCatalog.filter(i => i.name.toLowerCase().includes(query.trim().toLowerCase()) || i.id.includes(query.trim()));
  const loading = !!live && (!currentQuery || !!api?.catalogLoading);
  const validCount = Number.isInteger(count) && count > 0 && count <= 99999;
  const selectedIds = useMemo(() => new Set(selected.map(i => i.id)), [selected]);
  const added = useMemo(() => new Set(addedIds), [addedIds]);
  const allSelected = rows.length > 0 && rows.every(i => selectedIds.has(i.id));
  function choose(items: CatalogItem[]) {
    if (!items.length || (multiple && !validCount)) return;
    try { onChoose(items, multiple ? count : 1); setError(""); setSelected(s => s.filter(i => !items.some(x => x.id === i.id))); }
    catch (e) { setError((e as Error).message); }
  }
  return <section className="glass content-panel item-picker" aria-label={t("物品查找")}>
    <div className="panel-heading"><h3>{t("查找物品")}</h3><span className="item-picker-spinner" role="status" aria-label={loading ? t("正在查找物品") : t("查找完成")}>{loading && <LoaderCircle size={17} className="is-spinning" />}</span></div>
    <div className="item-picker-search"><Search size={17} /><input aria-label={t("模糊搜索物品名称或 ID")} placeholder={t("输入部分名称或完整 ID")} value={query} onChange={e => setQuery(e.target.value)} /></div>
    <div className="item-picker-toolbar">
      <span className="muted">{live ? `${t("匹配")} ${currentQuery ? api?.catalogTotal || 0 : 0}${t("条")} · ${t("已显示")} ${rows.length}${t("条可选")}` : `${rows.length}${t("条物品")}`}</span>
      <label>{t("每批")} <select aria-label={t("每批查询数量")} value={size} onChange={e => setSize(Number(e.target.value))}>{catalogBatchSizes.map(n => <option key={n} value={n}>{n}</option>)}</select></label>
    </div>
    {multiple && <div className="item-picker-select-all"><CheckBox label={t("勾选当前已加载物品")} checked={allSelected} onChange={() => setSelected(old => allSelected ? old.filter(i => !rows.some(r => r.id === i.id)) : [...new Map([...old, ...rows].map(i => [i.id, i])).values()])} /><span>{t("全选当前结果")}</span><span className="muted">{t("已选")} {selected.length} {t("种")}</span>{selected.length > 0 && <button type="button" className="text-button" onClick={() => setSelected([])}>{t("清空")}</button>}</div>}
    <div ref={results} className="item-picker-results" aria-busy={loading}>
      {rows.map(item => <div className="item-picker-row" key={item.id}>
        {multiple && <CheckBox label={`${t("选择")} ${item.name} ID ${item.id}`} checked={selectedIds.has(item.id)} onChange={() => setSelected(old => old.some(i => i.id === item.id) ? old.filter(i => i.id !== item.id) : [...old, item])} />}
        <div className="item-picker-info"><strong>{item.name}</strong><small>ID {item.id}</small><small>{itemExpirationLabel(item.expiration)}</small></div>
        <button type="button" className="secondary item-picker-use" disabled={multiple && !validCount} aria-label={`${t(multiple ? "加入附件" : "选用")} ${item.name} ID ${item.id}`} onClick={() => choose([item])}>
          {(!multiple && selectedId === item.id) || added.has(item.id) ? <Check size={14} /> : <Plus size={14} />}{multiple ? t("添加") : selectedId === item.id ? t("已选") : t("选用")}
        </button>
      </div>)}
      {!rows.length && <div className="item-picker-empty">{loading ? "" : t("没有匹配的物品，试试名称中的其他文字或物品 ID")}</div>}
    </div>
    {live && <div className="item-picker-pagination"><button type="button" className="secondary" disabled={loading || !api?.catalogNext} onClick={() => void live.guard(() => live.catalog(query.trim(), true, size))}>{api?.catalogNext ? `${t("再显示")} ${size} ${t("条")}` : t("已显示全部结果")}</button></div>}
    {multiple && <div className="item-picker-actions"><Field label={t("每种添加数量")}><input aria-label={t("每种添加数量")} type="number" min={1} max={99999} step={1} value={count} onChange={e => setCount(Number(e.target.value))} /></Field><button type="button" className="primary" disabled={!selected.length || !validCount} onClick={() => choose(selected)}><Plus size={16} />{t("加入")} {selected.length} {t("种附件")}</button></div>}
    {error && <p role="alert" className="inline-error">{t(error)}</p>}
  </section>;
}
