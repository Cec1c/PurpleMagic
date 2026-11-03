import { getLocale } from "../i18n";
import { t } from "../i18n";
import { numberDraft, draftNumber } from "../numeric";
import { useEnterMotion } from "../motion";
import { useState, useEffect } from "react";
import {
  LoaderCircle,
  ArrowRightLeft,
  Sword,
  Package,
  SlidersHorizontal,
  ArrowDownUp,
  Trash2,
  RotateCcw,
  Save,
  ChevronRight,
  Shield,
  LockKeyhole,
  Check,
} from "lucide-react";
import { useLive } from "../api/context";
import { LoadMore } from "../api/controls";
import type { Item, InventoryTab } from "../domain";
import { bagTabs } from "../fixtures";
import {
  SearchField,
  Tabs,
  Field,
  Pill,
  Empty,
  CheckBox,
  toggle,
  number,
  type PageProps,
} from "../ui";

const rarityTone: Record<string, string> = {
  史诗: "gold",
  神器: "pink",
  稀有: "purple",
  传说: "orange",
  高级: "cyan",
};
function ItemEditor({
  item,
  request,
}: Pick<PageProps, "request"> & { item: Item }) {
  const live = useLive();
  const capability = item.remote?.options;
  const equipment = capability?.configuration?.equipment;
  const expiration = capability?.configuration?.expiration;
  const [expirationDays, setExpirationDays] = useState("");
  const [replacementId, setReplacementId] = useState(item.id);
  const equipmentFields = !live || !!equipment;
  const [draft, setDraft] = useState({
    count: numberDraft(item.count),
    enhance: numberDraft(item.enhance),
    amplify: numberDraft(item.amplify),
    forge: numberDraft(item.forge),
    bound: item.bound,
  });
  const changed =
    expirationDays !== "" ||
    Object.keys(draft).some(
      (k) =>
        !Object.is(
          draft[k as keyof typeof draft],
          k === "bound" ? item.bound : numberDraft(item[k as "count" | "enhance" | "amplify" | "forge"]),
        ),
    );
  const numeric = (
    key: "count" | "enhance" | "amplify" | "forge",
    label: string,
    max: number,
  ) => (
    <Field label={label}>
      <input
        type="number"
        min={key === "count" ? Number(capability?.quantity?.min ?? 1) : 0}
        max={max}
        disabled={
          !!live &&
          (key === "count"
            ? !capability?.quantity
            : key === "enhance"
              ? !equipment?.canUpgrade
              : key === "forge"
                ? !equipment?.canForge
                : !equipment?.canAmplify)
        }
        required
        value={draft[key]}
        onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
      />
    </Field>
  );
  return (
    <aside className="inspector glass">
      <div className="panel-heading">
        <span>{t("物品详情")}</span>
        <span className="muted">{t("槽位")} {item.slot}</span>
      </div>
      <div className="item-overview">
        <span className={`item-icon large ${rarityTone[item.rarity] || ""}`}>
          {["武器", "辅助装备"].includes(item.category) ? (
            <Sword size={27} />
          ) : (
            <Package size={27} />
          )}
        </span>
        <div>
          <h2 className={`rarity-${item.rarity}`}>{item.name}</h2>
          <p>
            {t(item.category)} · Lv.{item.level}
          </p>
        </div>
      </div>
      <div className="item-meta">
        <Pill tone={rarityTone[item.rarity]}>{t(item.rarity)}</Pill>
        <span className="mono">ID {item.id}</span>
      </div>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          request({
            kind: "item",
            key: item.key,
            version: item.version,
            patch: { count: draftNumber(draft.count), enhance: draftNumber(draft.enhance), amplify: draftNumber(draft.amplify), forge: draftNumber(draft.forge), bound: draft.bound },
            ...(expirationDays !== ""
              ? { expirationDays: Number(expirationDays) }
              : {}),
          });
        }}
      >
        <div className="form-section">
          <h3>
            <SlidersHorizontal size={15} />
            {t("属性配置")}
          </h3>
          <div className="field-grid">
            {numeric(
              "count",
              "数量",
              Number(capability?.quantity?.max || 99999),
            )}
            {equipmentFields && numeric("enhance", "强化等级", equipment?.maxUpgradeLevel ?? 31)}
            {equipmentFields && (live ? (
              <Field label={t("增幅属性")}>
                <select
                  disabled={!equipment?.canAmplify}
                  value={draft.amplify}
                  onChange={(e) =>
                    setDraft({ ...draft, amplify: e.target.value })
                  }
                >
                  <option value="" disabled>
                    —
                  </option>
                  {(equipment?.amplifyTypes || []).map((v: any) => (
                    <option key={v.value} value={v.value}>
                      {v.label}
                    </option>
                  ))}
                </select>
              </Field>
            ) : (
              numeric("amplify", "增幅等级", 31)
            ))}
            {equipmentFields && numeric("forge", "锻造等级", equipment?.maxForgingLevel ?? 8)}
          </div>
          {equipment?.canUpgrade && <p className="muted field-hint">{t("强化可保存范围：0–")}{equipment.maxUpgradeLevel}</p>}
          {expiration?.canOverride && (
            <Field label={t("重新设置有效期（天）")}>
              <input
                type="number"
                min={1}
                max={expiration.maxDays}
                value={expirationDays}
                placeholder={`${t("剩余")} ${expiration.currentRemainingDays}${t("天")}`}
                onChange={(e) => setExpirationDays(e.target.value)}
              />
            </Field>
          )}
          <label className="checkline">
            <CheckBox
              label={t("绑定物品")}
              disabled={!!live}
              checked={draft.bound}
              onChange={() => setDraft({ ...draft, bound: !draft.bound })}
            />
            <LockKeyhole size={14} />
            {t("绑定角色")}
          </label>
        </div>
        <dl className="detail-list">
          <div>
            <dt>{t("归属范围")}</dt>
            <dd>{item.tab === "account" ? t("账号共享") : t("当前角色")}</dd>
          </div>
          <div>
            <dt>{t("有效期")}</dt>
            <dd>
              {item.remote?.expiresAt
                ? new Date(item.remote.expiresAt).toLocaleString(getLocale(), { timeZone: "Asia/Shanghai" })
                : t("无期限")}
            </dd>
          </div>
          <div>
            <dt>{t("物品状态")}</dt>
            <dd>
              <span
                className={
                  item.remote && !item.remote.known ? "" : "green-text"
                }
              >
                {item.remote && !item.remote.known ? t("PVF 未收录") : t("正常")}
              </span>
            </dd>
          </div>
        </dl>
        {capability?.item?.pet && (
          <dl className="detail-list">
            <div>
              <dt>{t("宠物名称")}</dt>
              <dd>{capability.item.pet.name || item.name}</dd>
            </div>
            <div>
              <dt>{t("宠物等级")}</dt>
              <dd>{capability.item.pet.level}</dd>
            </div>
            <div>
              <dt>{t("宠物经验")}</dt>
              <dd>{number(capability.item.pet.experience)}</dd>
            </div>
            <div>
              <dt>{t("饱食度")}</dt>
              <dd>{capability.item.pet.satiety}</dd>
            </div>
          </dl>
        )}
        {(capability?.item?.petLinkError ||
          capability?.item?.petArtifactLinkError) && (
          <p className="inline-error">
            {capability.item.petLinkError ||
              capability.item.petArtifactLinkError}
          </p>
        )}
        <div className="inspector-footer">
          <span className="change-note">
            {changed ? t("有未保存的修改") : t("选择物品，查看或调整属性")}
          </span>
          <div className="button-row">
            <button
              type="button"
              className="secondary"
              disabled={!changed}
              onClick={() => {
                setExpirationDays("");
                setDraft({
                  count: numberDraft(item.count),
                  enhance: numberDraft(item.enhance),
                  amplify: numberDraft(item.amplify),
                  forge: numberDraft(item.forge),
                  bound: item.bound,
                });
              }}
            >
              <RotateCcw size={15} />
              {t("重置")}
            </button>
            <button className="primary" disabled={!changed}>
              <Save size={15} />
              {t("保存修改")}
            </button>
          </div>
        </div>
      </form>
      {capability?.canReplace && <form className="item-replacement" onSubmit={e => {e.preventDefault(); request({kind:"replace-item", key:item.key, version:item.version, itemId:replacementId.trim()});}}>
        <Field label={t("替换物品 ID")} hint="同类替换，保留数量与实例属性">
          <input aria-label={t("替换物品 ID")} inputMode="numeric" pattern="[1-9][0-9]*" required value={replacementId} onChange={e => setReplacementId(e.target.value)} />
        </Field>
        <button className="secondary" disabled={replacementId === item.id}><ArrowRightLeft size={15} />{t("替换物品")}</button>
      </form>}
      <button type="button" className="danger-button delete-single" disabled={!!live && !capability?.canDelete} onClick={() => request({kind:"delete-items", keys:[item.key]})}><Trash2 size={15} />{t("删除这件物品")}</button>
    </aside>
  );
}
export function Inventory({ state, character, request, notify }: PageProps) {
  const live = useLive();
  const [tab, setTab] = useState<InventoryTab>("bag"),
    [query, setQuery] = useState(""),
    [category, setCategory] = useState("全部"),
    [selected, setSelected] = useState(""),
    [checked, setChecked] = useState<string[]>([]),
    [reverse, setReverse] = useState(false);
  const all = state.items.filter(
    (i) =>
      i.tab === tab &&
      (tab === "account"
        ? i.accountId === character.accountId
        : i.characterId === character.id),
  );
  const categories = ["全部", ...new Set(all.map((i) => i.category))];
  let rows = all.filter(
    (i) =>
      (category === "全部" || i.category === category) &&
      (!query ||
        `${i.name} ${i.id}`.toLowerCase().includes(query.toLowerCase())),
  );
  if (reverse) rows = [...rows].reverse();
  const item = rows.find((i) => i.key === selected) || rows[0];
  useEffect(() => {
    if (live && item?.remote && !item.remote.options)
      void live.guard(() => live.item(item.key));
  }, [live, item?.key, !!item?.remote?.options]);
  const ids = rows.map((i) => i.key);
  const visibleChecked = checked.filter((id) => all.some((i) => i.key === id));
  function changeTab(v: InventoryTab) {
    setTab(v);
    if (live) void live.guard(() => live.inventory(v));
    setCategory("全部");
    setSelected("");
    setChecked([]);
    setQuery("");
  }
  const loading = !!state.api?.inventory.loading;
  const motion = useEnterMotion(tab);
  return (
    <>
      <div className="section-tabs">
        <Tabs
          label={t("背包分类")}
          items={bagTabs}
          value={tab}
          onChange={changeTab}
        />
      </div>
      <div ref={motion} className="inventory-layout">
        <section className="inventory-main">
          <div className="toolbar">
            <SearchField
              placeholder={t("搜索物品名称或 ID")}
              value={query}
              onChange={setQuery}
            />
            <button className="danger-button" disabled={!visibleChecked.length || loading} onClick={() => request({kind:"delete-items", keys:visibleChecked})}><Trash2 size={15} />{t("删除选中")}{visibleChecked.length ? `（${visibleChecked.length}）` : ""}</button>
            <button className="secondary" onClick={() => setReverse(!reverse)}>
              <ArrowDownUp size={15} />
              {reverse ? t("槽位降序") : t("槽位升序")}
            </button>
            <button
              className="icon-button"
              aria-label={t("刷新物品")}
              title={t("刷新物品")}
              onClick={() => {
                setChecked([]);
                if (live) void live.guard(() => live.inventory(tab));
                else notify("物品列表已刷新");
              }}
            >
              <RotateCcw size={16} />
            </button>
          </div>
          <div className="table-panel glass" aria-busy={loading}>
            <div className="table-filter">
              <div className="text-tabs">
                {categories.map((c) => (
                  <button
                    key={c}
                    className={category === c ? "active" : ""}
                    onClick={() => {
                      setCategory(c);
                      setChecked([]);
                    }}
                  >
                    {t(c)}
                  </button>
                ))}
              </div>
              <span className="inventory-read-state">{loading ? <><LoaderCircle size={15} className="is-spinning" />{t("正在读取物品")}</> : `${rows.length}${t("件物品")}`}</span>
            </div>
            <div className="table-scroll">
              <table className="inventory-table">
                <thead>
                  <tr>
                    <th className="check-cell">
                      <CheckBox
                        label={t("全选当前物品")}
                        checked={
                          ids.length > 0 &&
                          ids.every((id) => checked.includes(id))
                        }
                        onChange={() =>
                          setChecked(
                            ids.every((id) => checked.includes(id)) ? [] : ids,
                          )
                        }
                      />
                    </th>
                    <th>{t("物品")}</th>
                    <th>{t("品质")}</th>
                    <th>{t("数量")}</th>
                    <th>{t("强化 / 增幅")}</th>
                    <th className="end">{t("槽位")}</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((i) => (
                    <tr
                      key={i.key}
                      className={item?.key === i.key ? "selected" : ""}
                      onClick={() => setSelected(i.key)}
                    >
                      <td onClick={(e) => e.stopPropagation()}>
                        <CheckBox
                          label={`选择 ${i.name}`}
                          checked={checked.includes(i.key)}
                          onChange={() => setChecked(toggle(checked, i.key))}
                        />
                      </td>
                      <td>
                        <button
                          className="item-link"
                          onClick={() => setSelected(i.key)}
                        >
                          <span
                            className={`item-icon ${rarityTone[i.rarity] || ""}`}
                          >
                            {i.category === "武器" ? (
                              <Sword size={19} />
                            ) : i.category === "防具" ? (
                              <Shield size={19} />
                            ) : (
                              <Package size={19} />
                            )}
                          </span>
                          <span>
                            <strong className={`rarity-${i.rarity}`}>
                              {i.name}
                            </strong>
                            <small>
                              {i.id} <span>· {t(i.category)}</span>
                            </small>
                          </span>
                        </button>
                      </td>
                      <td>
                        <span className={`rarity-${i.rarity}`}>{t(i.rarity)}</span>
                      </td>
                      <td className="numeric">{number(i.count)}</td>
                      <td>
                        {i.enhance ? (
                          <span className="cyan-text">+{i.enhance}</span>
                        ) : i.amplify ? (
                          <span className="purple-text">{t("增幅 +")}{i.amplify}</span>
                        ) : (
                          <span className="muted">—</span>
                        )}
                      </td>
                      <td className="end muted">
                        {i.slot}
                        <ChevronRight size={13} className="row-arrow" />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {!rows.length && (loading ? <div className="inventory-loading" role="status"><LoaderCircle size={28} className="is-spinning" /><span>{t("正在读取物品")}</span></div> : <Empty title={t("没有找到物品")} detail="调整关键词或切换分类" />)}
            <div className="table-footer">
              <span>
                {visibleChecked.length ? (
                  <>
                    <Check size={14} />
                    {t("已选择")} {visibleChecked.length}{t("件")}
                  </>
                ) : (
                  `${t(bagTabs.find((t) => t.id === tab)?.label || "物品")} · ${rows.length}${t("件物品")}`
                )}
              </span>
              {visibleChecked.length > 0 ? (
                <button
                  className="danger-button"
                  onClick={() =>
                    request({ kind: "delete-items", keys: visibleChecked })
                  }
                >
                  <Trash2 size={14} />
                  {t("删除选中")}
                </button>
              ) : (
                <span className="muted">{t("点击物品查看详情")}</span>
              )}
            </div>
          </div>
        </section>
        {item ? (
          <ItemEditor
            key={`${item.key}-${item.version}`}
            item={item}
            request={request}
          />
        ) : (
          <aside className="inspector glass">
            <Empty title={loading ? t("正在读取物品详情") : t("选择一件物品")} />
          </aside>
        )}
      </div>
      <LoadMore
        show={!!state.api && Object.keys(state.api.inventory.next).length > 0}
        onClick={() => void live?.guard(() => live.inventory(tab, true))}
      />
    </>
  );
}
