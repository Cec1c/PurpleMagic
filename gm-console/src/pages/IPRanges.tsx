import {useEffect,useRef,useState} from "react";
import {LoaderCircle,RefreshCw} from "lucide-react";
import {useLive} from "../api/context";
import {Empty,Field,Modal} from "../ui";

type Preview={target:string;first:string;last:string;count:number};
type Range=Preview & {id:string;reason:string;expiresAt:string|null;createdAt:string;status:string;applied:number;retained:number;conflict:number;pending:number;released:number;error?:string};
const status:Record<string,string>={applying:"正在封禁",active:"已封禁",partial:"部分记录已变化",releasing:"正在解除",released:"已解除",expired:"已到期"};
export function IPRangeBans(){
 const live=useLive();const [target,setTarget]=useState(""),[rows,setRows]=useState<Range[]>([]),[loading,setLoading]=useState(false),[busy,setBusy]=useState(false),[message,setMessage]=useState("");
 const [preview,setPreview]=useState<Preview|null>(null),[release,setRelease]=useState<Range|null>(null),[reason,setReason]=useState(""),[hours,setHours]=useState("24");
 const request=useRef<{target:string;reason:string;expiresAt:string|null;requestId:string}|null>(null);
 const polling=useRef(false);
 async function load(signal?:AbortSignal){
  if(!live||polling.current)return;polling.current=true;
  try{const data=await live.ipRanges(signal);if(!signal?.aborted)setRows(data);}catch(e){if(!signal?.aborted)setMessage((e as Error).message)}finally{polling.current=false;}
 }
 useEffect(()=>{const controller=new AbortController();void load(controller.signal);const timer=setInterval(()=>void load(controller.signal),3000);return()=>{controller.abort();clearInterval(timer)}},[live]);
 async function prepare(){
  if(!live||loading)return;setLoading(true);setMessage("");
  try{setPreview(await live.previewIPRange(target));request.current=null;setReason("");setHours("24");}catch(e){setMessage((e as Error).message)}finally{setLoading(false)}
 }
 async function confirm(){
  if(!live||busy)return;setBusy(true);setMessage("");
  try{
   if(release){await live.releaseIPRange(release.id);setRelease(null)}
   else if(preview){request.current??={target:preview.target,reason:reason.trim(),expiresAt:hours==="0"?null:new Date(Date.now()+Number(hours)*3600000).toISOString(),requestId:crypto.randomUUID()};await live.createIPRange(request.current);setPreview(null);request.current=null;setTarget("")}
   await load();
  }catch(e){setMessage((e as Error).message)}finally{setBusy(false)}
 }
 return <section className="glass ip-range-panel">
  <div className="qq-settings-header"><div><h3>网段封禁</h3><p>网段作为一条规则显示，后台逐个地址执行；关闭页面后继续处理。</p></div><button className="text-button" onClick={()=>void load()}><RefreshCw size={14}/>刷新</button></div>
  <div className="moderation-ip"><Field label="IPv4 网段"><input value={target} onChange={e=>setTarget(e.target.value)} placeholder="192.0.2.* 或 192.0.2.0/24" onKeyDown={e=>{if(e.key==="Enter")void prepare()}}/></Field><button className="primary" disabled={loading||!target.trim()} onClick={()=>void prepare()}>{loading&&<LoaderCircle size={15} className="spin"/>}封禁网段</button></div>
  {message&&!preview&&!release&&<p role="status">{message}</p>}
  <div className="table-scroll"><table><thead><tr><th>网段</th><th>状态 / 进度</th><th>原因</th><th>到期时间</th><th>操作</th></tr></thead><tbody>{rows.map(r=><tr key={r.id}><td><strong>{r.target}</strong><small>{r.count} 个地址</small></td><td>{status[r.status]||r.status}<small>本规则 {r.applied} · 保留原封禁 {r.retained} · 待处理 {r.pending}{r.released?` · 已解除 ${r.released}`:""}{r.conflict?` · 已变化 ${r.conflict}`:""}</small>{r.error&&<small>{r.error}</small>}</td><td>{r.reason}</td><td>{r.expiresAt?new Date(r.expiresAt).toLocaleString():"永久"}</td><td><button className="text-button" disabled={busy||r.status==="released"||r.status==="releasing"||r.status==="expired"} onClick={()=>{setMessage("");setRelease(r)}}>解除网段封禁</button></td></tr>)}</tbody></table></div>
  {!rows.length&&<Empty title="暂无网段规则"/>}
  {(preview||release)&&<Modal title={release?"解除网段封禁":"确认封禁网段"} dismissOnBackdrop={!busy} onClose={()=>{if(!busy){setPreview(null);setRelease(null);request.current=null}}} footer={<><button className="secondary" disabled={busy} onClick={()=>{setPreview(null);setRelease(null);request.current=null}}>取消</button><button className="primary" disabled={busy||!!preview&&!reason.trim()} onClick={()=>void confirm()}>{busy&&<LoaderCircle size={16} className="spin"/>}{release?"确认解除":request.current?"使用原请求重试":"确认封禁"}</button></>}>
   <strong>{(release||preview)?.target}</strong>
   {release?<p>解除本规则产生的封禁；原有独立封禁及后来修改的记录继续保留。</p>:preview&&<><p>{preview.first} — {preview.last}，共 {preview.count} 个地址。阻止这些来源注册、登录和进入游戏，并断开匹配的在线连接。</p><Field label="原因"><textarea value={reason} maxLength={160} disabled={busy||!!request.current} onChange={e=>setReason(e.target.value)} autoFocus/></Field><Field label="封禁期限"><select value={hours} disabled={busy||!!request.current} onChange={e=>setHours(e.target.value)}><option value="1">1 小时</option><option value="24">1 天</option><option value="168">7 天</option><option value="720">30 天</option><option value="0">永久</option></select></Field></>}
   {message&&<p role="alert">{message}</p>}
  </Modal>}
 </section>;
}
