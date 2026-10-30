import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';
const {outputFiles}=await build({entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',write:false});
const {ConsoleAPI}=await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
globalThis.sessionStorage={getItem:()=>null,setItem:()=>{},removeItem:()=>{}};
const response=(data,status=200)=>new Response(JSON.stringify(status===200?{data}:{error:{message:'fixture conflict',code:'revision_conflict'}}),{status});
const command=(n=12)=>({kind:'mail',title:'补给',body:'正文',gold:500,items:Array.from({length:n},(_,i)=>({id:String(i+1),name:'道具'+i,count:2,enhance:0}))});
const targets=[{characterId:'10',accountId:'a',name:'角色'}];
const selection={accounts:[],characters:['10']};
function api(){const a=new ConsoleAPI(()=>{});a.state.characters=[{id:'10',accountId:'a',name:'角色'}];a.meta.features.backgroundMail=true;return a;}

test('many attachments use one generation read and bounded parallel previews with stable order and gold',async()=>{
 const a=api(),requests=[];let active=0,peak=0;
 a.http.fetcher=async(path,init)=>{
  const body=init.body&&JSON.parse(init.body);requests.push({path,body});active++;peak=Math.max(peak,active);
  await new Promise(r=>setTimeout(r,path.includes('/confirmation')?4:8));active--;
  if(path.endsWith('/grant-options'))return response({pvfChecksum:'pvf'});
  if(path==='/api/v1/mail/preview')return response({id:'job'+body.itemId,revision:0,total:1,pending:1,reward:{attachments:[{itemId:body.itemId,expiresAt:body.itemId==='2'?'2030-01-02T03:04:05Z':null}]}});
  if(path.endsWith('/confirmation'))return response({recipients:targets});
  throw Error(path);
 };
 await a.prepare(command(),selection);
 assert.equal(requests.filter(r=>r.path.endsWith('/grant-options')).length,1);
 assert.equal(requests.length,25);assert.ok(peak>1&&peak<=4);
 const previews=requests.filter(r=>r.path==='/api/v1/mail/preview');
 assert.equal(previews.filter(r=>r.body.gold).length,1);assert.equal(previews.find(r=>r.body.gold).body.itemId,'1');
 assert.ok(previews.every(r=>r.body.pvfChecksum==='pvf'));
 assert.deepEqual(a.meta.pending.steps.map(s=>s.path),Array.from({length:12},(_,i)=>`/api/v1/tasks/job${i+1}/run`));
 assert.match(a.meta.pending.steps[1].label,/2030\/01\/02.*到期/);
 assert.match(a.meta.pending.steps[0].label,/永久/);
 assert.ok(!requests.some(r=>r.path.endsWith('/run')||r.path.endsWith('/schedule')));
});

test('confirmation only schedules each task and skips player refresh or delivery polling',async()=>{
 const a=api();const requests=[];
 a.meta.pending={id:'batch',confirmed:false,steps:[1,2,3].map(i=>({id:'request'+i,path:`/api/v1/tasks/job${i}/run`,kind:'task',body:{revision:0,confirm:true,retry:false},result:{revision:0,pending:500,total:500}}))};
 a.http.fetcher=async(path,init)=>{requests.push(path);assert.equal(init.method,'POST');assert.ok(path.endsWith('/schedule'));return response({schedule:'active',pending:500,total:500,sent:0,revision:0});};
 a.character=async()=>assert.fail('unrelated character reload');a.enter=async()=>assert.fail('unrelated page reload');
 await a.confirm();assert.equal(requests.length,3);assert.ok(a.meta.pending.steps.every(s=>s.done&&s.result.schedule==='active'));assert.equal(a.meta.pending.completed,true);
});

test('lost schedule reply recovers original task without duplicate submission',async()=>{
 const a=api();let schedules=0,reads=0;
 a.meta.pending={id:'batch',confirmed:false,steps:[{id:'same-key',path:'/api/v1/tasks/job/run',kind:'task',body:{revision:0,confirm:true,retry:false}}]};
 a.http.fetcher=async(path,init)=>{if(init.method==='POST'){schedules++;throw Error('reply lost');}reads++;assert.equal(path,'/api/v1/tasks/job');return response({schedule:'active',pending:10,total:20,sent:10,revision:1});};
 await assert.rejects(a.confirm());assert.equal(a.meta.pending.rejected,false);assert.equal(a.meta.pending.steps[0].attempted,true);
 await a.confirm();assert.equal(schedules,1);assert.equal(reads,1);assert.equal(a.meta.pending.completed,true);
});

test('queue conflicts never fall back to synchronous grants',async()=>{
 const a=api(),calls=[];
 a.meta.pending={id:'batch',confirmed:false,steps:[{id:'key',path:'/api/v1/tasks/job/run',kind:'task',body:{revision:0,confirm:true,retry:false}}]};
 a.http.fetcher=async(path,init)=>{calls.push(path);if(init.method==='POST')return response({},409);return response({schedule:'paused',revision:1,pending:10});};
 await assert.rejects(a.confirm());assert.equal(a.meta.pending.rejected,true);assert.ok(calls.every(p=>!p.endsWith('/run')));
});

test('preview recipient mismatch rejects confirmation and drains pending work without sending',async()=>{
 const a=api();let active=0,completed=0;
 a.http.fetcher=async(path,init)=>{
  active++;await new Promise(r=>setTimeout(r,4));active--;completed++;
  if(path.endsWith('/grant-options'))return response({pvfChecksum:'pvf'});
  if(path==='/api/v1/mail/preview')return response({id:'job'+JSON.parse(init.body).itemId,revision:0});
  if(path.endsWith('/confirmation'))return response({recipients:[{...targets[0],accountId:'wrong'}]});
  assert.fail(path);
 };
 await assert.rejects(a.prepare(command(),selection),/收件名单/);assert.equal(active,0);assert.equal(a.meta.pending,undefined);assert.ok(completed<25);
});

test('double click during prepare does not create a second batch',async()=>{
 const a=api();let release;const paused=new Promise(r=>release=r);let reads=0;
 a.http.fetcher=async path=>{reads++;if(path.endsWith('/grant-options')){await paused;return response({pvfChecksum:'pvf'});}if(path.endsWith('/preview'))return response({id:'job',revision:0});return response({recipients:targets});};
 const first=a.prepare(command(1),selection);await a.prepare(command(1),selection);release();await first;assert.equal(reads,3);
});
