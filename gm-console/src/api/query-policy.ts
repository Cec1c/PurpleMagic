import type { QueryPolicy } from "./query-cache";
export function queryKey(path: string) {
  const [base, search = ""] = path.split("?");
  const params = new URLSearchParams(search); params.sort();
  return base + (params.size ? "?" + params : "");
}
export function queryPolicy(path: string): QueryPolicy | undefined {
  const base = path.split("?")[0];
  const policy = (ttl: number, tags: string[], stale = 30000) => ({ ttl, stale, tags });
  if (/^\/api\/v1\/(items|quests)(?:\/[0-9]+(?:\/grant-options)?)?$/.test(base))
    return policy(20000, ["definitions"], 20000);
  if (/^\/api\/v1\/(accounts|characters)$/.test(base)) return policy(15000, ["directory", "players"]);
  const owner = base.match(/^\/api\/v1\/(accounts|characters)\/([^/]+)(.*)$/);
  if (owner) {
    const [, kind, id, tail] = owner;
    const tag = `${kind}:${id}`;
    if (!tail || tail === "/characters") return policy(15000, [tag, "directory", "players"]);
    if (/^\/(inventory(?:\/[^/]+\/\d+(?:\/options)?)?|mailbox(?:\/[^/]+)?|quests|level|skill-points|profession|adventure-name|gold|currency|premium)$/.test(tail))
      return policy(3000, [tag, "players", `${tag}:${tail.split('/')[1]}`]);
  }
  if (base === "/api/v1/presence") return policy(3000, ["presence"] , 3000);
  if (base === "/api/v1/benefits") return policy(30000, ["benefits"]);
  if (base === "/api/v1/activities") return policy(30000, ["activities"]);
  if (/^\/api\/v1\/anti-cheat\/(account-stats|clears)$/.test(base)) return policy(15000, ["moderation"]);
  if (base === "/api/v1/hub/news") return policy(30000, ["news"]);
  // Previews, operation receipts, secrets and live job progress are always live.
}
export function mutationTags(path: string): string[] | undefined {
  if (/^\/api\/auth\//.test(path) || path === "/api/v1/settings/pvf") return undefined;
  if (path.endsWith("/preview")) return [];
  const owner = path.match(/^\/api\/v1\/(accounts|characters)\/([^/]+)/);
  if (owner) {
    const tags = [`${owner[1]}:${owner[2]}`, "directory", "presence"];
    // Account-shared containers can be visible from more than one character.
    if (/inventory|mailbox|currency|premium/.test(path)) tags.push("players");
    return tags;
  }
  if (path.includes("/benefits")) return ["benefits", "players"];
  if (path.includes("/activities")) return ["activities"];
  if (path.includes("/news")) return ["news"];
  return ["players", "directory", "presence", "moderation"];
}
