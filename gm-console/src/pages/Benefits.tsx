import { getLocale, t } from "../i18n";
import { useState, useEffect } from "react";
import { useLive } from "../api/context";
import { ItemPicker } from "../ItemPicker";
import type { CatalogItem } from "../item-picker";
import { itemExpirationLabel } from "../item-expiration";
import { LoadMore, TaskDetail } from "../api/controls";
import { Gift, Pause, Play, Plus, Trash2 } from "lucide-react";
import { Field, CheckBox, Pill, number, type PageProps } from "../ui";
import { BenefitReconciliation } from "../api/BenefitReconciliation";

export function BenefitsPage({ state, request }: PageProps) {
  const live = useLive();
  const [campaignId, setCampaignId] = useState("");
  useEffect(() => {
    if (!live) return;
    void live.guard(() => live.benefits());
    return () => live.cancelScope("benefits");
  }, [live]);
  const [create, setCreate] = useState(false),
    [name, setName] = useState(() => getLocale() === "ko-KR" ? "모험 시작 보상" : "冒险启程福利"),
    [benefitGold, setBenefitGold] = useState(2000000),
    [enabled, setEnabled] = useState(true),
    [future, setFuture] = useState(true);
  const [rewardKind, setRewardKind] = useState<"gold" | "cera" | "item">("gold");
  const [benefitCera, setBenefitCera] = useState(1000);
  const [grantScope, setGrantScope] = useState<"character" | "account">("character");
  const [itemId, setItemId] = useState("");
  const [itemCount, setItemCount] = useState(1);
  const [selectedItem, setSelectedItem] = useState<CatalogItem>();
  return <div className="motion-section">
        <>
          <div className="workspace-intro">
            <div>
              <h2>{t("福利规则")}</h2>
              <p>{t("启用后自动发放，按规则保留领取记录")}</p>
            </div>
            <button className="primary" onClick={() => setCreate(!create)}>
              <Plus size={16} />
              {create ? t("收起表单") : t("创建规则")}
            </button>
          </div>
          <BenefitReconciliation />
          {create && (
            <div className={`benefit-create-layout ${rewardKind === "item" ? "with-item-picker" : ""}`}>
            <form
              className="glass content-panel benefit-create"
              onSubmit={(e) => {
                e.preventDefault();
                request({
                  kind: "benefit-create",
                  name,
                  gold: benefitGold,
                  rewardKind,
                  cera: benefitCera,
                  grantScope,
                  item: rewardKind === "item" ? {
                    id: itemId.trim(), name: selectedItem?.id === itemId.trim() ? selectedItem.name : itemId.trim(),
                    count: itemCount, enhance: 0,
                  } : undefined,
                  enabled,
                  scope: future ? "future" : "existing",
                });
              }}
            >
              <div className="field-grid">
                <Field label={t("规则名称")}>
                  <input
                    required
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                </Field>
                <Field label={t("奖励类型")}>
                  <select aria-label={t("奖励类型")} value={rewardKind} onChange={(e) => setRewardKind(e.target.value as typeof rewardKind)}>
                    <option value="gold">{t("金币")}</option>
                    <option value="cera">{t("点券")}</option>
                    <option value="item">{t("指定道具")}</option>
                  </select>
                </Field>
                <Field label={t("领取次数")}>
                  <select aria-label={t("领取次数")} value={rewardKind === "cera" ? "account" : grantScope}
                    disabled={rewardKind === "cera"} onChange={(e) => setGrantScope(e.target.value as typeof grantScope)}>
                    <option value="character">{t("每角色一次")}</option>
                    <option value="account">{t("每账号一次")}</option>
                  </select>
                </Field>
                {rewardKind !== "item" && <Field label={rewardKind === "cera" ? t("每个账号获得点券") : t("每次发放金币")}>
                  <input
                    type="number"
                    required
                    min="1"
                    max="2147483647"
                    step="1"
                    value={rewardKind === "cera" ? benefitCera : benefitGold}
                    onChange={(e) => (rewardKind === "cera" ? setBenefitCera : setBenefitGold)(Number(e.target.value))}
                  />
                </Field>}
                {rewardKind === "item" && <>
                  <Field label={t("物品 ID")}>
                    <input required inputMode="numeric" pattern="[1-9][0-9]*" value={itemId} onChange={(e) => { setItemId(e.target.value); setSelectedItem(undefined); }} />
                  </Field>
                  <Field label={t("每次发放数量")}>
                    <input required type="number" min="1" max="2147483647" step="1" value={itemCount} onChange={(e) => setItemCount(Number(e.target.value))} />
                  </Field>
                </>}
              </div>
              {rewardKind === "item" && selectedItem && <div className="selected-reward"><strong>{selectedItem.name}</strong><small>ID {selectedItem.id} · {itemExpirationLabel(selectedItem.expiration)}</small></div>}
              {rewardKind === "cera" && <p className="muted benefit-reward-hint">{t("点券直接存入账号，所有角色共享；收件角色收到到账通知。每账号仅发放一次。")}</p>}
              {rewardKind === "item" && <p className="muted benefit-reward-hint">{t("道具通过邮件发放，装备每封数量为 1。每条规则发放一种道具。")}</p>}
              <div className="check-options">
                <label className="checkline">
                  <CheckBox
                    label={t("包含未来新建角色")}
                    checked={future}
                    onChange={() => setFuture(!future)}
                  />
                  {t("包含未来新建角色")}
                </label>
                <label className="checkline">
                  <CheckBox
                    label={t("创建后立即启用")}
                    checked={enabled}
                    onChange={() => setEnabled(!enabled)}
                  />
                  {t("创建后立即启用")}
                </label>
              </div>
              <div className="form-footer">
                <span>{t("发放范围：全部现有角色")}{future ? t("及未来角色") : ""}</span>
                <button className="primary">
                  <Gift size={16} />
                  {t("创建")}{enabled ? t("并启用") : ""}
                </button>
              </div>
            </form>
            {rewardKind === "item" && <ItemPicker api={state.api} selectedId={itemId} onChoose={items => { setSelectedItem(items[0]); setItemId(items[0].id); }} />}
            </div>
          )}
          <div className="table-panel glass">
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>{t("规则")}</th>
                    <th>{t("发放内容")}</th>
                    <th>{t("已发放")}</th>
                    <th>{t("状态")}</th>
                    <th>{t("操作")}</th>
                  </tr>
                </thead>
                <tbody>
                  {state.benefits.map((b) => (
                    <tr key={b.id}>
                      <td>
                        <strong>{b.name}</strong>
                        <small className="cell-meta">
                          {b.scope === "future"
                            ? t("现有及未来角色")
                            : t("全部现有角色")}{" "}
                          · {b.scopeLabel || t("每角色一次")}
                        </small>
                      </td>
                      <td className="gold-text">{b.rewardSummary || `金币 ${number(b.gold)}`}</td>
                      <td>
                        {live ? (
                          <button
                            className="text-button"
                            onClick={() => {
                              setCampaignId(b.id);
                            }}
                          >
                            {b.sent === undefined
                              ? t("查看发放情况")
                              : getLocale() === "ko-KR"
                                ? `${b.grantScope === "account" ? "계정" : "캐릭터"} ${b.sent}${b.grantScope === "account" ? "개" : "명"} · ${t("查看")}`
                                : `${b.sent} ${t(b.grantScope === "account" ? "个账号" : "位角色")} · ${t("查看")}`}
                          </button>
                        ) : (
                          getLocale() === "ko-KR"
                            ? `${b.grantScope === "account" ? "계정" : "캐릭터"} ${b.recipients.length}${b.grantScope === "account" ? "개" : "명"}`
                            : `${b.recipients.length} ${t(b.grantScope === "account" ? "个账号" : "位角色")}`
                        )}
                      </td>
                      <td>
                        <Pill tone={b.enabled ? "green" : "neutral"}>
                          {b.enabled ? t("已启用") : t("已暂停")}
                        </Pill>
                      </td>
                      <td>
                        {live && <button className="secondary" disabled={!b.enabled || !live.meta.features.backgroundBenefits}
                          onClick={() => request({ kind: "benefit-redeliver", id: b.id })}>{t("补发未收到")}</button>}
                        <button
                          className="secondary"
                          onClick={() =>
                            request({ kind: "benefit-toggle", id: b.id })
                          }
                        >
                          {b.enabled ? <Pause size={14} /> : <Play size={14} />}{" "}
                          {b.enabled ? t("暂停") : t("启用")}
                        </button>
                        <button className="secondary" disabled={!!live&&!state.api?.features["benefits.delete"]}
                          onClick={() => request({kind:"benefit-delete",id:b.id})}><Trash2 size={14}/>{t("删除")}</button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </>

      <LoadMore show={!!state.api?.benefitNext} onClick={() => void live?.guard(() => live.benefits(true))} />
      {live && campaignId && <TaskDetail id={campaignId} campaignId={campaignId} onClose={() => setCampaignId("")} request={request} />}
    </div>;
}
