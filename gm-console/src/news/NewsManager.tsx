import { getLocale } from "../i18n";
import { t } from "../i18n";
import {lazy, Suspense, useEffect, useState} from "react";
import {Plus, Trash2, Save, Send, ArrowUp, ArrowDown, Search, RefreshCw} from "lucide-react";
import {Field, Pill, Tabs, type PageProps} from "../ui";
import {useLive} from "../api/context";
import type {News} from "../domain";
const MarkdownBody = lazy(() => import("./MarkdownBody").then(m => ({default:m.MarkdownBody})));
import "./news.css";

const fresh = (): News => ({id:"",title:"",body:"",published:false,category:"综合",date:new Intl.DateTimeFormat("sv-SE", {timeZone:"Asia/Shanghai"}).format(new Date()),format:"markdown"});
export function NewsManager({state,request}: PageProps) {
  const live = useLive();
  const [draft,setDraft]=useState<News>(fresh), [view,setView]=useState("edit"), [q,setQ]=useState(""), [page,setPage]=useState(0);
  const hub=state.api?.hub, remote=hub?.newsPage;
  const local=state.news.filter(n=>(n.title+"\n"+n.body).toLowerCase().includes(q.toLowerCase()));
  const rows=live ? state.news : local.slice(page*20,page*20+20);
  const total=remote?.total ?? local.length, offset=remote?.offset ?? page*20;
  useEffect(()=> {
    const pending=state.api?.pending;
    if (!pending?.completed) return;
    const op=pending.steps.find(s=>s.path==="/admin/api/news/entries" || s.path==="local-news-delete");
    if (op?.body.action==="delete" || op?.path==="local-news-delete") {
      if (op.body.id===draft.id) setDraft(fresh());
    } else if (op?.body.action==="save" && op.body.news.title===draft.title && op.body.news.body===draft.body) {
      setDraft(d=>({...d,id:op.result?.id || d.id,published:true,sourceRevision:op.result?.newsRevision}));
    }
  },[state.api?.pending?.completed]);
  useEffect(()=> {
    const saved=state.news.find(n=>!n.published && n.title===draft.title && n.body===draft.body);
    if (saved && draft.title && !draft.id.startsWith("draft-")) setDraft(saved);
  },[state.news]);
  function load(offset:number,query=q) { if(live) void live.guard(()=>live.hubNews(offset,query)); else setPage(Math.floor(offset/20)); }
  function choose(n:News) {setDraft({...n,category:n.category || "综合",date:n.date || "",format:n.format || "text"});setView("edit");}
  const revision=draft.sourceRevision || hub?.newsRevision;
  const publish=(published:boolean)=>request({kind:"news",...draft,published,sourceRevision:revision});
  return <div className="news-layout">
    <aside className="glass content-panel news-list">
      <div className="panel-heading"><h3>{t("公告列表")} <small>{remote?.archiveTotal ?? state.news.length}</small></h3><div className="button-row">
        <button className="icon-button" aria-label={t("刷新公告列表")} onClick={()=>load(offset)}><RefreshCw size={16}/></button>
        <button className="icon-button" aria-label={t("新建公告")} onClick={()=>{setDraft(fresh());setView("edit");}}><Plus size={18}/></button>
      </div></div>
      <form className="news-search" onSubmit={e=>{e.preventDefault();load(0);}}><input aria-label={t("搜索历史公告")} placeholder={t("搜索标题或正文")} value={q} onChange={e=>{setQ(e.target.value);if(!live)setPage(0);}}/><button className="icon-button" aria-label={t("搜索公告")}><Search size={16}/></button></form>
      <div className="news-rows">{rows.map(n=><button key={n.id} className={n.id===draft.id?"active":""} onClick={()=>choose(n)}><span><strong>{n.title}</strong><small>{n.published ? `${t(n.category || "综合")} · ${n.date || t("已发布")}`:t("本地草稿")}</small></span></button>)}{!rows.length && <p className="muted">{t("暂无公告")}</p>}</div>
      <div className="news-pagination"><button className="secondary" disabled={!offset} onClick={()=>load(Math.max(0,offset-20))}>{t("上一页")}</button><small>{total ? `${offset+1}–${Math.min(offset+20,total)} / ${total}`:t("0 条")}</small><button className="secondary" disabled={offset+20>=total} onClick={()=>load(offset+20)}>{t("下一页")}</button></div>
    </aside>
    <form className="glass content-panel news-editor" onSubmit={e=>{e.preventDefault();publish(false);}}>
      <div className="panel-heading"><h2>{draft.id ? t("编辑公告"):t("新建公告")}</h2><Pill>{draft.published?t("已发布"):t("草稿")}</Pill></div>
      <Field label={t("标题")}><input required maxLength={160} value={draft.title} onChange={e=>setDraft({...draft,title:e.target.value})} placeholder={t("公告标题")}/></Field>
      <div className="news-fields"><Field label={t("分类")}><select value={draft.category} onChange={e=>setDraft({...draft,category:e.target.value})}>{["综合","更新","指南"].map(c=><option key={c} value={c}>{t(c)}</option>)}</select></Field><Field label={t("显示日期")}><input maxLength={32} value={draft.date} onChange={e=>setDraft({...draft,date:e.target.value})}/></Field><Field label={t("正文格式")}><select value={draft.format} onChange={e=>setDraft({...draft,format:e.target.value as News["format"]})}><option value="markdown">Markdown</option><option value="text">{t("纯文本")}</option></select></Field></div>
      <Tabs value={view} onChange={setView} label={t("公告正文")} items={[{id:"edit",label:"编辑"},{id:"preview",label:"预览"}]}/>
      <div className="news-editor-body">{view==="edit" ? <textarea aria-label={t("公告正文")} rows={16} value={draft.body} onChange={e=>setDraft({...draft,body:e.target.value})} placeholder={draft.format==="markdown" ? t("## 更新内容\n\n- 支持 **加粗**、列表、表格、代码和链接"):t("输入公告内容")}/> : <Suspense fallback={<p className="muted">{t("正在准备预览…")}</p>}><MarkdownBody body={draft.body || "暂无正文"} format={draft.format}/></Suspense>}</div>
      <div className="news-hint"><span>{draft.format==="markdown" ? t("支持标题、列表、表格、引用、代码、链接及图片"):t("保留原有文字与换行")}</span><small>{new TextEncoder().encode(draft.body).length.toLocaleString(getLocale())} {t("/ 65,536 字节")}</small></div>
      <div className="news-actions"><div className="button-row">
        {draft.id && <button type="button" className="danger secondary" onClick={()=>request({kind:"news-delete",id:draft.id,sourceRevision:revision})}><Trash2 size={15}/>{t("删除")}{draft.published?t("公告"):t("草稿")}</button>}
        {draft.published && <><button type="button" className="icon-button" title={t("上移")} aria-label={t("上移公告")} onClick={()=>request({kind:"news-move",id:draft.id,direction:"up",sourceRevision:revision})}><ArrowUp size={16}/></button><button type="button" className="icon-button" title={t("下移")} aria-label={t("下移公告")} onClick={()=>request({kind:"news-move",id:draft.id,direction:"down",sourceRevision:revision})}><ArrowDown size={16}/></button></>}
      </div><div className="button-row"><button className="secondary" disabled={!draft.title.trim()}><Save size={15}/>{t("保存草稿")}</button><button type="button" className="primary" disabled={!draft.title.trim() || !draft.body.trim() || new TextEncoder().encode(draft.body).length>65536} onClick={()=>publish(true)}><Send size={15}/>{t("发布公告")}</button></div></div>
      <p className="muted news-hint">{t("历史公告保留在后台，登录器同步列表前部的近期公告。")}</p>
    </form>
  </div>;
}
