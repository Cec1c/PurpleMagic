import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { appendAttachments } from '../src/item-picker.ts';
const { outputFiles } = await build({ entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',write:false });
const { ConsoleAPI } = await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
globalThis.sessionStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};
const response=(data,cursor='')=>new Response(JSON.stringify({data,page:{limit:100,nextCursor:cursor}}));
const rows=(start,count)=>Array.from({length:count},(_,i)=>({itemId:String(start+i),name:'礼盒',templateExpiration:{known:true,absoluteExpireTime:0,usablePeriodDays:i%2?7:0}}));

test('catalog automatically follows stable 100-row cursors for 300/500/1000 results and continues from the last page',async()=>{
 for(const size of [300,500,1000]){
  const api=new ConsoleAPI(()=>{});const requested=[];
  api.http.fetcher=async path=>{const q=new URL(path,'https://fixture.invalid').searchParams;assert.equal(q.get('q'),'礼');assert.equal(q.get('limit'),'100');const start=Number(q.get('cursor')||0);requested.push(start);return response({items:rows(start,100),total:1200},start+100<1200?String(start+100):'');};
  await api.catalog(' 礼 ',false,size);assert.equal(api.meta.catalog.length,size);assert.equal(api.meta.catalogTotal,1200);assert.equal(requested.length,size/100);assert.equal(api.meta.catalogLoading,false);
  await api.catalog('礼',true,size);assert.equal(api.meta.catalog.length,Math.min(size*2,1200));assert.equal(api.meta.catalog[1].expiration.usablePeriodDays,7);
 }
});
test('new queries cancel in-flight pages and old results cannot overwrite the new selection',async()=>{
 const api=new ConsoleAPI(()=>{});let release;const waiting=new Promise(r=>release=r);let started;const ready=new Promise(r=>started=r);
 api.http.fetcher=async path=>{const q=new URL(path,'https://fixture.invalid').searchParams.get('q');if(q==='旧'){started();await waiting;return response({items:rows(1,100),total:400},'100');}return response({items:rows(9000,1),total:1});};
 const old=api.catalog('旧');await ready;await api.catalog('新');release();await old.catch(e=>assert.equal(e.name,'AbortError'));
 assert.deepEqual(api.meta.catalog.map(i=>i.id),['9000']);assert.equal(api.meta.catalogNext,'');assert.equal(api.meta.catalogQuery,'新');assert.equal(api.meta.catalogLoading,false);
});
test('failed continuation retains loaded results and its cursor for retry',async()=>{
 const api=new ConsoleAPI(()=>{});let fail=true;
 api.http.fetcher=async path=>{const start=Number(new URL(path,'https://fixture.invalid').searchParams.get('cursor')||0);if(start===100&&fail) return new Response(JSON.stringify({error:{message:'temporary'}}),{status:503});return response({items:rows(start,100),total:400},start+100<400?String(start+100):'');};
 await assert.rejects(api.catalog('礼'));assert.equal(api.meta.catalog.length,100);assert.equal(api.meta.catalogNext,'100');assert.equal(api.meta.catalogLoading,false);
 fail=false;await api.catalog('礼',true);assert.equal(api.meta.catalog.length,400);assert.equal(api.meta.catalogNext,'');
});
test('attachment selection preserves same-name IDs and expiration, merges counts, and rejects overflow atomically',()=>{
 const seven={id:'2',name:'礼盒',expiration:{known:true,absoluteExpireTime:0,usablePeriodDays:7}};
 const current=[{id:'1',name:'礼盒',count:5,enhance:0}];
 const result=appendAttachments(current,[{id:'1',name:'礼盒'},seven,seven],3);
 assert.equal(result.length,2);assert.equal(result[0].count,8);assert.equal(result[1].count,3);assert.equal(result[1].expiration.usablePeriodDays,7);assert.equal(current[0].count,5);
 assert.throws(()=>appendAttachments(current,[seven,{id:'1',name:'礼盒'}],99999));assert.equal(current.length,1);assert.throws(()=>appendAttachments([], [seven],1.5));
});
