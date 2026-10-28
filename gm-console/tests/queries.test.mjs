import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
const {outputFiles} = await build({entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',write:false});
const {ConsoleAPI} = await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
globalThis.sessionStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};
const response=(data,status=200)=>new Response(JSON.stringify(status===200?{data}:{error:{message:'fixture failure'}}),{status});
const tick=()=>new Promise(r=>setTimeout(r,0));

test('cancelled character selection never starts a late presence read in the new page',async()=>{
 const api=new ConsoleAPI(()=>{});let reads=0;
 api.character=async()=>api.navigate('quests');
 api.http.fetcher=async()=>{reads++;return response({characterIds:[]});};
 await api.select('10');assert.equal(reads,0);
});

test('successful connection publication refreshes the signed Hub state shown in settings',async()=>{
 const api=new ConsoleAPI(()=>{});api.meta.authenticated=true;api.sectionId='settings';let published=false;
 api.meta.pending={id:'connection-request',title:'连接',target:'设置',summary:'修改游戏地址',confirmed:false,steps:[{id:'connection-step',path:'/admin/api/connection',method:'PUT',body:{gameHost:'new.example',bootstrapPort:1234},label:'发布'}]};
 api.http.fetcher=async(path,init)=>{
  if(init.method==='PUT'){published=true;return response({});}
  assert.equal(path,'/admin/api/state');assert.equal(published,true);
  return new Response(JSON.stringify({csrf:'next',sources:[],published:{profile:{GameHost:'new.example',BootstrapPort:1234}}}));
 };
 await api.confirm();assert.equal(api.meta.pending.completed,true);assert.equal(api.state.sources.gameHost,'new.example');assert.equal(api.state.sources.gamePort,1234);
});

test('account rows publish before supplements; GET concurrency is bounded and roles stay on demand',async()=>{
 const api=new ConsoleAPI(()=>{});api.meta.features={'currency.read':true};
 let active=0,peak=0;const pending=[];const paths=[];
 api.http.fetcher=async path=>{
  paths.push(path);
  if(path.startsWith('/api/v1/accounts?'))return response(Array.from({length:20},(_,i)=>({accountId:String(i),name:'account'+i,characterCount:3})));
  assert.ok(path.endsWith('/currency'));
  active++;peak=Math.max(peak,active);await new Promise(r=>pending.push(r));active--;return response({balance:'123'});
 };
 const work=api.guard(()=>api.directory('accounts',''));await tick();
 assert.equal(api.state.accounts.length,20);assert.equal(api.state.accounts[0].characterCount,3);assert.equal(peak,4);
 while(api.meta.reading){pending.splice(0).forEach(r=>r());await tick();}
 await work;assert.equal(paths.length,21);assert.equal(peak,4);assert.ok(api.state.accounts.every(a=>a.points===123));
});

test('navigation cancels an uncooperative old body and releases the new page spinner',async()=>{
 const api=new ConsoleAPI(()=>{});let finish;
 api.http.fetcher=async()=>({ok:true,json:()=>new Promise(r=>finish=r)});
 const old=api.guard(()=>api.activities());await tick();assert.equal(api.meta.reading,1);
 api.navigate('quests');api.http.fetcher=async()=>response({quests:[{questId:'1',name:'new'}],pvfChecksum:'h'});
 await api.guard(()=>api.quests('catalog'));await old;
 assert.equal(api.meta.reading,0);assert.equal(api.meta.quests[0].name,'new');
 finish({data:{activities:[{name:'stale'}]}});await tick();assert.equal(api.meta.resources.activities,undefined);assert.equal(api.meta.error,'');
});

test('deadline covers response body and queued reads and permits retry',async()=>{
 const api=new ConsoleAPI(()=>{});api.http.readTimeout=30;
 api.http.fetcher=async()=>({ok:true,json:()=>new Promise(()=>{})});
 const results=await Promise.allSettled(Array.from({length:8},()=>api.http.get('/api/v1/presence')));
 assert.ok(results.every(r=>r.status==='rejected' && r.reason.code==='timeout'));
 api.http.fetcher=async()=>response({activities:[]});await api.guard(()=>api.activities());assert.equal(api.meta.reading,0);
});

test('failed supplement preserves account rows and retry only reads the failed wallet',async()=>{
 const api=new ConsoleAPI(()=>{});api.meta.features={'currency.read':true};let fail=true;const paths=[];
 api.http.fetcher=async path=>{paths.push(path);return path.includes('?')?response([{accountId:'a',name:'A',characterCount:2},{accountId:'b',name:'B',characterCount:1}]):path.includes('/a/')&&fail?response({},503):response({balance:'88'});};
 await api.guard(()=>api.directory('accounts',''));
 assert.equal(api.state.accounts.length,2);assert.equal(api.state.accounts[1].points,88);assert.ok(api.meta.error);
 fail=false;paths.length=0;await api.retry();assert.deepEqual(paths,['/api/v1/accounts/a/currency']);assert.equal(api.state.accounts[0].points,88);assert.equal(api.meta.error,'');
});

test('older item search cannot replace newer search results',async()=>{
 const api=new ConsoleAPI(()=>{});let release;
 api.http.fetcher=async path=>{if(path.includes('q=old'))await new Promise(r=>release=r);return response({items:[{itemId:path.includes('q=old')?'1':'2'}],total:1});};
 const old=api.guard(()=>api.library('old',''));await tick();await api.guard(()=>api.library('new',''));
 release();await old;assert.equal(api.meta.resources.library.items[0].itemId,'2');assert.equal(api.meta.reading,0);
});

test('short static view cache excludes write preparation and resets after writes',async()=>{
 const api=new ConsoleAPI(()=>{});let reads=0;
 api.http.fetcher=async()=>{reads++;return response({items:[]});};
 await api.http.get('/api/v1/items?q=1');await api.http.get('/api/v1/items?q=1');assert.equal(reads,1);
 await api.http.get('/api/v1/items/1/grant-options');await api.http.get('/api/v1/items/1/grant-options');assert.equal(reads,3);
 await api.http.post('/api/v1/settings/pvf',{mode:'disk'});await api.http.get('/api/v1/items?q=1');assert.equal(reads,5);
});

test('multi-character task preview freezes targets and lost response retry skips successful characters',async()=>{
 const api=new ConsoleAPI(()=>{});api.state.characters=[{id:'10',accountId:'a',name:'A'},{id:'20',accountId:'b',name:'B'}];api.meta.questPVF='a'.repeat(64);api.meta.quests=[{id:'1',name:'任务'}];
 const calls=[];let lost=true;
 api.http.fetcher=async(path,init)=>{calls.push({path,body:init.body,key:init.headers['Idempotency-Key']});if(path.endsWith('/preview'))return response({revision:'r',planToken:'plan',equipmentSlots:{before:0,after:3,requested:3}});if(path.includes('/20/')&&lost){lost=false;throw Error('lost response');}return response({requestId:path,equipmentSlots:{before:0,after:3,requested:3}});};
 const selection={accounts:[],characters:['10','20']};
 await api.prepare({kind:'quest',characterId:'',selectedTargets:true,ids:['1'],status:'completed'},selection);
 assert.equal(calls.length,2);assert.ok(calls.every(x=>x.path.endsWith('/preview')));assert.deepEqual(api.meta.pending.recipients.map(x=>x.id),['10','20']);
 assert.deepEqual(api.meta.pending.steps[0].result.equipmentSlots,{before:0,after:3,requested:3});
 selection.characters.length=0;api.character=async()=>{};api.enter=async()=>{};
 await assert.rejects(api.confirm());assert.equal(api.meta.pending.steps[0].done,true);
 await api.confirm();const writes=calls.filter(x=>!x.path.endsWith('/preview'));
 assert.equal(writes.filter(x=>x.path.includes('/10/')).length,1);assert.deepEqual(writes[1],writes[2]);assert.match(api.meta.pending.summary,/同步开槽/);assert.match(api.meta.pending.summary,/不发金币、经验和道具/);
 assert.deepEqual(api.meta.pending.steps[0].result.equipmentSlots,{before:0,after:3,requested:3});
});
