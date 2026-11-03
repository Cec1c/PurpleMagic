import { getLocale } from "../i18n";
import { t } from "../i18n";
import { useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import { useLive } from "./context";
import type { DTO } from "./model";

export function BenefitReconciliation() {
  const live = useLive();
  const [status, setStatus] = useState<DTO>(), [error, setError] = useState("");
  useEffect(() => {
    if (!live?.meta.features.backgroundBenefits) return;
    const controller = new AbortController();
    let active = false;
    const read = async () => {
      if (active) return;
      active = true;
      try {
        const r = await live.http.get("/api/v1/benefits/reconciliation", controller.signal);
        if (!controller.signal.aborted) { setStatus(r.data); setError(""); }
      } catch (e) { if (!controller.signal.aborted) setError((e as Error).message); }
      finally { active = false; }
    };
    void read();
    const timer = setInterval(() => void read(), 15000);
    return () => { clearInterval(timer); controller.abort(); };
  }, [live]);
  const date = (s: string) => new Date(s).toLocaleString(getLocale(), { timeZone: "Asia/Shanghai", hour12: false });
  return <div className="glass benefit-reconciliation" role="status">
    {status?.status === "active" && <RefreshCw size={15} className="is-spinning" />}
    <strong>{t("每 3 小时自动校验")}</strong><span>{t("包含新账号、老账号的新角色，按原规则补齐")}</span>
    {status && <span>{!status.enabled ? t("后台校验已暂停") : status.status === "active" ? `${t("校验中")} ${status.checked}${t("项已检查")}` : status.nextAt ? `${t("下次")} ${date(status.nextAt)}` : t("等待首次校验")}</span>}
    {status?.completedAt && <span>{t("上轮完成")} {date(status.completedAt)}</span>}
    {status && status.blocked?.length > 0 && <span>{t("有")} {status.blocked.length} {t("条规则的物品版本待核对")}</span>}
    {(error || status?.error) && <span role="alert">{t(error || status?.error || "")}</span>}
  </div>;
}
