/** Page sections render in this order; remove an ID to remove its widget. */
export const dashboardLayout = [
  "overview",
  "metrics",
  "registrations",
  "news",
] as const;
/** Optional, aggregate-only contract. Missing values are unknown, never zero. */
export type MetricKey =
  | "accounts"
  | "characters"
  | "online"
  | "registrationsToday"
  | "dailyActive"
  | "weeklyActive"
  | "fatigueToday"
  | "clearsToday"
  | "fastClearsToday";
export type DayCount = { date: string; count: number };
export type DashboardSnapshot = {
  observedAt: string;
  timezone: "Asia/Shanghai";
  metrics: Partial<Record<MetricKey, number>>;
  registrations?: DayCount[];
  fastThresholdMs?: number;
  fatigueSamples?: number;
};
export type NewsItem = {
  title: string;
  body: string;
  date: string;
  category: string;
};
export type MetricDefinition = {
  id: MetricKey;
  label: string;
  unit: string;
  detail: string;
  section: "accounts" | "anti-cheat";
  icon: "users" | "online" | "new" | "activity" | "fatigue" | "clear" | "speed";
};
// Add, remove or reorder a metric here. Rendering and availability are shared.
export const dashboardMetrics: readonly MetricDefinition[] = [
  {
    id: "accounts",
    label: "总账号数",
    unit: "个",
    detail: "全部注册账号",
    section: "accounts",
    icon: "users",
  },
  {
    id: "characters",
    label: "角色数",
    unit: "位",
    detail: "当前未删除角色",
    section: "accounts",
    icon: "users",
  },
  {
    id: "online",
    label: "当前在线",
    unit: "人",
    detail: "游戏在线角色快照",
    section: "accounts",
    icon: "online",
  },
  {
    id: "registrationsToday",
    label: "今日新注册",
    unit: "个",
    detail: "今日 00:00 起",
    section: "accounts",
    icon: "new",
  },
  {
    id: "dailyActive",
    label: "今日日活",
    unit: "个账号",
    detail: "今日成功登录账号",
    section: "accounts",
    icon: "activity",
  },
  {
    id: "weeklyActive",
    label: "本周周活",
    unit: "个账号",
    detail: "本周一至今成功登录账号",
    section: "accounts",
    icon: "activity",
  },
  {
    id: "fatigueToday",
    label: "疲劳消耗",
    unit: "点",
    detail: "今日已记录通关消耗",
    section: "anti-cheat",
    icon: "fatigue",
  },
  {
    id: "clearsToday",
    label: "副本通关",
    unit: "次",
    detail: "今日已结算通关记录",
    section: "anti-cheat",
    icon: "clear",
  },
  {
    id: "fastClearsToday",
    label: "快速通关",
    unit: "次",
    detail: "耗时低于设置阈值",
    section: "anti-cheat",
    icon: "speed",
  },
];
export const countValue = (value: unknown): value is number =>
  typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
export function parseSnapshot(raw: unknown): DashboardSnapshot | undefined {
  const v = raw as Partial<DashboardSnapshot> | null;
  if (
    !v ||
    v.timezone !== "Asia/Shanghai" ||
    typeof v.observedAt !== "string" ||
    !Number.isFinite(Date.parse(v.observedAt)) ||
    !v.metrics ||
    typeof v.metrics !== "object"
  )
    return;
  const metrics: DashboardSnapshot["metrics"] = {};
  for (const { id } of dashboardMetrics)
    if (countValue(v.metrics[id])) metrics[id] = v.metrics[id];
  let registrations: DayCount[] | undefined;
  if (
    Array.isArray(v.registrations) &&
    v.registrations.length <= 366 &&
    v.registrations.every(
      (d) => d && validDay(d.date) && countValue(d.count),
    ) &&
    new Set(v.registrations.map((d) => d.date)).size === v.registrations.length
  )
    registrations = [...v.registrations].sort((a, b) =>
      a.date.localeCompare(b.date),
    );
  return {
    observedAt: v.observedAt,
    timezone: v.timezone,
    metrics,
    registrations,
    fastThresholdMs: countValue(v.fastThresholdMs)
      ? v.fastThresholdMs
      : undefined,
    fatigueSamples: countValue(v.fatigueSamples) ? v.fatigueSamples : undefined,
  };
}
function validDay(value: unknown): value is string {
  return (
    typeof value === "string" &&
    /^\d{4}-\d{2}-\d{2}$/.test(value) &&
    Number.isFinite(Date.parse(value)) &&
    new Date(value).toISOString().slice(0, 10) === value
  );
}
export function visibleMetrics(snapshot?: DashboardSnapshot) {
  return dashboardMetrics.filter((m) => countValue(snapshot?.metrics[m.id]));
}
export function latestNews(raw: unknown): NewsItem[] | undefined {
  if (!Array.isArray(raw)) return;
  return raw
    .filter(
      (n) => n && typeof n.title === "string" && typeof n.body === "string",
    )
    .map((n) => ({
      title: n.title.slice(0, 160),
      body: n.body.slice(0, 12000),
      date: typeof n.date === "string" ? n.date.slice(0, 32) : "",
      category:
        typeof n.category === "string" ? n.category.slice(0, 32) : "公告",
    }))
    .sort((a, b) => (Date.parse(b.date) || 0) - (Date.parse(a.date) || 0))
    .slice(0, 3);
}
export function heatLevel(count: number, maximum: number) {
  return count === 0
    ? 0
    : Math.min(4, Math.max(1, Math.ceil((count / Math.max(1, maximum)) * 4)));
}
export function heatmapCells(days: DayCount[]): (DayCount | null)[] {
  if (!days.length) return [];
  const byDate = new Map(days.map((d) => [d.date, d]));
  const first = new Date(days[0].date + "T00:00:00Z"),
    last = new Date(days[days.length - 1].date + "T00:00:00Z");
  const cells: (DayCount | null)[] = Array((first.getUTCDay() + 6) % 7).fill(
    null,
  );
  // Omitted dates remain unknown. The API fills actual zero days explicitly.
  for (
    let at = first.getTime();
    at <= last.getTime() && cells.length < 373;
    at += 86400000
  )
    cells.push(byDate.get(new Date(at).toISOString().slice(0, 10)) ?? null);
  return cells;
}
