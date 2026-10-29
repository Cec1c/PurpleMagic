import {useEffect,useRef,useState} from "react";
import {LoaderCircle,Search,Save,Link,Plus,Trash2,Users} from "lucide-react";
import {useLive} from "../api/context";
import {Empty,Field,Tabs,type PageProps} from "../ui";
type Settings={enabled:boolean;allowBinding:boolean;groups:string[];revision:number};
type Binding={accountId:string;account:string;qq:string;group:string;boundAt:number};
type Page={rows:Binding[];hasMore:boolean;total:number};
export function QQBindingPage({state}:PageProps){
 const live=useLive();const [settings,setSettings]=useState<Settings|null>(null),[groupInput,setGroupInput]=useState("");
 const [page,setPage]=useState<Page>({rows:[],hasMore:false,total:0}),[search,setSearch]=useState(""),[offset,setOffset]=useState(0);
 const [loading,setLoading]=useState(false),[saving,setSaving]=useState(false),[notice,setNotice]=useState("");
 const read=useRef<AbortController|null>(null);
 const editable=!!settings&&!saving&&!!state.api?.features["qqBinding.write"];
 const mode=settings?.enabled?"required":settings?.allowBinding?"optional":"disabled";
 async function load(next=0,policy=false){
  if(!live)return;read.current?.abort();const controller=new AbortController();read.current=controller;setLoading(true);
  await live.guard(async()=>{try{
   const [rows,cfg]=await Promise.all([live.qqBindings(search,next,controller.signal),policy?live.qqBindingSettings(controller.signal):Promise.resolve(null)]);
   if(!controller.signal.aborted){setPage(rows);setOffset(next);if(cfg)setSettings({...cfg,allowBinding:cfg.allowBinding===true});}
  }finally{if(!controller.signal.aborted)setLoading(false)}});
 }
 useEffect(()=>{void load(0,true);return()=>read.current?.abort()},[live]);
 function addGroup(){
  const group=groupInput.trim();if(!settings||!editable)return;
  if(!/^[1-9][0-9]{4,11}$/.test(group)){setNotice("请填写有效的 QQ 群号");return;}
  if(settings.groups.includes(group)){setNotice("这个群已经添加");return;}
  if(settings.groups.length>=32){setNotice("最多添加 32 个群");return;}
  setSettings({...settings,groups:[...settings.groups,group]});setGroupInput("");setNotice("");
 }
 async function save(){
  if(!live||!settings||!editable)return;
  if(groupInput.trim()){setNotice("请先添加输入的群号，再保存配置");return;}
  if(mode!=="disabled"&&!settings.groups.length){setNotice("请至少添加一个允许绑定的群");return;}
  setSaving(true);setNotice("");
  await live.guard(async()=>{try{setSettings(await live.saveQQBindingSettings(settings));setNotice("绑定配置已保存");}finally{setSaving(false)}});
 }
 return <div className="motion-section qq-binding-page">
  <section className="glass qq-binding-settings">
   <div className="qq-settings-header"><div><h3><Link size={18}/>账号与 QQ 号</h3><p>一个 QQ 对应一个游戏账号，使用登录器生成的一次性绑定码完成绑定。</p></div>
    <button className="primary" disabled={!editable} onClick={()=>void save()}>{saving?<LoaderCircle size={16} className="spin"/>:<Save size={16}/>}保存配置</button>
   </div>
   <div className="qq-binding-policy"><Tabs label="绑定方式" items={[{id:"disabled",label:"关闭绑定"},{id:"optional",label:"自愿绑定"},{id:"required",label:"必须绑定"}]} value={mode} onChange={value=>{if(editable)setSettings(s=>s&&({...s,enabled:value==="required",allowBinding:value!=="disabled"}));}}/>
    <p>{mode==="optional"?"玩家可以主动绑定 QQ，未绑定也能登录游戏。":mode==="required"?"玩家完成 QQ 绑定后才能登录游戏。":"暂停新的 QQ 绑定，玩家可直接登录游戏。"}</p>
   </div>
   <div className="qq-groups-heading"><h4>允许绑定的群</h4><span>{settings?.groups.length||0} 个群</span></div>
   <div className="qq-group-list">{settings?.groups.map(group=><div className="qq-group-row" key={group}><Users size={17}/><span>{group}</span><button className="icon-button" aria-label={`移除群 ${group}`} disabled={!editable} onClick={()=>setSettings(s=>s&&({...s,groups:s.groups.filter(g=>g!==group)}))}><Trash2 size={15}/></button></div>)}</div>
   <div className="qq-group-add"><Field label="QQ群号"><input inputMode="numeric" value={groupInput} onChange={e=>setGroupInput(e.target.value)} disabled={!editable} placeholder="输入群号" onKeyDown={e=>{if(e.key==="Enter"){e.preventDefault();addGroup();}}}/></Field><button className="secondary" disabled={!editable||!groupInput.trim()} onClick={addGroup}><Plus size={16}/>添加群</button></div>
   <p>群内艾特机器人发送绑定指令；私聊时核对是否加入以上任一群。</p>
   <span className="qq-settings-notice" role="status">{notice}</span>
  </section>
  <div className="moderation-filters glass"><Field label="账号 / 账号 ID / QQ 号"><input value={search} onChange={e=>setSearch(e.target.value)} onKeyDown={e=>{if(e.key==="Enter")void load(0)}} placeholder="输入完整账号或 QQ 号"/></Field><button className="primary" disabled={loading} onClick={()=>void load(0)}>{loading?<LoaderCircle size={16} className="spin"/>:<Search size={16}/>}查询</button><span>已绑定 {page.total} 个账号</span></div>
  <div className="glass moderation-table"><table><thead><tr><th>游戏账号</th><th>QQ 号</th><th>验证群</th><th>绑定时间</th></tr></thead><tbody>{page.rows.map(row=><tr key={row.accountId}><td>{row.account}<small>{row.accountId}</small></td><td>{row.qq}</td><td>{row.group}</td><td>{new Date(row.boundAt*1000).toLocaleString()}</td></tr>)}</tbody></table>{!loading&&!page.rows.length&&<Empty title="暂无绑定记录" detail="玩家完成绑定后将在这里显示"/>}</div>
  <div className="moderation-pagination"><button className="secondary" disabled={loading||offset===0} onClick={()=>void load(Math.max(0,offset-50))}>上一页</button><span>第 {Math.floor(offset/50)+1} 页</span><button className="secondary" disabled={loading||!page.hasMore} onClick={()=>void load(offset+50)}>下一页</button></div>
 </div>;
}
