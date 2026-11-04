import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
const bundle=async(path)=>{const {outputFiles}=await build({entryPoints:[path],bundle:true,platform:'node',format:'esm',write:false});return import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));};
const {ConsoleAPI}=await bundle('src/api/console.ts');
const storage=new Map();
globalThis.sessionStorage={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
const selection={accounts:[],characters:[]};
const row={id:'stable-id',title:'老公告',body:'正文',category:'指南',date:'10 / 15',format:'text',published:true,sourceRevision:'r1'};
function api(){storage.clear();const a=new ConsoleAPI(()=>{});a.character=async()=>{};a.enter=async()=>{};a.meta.hub={newsManager:true,newsRevision:'r1',published:{news:[row]}};a.patch({news:[row]});return a;}
test('delete targets stable ID; only confirmation writes, response loss retains the exact request',async()=>{
 const a=api(),calls=[];a.http.fetcher=async(path,init)=>{calls.push({path,key:init.headers['Idempotency-Key'],revision:init.headers['If-Match'],body:JSON.parse(init.body)});if(calls.length===1)throw Error('lost');return new Response(JSON.stringify({ok:true,id:row.id,newsRevision:'r2'}));};
 await a.prepare({kind:'news-delete',id:row.id,sourceRevision:'r1'},selection);assert.equal(calls.length,0);
 await assert.rejects(a.confirm());await a.confirm();assert.equal(calls.length,2);assert.deepEqual(calls[0],calls[1]);assert.deepEqual(calls[0].body,{action:'delete',id:'stable-id'});
});
test('draft deletion is local and never deletes the published original',async()=>{
 const a=api();a.patch({news:[{...row,id:'draft-1',published:false,originId:row.id},row]});a.http.fetcher=()=>{throw Error('unexpected write');};
 await a.prepare({kind:'news-delete',id:'draft-1'},selection);await a.confirm();assert.equal(a.state.news.length,1);assert.equal(a.state.news[0].id,row.id);assert.deepEqual(JSON.parse(storage.get('purplemagic-news-drafts-v1')),[]);
});
test('edit preserves metadata and sends just one Markdown entry even with 30 published announcements',async()=>{
 const a=api();a.meta.hub.published.news=Array.from({length:30},()=>row);
 await a.prepare({kind:'news',id:row.id,title:'改标题',body:'## 章节\n\n**重点**',format:'markdown',published:true,sourceRevision:'r1'},selection);
 const step=a.meta.pending.steps[0];assert.equal(step.path,'/api/v1/hub/news/entries');assert.equal(step.body.news.category,'指南');assert.equal(step.body.news.date,'10 / 15');assert.equal(step.body.news.format,'markdown');assert.equal(step.body.id,row.id);
});
test('a stale editor cannot delete or overwrite another announcement after a refresh',async()=>{
 const a=api();a.meta.hub.newsRevision='r2';await assert.rejects(a.prepare({kind:'news-delete',id:row.id,sourceRevision:'r1'},selection),/已更新/);
});
test('paged history query keeps stable IDs, revision and draft; empty final page returns to previous page',async()=>{
 const a=api(),calls=[];a.http.fetcher=async(path)=>{calls.push(path);return new Response(JSON.stringify({rows:path.includes('offset=40')?[]:[row],total:21,archiveTotal:51,offset:path.includes('offset=40')?40:20,next:-1,revision:'r3'}));};
 await a.hubNews(40,'更新');assert.equal(calls.length,2);assert.ok(calls[1].includes('offset=20'));assert.equal(a.state.news[0].id,'stable-id');assert.equal(a.state.news[0].sourceRevision,'r3');assert.equal(a.meta.hubNewsQuery,'更新');
});

// Server rendering exercises actual React Markdown parsing and URL filtering.
const source=`import React from 'react';import {renderToStaticMarkup} from 'react-dom/server';import {MarkdownBody,newsURL} from './src/news/MarkdownBody';export {newsURL};export const render=(body,format='markdown')=>renderToStaticMarkup(React.createElement(MarkdownBody,{body,format}));`;
const {outputFiles}=await build({stdin:{contents:source,resolveDir:process.cwd(),loader:'tsx'},bundle:true,platform:'node',format:'cjs',write:false});
const {createRequire}=await import('node:module');const require=createRequire(import.meta.url);const module={exports:{}};new Function('require','module','exports',outputFiles[0].text)(require,module,module.exports);const {render,newsURL}=module.exports;
test('Markdown preview renders tables and emphasis; no raw HTML or executable URLs',()=>{
 const html=render('## 标题\n\n**重点**\n\n| 项目 | 结果 |\n|---|---|\n|公告|完成|\n\n<script>alert(1)</script>\n\n[危险](javascript:alert(1))\n\n![本地](file:///secret)');
 assert.match(html,/<h2>标题<\/h2>/);assert.match(html,/<strong>重点<\/strong>/);assert.match(html,/<table>/);assert.doesNotMatch(html,/<script|javascript:|file:\/\//);
 assert.equal(newsURL('https://example.test/help'),'https://example.test/help');assert.equal(newsURL('https://user:pass@example.test'),'');
 assert.match(render('**原文**\n第二行','text'),/\*\*原文\*\*/);
});


test('GM settings load and publish with the GM session CSRF, without a second login', async()=>{
 const a=api(),calls=[];a.meta.hub=undefined;a.http.csrf='gm-session-csrf';
 a.http.fetcher=async(path,init)=>{
  calls.push({path,method:init.method,headers:init.headers});
  if(path==='/api/v1/hub/state')return new Response(JSON.stringify({newsManager:true,newsRevision:'r1',sources:[],published:{}}));
  if(path.startsWith('/api/v1/hub/news?'))return new Response(JSON.stringify({rows:[row],total:1,offset:0,next:-1,revision:'r1'}));
  assert.equal(path,'/api/v1/hub/news/entries');assert.equal(init.headers['X-CSRF-Token'],'gm-session-csrf');assert.equal(init.headers['If-Match'],'"r1"');
  return new Response(JSON.stringify({ok:true,newsRevision:'r2'}));
 };
 await a.hub();assert.ok(a.meta.hub);assert.equal(calls.length,2);assert.ok(calls.every(c=>c.method==='GET'));
 await a.prepare({kind:'news',id:row.id,title:'一次登录',body:'正文',published:true,sourceRevision:'r1'},selection);
 await a.confirm();assert.equal(calls.length,3);assert.ok(calls[2].headers['Idempotency-Key']);
});
test('stored legacy announcement requests keep their ID and revision on the GM route', async()=>{
 const a=api();a.http.csrf='gm-csrf';
 a.http.fetcher=async(path,init)=>{
  assert.equal(path,'/api/v1/hub/news/entries');assert.equal(init.headers['Idempotency-Key'],'original-request');assert.equal(init.headers['If-Match'],'"r1"');assert.equal(init.headers['X-CSRF-Token'],'gm-csrf');return new Response('{"ok":true}');
 };
 await a.http.raw('/admin/api/news/entries','POST',{action:'delete',id:row.id},'original-request',undefined,{'If-Match':'"r1"'});
 await assert.rejects(a.http.raw('/admin/api/login','POST',{}),/接口路径无效/);
});
