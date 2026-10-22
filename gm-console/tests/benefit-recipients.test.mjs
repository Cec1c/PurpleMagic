import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { build } from 'esbuild';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { collectRecipients, partitionRecipients } from '../src/api/benefit-recipients.ts';

const row=(id,state='pending')=>({accountId:'a',characterId:id,name:'role-'+id,state});
test('details automatically follow nonempty and empty pages and retain each recipient once',async()=>{
 const calls=[], updates=[];
 const pages={ '':{data:[row('1','sent')],page:{nextCursor:'a'}}, a:{data:[],page:{nextCursor:'b'}}, b:{data:[row('1','sent'),row('2'),row('3','failed'),row('4','ineligible')]}};
 await collectRecipients(async cursor=>{calls.push(cursor);return pages[cursor]},new AbortController().signal,(rows,next)=>updates.push({rows,next}));
 assert.deepEqual(calls,['','a','b']);assert.equal(updates.at(-1).next,'');
 const parts=partitionRecipients(updates.at(-1).rows);
 assert.deepEqual(parts.pending.map(r=>r.characterId),['2','3']);assert.equal(parts.sent.length,1);assert.equal(parts.ineligible.length,1);
});
test('a failed shard retains progress and continuation resumes at that cursor',async()=>{
 let loaded=[],next='';
 await assert.rejects(collectRecipients(async cursor=>{if(cursor)throw Error('network');return {data:[row('1')],page:{nextCursor:'next'}}},new AbortController().signal,(rows,cursor)=>{loaded=rows;next=cursor}),/network/);
 const calls=[];
 await collectRecipients(async cursor=>{calls.push(cursor);return {data:[row('2','sent')]}},new AbortController().signal,(rows,cursor)=>{loaded=rows;next=cursor},loaded,next);
 assert.deepEqual(calls,['next']);assert.equal(loaded.length,2);assert.equal(next,'');
});
test('closing or refreshing details aborts an in-flight page before publishing its rows',async()=>{
 const controller=new AbortController();let updates=0;
 await assert.rejects(collectRecipients(async()=>{controller.abort();return {data:[row('1')],page:{nextCursor:'a'}}},controller.signal,()=>updates++));
 assert.equal(updates,0);
});
test('repeating a cursor stops the query instead of looping forever',async()=>{
 let calls=0;
 await assert.rejects(collectRecipients(async()=>{calls++;return {data:[],page:{nextCursor:'a'}}},new AbortController().signal,()=>{}),/游标未前进/);
 assert.equal(calls,2);
});
const { outputFiles }=await build({entryPoints:['src/api/BenefitRecipients.tsx'],bundle:true,platform:'node',format:'cjs',write:false,packages:'external'});
const compiled={exports:{}};
new Function('require','module','exports',outputFiles[0].text)(createRequire(import.meta.url),compiled,compiled.exports);
test('successful recipients are collapsed without rendering their rows and pending count includes failures',()=>{
 const html=renderToStaticMarkup(createElement(compiled.exports.BenefitRecipients,{rows:[row('sent-only','sent'),row('waiting'),row('failure','failed'),row('out','ineligible')],busy:false,complete:true}));
 assert.match(html,/未发放 2/);assert.match(html,/<summary>已成功发放（1）<\/summary>/);assert.doesNotMatch(html,/<details[^>]*open/);
 assert.match(html,/role-waiting/);assert.match(html,/role-failure/);assert.doesNotMatch(html,/role-sent-only|role-out/);assert.doesNotMatch(html,/加载更多/);
});
test('partial enumeration is labelled as partial and has a spinner in a stable summary row',()=>{
 const html=renderToStaticMarkup(createElement(compiled.exports.BenefitRecipients,{rows:[],busy:true,complete:false}));
 assert.match(html,/已查询范围/);assert.match(html,/自动查询中/);assert.doesNotMatch(html,/当前范围已全部发放/);assert.match(html,/class="[^"]*spin/);
});
