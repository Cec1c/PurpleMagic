import type { ConsoleAPI } from "./console";
import type { Pending } from "./model";

// Write results invalidate only the resources owned by that operation. Keep
// this list separate from navigation so new modules do not inherit a full reload.
export async function refreshOperation(api: ConsoleAPI, pending: Pending) {
  const paths = pending.steps.filter(s => s.done).map(s => s.path);
  const character = api.state.characters.find(c => c.id === api.current);
  const current = `/api/v1/characters/${encodeURIComponent(api.current)}/`;
  const account = `/api/v1/accounts/${encodeURIComponent(character?.accountId || "")}/`;
  const changed = (suffix: string) => paths.some(p => p === current + suffix || p === account + suffix);
  const jobs: (() => Promise<void>)[] = [];
  const growth: string[] = [];
  if (changed("level-operations") || changed("profession-operations")) growth.push("level", "skillPoints", "profession");
  else if (changed("skill-point-operations")) growth.push("skillPoints");
  if (growth.length) jobs.push(() => api.growth(growth));
  const balances = [changed("gold-operations") ? "gold" : "", changed("currency-adjustments") ? "currency" : ""].filter(Boolean);
  if (balances.length) jobs.push(() => api.balances(balances));
  if (changed("quest-operations") && api.sectionId === "quests") jobs.push(() => api.quests(api.meta.questFilter.status, api.meta.questFilter.q));
  if (paths.some(p => /^\/api\/v1\/benefits(?:\/|$)/.test(p))) jobs.push(() => api.benefits());
  if (paths.some(p => /^\/api\/v1\/tasks\/[^/]+\/pause$/.test(p))) jobs.push(() => api.tasks());
  if (paths.some(p => /^\/(api\/v1\/hub|admin\/api)\/news/.test(p))) {
    jobs.push(() => api.meta.hub?.newsManager ? api.hubNews(api.meta.hubNewsOffset, api.meta.hubNewsQuery) : api.hub());
  } else if (paths.some(p => /^\/(api\/v1\/hub|admin\/api)\/(source|connection)$/.test(p))) jobs.push(() => api.hub());
  await Promise.all(jobs.map(job => api.guard(job)));
}
