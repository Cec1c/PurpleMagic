import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { parseSnapshot, visibleMetrics, heatmapCells, heatLevel, latestNews } from '../src/dashboard/model.ts';
const {outputFiles}=await build({stdin:{contents:"export {DashboardClient} from './src/dashboard/client'; export {HttpClient} from './src/api/http';",resolveDir:process.cwd()},bundle:true,platform:'node',format:'esm',write:false});
const {DashboardClient,HttpClient}=await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
const snapshot=(metrics={})=>({observedAt:'2026-10-31T01:02:00Z',timezone:'Asia/Shanghai',metrics});
const published=(news=[])=>new Response(JSON.stringify({Payload:Buffer.from(JSON.stringify({product:'DNF90-Flutter',format:1,news})).toString('base64')}));
const response=(data,status=200)=>new Response(JSON.stringify(status===200?{data}:{error:{message:'fixture',code:'fixture'}}),{status});
test('missing, null, negative, overflowing and text values never become zero cards',()=>{
 const view=parseSnapshot(snapshot({accounts:0,online:2,characters:null,dailyActive:'4',fatigueToday:-1,clearsToday:NaN,weeklyActive:2**53}));
 assert.deepEqual(visibleMetrics(view).map(m=>m.id),['accounts','online']);
 assert.equal(parseSnapshot({...snapshot(),timezone:'UTC'}),undefined);
});
test('heatmap distinguishes zero days, omitted days and invalid calendars',()=>{
 const days=[{date:'2026-10-26',count:0},{date:'2026-10-28',count:8}];
 assert.deepEqual(heatmapCells(days),[days[0],null,days[1]]);
 assert.equal(heatLevel(0,8),0);assert.equal(heatLevel(8,8),4);
 assert.equal(parseSnapshot({...snapshot(),registrations:[{date:'2026-02-31',count:2}]}).registrations,undefined);
 assert.equal(parseSnapshot({...snapshot(),registrations:[days[0],days[0]]}).registrations,undefined);
});
test('published news are date sorted and limited to three, rendered as text',()=>{
 assert.deepEqual(latestNews([1,2,3,4].map(i=>({title:'news'+i,body:'<script>fixture</script>',date:`2026-10-0${i}`}))).map(n=>n.title),['news4','news3','news2']);
});
test('old gateway hides aggregate cards; bounded presence and news still work and capability is probed once',async()=>{
 const paths=[];
 const http=new HttpClient(async path=>{paths.push(path);return path==='/api/v1/dashboard'?response(null,404):response({complete:true,characterIds:['1','1','2'],observedAt:'2026-10-31T01:00:00Z'});});
 const client=new DashboardClient(http,async()=>published([{title:'公告',body:'正文'}]));
 const a=await client.load(new AbortController().signal),b=await client.load(new AbortController().signal);
 assert.deepEqual(a.snapshot.metrics,{online:2});assert.equal(a.news.length,1);assert.deepEqual(a.failures,[]);
 assert.equal(paths.filter(p=>p==='/api/v1/dashboard').length,1);assert.equal(paths.length,3);assert.deepEqual(b.snapshot.metrics,{online:2});
});
test('aggregate metrics and the authoritative presence snapshot merge regardless of request completion order',async()=>{
 const http=new HttpClient(async path=>{if(path.endsWith('dashboard')){await new Promise(r=>setTimeout(r,8));return response(snapshot({accounts:20,online:999,clearsToday:0}));}return response({complete:true,characterIds:[],observedAt:'2026-10-31T01:00:00Z'});});
 const result=await new DashboardClient(http,async()=>published()).load(new AbortController().signal);
 assert.deepEqual(result.snapshot.metrics,{accounts:20,online:0,clearsToday:0});
});
test('temporary failures do not invent metrics or permanently disable later recovery',async()=>{
 let healthy=false;
 const http=new HttpClient(async path=>path.endsWith('dashboard')?response(healthy?snapshot({accounts:8}):null,healthy?200:503):response({complete:false,characterIds:[]}));
 const client=new DashboardClient(http,async()=>published());
 const first=await client.load(new AbortController().signal);assert.equal(first.snapshot,undefined);assert.equal(first.failures.length,2);
 healthy=true;const next=await client.load(new AbortController().signal);assert.equal(next.snapshot.metrics.accounts,8);assert.equal(next.snapshot.metrics.online,undefined);
});
test('cancelled dashboard reads cannot publish a late response after navigation',async()=>{
 const c=new AbortController();const http=new HttpClient(async()=>{await new Promise(r=>setTimeout(r,10));return response(snapshot({accounts:8}));});
 const work=new DashboardClient(http,async()=>published()).load(c.signal);c.abort();await assert.rejects(work,{name:'AbortError'});
});
test('expired GM session remains an authentication error instead of a missing capability',async()=>{
 const http=new HttpClient(async()=>response(null,401));
 await assert.rejects(new DashboardClient(http,async()=>published()).load(new AbortController().signal),e=>e.status===401);
});
