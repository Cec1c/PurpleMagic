import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { professionGroups } from '../src/professions.ts';
import { itemExpirationLabel, itemOptionLabel } from '../src/item-expiration.ts';
const {outputFiles}=await build({entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',write:false});
const {ConsoleAPI,itemDTO}=await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
globalThis.sessionStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};
const response=data=>new Response(JSON.stringify({data}));

test('profession choices keep root ownership and duplicate names remain distinct',()=>{
 const groups=professionGroups([{job:0,name:'鬼剑士（男）',choices:[{growType:1,name:'剑魂'},{growType:17,name:'剑圣'}]},{job:10,name:'鬼剑士（女）',choices:[{growType:1,name:'剑魂'}]}]);
 assert.deepEqual(groups.map(v=>v.value),['0','10']);
 assert.deepEqual(groups[0].choices.map(v=>v.value),['0:1','0:17']);
 assert.equal(groups[1].choices[0].value,'10:1');
});
test('item expiration distinguishes permanent, duration, absolute date and unknown values',()=>{
 assert.equal(itemExpirationLabel({known:true,absoluteExpireTime:0,usablePeriodDays:0}),'永久');
 assert.equal(itemExpirationLabel({known:true,absoluteExpireTime:0,usablePeriodDays:7}),'有效期 7 天');
 assert.equal(itemExpirationLabel(undefined),'期限未读取');
 const expiry={known:true,absoluteExpireTime:Date.parse('2030-01-02T03:04:05Z')/1000,usablePeriodDays:30};
 assert.match(itemExpirationLabel(expiry,0),/2030\/01\/02 11:04:05 到期（北京时间）.*有效期 30 天/);
 assert.match(itemExpirationLabel(expiry,Date.parse('2031-01-01')),/已过期/);
 assert.match(itemOptionLabel({id:'12345',name:'同名物品',expiration:expiry}),/同名物品 · ID 12345 · 2030/);
});
test('catalog adapter keeps each same-name item ID and its template expiry',async()=>{
 const api=new ConsoleAPI(()=>{});
 api.http.fetcher=async()=>response({items:[{itemId:'1',name:'觉醒券',templateExpiration:{known:true,absoluteExpireTime:0,usablePeriodDays:0}},{itemId:'2',name:'觉醒券',templateExpiration:{known:true,absoluteExpireTime:0,usablePeriodDays:7}}]});
 await api.catalog('觉醒');
 assert.equal(api.meta.catalog.length,2);
 assert.match(itemOptionLabel(api.meta.catalog[0]),/ID 1 · 永久/);
 assert.match(itemOptionLabel(api.meta.catalog[1]),/ID 2 · 有效期 7 天/);
 assert.equal(itemDTO({itemId:'3',expiresAt:'2030-01-01'}, {id:'c'},'bag').remote.expiresAt,'2030-01-01');
});
test('mail confirmation preserves selected ID and expiry after catalog changes',async()=>{
 const api=new ConsoleAPI(()=>{});api.state.characters=[{id:'10',accountId:'a',name:'A'}];
 const requests=[];
 api.http.fetcher=async(path,init)=>{
   requests.push({path,body:init.body&&JSON.parse(init.body)});
   if(path.endsWith('/grant-options'))return response({pvfChecksum:'pvf',expiration:{defaultExpireTime:0,usablePeriodDays:30}});
   if(path==='/api/v1/mail/preview')return response({id:'task',revision:'r'});
   if(path.endsWith('/confirmation'))return response({recipients:[{characterId:'10',accountId:'a',name:'A'}]});
   throw Error(path);
 };
 await api.prepare({kind:'mail',title:'礼物',body:'',gold:0,items:[{id:'2',name:'觉醒券',count:1,enhance:0,expiration:{known:true,absoluteExpireTime:0,usablePeriodDays:7}}]},{accounts:[],characters:['10']});
 assert.match(api.meta.pending.steps[0].label,/ID 2.*有效期 30 天/);
 assert.equal(requests.find(v=>v.path==='/api/v1/mail/preview').body.itemId,'2');
 assert.ok(!requests.some(v=>v.path.endsWith('/run')));
});
