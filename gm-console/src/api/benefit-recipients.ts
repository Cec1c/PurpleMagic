export type RecipientRow = { accountId: string; characterId: string; state: string; [key: string]: any };
export function partitionRecipients(rows: RecipientRow[]) {
  return {
    pending: rows.filter(r => r.state !== "sent" && r.state !== "ineligible"),
    sent: rows.filter(r => r.state === "sent"),
    ineligible: rows.filter(r => r.state === "ineligible"),
  };
}

// A cursor may advance through accounts with no characters, producing an empty
// page. Only the absence of a cursor means enumeration has finished.
export async function collectRecipients<T extends RecipientRow>(
  fetchPage: (cursor: string, signal: AbortSignal) => Promise<{ data: T[]; page?: { nextCursor?: string } }>,
  signal: AbortSignal,
  onPage: (rows: T[], next: string) => void,
  seed: T[] = [],
  start = "",
) {
  const rows = new Map(seed.map(r => [`${r.accountId}/${r.characterId}`, r]));
  const seen = new Set<string>();
  let cursor = start;
  do {
    signal.throwIfAborted();
    seen.add(cursor);
    const result = await fetchPage(cursor, signal);
    signal.throwIfAborted();
    const next = result.page?.nextCursor || "";
    if (next && seen.has(next)) throw new Error("查询游标未前进，请刷新进度");
    for (const row of result.data) rows.set(`${row.accountId}/${row.characterId}`, row);
    onPage([...rows.values()], next);
    cursor = next;
  } while (cursor);
}
