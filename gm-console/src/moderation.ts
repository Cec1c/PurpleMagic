export type ClearStats = {accountId:string;characterId:string;characterName:string;dungeonId:number;dungeonName:string;difficulty:number;count:number;lastClearAt:string;fastestMs:number;averageMs:number;peakHourly:number};
export type Ban = {kind:"account"|"ip";target:string;label:string;reason:string;expiresAt:string|null;revoked:boolean;active:boolean;revision:number;updatedAt:string};
export type BanChange = {requestId:string;kind:"account"|"ip";target:string;reason:string;expiresAt:string|null;revoke:boolean;revision:number};
export type ModerationAccount = {id:string;name:string;lastLoginIp:string;registrationIp:string;disabled:boolean};
export type Page<T> = {rows:T[];hasMore:boolean};
export type PendingBan = {change:BanChange;label:string;confirmed:boolean};
export const pendingBanKey="purplemagic-moderation-pending-v1";
export function readPendingBan(storage:Pick<Storage,"getItem">):PendingBan|null {
  try {const p=JSON.parse(storage.getItem(pendingBanKey)||"null");return p?.confirmed && typeof p.change?.requestId==="string" && ["account","ip"].includes(p.change.kind) && typeof p.change.target==="string" ? p : null;}catch{return null;}
}
export function duration(ms:number){return ms>0?`${(ms/1000).toFixed(1)} 秒`:"—";}
export function banState(b:Ban,now=Date.now()){return b.revoked?"已解除":b.expiresAt&&Date.parse(b.expiresAt)<=now?"已到期":"封禁中";}
export function suspicious(c:ClearStats,seconds:number,hourly:number){return c.fastestMs>0&&seconds>0&&c.fastestMs<=seconds*1000||hourly>0&&c.peakHourly>=hourly;}
