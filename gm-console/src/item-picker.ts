import type { Attachment, Item } from "./domain.ts";

export type CatalogItem = Pick<Item, "id" | "name" | "expiration"> & {
  category?: string;
  kind?: string;
  subtype?: string;
  remote?: { kind?: string; subtype?: string };
};
export const catalogBatchSizes = [300, 500, 1000] as const;

export function isSingleCountEquipment(item: { kind?: string; category?: string; subtype?: string; remote?: { kind?: string; subtype?: string } }): boolean {
  const kind = item.kind || item.remote?.kind;
  if (kind === "equipment") return true;
  return ["武器", "防具", "首饰", "辅助装备", "特殊装备", "装备", "时装", "宠物", "宠物装备"].includes(item.category || "");
}

const ENHANCEABLE_SUBTYPES = new Set([
  "weapon",
  "coat",
  "shoulder",
  "pants",
  "shoes",
  "waist",
  "amulet",
  "wrist",
  "ring",
  "support",
  "magic stone",
  "support weapon",
]);

export function canEnhanceAttachment(item: { kind?: string; category?: string; subtype?: string }): boolean {
  const sub = (item.subtype || "").trim().toLowerCase();
  if (sub) {
    return (!item.kind || item.kind === "equipment") && ENHANCEABLE_SUBTYPES.has(sub);
  }
  if (["时装", "徽章", "宠物", "宠物装备", "称号"].includes(item.category || "")) return false;
  return item.kind === "equipment" || ["武器", "防具", "首饰", "辅助装备", "特殊装备", "装备"].includes(item.category || "");
}

export function canForgeAttachment(item: { kind?: string; category?: string; subtype?: string }): boolean {
  const sub = (item.subtype || "").trim().toLowerCase();
  if (sub) return (!item.kind || item.kind === "equipment") && sub === "weapon";
  return item.category === "武器";
}

function clampInt(value: unknown, max: number): number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0
    ? Math.min(max, value)
    : 0;
}

function cleanAttachment(item: Attachment): Attachment {
  const kind = item.kind;
  const subtype = item.subtype;
  const category = item.category;
  const enhanceable = canEnhanceAttachment({ kind, category, subtype });
  const forgeable = canForgeAttachment({ kind, category, subtype });
  return {
    id: item.id,
    name: item.name,
    ...(item.expiration ? { expiration: item.expiration } : {}),
    ...(kind ? { kind } : {}),
    ...(subtype ? { subtype } : {}),
    ...(category ? { category } : {}),
    count: item.count,
    enhance: enhanceable ? clampInt(item.enhance, 31) : 0,
    ...(forgeable ? { forge: clampInt(item.forge, 8) } : {}),
  };
}

export function appendAttachments(current: Attachment[], selected: CatalogItem[], count: number): Attachment[] {
  if (!Number.isInteger(count) || count < 1 || count > 99999) throw new Error("数量须为 1–99999 的整数");
  const result = new Map(current.map(item => [item.id, cleanAttachment(item)]));
  for (const item of new Map(selected.map(item => [item.id, item])).values()) {
    const previous = result.get(item.id);
    const single = isSingleCountEquipment(item);
    const prevCount = previous && Number.isInteger(previous.count) && previous.count > 0 ? previous.count : 0;
    const nextCount = single ? 1 : prevCount + count;
    if (nextCount > 99999) throw new Error(`${item.name} 的附件总数量不能超过 99999`);
    const kind = item.kind || item.remote?.kind || previous?.kind;
    const subtype = item.subtype || item.remote?.subtype || previous?.subtype;
    const category = item.category || previous?.category;
    const expiration = item.expiration ?? previous?.expiration;
    const enhanceable = canEnhanceAttachment({ kind, category, subtype });
    const forgeable = canForgeAttachment({ kind, category, subtype });
    result.set(item.id, {
      id: item.id,
      name: item.name,
      ...(expiration ? { expiration } : {}),
      ...(kind ? { kind } : {}),
      ...(subtype ? { subtype } : {}),
      ...(category ? { category } : {}),
      count: nextCount,
      enhance: enhanceable ? clampInt(previous?.enhance, 31) : 0,
      ...(forgeable ? { forge: clampInt(previous?.forge, 8) } : {}),
    });
  }
  return [...result.values()];
}
