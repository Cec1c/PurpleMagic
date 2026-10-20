import { useState, useEffect } from "react";
import { useLive } from "../api/context";
import { catalog as demoCatalog } from "../fixtures";
import { LoadMore, TaskDetail } from "../api/controls";
import { Gift, Pause, Play, Plus } from "lucide-react";
import { Field, CheckBox, Pill, number, type PageProps } from "../ui";

export function BenefitsPage({ state, request }: PageProps) {
  const live = useLive();
  const [campaignId, setCampaignId] = useState("");
  useEffect(() => {
    if (!live) return;
    void live.guard(() => live.benefits());
    return () => live.cancelScope("benefits");
  }, [live]);
  const [create, setCreate] = useState(false),
    [name, setName] = useState("冒险启程福利"),
    [benefitGold, setBenefitGold] = useState(2000000),
    [enabled, setEnabled] = useState(true),
    [future, setFuture] = useState(true);
  const [rewardKind, setRewardKind] = useState<"gold" | "cera" | "item">("gold");
  const [benefitCera, setBenefitCera] = useState(1000);
  const [grantScope, setGrantScope] = useState<"character" | "account">("character");
  const [itemId, setItemId] = useState("");
  const [itemCount, setItemCount] = useState(1);
  const [itemQuery, setItemQuery] = useState("");
  const catalog = state.api?.catalog || demoCatalog;
  useEffect(() => {
    if (!live || !create || rewardKind !== "item") return;
    const timer = setTimeout(() => void live.guard(() => live.catalog(itemQuery)), 250);
    return () => clearTimeout(timer);
  }, [live, create, rewardKind, itemQuery]);
  return <div className="motion-section">
        <>
          <div className="workspace-intro">
            <div>
              <h2>福利规则</h2>
              <p>启用后自动发放，按规则保留领取记录</p>
            </div>
            <button className="primary" onClick={() => setCreate(!create)}>
              <Plus size={16} />
              {create ? "收起表单" : "创建规则"}
            </button>
          </div>
          {create && (
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
                    id: itemId.trim(), name: catalog.find((i) => i.id === itemId.trim())?.name || itemId.trim(),
                    count: itemCount, enhance: 0,
                  } : undefined,
                  enabled,
                  scope: future ? "future" : "existing",
                });
              }}
            >
              <div className="field-grid">
                <Field label="规则名称">
                  <input
                    required
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                  />
                </Field>
                <Field label="奖励类型">
                  <select aria-label="奖励类型" value={rewardKind} onChange={(e) => setRewardKind(e.target.value as typeof rewardKind)}>
                    <option value="gold">金币</option>
                    <option value="cera">点券</option>
                    <option value="item">指定道具</option>
                  </select>
                </Field>
                <Field label="领取次数">
                  <select aria-label="领取次数" value={rewardKind === "cera" ? "account" : grantScope}
                    disabled={rewardKind === "cera"} onChange={(e) => setGrantScope(e.target.value as typeof grantScope)}>
                    <option value="character">每角色一次</option>
                    <option value="account">每账号一次</option>
                  </select>
                </Field>
                {rewardKind !== "item" && <Field label={rewardKind === "cera" ? "每个账号获得点券" : "每次发放金币"}>
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
                  <Field label="查找道具">
                    <input placeholder="输入物品名称或 ID" value={itemQuery} onChange={(e) => setItemQuery(e.target.value)} />
                  </Field>
                  <Field label="搜索结果">
                    <select aria-label="搜索结果" value={catalog.some((i) => i.id === itemId) ? itemId : ""} onChange={(e) => setItemId(e.target.value)}>
                      <option value="">选择道具</option>
                      {catalog.filter((i) => live || !itemQuery || i.name.includes(itemQuery) || i.id.includes(itemQuery)).map((i) =>
                        <option key={i.id} value={i.id}>{i.name} · {i.id}</option>)}
                    </select>
                  </Field>
                  <Field label="物品 ID">
                    <input required inputMode="numeric" pattern="[1-9][0-9]*" value={itemId} onChange={(e) => setItemId(e.target.value)} />
                  </Field>
                  <Field label="每次发放数量">
                    <input required type="number" min="1" max="2147483647" step="1" value={itemCount} onChange={(e) => setItemCount(Number(e.target.value))} />
                  </Field>
                </>}
              </div>
              {rewardKind === "cera" && <p className="muted benefit-reward-hint">点券直接存入账号，所有角色共享；收件角色收到到账通知。每账号仅发放一次。</p>}
              {rewardKind === "item" && <p className="muted benefit-reward-hint">道具通过邮件发放，装备每封数量为 1。每条规则发放一种道具。</p>}
              <div className="check-options">
                <label className="checkline">
                  <CheckBox
                    label="包含未来新建角色"
                    checked={future}
                    onChange={() => setFuture(!future)}
                  />
                  包含未来新建角色
                </label>
                <label className="checkline">
                  <CheckBox
                    label="创建后立即启用"
                    checked={enabled}
                    onChange={() => setEnabled(!enabled)}
                  />
                  创建后立即启用
                </label>
              </div>
              <div className="form-footer">
                <span>发放范围：全部现有角色{future ? "及未来角色" : ""}</span>
                <button className="primary">
                  <Gift size={16} />
                  创建{enabled ? "并启用" : ""}
                </button>
              </div>
            </form>
          )}
          <div className="table-panel glass">
            <div className="table-scroll">
              <table>
                <thead>
                  <tr>
                    <th>规则</th>
                    <th>发放内容</th>
                    <th>已发放</th>
                    <th>状态</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {state.benefits.map((b) => (
                    <tr key={b.id}>
                      <td>
                        <strong>{b.name}</strong>
                        <small className="cell-meta">
                          {b.scope === "future"
                            ? "现有及未来角色"
                            : "全部现有角色"}{" "}
                          · {b.scopeLabel || "每角色一次"}
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
                              ? "查看发放情况"
                              : `${b.sent} ${b.grantScope === "account" ? "个账号" : "位角色"} · 查看`}
                          </button>
                        ) : (
                          `${b.recipients.length} ${b.grantScope === "account" ? "个账号" : "位角色"}`
                        )}
                      </td>
                      <td>
                        <Pill tone={b.enabled ? "green" : "neutral"}>
                          {b.enabled ? "已启用" : "已暂停"}
                        </Pill>
                      </td>
                      <td>
                        {live && <button className="secondary" disabled={!b.enabled || !live.meta.features.backgroundBenefits}
                          onClick={() => request({ kind: "benefit-redeliver", id: b.id })}>补发未收到</button>}
                        <button
                          className="secondary"
                          onClick={() =>
                            request({ kind: "benefit-toggle", id: b.id })
                          }
                        >
                          {b.enabled ? <Pause size={14} /> : <Play size={14} />}{" "}
                          {b.enabled ? "暂停" : "启用"}
                        </button>
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
