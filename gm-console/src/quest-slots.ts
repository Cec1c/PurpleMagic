export function questSlotSummary(slots?: { before: number; after: number; requested: number }, done = false): string {
  if (!slots) return "";
  return [[1, "辅助装备（左槽）"], [2, "魔法石（右槽）"], [16, "耳环"]]
    .filter(([bit]) => (slots.requested & Number(bit)) !== 0)
    .map(([bit, name]) => `${name}：${slots.before & Number(bit) ? "已开启" : slots.after & Number(bit) ? (done ? "已开启" : "将开启") : "未开启"}`)
    .join(" · ");
}
