import {useEffect,useRef,useState} from "react";
import {ShieldCheck,Timer,Search,RefreshCw,ArrowUpDown,ArrowUp,ArrowDown,Zap,Ban as BanIcon,UnlockKeyhole,LoaderCircle} from "lucide-react";
import {useLive} from "../api/context";
import {ApiError} from "../api/http";
import {Empty,Field,Modal,Tabs,type PageProps} from "../ui";
import {banState,duration,fatigue,nextSort,type Sort,pendingBanKey,readPendingBan,suspicious,type Ban,type BanChange,type ClearStats,type ModerationAccount,type Page,type PendingBan} from "../moderation";
import "../moderation.css";

const stamp=(raw:string)=>new Date(raw).toLocaleString();
const dateInput=(date:Date)=>new Date(date.getTime()-date.getTimezoneOffset()*60000).toISOString().slice(0,16);
type Tab="clears"|"accounts"|"bans";
export function SortHeader({fields,sort,onSort,disabled}:{fields:[string,string][];sort:Sort;onSort:(field:string)=>void;disabled:boolean}){
 const active=fields.some(([key])=>key===sort.sortBy);
 return <th aria-sort={active?(sort.sortOrder==="asc"?"ascending":"descending"):"none"}><div className="moderation-sort-fields">{fields.map(([key,label])=><button key={key} className="moderation-sort" disabled={disabled} onClick={()=>onSort(key)} title={`按${label}${sort.sortBy===key&&sort.sortOrder==="asc"?"降序":"升序"}排列`}>{label}{sort.sortBy===key?(sort.sortOrder==="asc"?<ArrowUp size={12}/>:<ArrowDown size={12}/>):<ArrowUpDown size={12}/>}</button>)}</div></th>;
}
export function AntiCheatPage({state}:PageProps){
 const live=useLive();
 const [tab,setTab]=useState<Tab>("clears"),[q,setQ]=useState(""),[from,setFrom]=useState(()=>dateInput(new Date(Date.now()-86400000))),[to,setTo]=useState(()=>dateInput(new Date()));
 const [seconds,setSeconds]=useState(30),[hourly,setHourly]=useState(60),[filter,setFilter]=useState("all");
 const [page,setPage]=useState<Page<any>>({rows:[],hasMore:false}),[offset,setOffset]=useState(0),[loaded,setLoaded]=useState(false),[loading,setLoading]=useState(false);
 const [ip,setIP]=useState(""),[pending,setPending]=useState<PendingBan|null>(()=>readPendingBan(sessionStorage)),[show,setShow]=useState(()=>!!readPendingBan(sessionStorage)),[busy,setBusy]=useState(false),[message,setMessage]=useState("");
 const [reason,setReason]=useState(""),[hours,setHours]=useState("24"),[writeError,setWriteError]=useState("");
 const [sorts,setSorts]=useState<Record<Tab,Sort>>({clears:{sortBy:"count",sortOrder:"desc"},accounts:{sortBy:"id",sortOrder:"asc"},bans:{sortBy:"updatedAt",sortOrder:"desc"}});
 const read=useRef<AbortController|null>(null);
 const canWrite=!!live&&!!state.api?.features["moderation.write"];
 async function load(next=0,which=tab,sorting=sorts[which]){
  read.current?.abort();const controller=new AbortController();read.current=controller;
  if(!live){setLoaded(true);return;}
  setLoading(true);
  await live.guard(async()=>{
   try {
    const params:Record<string,string|number|undefined>={search:q,offset:next,limit:50,...sorting};
    if(which==="clears")Object.assign(params,{from:new Date(from).toISOString(),to:new Date(to).toISOString(),maxDurationMs:filter==="fast"?seconds*1000:0,minHourlyPeak:filter==="frequent"?hourly:0});
    const result=await live.moderation(which,params,controller.signal);
    if(!controller.signal.aborted){setPage(result);setOffset(next);setLoaded(true);}
   }finally{if(!controller.signal.aborted)setLoading(false);}
  });
 }
 useEffect(()=>{setPage({rows:[],hasMore:false});setLoaded(false);void load(0,tab);return()=>read.current?.abort();},[tab,live,state.api?.resources.moderationRefresh?.revision]);
 async function prepare(kind:"account"|"ip",target:string,label:string,revoke=false,existing?:Ban){
  if(!live||pending?.confirmed||busy)return;
  setWriteError("");setMessage("");setReason("");setHours("24");
  await live.guard(async()=>{
   const result=existing?{rows:[existing]}:await live.moderation("bans",{search:target,limit:100});
   const prior=(result.rows as Ban[]).find(v=>v.kind===kind&&(kind==="ip"||v.target===target));
   setPending({change:{requestId:crypto.randomUUID(),kind,target:prior?.target||target,reason:"",expiresAt:null,revoke,revision:prior?.revision||0},label,confirmed:false});setShow(true);
  });
 }
 async function confirm(){
  if(!pending||!live||busy)return;
  let operation=pending;
  if(!operation.confirmed){
   const change:BanChange={...operation.change,reason:reason.trim(),expiresAt:!operation.change.revoke&&hours!=="0"?new Date(Date.now()+Number(hours)*3600000).toISOString():null};
   if(!change.reason){setWriteError("请填写操作原因");return;}
   operation={...operation,change,confirmed:true};
   sessionStorage.setItem(pendingBanKey,JSON.stringify(operation));setPending(operation);
  }
  setBusy(true);setWriteError("");
  try{
   const result=await live.changeBan(operation.change);
   sessionStorage.removeItem(pendingBanKey);setPending(null);setShow(false);
   setMessage(`${result.label}：${banState(result)}`);void load(offset);
  }catch(e){
   if(e instanceof ApiError&&(e.status===400||e.status===409)){sessionStorage.removeItem(pendingBanKey);setPending(null);setShow(false);setMessage(e.message);void load(0);}
   else{setWriteError((e as Error).message);if(e instanceof ApiError&&e.status===401)live.error(e);}
  }finally{setBusy(false);}
 }
 function close(){if(busy)return;setShow(false);if(!pending?.confirmed)setPending(null);}
 function changeSort(field:string){const next=nextSort(sorts[tab],field);setSorts(previous=>({...previous,[tab]:next}));void load(0,tab,next);}
 const header=(fields:[string,string][]) => <SortHeader fields={fields} sort={sorts[tab]} onSort={changeSort} disabled={loading}/>;
 const rows=page.rows as ClearStats[];
 const sampled=rows.reduce((n,c)=>n+(c.fatigueSamples||0),0);
 const spent=rows.reduce((n,c)=>n+(c.fatigueSpent||0),0);
 return <div className="motion-section moderation-page">
  <Tabs label="反作弊功能" items={[{id:"clears",label:"通关统计"},{id:"accounts",label:"账号封禁"},{id:"bans",label:"IP 与封禁记录"}]} value={tab} onChange={v=>setTab(v as Tab)}/>
  {pending?.confirmed&&!show&&<div className="moderation-notice glass">有一笔操作等待回执<button className="secondary" onClick={()=>setShow(true)}>继续查询</button></div>}
  {message&&<div role="status" className="moderation-notice glass">{message}</div>}
  <div className="moderation-filters glass">
   <Field label={tab==="clears"?"角色 / 账号 ID / 地下城":"账号 / ID / IP"}><input value={q} onChange={e=>setQ(e.target.value)} placeholder="输入关键词" onKeyDown={e=>{if(e.key==="Enter")void load(0);}}/></Field>
   {tab==="clears"&&<>
    <Field label="开始时间"><input type="datetime-local" value={from} onChange={e=>setFrom(e.target.value)}/></Field>
    <Field label="结束时间"><input type="datetime-local" value={to} onChange={e=>setTo(e.target.value)}/></Field>
    <Field label="筛选"><select value={filter} onChange={e=>setFilter(e.target.value)}><option value="all">全部通关</option><option value="fast">低于耗时阈值</option><option value="frequent">高于频率阈值</option></select></Field>
    <Field label="耗时阈值（秒）"><input type="number" min="1" max="86400" value={seconds} onChange={e=>setSeconds(Number(e.target.value))}/></Field>
    <Field label="单小时次数阈值"><input type="number" min="1" max="100000" value={hourly} onChange={e=>setHourly(Number(e.target.value))}/></Field>
   </>}
   <button className="primary" disabled={loading} onClick={()=>void load(0)}>{loading?<LoaderCircle size={16} className="spin"/>:<Search size={16}/>}查询</button>
  </div>
  {tab==="clears"?<>
   <div className="moderation-overview glass"><div><ShieldCheck size={19}/><span>当前页角色 × 地下城<strong>{page.rows.length}</strong></span></div><div><Timer size={19}/><span>当前页通关<strong>{rows.reduce((n,c)=>n+c.count,0)}</strong></span></div><div><BanIcon size={19}/><span>达到阈值<strong>{rows.filter(c=>suspicious(c,seconds,hourly)).length}</strong></span></div><div><Zap size={19}/><span>当前页已采集疲劳<strong>{sampled?spent:"—"}</strong></span></div></div>
   <p className="moderation-caption">统计采集启用后的成功结算。耗时从服务器接受入场到通关；疲劳按成功通关局内的实际扣除累计，平均值只计算已采集局次，未采集显示“—”。频率为每个整小时的通关峰值。阈值用于筛选，封禁由管理员确认。</p>
   <div className="table-panel glass moderation-table"><div className="table-scroll"><table><thead><tr>{header([["characterName","角色"],["characterId","角色 ID"],["accountId","账号"]])}{header([["dungeonName","地下城"],["dungeonId","ID"],["difficulty","难度"]])}{header([["count","次数"]])}{header([["fastestMs","最快"],["averageMs","平均"]])}{header([["fatigueSpent","消耗疲劳"],["averageFatigue","平均每次"]])}{header([["peakHourly","单小时峰值"]])}{header([["lastClearAt","最近通关"]])}<th>操作</th></tr></thead><tbody>{rows.map(c=><tr key={`${c.characterId}/${c.dungeonId}/${c.difficulty}`}><td><strong>{c.characterName||c.characterId}</strong><small>角色 {c.characterId} · {c.accountId}</small></td><td>{c.dungeonName||c.dungeonId}<small>ID {c.dungeonId} · 难度 {c.difficulty}</small></td><td>{c.count}</td><td className={c.fastestMs>0&&c.fastestMs<=seconds*1000?"moderation-flag":""}>{duration(c.fastestMs)}<small>{duration(c.averageMs)}</small></td><td>{fatigue(c.fatigueSpent)}<small>平均 {fatigue(c.averageFatigue,true)} · {c.fatigueSamples||0}/{c.count} 局已采集</small></td><td className={c.peakHourly>=hourly?"moderation-flag":""}>{c.peakHourly} 次</td><td>{stamp(c.lastClearAt)}</td><td><button className="text-button" disabled={!canWrite||!!pending?.confirmed} onClick={()=>void prepare("account",c.accountId,c.accountId)}>封禁账号</button></td></tr>)}</tbody></table></div>{loaded&&!rows.length&&<Empty title="当前条件下没有通关记录"/>}</div>
  </>:tab==="accounts"?<div className="table-panel glass moderation-table"><div className="table-scroll"><table><thead><tr>{header([["name","账号"],["id","ID"]])}{header([["lastLoginIp","最近登录 IP"]])}{header([["registrationIp","注册 IP"]])}{header([["disabled","登录限制"]])}<th>操作</th></tr></thead><tbody>{(page.rows as ModerationAccount[]).map(a=><tr key={a.id}><td><strong>{a.name}</strong><small>{a.id}</small></td><td>{a.lastLoginIp||"—"}</td><td>{a.registrationIp||"—"}</td><td>{a.disabled?"已限制":"正常"}</td><td><div className="moderation-actions"><button className="text-button" disabled={!canWrite||!!pending?.confirmed} onClick={()=>void prepare("account",a.id,a.name)}>封禁账号</button>{a.lastLoginIp&&<button className="text-button" disabled={!canWrite||!!pending?.confirmed} onClick={()=>void prepare("ip",a.lastLoginIp,a.lastLoginIp)}>封禁登录 IP</button>}</div></td></tr>)}</tbody></table></div>{loaded&&!page.rows.length&&<Empty title="没有匹配的账号"/>}</div>:<>
   <div className="moderation-ip glass"><Field label="封禁 IP"><input value={ip} onChange={e=>setIP(e.target.value)} placeholder="IPv4 或 IPv6 地址"/></Field><button className="secondary" disabled={!canWrite||!ip.trim()||!!pending?.confirmed} onClick={()=>void prepare("ip",ip.trim(),ip.trim())}><BanIcon size={15}/>封禁 IP</button></div>
   <div className="table-panel glass moderation-table"><div className="table-scroll"><table><thead><tr>{header([["label","目标"],["kind","类型"],["target","ID / IP"]])}{header([["state","状态"]])}{header([["reason","原因"]])}{header([["expiresAt","到期时间"]])}{header([["updatedAt","更新时间"]])}<th>操作</th></tr></thead><tbody>{(page.rows as Ban[]).map(b=><tr key={`${b.kind}/${b.target}`}><td><strong>{b.label}</strong><small>{b.kind==="ip"?"IP 地址":"账号"} · {b.target}</small></td><td>{banState(b)}</td><td>{b.reason}</td><td>{b.expiresAt?stamp(b.expiresAt):"永久"}</td><td>{stamp(b.updatedAt)}</td><td><button className="text-button" disabled={!canWrite||!!pending?.confirmed} onClick={()=>void prepare(b.kind,b.target,b.label,banState(b)==="封禁中",b)}>{banState(b)==="封禁中"?<><UnlockKeyhole size={14}/>解除封禁</>:"重新封禁"}</button></td></tr>)}</tbody></table></div>{loaded&&!page.rows.length&&<Empty title="没有封禁记录"/>}</div>
  </>}
  <div className="moderation-pagination"><span>第 {offset/50+1} 页 · 当前 {page.rows.length} 条</span><button className="secondary" disabled={loading||offset===0} onClick={()=>void load(Math.max(0,offset-50))}>上一页</button><button className="secondary" disabled={loading||!page.hasMore} onClick={()=>void load(offset+50)}>下一页</button><button className="text-button" disabled={loading} onClick={()=>void load(offset)}><RefreshCw size={14}/>刷新</button></div>
  {pending&&show&&<Modal title={pending.change.revoke?"解除封禁":"确认封禁"} onClose={close} dismissOnBackdrop={!busy} footer={<><button className="secondary" disabled={busy} onClick={close}>{pending.confirmed?"稍后继续":"取消"}</button><button className="primary" disabled={busy||(!pending.confirmed&&!reason.trim())} onClick={()=>void confirm()}>{busy?<LoaderCircle size={16} className="spin"/>:null}{pending.confirmed?"使用原请求重试":pending.change.revoke?"确认解除":"确认封禁"}</button></>}>
   <div className="moderation-confirm"><strong>{pending.label}</strong><p>{pending.change.kind==="ip"?"IP":"账号"}：{pending.change.target}</p><p>{pending.change.revoke?"解除本条封禁记录。":pending.change.kind==="ip"?"阻止此 IP 注册、登录和进入游戏，并断开匹配的游戏连接。":"阻止此账号登录和进入游戏，并断开当前游戏连接。"}</p>
   {pending.confirmed?<><p>原因：{pending.change.reason}</p><p>期限：{pending.change.expiresAt?stamp(pending.change.expiresAt):"永久"}</p></>:<><Field label="原因"><textarea maxLength={160} value={reason} onChange={e=>setReason(e.target.value)} autoFocus/></Field>{!pending.change.revoke&&<Field label="封禁期限"><select value={hours} onChange={e=>setHours(e.target.value)}><option value="1">1 小时</option><option value="24">1 天</option><option value="168">7 天</option><option value="720">30 天</option><option value="0">永久</option></select></Field>}</>}
   {writeError&&<p role="alert">{writeError}</p>}</div>
  </Modal>}
 </div>;
}
