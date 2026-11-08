import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
async function module(path) {
 const {outputFiles}=await build({entryPoints:[path],bundle:true,platform:'node',format:'esm',write:false});
 return import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
}
const {QueryCache}=await module('src/api/query-cache.ts');
const {ConsoleAPI}=await module('src/api/console.ts');
const response=data=>new Response(JSON.stringify({data}));
const tick=()=>new Promise(r=>setTimeout(r,0));
const policy={ttl:10,stale:50,tags:['players']};
const signal=()=>new AbortController().signal;

test('LRU touches reads, expires fresh data, and preserves only bounded stale snapshots',async()=>{
 let now=0;const c=new QueryCache(2,4096,()=>now);
 const get=k=>c.get(k,policy,async()=>({id:k}),signal());
 await get('a');await get('b');assert.equal(c.peek('a').id,'a');await get('c');
 assert.equal(c.peek('b'),undefined);assert.equal(c.peek('a').id,'a');
 now=11;assert.equal(c.peek('a'),undefined);assert.equal(c.peek('a',true).id,'a');
 now=51;assert.equal(c.peek('a',true),undefined);
});
test('cache bounds bytes and does not retain oversize entries',async()=>{
 const c=new QueryCache(10,50);await c.get('a',policy,async()=>'a'.repeat(30),signal());
 await c.get('b',policy,async()=>'b'.repeat(30),signal());assert.equal(c.peek('a'),undefined);
 await c.get('large',policy,async()=>'c'.repeat(60),signal());assert.equal(c.peek('large'),undefined);
});
test('same query shares one request, cancelling one reader leaves the other subscribed',async()=>{
 const c=new QueryCache();let calls=0,release,transportSignal;
 const load=async s=>{calls++;transportSignal=s;return new Promise(r=>release=r)};
 const a=new AbortController(),b=new AbortController();
 const first=c.get('a',policy,load,a.signal),second=c.get('a',policy,load,b.signal);
 await tick();a.abort();await assert.rejects(first,{name:'AbortError'});assert.equal(transportSignal.aborted,false);
 release({id:1});assert.deepEqual(await second,{id:1});assert.equal(calls,1);
});
test('invalidating a write target rejects late readers and cannot repopulate old state',async()=>{
 const c=new QueryCache();let release;
 const work=c.get('a',policy,async()=>new Promise(r=>release=r),signal());await tick();
 c.invalidate(['players']);await assert.rejects(work,{name:'AbortError'});
 release({id:'old'});await tick();assert.equal(c.peek('a',true),undefined);
 assert.deepEqual(await c.get('a',policy,async()=>({id:'new'}),signal()),{id:'new'});
});
test('failed and cancelled reads are retried and callers cannot mutate cache values',async()=>{
 const c=new QueryCache();await assert.rejects(c.get('a',policy,async()=>{throw Error('failed')},signal()));
 const a=await c.get('a',policy,async()=>({nested:{n:1}}),signal());a.nested.n=5;
 assert.equal(c.peek('a').nested.n,1);
});
test('normalized query parameters share cache and forced reads ignore it',async()=>{
 const api=new ConsoleAPI(()=>{});let reads=0;api.http.fetcher=async()=>response({items:[],n:++reads});
 await api.http.get('/api/v1/items?q=x&limit=10');await api.http.get('/api/v1/items?limit=10&q=x');assert.equal(reads,1);
 await api.http.fresh('/api/v1/items?limit=10&q=x');assert.equal(reads,2);
});
test('directory uses the batch wallet projection without twenty supplement requests',async()=>{
 const api=new ConsoleAPI(()=>{});api.meta.features={'currency.read':true};let reads=0;
 api.http.fetcher=async path=>{reads++;assert.match(path,/accounts\?/);return response(Array.from({length:20},(_,i)=>({accountId:String(i),name:'A'+i,currency:{balance:String(i+1),revision:'r'}})))};
 await api.directory('accounts','');await api.directory('accounts','');assert.equal(reads,1);assert.equal(api.state.accounts[19].points,20);
});
test('inventory revisit uses its own container snapshot and never shows another character',async()=>{
 const api=new ConsoleAPI(()=>{});api.current='1';api.state.characters=[{id:'1',accountId:'a'},{id:'2',accountId:'b'}];
 let reads=0;api.http.fetcher=async path=>{reads++;return response({pvfChecksum:'pvf',items:[{itemId:'7',container:'warehouse',slot:1,count:'1'}]})};
 await api.inventory('warehouse');const n=reads;await api.inventory('warehouse');assert.equal(reads,n);
 api.current='2';let release;api.http.fetcher=async()=>new Promise(r=>release=r);
 const work=api.inventory('warehouse');assert.equal(api.state.items.length,0);await tick();
 release(response({pvfChecksum:'pvf',items:[]}));await work;assert.equal(api.state.items.length,0);
});
test('same inventory keeps rows during stale revalidation and removes deleted rows on arrival',async()=>{
 const api=new ConsoleAPI(()=>{});let now=0;api.http.cache=new QueryCache(200,32e6,()=>now);
 api.current='1';api.state.characters=[{id:'1',accountId:'a'}];
 api.http.fetcher=async()=>response({pvfChecksum:'pvf',items:[{itemId:'7',container:'warehouse',slot:1,count:'1'}]});
 await api.inventory('warehouse');now=4000;let release;
 api.http.fetcher=async()=>new Promise(r=>release=r);const work=api.inventory('warehouse');await tick();
 assert.equal(api.state.items.length,1);release(response({pvfChecksum:'pvf',items:[]}));await work;assert.equal(api.state.items.length,0);
});
test('PVF generation switches drop definitions while ordinary writes preserve them',async()=>{
 const api=new ConsoleAPI(()=>{});let hash='a'.repeat(64),reads=0;
 api.http.fetcher=async path=>{
  if(path.endsWith('capabilities'))return response({features:{'query.cacheGeneration':true},csrfToken:'x'});
  if(path.endsWith('query-generation'))return response({pvfChecksum:hash});
  if(path.startsWith('/api/v1/items'))reads++;
  return response({items:[],pvfChecksum:hash});
 };
 await api.http.capabilities();await api.http.get('/api/v1/items?q=x');await api.http.post('/api/v1/characters/1/gold-operations',{});
 await api.http.get('/api/v1/items?q=x');assert.equal(reads,1);
 hash='b'.repeat(64);api.http.generationAt=0;await api.http.get('/api/v1/items?q=x');assert.equal(reads,2);
});
test('authentication failure drops cached player information',async()=>{
 const api=new ConsoleAPI(()=>{});api.http.fetcher=async()=>response({name:'a'});
 await api.http.get('/api/v1/accounts/a');assert.ok(api.http.peek('/api/v1/accounts/a'));
 api.http.fetcher=async()=>new Response(JSON.stringify({error:{message:'expired'}}),{status:401});
 await assert.rejects(api.http.get('/api/v1/capabilities'));assert.equal(api.http.peek('/api/v1/accounts/a'),undefined);
});
test('authentication error propagates from a cached route instead of becoming a silent cancellation',async()=>{
 const api=new ConsoleAPI(()=>{});api.meta.authenticated=true;
 api.http.fetcher=async()=>new Response(JSON.stringify({error:{message:'expired'}}),{status:401});
 await api.guard(()=>api.directory('accounts',''));assert.equal(api.meta.authenticated,false);assert.equal(api.meta.error,'expired');
});
test('last cancelled reader aborts transport and a later caller starts a new flight',async()=>{
 const c=new QueryCache(200,32e6,()=>0);let transportSignal,release;
 const controller=new AbortController();
 const pending=c.get('q',policy,s=>{transportSignal=s;return new Promise(r=>release=r)},controller.signal);
 await tick();controller.abort();await assert.rejects(pending,{name:'AbortError'});assert.equal(transportSignal.aborted,true);
 assert.equal(await c.get('q',policy,async()=>2,signal()),2);release(1);await tick();assert.equal(c.peek('q'),2);
});
test('pagination and moderation filters have separate entries and bans invalidate aggregates',async()=>{
 const api=new ConsoleAPI(()=>{});let reads=0;
 api.http.fetcher=async(path,opt)=>response(opt.method==='GET'?{n:++reads}:{});
 const a='/api/v1/anti-cheat/clears?offset=0&hideBanned=true';
 await api.http.get(a);await api.http.get(a);await api.http.get(a.replace('offset=0','offset=50'));await api.http.get(a.replace('true','false'));assert.equal(reads,3);
 await api.http.post('/api/v1/anti-cheat/bans',{});await api.http.get(a);assert.equal(reads,4);
});
test('draft refresh updates untouched fields, preserves edits and resets only on object change',async()=>{
 const {refreshDraftValue:f}=await module('src/draft-refresh.ts');
 assert.equal(f('91','90','92'),'91');assert.equal(f('90','90','92'),'92');assert.equal(f('91','90','1',true),'1');assert.equal(f(NaN,NaN,100),100);
});
test('mailbox revalidation keeps the already opened message body',async()=>{
 const api=new ConsoleAPI(()=>{});api.current='1';api.state.mails=[{id:'2',characterId:'1',body:'opened',items:[{id:'5',count:1}]}];
 api.http.fetcher=async()=>response([{mailId:'2',title:'mail',gold:'0'}]);await api.mailbox();assert.equal(api.state.mails[0].body,'opened');assert.equal(api.state.mails[0].items[0].id,'5');
});
test('account pagination retains the previous page and uses a distinct cached cursor',async()=>{
 const api=new ConsoleAPI(()=>{});let reads=0;
 api.http.fetcher=async path=>{reads++;const next=path.includes('cursor=next');return new Response(JSON.stringify({data:[{accountId:next?'2':'1',name:next?'two':'one',currency:{balance:'5'}}],page:{nextCursor:next?'':'next'}}))};
 await api.directory('accounts','');await api.directory('accounts','',true);assert.deepEqual(api.state.accounts.map(a=>a.id),['1','2']);assert.equal(reads,2);
});
