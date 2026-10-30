import type { ConsoleAPI } from "./console";
import type { Command, Selection } from "../domain";
import type { DTO, Pending, Step } from "./model";
import { ApiError, segment } from "./http";
import { itemExpirationLabel } from "../item-expiration";

// Stop starting work after an error, but drain already-started requests before
// returning. Results keep attachment order regardless of response order.
export async function mapMailBatch<T, R>(rows: T[], run: (row: T, index: number) => Promise<R>): Promise<R[]> {
  const out = new Array<R>(rows.length);
  let cursor = 0, failed = false, error: unknown;
  await Promise.all(Array.from({ length: Math.min(4, rows.length) }, async () => {
    while (!failed && cursor < rows.length) {
      const index = cursor++;
      try { out[index] = await run(rows[index], index); }
      catch (e) { failed = true; error ??= e; }
    }
  }));
  if (failed) throw error;
  return out;
}

export async function prepareMail(api: ConsoleAPI, command: Extract<Command, {kind: "mail"}>, selection: Selection, p: Pending) {
  if (!command.title.trim()) throw new Error("请填写邮件标题");
  for (const a of command.items) {
    if (!/^[1-9][0-9]*$/.test(a.id) || !Number.isSafeInteger(a.count) || a.count < 1 || a.count > 2147483647)
      throw new Error("请核对物品 ID 与数量");
  }
  const recipients = await api.resolveSelection(selection);
  if (!recipients.length) throw new Error("请先选择收件人");
  const targets = recipients.map(c => ({ accountId: c.accountId, characterId: c.id }));
  const owners = new Map(recipients.map(c => [c.id, c.accountId]));
  p.target = `${recipients.length} 个收件角色`;
  // All previews validate the item and its options on the server. One fresh
  // generation is sufficient for the whole batch; no per-item options lookup.
  const options: DTO = command.items.length
    ? (await api.http.get(`/api/v1/items/${segment(command.items[0].id)}/grant-options`)).data : {};
  const attachments = command.items.length ? command.items : [null];
  const prepared = await mapMailBatch(attachments, async (a, i) => {
    const reward: DTO = { title: command.title, body: command.body, gold: i === 0 ? command.gold : 0, recipients: targets };
    if (a) Object.assign(reward, { itemId: a.id, count: a.count, pvfChecksum: options.pvfChecksum,
      options: { reinforce: a.enhance, amplifyType: 0, forgingLevel: 0, expireDays: 0, equipmentQuality: "default" } });
    const job = await api.http.post("/api/v1/mail/preview", reward, `${p.id}-preview-${i}`);
    const { data } = await api.http.get(`/api/v1/tasks/${job.id}/confirmation`);
    const names = (data.recipients || []).map((v: DTO) => ({ id: v.characterId, name: v.name, accountId: v.accountId }));
    if (names.length !== recipients.length || new Set(names.map((v: DTO) => v.id)).size !== recipients.length ||
      names.some((v: DTO) => owners.get(v.id) !== v.accountId)) throw new Error("收件名单已变更，请重新选择");
    let expiry = a?.expiration;
    const actual = job.reward?.attachments?.[0];
    if (a && actual && Object.hasOwn(actual, "expiresAt")) {
      const timestamp = actual.expiresAt ? Date.parse(actual.expiresAt) / 1000 : 0;
      if (Number.isFinite(timestamp)) expiry = { known: true, absoluteExpireTime: timestamp, usablePeriodDays: 0 };
    } else if (i === 0 && options.expiration) expiry = { known: true, absoluteExpireTime: options.expiration.defaultExpireTime || 0, usablePeriodDays: options.expiration.usablePeriodDays || 0 };
    const step: Step = { id: `${p.id}-${i}`, label: `邮件 ${i + 1} / ${attachments.length}${a ? ` · ${a.name}（ID ${a.id}）× ${a.count} · ${itemExpirationLabel(expiry)}` : ""}`,
      path: `/api/v1/tasks/${job.id}/run`, body: { revision: job.revision, confirm: true, retry: false }, kind: "task", result: job };
    return { step, names };
  });
  p.steps.push(...prepared.map(v => v.step));
  p.recipients = prepared[0].names;
  p.summary = `${command.title}；每人金币 ${command.gold.toLocaleString()}；${attachments.length} 封邮件${attachments.length > 1 ? "（每种附件一封，金币仅随第一封）" : ""}`;
}

export async function scheduleMail(api: ConsoleAPI, step: Step) {
  const taskPath = step.path.replace(/\/(run|schedule)$/, "");
  // Persist intent first. Reloads and lost replies always reuse this task ID.
  if (step.attempted) {
    const task = (await api.http.get(taskPath)).data;
    if (task.schedule === "active" || task.schedule === "completed" || (task.pending === 0 && (!step.body.retry || task.failed === 0))) {
      step.result = task;
      return;
    }
    step.body.revision = task.revision;
  }
  step.attempted = true;
  api.persistPending();
  try { step.result = await api.http.post(taskPath + "/schedule", step.body, step.id); }
  catch (e) {
    if (!(e instanceof ApiError && e.status === 409)) throw e;
    const task = (await api.http.get(taskPath)).data;
    if (task.schedule !== "active" && task.schedule !== "completed") throw e;
    step.result = task;
  }
}
