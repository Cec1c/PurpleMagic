import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';
const { outputFiles } = await build({entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',write:false});
const { ConsoleAPI } = await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
const storage = new Map();
globalThis.sessionStorage = {getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
const response = data => new Response(JSON.stringify({data}));
const selection = {accounts:[],characters:[]};
const command = name => ({kind:'benefit-create',name,gold:0,rewardKind:'cera',cera:1,grantScope:'account',enabled:false,scope:'future'});
function api() {
 const a = new ConsoleAPI(()=>{});a.character=async()=>{};a.enter=async()=>{};return a;
}

test('failed operation releases the workspace; reload and retry retain the original request and completed steps',async()=>{
 storage.clear();const a=api();const calls=[];
 a.http.fetcher=async(path,init)=>{calls.push({path,body:init.body,id:init.headers['Idempotency-Key']});throw Error('queue unavailable');};
 await a.prepare(command('first'),selection);const first=a.meta.pending;
 first.steps.unshift({id:'finished-step',label:'finished',path:'/already-done',body:{},done:true,result:{requestId:'permanent'}});
 await assert.rejects(a.confirm());a.dismissPending();assert.equal(a.meta.pending,undefined);
 await a.prepare(command('second'),selection);const second=a.meta.pending;
 a.http.fetcher=async()=>response({id:'second-receipt'});await a.confirm();a.dismissPending();
 const restored=api();restored.restore();assert.equal(restored.operationReceipts().length,2);
 assert.equal(restored.meta.operationHistory.find(p=>p.id===second.id).completed,true);
 restored.openPending(first.id);assert.equal(restored.meta.pending.steps[0].result.requestId,'permanent');
 restored.http.fetcher=async(path,init)=>{calls.push({path,body:init.body,id:init.headers['Idempotency-Key']});return response({id:'first-receipt'});};
 await restored.confirm(first.id);assert.deepEqual(calls[0],calls[1]);assert.equal(calls.length,2);
 assert.equal(restored.meta.pending.completed,true);assert.equal(restored.operationReceipts().length,2);
});

test('old single-receipt storage migrates without blocking a new preparation',async()=>{
 storage.clear();const legacy={id:'legacy-request',title:'old',target:'role',summary:'mail',confirmed:true,hidden:true,error:'lost response',steps:[{id:'legacy-step',path:'/api/v1/tasks/old/run',label:'mail',body:{revision:0},attempted:true}]};
 storage.set('purplemagic-pending-v1',JSON.stringify(legacy));const a=api();a.restore();
 await a.prepare(command('new'),selection);assert.deepEqual(a.meta.operationHistory[0],legacy);
 assert.equal(storage.has('purplemagic-pending-v1'),false);assert.ok(storage.has('purplemagic-operations-v2'));
 a.cancel();a.openPending(legacy.id);assert.equal(a.meta.pending.steps[0].id,'legacy-step');
});

test('in-flight execution stays serialized while closing and opening receipt details',async()=>{
 storage.clear();const a=api();let release;
 await a.prepare(command('active'),selection);const active=a.meta.pending;
 a.http.fetcher=()=>new Promise(r=>release=r);const execution=a.confirm();a.dismissPending();
 await a.prepare(command('blocked only while running'),selection);assert.equal(a.meta.pending,active);
 a.openPending('another-id');assert.equal(a.meta.pending,active);assert.equal(a.meta.busy,true);
 release(response({id:'ok'}));await execution;await a.prepare(command('after'),selection);
 assert.notEqual(a.meta.pending.id,active.id);assert.equal(a.meta.operationHistory[0].completed,true);
});

test('an old generic retry cannot execute a different operation awaiting confirmation',async()=>{
 storage.clear();const a=api();let writes=0;
 a.http.fetcher=async()=>{writes++;throw Error('lost response');};
 await a.prepare(command('first'),selection);const id=a.meta.pending.id;
 await a.guard(()=>a.confirm(id));a.dismissPending();await a.prepare(command('second'),selection);
 await a.retry();assert.equal(writes,1);assert.equal(a.meta.pending.confirmed,false);
});

test('completed and rejected receipts remain available after closing',async()=>{
 storage.clear();const a=api();a.http.fetcher=async()=>new Response(JSON.stringify({error:{code:'revision_conflict',message:'changed'}}),{status:409});
 await a.prepare(command('rejected'),selection);const id=a.meta.pending.id;
 await assert.rejects(a.confirm());a.cancel();assert.equal(a.meta.operationHistory[0].rejected,true);
 a.openPending(id);assert.equal(a.meta.pending.rejected,true);assert.equal(a.meta.pending.error,'changed');
});
