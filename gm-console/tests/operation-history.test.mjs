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

function receipt(id, extra={}) {
 return {id,title:'edit',target:'fixture',summary:'inventory',confirmed:true,steps:[{id:id+'-0',path:'/api/v1/characters/10/inventory-operations',label:'edit',body:{accountId:'fixture',items:[{container:'shared',slot:354,revision:'reviewed'}],options:{count:'5446'},pvfChecksum:'approved',planToken:'expired'},attempted:true}],...extra};
}

test('deleting current and archived terminal receipts persists locally without business requests',async()=>{
 storage.clear();const a=api();a.http.fetcher=async()=>{assert.fail('cleanup must not call the server');};
 a.meta.pending=receipt('rejected',{rejected:true,error:'occupied'});a.meta.error='occupied';
 a.meta.operationHistory=[receipt('success',{completed:true}),receipt('older',{rejected:true})];
 a.deleteReceipt('rejected');assert.equal(a.meta.pending,undefined);assert.equal(a.meta.error,'');
 a.deleteReceipt('success');a.deleteReceipt('missing');
 const restored=api();restored.restore();assert.deepEqual(restored.operationReceipts().map(p=>p.id),['older']);
 restored.clearReceipts();const empty=api();empty.restore();assert.deepEqual(empty.operationReceipts(),[]);
 await a.prepare(command('next'),selection);assert.equal(a.meta.pending.confirmed,false);
});

test('bulk cleanup keeps successful receipts on failed-only cleanup and retains unknown/in-flight requests',async()=>{
 storage.clear();const a=api();let release;
 await a.prepare(command('running'),selection);const running=a.meta.pending;
 a.meta.operationHistory=[receipt('failed',{rejected:true}),receipt('success',{completed:true}),receipt('unknown',{error:'lost response'})];
 a.http.fetcher=()=>new Promise(r=>release=r);const execution=a.confirm();
 a.deleteReceipt(running.id);a.clearReceipts(true);
 assert.equal(a.meta.pending,running);assert.deepEqual(a.meta.operationHistory.map(p=>p.id),['success','unknown']);
 a.clearReceipts();assert.deepEqual(a.meta.operationHistory.map(p=>p.id),['unknown']);
 a.deleteReceipt('unknown');assert.equal(a.meta.operationHistory[0].steps[0].id,'unknown-0');
 release(response({id:'ok'}));await execution;a.clearReceipts();
 const restored=api();restored.restore();assert.deepEqual(restored.operationReceipts().map(p=>p.id),['unknown']);
});

test('clearing an old rejection makes its generic retry harmless to the next operation',async()=>{
 storage.clear();const a=api();let writes=0;
 a.http.fetcher=async()=>{writes++;return new Response(JSON.stringify({error:{code:'character_online',message:'occupied'}}),{status:409});};
 await a.prepare(command('first'),selection);const id=a.meta.pending.id;
 await a.guard(()=>a.confirm(id));a.clearReceipts(true);await a.prepare(command('next'),selection);
 await a.retry();assert.equal(writes,1);assert.equal(a.meta.pending.confirmed,false);
});

test('repreview rejected inventory preserves reviewed values and creates a new identity for confirmation',async()=>{
 storage.clear();const a=api();a.meta.pending=receipt('old',{rejected:true,error:'expired',errorCode:'plan_expired'});
 const posts=[];a.http.fetcher=async(path,init)=>{posts.push({path,body:JSON.parse(init.body),key:init.headers['Idempotency-Key']});return response({planToken:'fresh',receipt:{changes:[]},forceAvailable:true});};
 assert.equal(a.canRepreviewInventory(),true);await a.repreviewInventory('old');
 const p=a.meta.pending;assert.notEqual(p.id,'old');assert.notEqual(p.steps[0].id,'old-0');
 assert.equal(p.confirmed,false);assert.equal(p.rejected,false);assert.equal(p.steps[0].attempted,false);assert.equal(p.inventoryForceAvailable,true);
 assert.equal(posts.length,1);assert.ok(posts[0].path.endsWith('/preview'));assert.equal(posts[0].key,undefined);
 const {planToken,...reviewed}=receipt('old').steps[0].body;assert.deepEqual(posts[0].body,reviewed);
 assert.deepEqual(p.steps[0].body,{...reviewed,planToken:'fresh'});
 await a.confirm('old');assert.equal(posts.length,1);
 const restored=api();restored.restore();assert.equal(restored.meta.pending.id,p.id);assert.equal(restored.meta.pending.steps[0].body.planToken,'fresh');
});

test('failed repreview preserves the old request; unknown outcomes and changed items cannot be re-signed',async()=>{
 storage.clear();const a=api();a.meta.pending=receipt('old',{rejected:true,error:'occupied',errorCode:'character_online'});
 const original=JSON.stringify(a.meta.pending);a.http.fetcher=async()=>new Response(JSON.stringify({error:{code:'revision_conflict',message:'changed'}}),{status:409});
 await assert.rejects(a.repreviewInventory('old'));assert.equal(JSON.stringify(a.meta.pending),original);assert.equal(a.meta.busy,false);
 a.http.fetcher=async()=>assert.fail('unknown or changed request must not be re-signed');
 for(const patch of [{rejected:false,errorCode:'timeout'},{rejected:true,errorCode:'revision_conflict'},{rejected:true,errorCode:'pvf_conflict'},{rejected:true,completed:true,errorCode:'plan_expired'}]) {
  a.meta.pending=receipt('old',patch);assert.equal(a.canRepreviewInventory(),false);await a.repreviewInventory('old');
 }
});
