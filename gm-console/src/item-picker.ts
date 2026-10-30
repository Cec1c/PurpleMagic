import type { Attachment, Item } from "./domain.ts";

export type CatalogItem = Pick<Item, "id" | "name" | "expiration">;
export const catalogBatchSizes = [300, 500, 1000] as const;

export function appendAttachments(current: Attachment[], selected: CatalogItem[], count: number): Attachment[] {
  if (!Number.isInteger(count) || count < 1 || count > 99999) throw new Error("数量须为 1–99999 的整数");
  const result = new Map(current.map(item => [item.id, { ...item }]));
  for (const item of new Map(selected.map(item => [item.id, item])).values()) {
    const previous = result.get(item.id);
    const nextCount = (previous?.count || 0) + count;
    if (nextCount > 99999) throw new Error(`${item.name} 的附件总数量不能超过 99999`);
    result.set(item.id, { ...item, count: nextCount, enhance: previous?.enhance || 0 });
  }
  return [...result.values()];
}
