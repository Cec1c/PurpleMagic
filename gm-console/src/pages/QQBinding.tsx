import {useEffect,useRef,useState} from "react";
import {LoaderCircle,Search,Save,Link} from "lucide-react";
import {useLive} from "../api/context";
import {Empty,Field,type PageProps} from "../ui";
type Settings={enabled:boolean;groups:string[];revision:number};
type Binding={accountId:string;account:string;qq:string;group:string;boundAt:number};
type Page={rows:Binding[];hasMore:boolean;total:number};
export function QQBindingPage({state}:PageProps){
 const live=useLive();const [settings,setSettings]=useState<Settings|null>(null),[groups,setGroups]=useState("");
 const [page,setPage]=useState<Page>({rows:[],hasMore:false,total:0}),[search,setSearch]=useState(""),[offset,setOffset]=useState(0);
 const [loading,setLoading]=useState(false),[saving,setSaving]=useState(false),[notice,setNotice]=useState("");
 const read=useRef<AbortController|null>(null);
 async function load(next=0,policy=false){
  if(!live)return;read.current?.abort();const controller=new AbortController();read.current=controller;setLoading(true);
  await live.guard(async()=>{try{
   const [rows,cfg]=await Promise.all([live.qqBindings(search,next,controller.signal),policy?live.qqBindingSettings(controller.signal):Promise.resolve(null)]);
   if(!controller.signal.aborted){setPage(rows);setOffset(next);if(cfg){setSettings(cfg);setGroups(cfg.groups.join("\n"));}}
  }finally{if(!controller.signal.aborted)setLoading(false)}});
 }
 useEffect(()=>{void load(0,true);return()=>read.current?.abort()},[live]);
 async function save(){
  if(!live||!settings||saving)return;setSaving(true);setNotice("");
  await live.guard(async()=>{try{
   const value=await live.saveQQBindingSettings({...settings,groups:[...new Set(groups.split(/[\s,，;；]+/).filter(Boolean))]});
   setSettings(value);setGroups(value.groups.join("\n"));setNotice("绑定配置已保存");
  }finally{setSaving(false)}});
 }
 return <div className="motion-section qq-binding-page">
  <section className="glass qq-binding-settings">
   <div><h3><Link size={18}/>账号与 QQ 号</h3><p>一个 QQ 对应一个游戏账号。玩家使用登录器生成的一次性绑定码完成绑定。</p></div>
   <label className="qq-binding-toggle"><input type="checkbox" checked={settings?.enabled||false} disabled={!settings||saving} onChange={e=>setSettings(s=>s&&({...s,enabled:e.target.checked}))}/>登录时要求绑定 QQ</label>
   <Field label="允许绑定的群（每行一个）"><textarea value={groups} rows={3} onChange={e=>setGroups(e.target.value)} disabled={!settings||saving} placeholder="填写 QQ 群号"/></Field>
   <p>群内艾特机器人发送指令；私聊时核对是否加入以上任一群。</p>
   <button className="primary" disabled={!settings||saving||!state.api?.features["qqBinding.write"]} onClick={()=>void save()}>{saving?<LoaderCircle size={16} className="spin"/>:<Save size={16}/>}保存配置</button>
   <span role="status">{notice}</span>
  </section>
  <div className="moderation-filters glass"><Field label="账号 / 账号 ID / QQ 号"><input value={search} onChange={e=>setSearch(e.target.value)} onKeyDown={e=>{if(e.key==="Enter")void load(0)}} placeholder="输入完整账号或 QQ 号"/></Field><button className="primary" disabled={loading} onClick={()=>void load(0)}>{loading?<LoaderCircle size={16} className="spin"/>:<Search size={16}/>}查询</button><span>已绑定 {page.total} 个账号</span></div>
  <div className="glass moderation-table"><table><thead><tr><th>游戏账号</th><th>QQ 号</th><th>验证群</th><th>绑定时间</th></tr></thead><tbody>{page.rows.map(row=><tr key={row.accountId}><td>{row.account}<small>{row.accountId}</small></td><td>{row.qq}</td><td>{row.group}</td><td>{new Date(row.boundAt*1000).toLocaleString()}</td></tr>)}</tbody></table>{!loading&&!page.rows.length&&<Empty title="暂无绑定记录" detail="玩家完成绑定后将在这里显示"/>}</div>
  <div className="moderation-pagination"><button className="secondary" disabled={loading||offset===0} onClick={()=>void load(Math.max(0,offset-50))}>上一页</button><span>第 {Math.floor(offset/50)+1} 页</span><button className="secondary" disabled={loading||!page.hasMore} onClick={()=>void load(offset+50)}>下一页</button></div>
 </div>;
}
