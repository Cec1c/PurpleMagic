export type ProfessionGroup = { value: string; label: string; choices: { value: string; label: string }[] };
export type ProfessionDefinition = { job: string | number; name: string; choices?: { growType: number; name: string }[] };
export function professionGroups(jobs: ProfessionDefinition[]): ProfessionGroup[] {
  return jobs.map(j => ({ value: String(j.job), label: j.name, choices: (j.choices || []).map(g => ({ value: `${j.job}:${g.growType}`, label: g.name || j.name })) })).filter(j => j.choices.length > 0);
}
export const demoProfessionGroups = professionGroups([
  { job: 0, name: "鬼剑士（男）", choices: [{ growType: 0, name: "鬼剑士（男）" }, { growType: 1, name: "剑魂" }, { growType: 17, name: "剑圣" }, { growType: 33, name: "剑神" }, { growType: 35, name: "帝血弑天" }] },
  { job: 1, name: "格斗家（女）", choices: [{ growType: 34, name: "极武圣" }] },
  { job: 2, name: "神枪手（男）", choices: [{ growType: 36, name: "机械元首" }] },
  { job: 3, name: "魔法师（女）", choices: [{ growType: 18, name: "月之女皇" }] },
  { job: 14, name: "圣职者（女）", choices: [{ growType: 33, name: "炽天使" }] },
]);
