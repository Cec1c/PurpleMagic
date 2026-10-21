export type Activity = {
  id: number;
  name: string;
  description: string;
  source: string;
  defaultEnabled: boolean;
  enabled: boolean;
  effectiveEnabled: boolean;
  pendingRestart: boolean;
};
export type ActivitySnapshot = {
  revision: number;
  activities: Activity[];
  restartRequired: boolean;
};
export type ActivityChange = { id: number; enabled: boolean };
export function activityChanges(snapshot: ActivitySnapshot, changes: ActivityChange[]) {
  if (!Number.isSafeInteger(snapshot.revision) || snapshot.revision < 0)
    throw new Error("活动配置版本无效，请刷新");
  if (!changes.length || changes.length > 256) throw new Error("请选择 1–256 个活动");
  const seen = new Set<number>();
  const rows = changes.map((change) => {
    const row = snapshot.activities.find((v) => v.id === change.id);
    if (!Number.isInteger(change.id) || change.id < 1 || change.id > 65535 ||
      !row || seen.has(change.id) || typeof change.enabled !== "boolean")
      throw new Error("活动列表已变化，请刷新后重新选择");
    seen.add(change.id);
    return { id: row.id, name: row.name, before: row.enabled, enabled: change.enabled };
  }).filter((v) => v.before !== v.enabled);
  if (!rows.length) throw new Error("所选活动已经是目标配置");
  return rows;
}
export function activityStatesMatch(snapshot: ActivitySnapshot, changes: ActivityChange[]) {
  return changes.length > 0 && changes.every((change) =>
    snapshot.activities.some((row) => row.id === change.id && row.enabled === change.enabled));
}
export const demoActivities: ActivitySnapshot = {
  revision: 0, restartRequired: false,
  activities: [
    { id: 145, name: "大富翁骰子", description: "通关推荐地下城增加掷骰次数，奖励通过邮件发放，每日 06:00 重置。", source: "event/attendancemarble.evt", defaultEnabled: true },
    { id: 331, name: "普通签到", description: "等级 17 起，账号共享签到进度，每日 06:00 更新活动日。", source: "AttendanceDailyEvent.evt", defaultEnabled: true },
    { id: 2347, name: "累计在线得好礼", description: "查看每日在线时长与累计天数，领取奖励邮件。", source: "PCRoomTimePoint", defaultEnabled: true },
    { id: 2395, name: "红尾乔纳森", description: "开启中央公园地铁中心 NPC 红尾乔纳森的可见性。", source: "npc/476_jonathan.npc", defaultEnabled: true },
    { id: 2365, name: "竞技大赛", description: "开启原生竞猜界面、赛事状态及下注流程。", source: "原生事件 2365", defaultEnabled: false },
  ].map((row) => ({ ...row, enabled: row.defaultEnabled, effectiveEnabled: row.defaultEnabled, pendingRestart: false })),
};
