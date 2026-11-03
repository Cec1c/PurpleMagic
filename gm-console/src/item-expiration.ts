import { getLocale } from "./i18n.ts";
import { t } from "./i18n.ts";
import type { ItemExpiration } from "./domain";

export function itemExpirationLabel(expiration?: ItemExpiration, now = Date.now()): string {
  if (!expiration?.known) return t("期限未读取");
  const parts: string[] = [];
  const at = expiration.absoluteExpireTime;
  if (Number.isFinite(at) && at > 0) {
    const date = new Date(at * 1000);
    const text = new Intl.DateTimeFormat(getLocale(), { timeZone: "Asia/Shanghai", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false }).format(date);
    parts.push(`${text} ${t("到期（北京时间）")}${at * 1000 <= now ? ` · ${t("已过期")}` : ""}`);
  }
  if (Number.isFinite(expiration.usablePeriodDays) && expiration.usablePeriodDays > 0)
    parts.push(`${t("有效期")} ${expiration.usablePeriodDays} ${t("天")}`);
  return parts.join(" · ") || t("永久");
}
export function itemOptionLabel(item: { id: string; name: string; expiration?: ItemExpiration }): string {
  return `${item.name} · ID ${item.id} · ${itemExpirationLabel(item.expiration)}`;
}
