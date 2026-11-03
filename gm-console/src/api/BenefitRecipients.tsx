import { getLocale } from "../i18n";
import { t } from "../i18n";
import { useState } from "react";
import { RefreshCw } from "lucide-react";
import { partitionRecipients, type RecipientRow } from "./benefit-recipients";

export function RecipientTable({ rows }: { rows: RecipientRow[] }) {
  return <div className="table-scroll"><table><thead><tr><th>{t("收件角色")}</th><th>{t("账号")}</th><th>{t("状态")}</th><th>{t("结果")}</th></tr></thead>
    <tbody>{rows.map(r => <tr key={`${r.accountId}/${r.characterId}`}>
      <td>{r.name} · {r.characterId}</td><td>{r.accountId}</td>
      <td>{r.state === "sent" ? t("已发送") : r.state === "failed" ? t("失败") : r.state === "ineligible" ? t("不在发放范围") : t("待发送")}</td>
      <td>{r.failure?.message ? t(r.failure.message) : (r.delivery?.reward?.cera ? `${t("点券")} +${Number(r.delivery.reward.cera).toLocaleString(getLocale())} · ${t("余额")} ${r.delivery.before} → ${r.delivery.after}` : r.mailId ? `${t("邮件")} ${r.mailId}` : "—")}</td>
    </tr>)}</tbody></table></div>;
}
function RecipientGroup({ title, rows }: { title: string; rows: RecipientRow[] }) {
  const [open, setOpen] = useState(false), [count, setCount] = useState(100);
  return <details className="benefit-recipient-group" onToggle={e => setOpen(e.currentTarget.open)}>
    <summary>{title}（{rows.length}）</summary>
    {open && <><RecipientTable rows={rows.slice(0, count)} />
      {rows.length > count && <button className="secondary" onClick={() => setCount(count + 100)}>{t("显示后 100 项")}</button>}</>}
  </details>;
}
export function BenefitRecipients({ rows, busy, complete }: { rows: RecipientRow[]; busy: boolean; complete: boolean }) {
  const groups = partitionRecipients(rows);
  return <section className="benefit-recipients" aria-label={t("福利收件人")}>
    <div className="benefit-recipient-summary" role="status">
      <strong>{t("未发放")} {groups.pending.length}{!complete && t("（已查询范围）")}</strong>
      <span>{t("已成功")} {groups.sent.length} {t("· 已查询")} {rows.length} {t("项")}</span>
      <span className="benefit-query-state">{busy && <RefreshCw size={15} className="is-spinning" />}{busy ? t("自动查询中") : complete ? t("本轮查询完成") : t("查询未完成")}</span>
    </div>
    <div className="benefit-pending-list">
      {groups.pending.length ? <RecipientTable rows={groups.pending} /> : <p className="muted">{complete ? t("当前范围暂无未发放对象") : t("已查询范围暂无未发放对象")}</p>}
    </div>
    <RecipientGroup title={t("已成功发放")} rows={groups.sent} />
    {groups.ineligible.length > 0 && <RecipientGroup title={t("不在发放范围")} rows={groups.ineligible} />}
  </section>;
}
