import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
const {outputFiles} = await build({entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',write:false});
const {ConsoleAPI,itemDTO} = await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
const storage=new Map();
globalThis.sessionStorage={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
const response=(data,status=200)=>new Response(JSON.stringify(status===200?{data}:{error:{code:'conflict',message:'revision changed'}}),{status});

test('cera benefit prepares an account-only credit without a PVF lookup or write', async()=>{
 const api=new ConsoleAPI(()=>{});api.http.fetcher=async()=>{throw Error('unexpected request')};
 await api.prepare({kind:'benefit-create',name:'账号福利',gold:2000,rewardKind:'cera',cera:500,grantScope:'character',enabled:true,scope:'future'},{accounts:[],characters:[]});
 const p=api.meta.pending;assert.equal(p.steps.length,2);assert.equal(p.steps[1].kind,'benefit-enable');
 assert.deepEqual(p.steps[0].body,{title:'账号福利',body:'账号福利',gold:0,cera:500,scope:'account',autoNewCharacters:true});
 assert.match(p.summary,/直接存入账号/);api.cancel();
 await assert.rejects(api.prepare({kind:'benefit-create',name:'bad',gold:0,rewardKind:'cera',cera:0,enabled:false,scope:'existing'},{accounts:[],characters:[]}),/整数/);
});

test('specific-item benefit binds the approved PVF and canonical item name before confirmation',async()=>{
 const api=new ConsoleAPI(()=>{});let reads=0;
 api.http.fetcher=async(path,opts)=>{assert.equal(opts.method,'GET');assert.equal(path,'/api/v1/items/7001/grant-options');reads++;return response({itemId:'7001',name:'权威短剑',kind:'equipment',pvfChecksum:'a'.repeat(64)});};
 const cmd={kind:'benefit-create',name:'道具福利',gold:900,rewardKind:'item',item:{id:'7001',name:'stale',count:1,enhance:0},enabled:false,scope:'existing',grantScope:'character'};
 await api.prepare(cmd,{accounts:[],characters:[]});const p=api.meta.pending;
 assert.equal(reads,1);assert.equal(p.steps.length,1);assert.equal(p.steps[0].body.gold,0);
 assert.equal(p.steps[0].body.itemId,'7001');assert.equal(p.steps[0].body.count,1);assert.equal(p.steps[0].body.pvfChecksum,'a'.repeat(64));
 assert.match(p.summary,/权威短剑/);assert.doesNotMatch(p.summary,/stale/);api.cancel();
 await assert.rejects(api.prepare({...cmd,item:{...cmd.item,count:2}},{accounts:[],characters:[]}),/数量为 1/);
});

test('benefit list preserves currency values and account scope on reload',async()=>{
 const api=new ConsoleAPI(()=>{});api.meta.features={'benefits.write':true};
 api.http.fetcher=async()=>response([{id:'rule',scope:'account',reward:{title:'点券',gold:0,cera:500},autoNewCharacters:true}]);
 await api.benefits();assert.equal(api.state.benefits[0].cera,500);assert.equal(api.state.benefits[0].grantScope,'account');assert.equal(api.state.benefits[0].rewardSummary,'点券 500');
});

test('delete benefit confirms once and reloads rules without a selected character',async()=>{
 const api=new ConsoleAPI(()=>{});api.meta.authenticated=true;api.meta.features={'benefits.write':true,'benefits.delete':true};api.sectionId='benefits';const calls=[];
 api.http.fetcher=async(path,init)=>{calls.push([path.split('?')[0],init.method]);
  if(path==='/api/v1/benefits/rule')return response({id:'rule',revision:8,reward:{title:'福利'}});
  if(path==='/api/v1/benefits/rule/delete'){assert.deepEqual(JSON.parse(init.body),{revision:8,confirm:true});return response({id:'rule',deleted:true,enabled:false});}
  return response([]);
 };
 await api.prepare({kind:'benefit-delete',id:'rule'},{accounts:[],characters:[]});
 assert.deepEqual(calls,[['/api/v1/benefits/rule','GET']]);assert.match(api.meta.pending.summary,/领取记录保留/);
 await api.confirm();assert.equal(api.meta.pending.completed,true);assert.equal(api.state.benefits.length,0);
 assert.deepEqual(calls,[['/api/v1/benefits/rule','GET'],['/api/v1/benefits/rule/delete','POST'],['/api/v1/benefits','GET']]);
});

test('token login requests 30-day memory by default and honors opting out',async()=>{
 const api=new ConsoleAPI(()=>{});const bodies=[];
 api.http.fetcher=async(path,init)=>{
  if(path==='/api/auth/login'){bodies.push(JSON.parse(init.body));return new Response(JSON.stringify({success:true}));}
  return response({features:{},csrfToken:'csrf'});
 };
 await api.http.login('fixture-token');await api.http.login('fixture-token',false);
 assert.deepEqual(bodies,[{password:'fixture-token',remember:true},{password:'fixture-token',remember:false}]);
});
test('expired session finishes boot at token entry and explicit disconnect clears client identity',async()=>{
 const api=new ConsoleAPI(()=>{});
 api.http.fetcher=async()=>response({},401);
 await api.boot();assert.equal(api.meta.authReady,true);assert.equal(api.meta.authenticated,false);
 api.meta.authenticated=true;api.current='10';api.http.csrf='csrf';
 api.http.fetcher=async()=>new Response(JSON.stringify({success:true}));
 await api.logout();assert.equal(api.meta.authReady,true);assert.equal(api.meta.authenticated,false);assert.equal(api.current,'');assert.equal(api.http.csrf,'');
});
function setup(fetcher) {
 const api=new ConsoleAPI(()=>{});api.http.fetcher=fetcher;
 api.character=async()=>{};api.enter=async()=>{};
 api.meta.pending={id:'request-1234567890',title:'修改',target:'角色10',summary:'属性',confirmed:false,steps:[{id:'request-1234567890-0',label:'金币',path:'/api/v1/characters/10/gold-operations',body:{amount:'100'}}]};
 return api;
}
test('lost commit response retries the same body and idempotency key',async()=>{
 const calls=[];
 const api=setup(async(path,opts)=>{calls.push({path,body:opts.body,key:opts.headers['Idempotency-Key']});if(calls.length===1) throw Error('socket closed');return response({requestId:'receipt'});});
 await assert.rejects(api.confirm());assert.equal(api.meta.pending.confirmed,true);assert.ok(!api.meta.pending.completed);
 await api.confirm();assert.equal(api.meta.pending.completed,true);assert.deepEqual(calls[0],calls[1]);api.cancel();
});

test('closing an in-flight operation keeps it running and exposes the persisted receipt after refresh',async()=>{
 storage.clear();let release;
 const api=setup(()=>new Promise(r=>release=r));const pending=api.confirm();
 api.dismissPending();assert.equal(api.meta.pending.hidden,true);assert.equal(api.meta.busy,true);
 assert.throws(()=>api.cancel());release(response({requestId:'kept-receipt'}));await pending;
 const restored=new ConsoleAPI(()=>{});restored.restore();assert.equal(restored.meta.pending.completed,true);
 assert.equal(restored.meta.pending.hidden,true);restored.openPending();assert.equal(restored.meta.pending.hidden,false);
 assert.equal(restored.meta.pending.steps[0].result.requestId,'kept-receipt');restored.cancel();
});

test('unknown response can be collapsed, navigated and restored without a new request identity',async()=>{
 storage.clear();const calls=[];
 const api=setup(async(path,init)=>{calls.push({path,body:init.body,key:init.headers['Idempotency-Key']});throw Error('lost');});
 await assert.rejects(api.confirm());api.dismissPending();api.navigate('quests',true);
 const restored=new ConsoleAPI(()=>{});restored.restore();restored.character=async()=>{};restored.enter=async()=>{};
 restored.http.fetcher=async(path,init)=>{calls.push({path,body:init.body,key:init.headers['Idempotency-Key']});return response({requestId:'permanent'});};
 assert.equal(restored.meta.pending.hidden,true);assert.equal(restored.canRepreviewQuests(),false);
 restored.openPending();await restored.confirm();assert.deepEqual(calls[0],calls[1]);restored.cancel();
});

test('quest revision rejection re-previews only unfinished targets before another explicit confirmation',async()=>{
 storage.clear();const calls=[];const api=setup(async()=>new Response(JSON.stringify({error:{code:'revision_conflict',message:'changed'}}),{status:409}));
 api.meta.pending.steps=[
  {id:'done',path:'/api/v1/characters/1/quest-operations',label:'A',body:{},done:true,result:{requestId:'first'}},
  {id:'old-key',path:'/api/v1/characters/2/quest-operations',label:'B',body:{accountId:'a',revision:'old',pvfChecksum:'old'}}
 ];
 await assert.rejects(api.confirm());assert.equal(api.canRepreviewQuests(),true);
 api.http.fetcher=async(path,init)=>{calls.push({path,body:JSON.parse(init.body)});return response({revision:'fresh',pvfChecksum:'approved'});};
 await api.repreviewQuests();assert.equal(calls.length,1);assert.ok(calls[0].path.endsWith('/preview'));
 assert.equal(api.meta.pending.confirmed,false);assert.equal(api.meta.pending.steps[0].result.requestId,'first');
 const step=api.meta.pending.steps[1];assert.notEqual(step.id,'old-key');assert.equal(step.body.revision,'fresh');assert.equal(step.body.pvfChecksum,'approved');
 await api.confirm();assert.equal(calls.length,2);assert.equal(calls[1].path,'/api/v1/characters/2/quest-operations');api.cancel();
});
test('successful commit survives a subsequent refresh failure',async()=>{
 let writes=0;const api=setup(async()=>{writes++;return response({requestId:'receipt'});});
 api.character=async()=>{throw Error('read unavailable')};
 await api.confirm();assert.equal(writes,1);assert.equal(api.meta.pending.completed,true);assert.equal(api.meta.error,'read unavailable');api.cancel();
});
test('definite rejection can be closed while completed earlier steps stay committed',async()=>{
 let writes=0;const api=setup(async()=>{writes++;return response({},409)});
 api.meta.pending.steps.unshift({id:'already-done',label:'先前步骤',path:'/api/v1/gold',body:{},done:true,result:{requestId:'permanent'}});
 await assert.rejects(api.confirm());assert.equal(writes,1);assert.equal(api.meta.pending.rejected,true);assert.equal(api.meta.pending.steps[0].result.requestId,'permanent');api.cancel();
});
test('partial mail failure produces a receipt and can be handled in task details',async()=>{
 const api=setup(async()=>response({total:3,sent:2,failed:1,pending:0,revision:4}));
 api.meta.pending.steps[0]={id:'task-key-1234567890',label:'发送',path:'/api/v1/tasks/job/run',body:{confirm:true,retry:false},kind:'task'};
 await api.confirm();assert.equal(api.meta.pending.completed,true);assert.equal(api.meta.pending.steps[0].result.failed,1);api.cancel();
});
test('instance projection preserves unknowns and actual numeric strings',()=>{
 const c={id:'10',accountId:'a'};
 const item=itemDTO({container:'equipment',slot:9,itemId:'1',count:'1',kind:'equipment',tag:'weapon',attributes:{reinforce:'7',amplifyType:null}},c,'bag');
 assert.equal(item.category,'武器');assert.equal(item.enhance,7);assert.ok(Number.isNaN(item.amplify));
});
test('account selection expands complete roles and deduplicates explicit selections',async()=>{
 const api=new ConsoleAPI(()=>{});api.http.fetcher=async()=>response([{characterId:'10',accountId:'a',name:'one',job:'0',level:90},{characterId:'11',accountId:'a',name:'two',job:'1',level:90}]);
 const list=await api.resolveSelection({accounts:['a'],characters:['10']});assert.deepEqual(list.map(x=>x.id),['10','11']);
});
test('view account characters uses ownership instead of searching account name in role names',async()=>{
 const api=new ConsoleAPI(()=>{});const calls=[];
 api.http.fetcher=async path=>{calls.push(path);if(path==='/api/v1/presence')return response({characterIds:[]});if(path.endsWith('/characters'))return response([{characterId:'10',accountId:'wallet:1',name:'unrelated-role-name',job:'0',level:90}]);return response({accountId:'wallet:1',name:'login-name'});};
 await api.directory('characters','',false,'wallet:1');
 assert.equal(api.state.characters[0].name,'unrelated-role-name');assert.ok(calls.includes('/api/v1/accounts/wallet%3A1/characters'));assert.ok(!calls.some(p=>p.startsWith('/api/v1/characters?')));
});

test('avatar bytes never become reinforcement, amplification or forging',()=>{
 const item=itemDTO({container:'avatars',slot:0,itemId:'47086',kind:'equipment',tag:'hair avatar',attributes:{reinforce:19,amplifyType:4,forging:8}},{id:'10',accountId:'a'},'avatars');
 assert.ok(Number.isNaN(item.enhance));assert.ok(Number.isNaN(item.amplify));assert.ok(Number.isNaN(item.forge));
});
test('overlapping reads retain busy state until the final request settles',async()=>{
 const api=new ConsoleAPI(()=>{});let finish1,finish2;
 const one=api.guard(()=>new Promise(r=>finish1=r));const two=api.guard(()=>new Promise(r=>finish2=r));
 assert.equal(api.meta.reading,2);finish1();await one;assert.equal(api.meta.reading,1);finish2();await two;assert.equal(api.meta.reading,0);
 await api.guard(async()=>{throw Error('fixture failure')});assert.equal(api.meta.reading,0);assert.equal(api.meta.error,'fixture failure');
});
test('cancelling a response body while switching views is not an invalid-data error',async()=>{
 const api=new ConsoleAPI(()=>{});
 api.http.fetcher=async()=>({json:async()=>{throw new DOMException('cancelled','AbortError')}});
 await api.guard(async()=>{await api.http.get('/api/v1/presence')});assert.equal(api.meta.error,'');assert.equal(api.meta.reading,0);
});
test('inventory loading covers every container and excludes an aborted previous character',async()=>{
 const api=new ConsoleAPI(()=>{});api.current='10';api.state.characters=[{id:'10',accountId:'a'}];
 let release;const blocked=new Promise(r=>release=r);let count=0;
 api.http.fetcher=async()=>{count++;if(count===6)await blocked;return response({items:[],pvfChecksum:'hash'});};
 const read=api.inventory('bag');await new Promise(r=>setTimeout(r,20));assert.equal(count,6);assert.equal(api.meta.inventory.loading,true);
 release();await read;assert.equal(api.meta.inventory.loading,false);
 let done;api.http.fetcher=async()=>{await new Promise(r=>done=r);return response({items:[{itemId:'123',container:'avatars',slot:1}],pvfChecksum:'hash'});};
 const old=api.inventory('avatars');await new Promise(r=>setTimeout(r,0));api.current='20';done();await old;assert.equal(api.state.items.length,0);
});
test('online selection sends the server filter before pagination',async()=>{
 const api=new ConsoleAPI(()=>{});const paths=[];api.http.fetcher=async path=>{paths.push(path);return response(path==='/api/v1/presence'?{characterIds:[]}:[]);};
 await api.directory('characters','',false,'','online');assert.ok(paths.some(p=>p.includes('online=online')));assert.equal(api.meta.directory.online,'online');
});
test('skill point balances survive later profession read errors',async()=>{
 const api=new ConsoleAPI(()=>{});api.current='10';api.state.characters=[{id:'10',accountId:'a',sp:NaN,tp:NaN}];api.meta.features={'skillPoints.read':true,'profession.read':true};
 api.http.fetcher=async path=>path.endsWith('/skill-points')?response({balance:{bonusSp:20,bonusTp:2,totalSp:100,remainingSp:70}}):response({},503);
 await api.guard(()=>api.growth());assert.equal(api.state.characters[0].sp,20);assert.equal(api.meta.resources.skillPoints.balance.remainingSp,70);
});
test('ID replacement and level defaults prepare one signed operation and no writes',async()=>{
 const api=new ConsoleAPI(()=>{});api.current='10';api.state.characters=[{id:'10',accountId:'a',name:'fixture'}];
 api.state.items=[{key:'item',id:'1',name:'weapon',slot:9,count:1,bound:false,version:2,remote:{container:'equipment',revision:'revision'}}];let posts=[];
 api.http.fetcher=async(path,opts)=>{
  if(opts.method==='POST'){posts.push({path,body:JSON.parse(opts.body)});return response({planToken:'signed',receipt:{}});}
  if(path.endsWith('options'))return response({canReplace:true,ownerScope:'character',item:{itemId:'1',count:'1',bound:false,revision:'revision',pvfChecksum:'hash'}});
  return response({level:90,balance:{},definition:{sp:11970,tp:41},revision:'skills',definitionRevision:'definition'});
 };
 await api.prepare({kind:'replace-item',key:'item',version:2,itemId:'2'},{accounts:[],characters:[]});
 assert.equal(posts[0].body.options.itemId,'2');assert.equal(api.meta.pending.steps[0].body.planToken,'signed');api.cancel();
 await api.prepare({kind:'reset-skill-points',characterId:'10'},{accounts:[],characters:[]});assert.equal(posts[1].body.action,'reset_defaults');assert.ok(posts.every(x=>x.path.endsWith('/preview')));api.cancel();
});

test('batch deletion resolves full write revisions from summary rows without requiring detail clicks',async()=>{
 const api=new ConsoleAPI(()=>{});const c={id:'10',accountId:'a',name:'fixture'};api.current='10';api.state.characters=[c];
 api.state.items=[9,10].map(slot=>itemDTO({container:'equipment',slot,itemId:'1',count:'1',bound:false,revision:'summary-'+slot,revisionScope:'summary'},c,'bag'));
 let body;
 api.http.fetcher=async(path,opts)=>{
  if(opts.method==='POST'){body=JSON.parse(opts.body);return response({planToken:'signed',receipt:{}});}
  const slot=path.includes('/9/')?9:10;
  return response({canDelete:true,ownerScope:'character',item:{itemId:'1',count:'1',bound:false,revision:'full-'+slot,pvfChecksum:'hash'}});
 };
 await api.prepare({kind:'delete-items',keys:api.state.items.map(x=>x.key)},{accounts:[],characters:[]});
 assert.deepEqual(body.items.map(x=>x.revision),['full-9','full-10']);api.cancel();
 api.state.items[0].id='2';await assert.rejects(api.prepare({kind:'delete-items',keys:[api.state.items[0].key]},{accounts:[],characters:[]}),/物品已变更/);
});


test('global benefit confirmation refreshes only benefits even when a character was selected', async()=>{
 const api=new ConsoleAPI(()=>{});api.meta.authenticated=true;api.meta.features={'benefits.write':true};api.sectionId='benefits';api.current='10';
 api.state.characters=[{id:'10',accountId:'a',name:'selected'}];
 const calls=[];api.http.fetcher=async(path,opts)=>{calls.push([path.split('?')[0],opts.method]);assert.equal(path.split('?')[0],'/api/v1/benefits');return response(opts.method==='POST'?{id:'rule',revision:0}:[]);};
 await api.prepare({kind:'benefit-create',name:'全局',gold:100,enabled:false,scope:'future'},{accounts:[],characters:[]});
 assert.match(api.meta.pending.target,/全部现有角色/);await api.confirm();
 assert.equal(api.meta.pending.completed,true);assert.deepEqual(calls,[['/api/v1/benefits','POST'],['/api/v1/benefits','GET']]);
});

test('benefit redelivery confirms original rule without a whole-server preview or character', async()=>{
 const api=new ConsoleAPI(()=>{});api.meta.features={backgroundBenefits:true};const calls=[];
 api.http.fetcher=async(path,init)=>{calls.push([path,init.method]);assert.equal(path,'/api/v1/benefits/rule');assert.equal(init.method,'GET');return response({id:'rule',revision:7,enabled:true,scope:'account',reward:{title:'点券福利'}})};
 await api.prepare({kind:'benefit-redeliver',id:'rule'},{accounts:[],characters:[]});
 const pending=api.meta.pending;assert.equal(calls.length,1);assert.equal(pending.steps.length,1);
 assert.equal(pending.steps[0].path,'/api/v1/benefits/rule/redelivery');assert.deepEqual(pending.steps[0].body,{revision:7,confirm:true});assert.match(pending.summary,/每账号仅发一次/);assert.match(pending.summary,/后台继续/);
 api.cancel();api.http.fetcher=async()=>response({enabled:false});await assert.rejects(api.prepare({kind:'benefit-redeliver',id:'rule'},{accounts:[],characters:[]}),/先启用/);
});
