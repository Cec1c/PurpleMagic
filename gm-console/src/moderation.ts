export type ClearStats = {accountId:string;characterId:string;characterName:string;dungeonId:number;dungeonName:string;difficulty:number;count:number;lastClearAt:string;fastestMs:number;averageMs:number;peakHourly:number;fatigueSpent:number|null;averageFatigue:number|null;fatigueSamples:number};
export type BanTargets={lastLoginIp?:string;registrationIp?:string;qq?:string;groups?:string[];settingsRevision?:number};
export type QQAction={id:string;qq:string;group:string;state:string;cause:string;attempts:number};
export function qqActionLabel(a:QQAction){return `群 ${a.group}：${a.state==="succeeded"?(a.cause==="not_in_group"?"已列黑名单，当前不在群内":"已踢出并列入黑名单"):a.state==="running"?"正在处理":a.state==="retry"?(a.cause==="permission_denied"?"机器人权限不足，等待重试":"QQ 接口失败，等待重试"):"等待机器人处理"}`;}
export type Ban = {linkedIps?:string[];qqActions?:QQAction[];kind:"account"|"ip";target:string;label:string;reason:string;expiresAt:string|null;revoked:boolean;active:boolean;revision:number;updatedAt:string};
export type BanChange = {linked?:BanTargets;requestId:string;kind:"account"|"ip";target:string;reason:string;expiresAt:string|null;revoke:boolean;revision:number};
export type ModerationAccount = {id:string;name:string;lastLoginIp:string;registrationIp:string;disabled:boolean};
export type Page<T> = {rows:T[];hasMore:boolean};
export type PendingBan = {targets?:BanTargets;change:BanChange;label:string;confirmed:boolean};
export const pendingBanKey="purplemagic-moderation-pending-v1";
export function readPendingBan(storage:Pick<Storage,"getItem">):PendingBan|null {
  try {const p=JSON.parse(storage.getItem(pendingBanKey)||"null");return p?.confirmed && typeof p.change?.requestId==="string" && ["account","ip"].includes(p.change.kind) && typeof p.change.target==="string" ? p : null;}catch{return null;}
}
export function duration(ms:number){return ms>0?`${(ms/1000).toFixed(1)} 秒`:"—";}
export function banState(b:Ban,now=Date.now()){return b.revoked?"已解除":b.expiresAt&&Date.parse(b.expiresAt)<=now?"已到期":"封禁中";}
export function suspicious(c:ClearStats,seconds:number,hourly:number){return c.fastestMs>0&&seconds>0&&c.fastestMs<=seconds*1000||hourly>0&&c.peakHourly>=hourly;}

export type Sort = {sortBy:string;sortOrder:"asc"|"desc"};
export function nextSort(current:Sort,field:string):Sort {
 return {sortBy:field,sortOrder:current.sortBy===field&&current.sortOrder==="asc"?"desc":"asc"};
}
export function fatigue(value:number|null|undefined,average=false){return value==null?"—":average?value.toFixed(1):String(value);}
