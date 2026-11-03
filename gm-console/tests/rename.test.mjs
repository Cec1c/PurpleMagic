import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';
const {outputFiles}=await build({entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',write:false});
const {ConsoleAPI}=await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
const storage=new Map();
globalThis.sessionStorage={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
const selection={accounts:[],characters:[]};
const cmd={kind:'rename-character',characterId:'10',expectedName:'旧名字',newName:' 新名字 '};
function fixture(){storage.clear();const api=new ConsoleAPI(()=>{});api.current='10';api.state.characters=[{id:'10',accountId:'a',name:'旧名字'},{id:'11',accountId:'a',name:'另一角色'}];api.meta.features['characters.rename']=true;api.character=async()=>{};api.enter=async()=>{};return api;}
test('rename previews once and commits once, then updates only the selected name',async()=>{
 const api=fixture(),calls=[];
 api.http.fetcher=async(path,init)=>{const body=JSON.parse(init.body);calls.push({path,body,key:init.headers['Idempotency-Key']});const receipt={kind:'rename-operation',characterId:'10',accountId:'a',before:'旧名字',after:'新名字'};return new Response(JSON.stringify({data:path.endsWith('/preview')?{receipt}:receipt}));};
 await api.prepare(cmd,selection);assert.equal(calls.length,1);assert.match(api.meta.pending.summary,/旧名字 → 新名字/);assert.equal(api.state.characters[0].name,'旧名字');
 await api.confirm();assert.equal(calls.length,2);assert.equal(calls[1].body.newName,'新名字');assert.equal(calls[1].body.expectedName,'旧名字');assert.ok(calls[1].key);assert.equal(api.state.characters[0].name,'新名字');assert.equal(api.state.characters[1].name,'另一角色');
});
test('rename rejects stale selection, changed name and unavailable capability before preview',async()=>{
 for(const change of [a=>{a.current='11'},a=>{a.state.characters[0].name='已变化'},a=>{a.meta.features['characters.rename']=false}]){const api=fixture();change(api);let calls=0;api.http.fetcher=async()=>{calls++;throw Error('unexpected request')};await assert.rejects(api.prepare(cmd,selection));assert.equal(calls,0);}
});
test('rename lost response retries the same request identity and refreshes from its receipt',async()=>{
 const api=fixture(),keys=[];let fail=true;
 api.http.fetcher=async(path,init)=>{if(path.endsWith('/preview'))return new Response(JSON.stringify({data:{receipt:{}}}));keys.push(init.headers['Idempotency-Key']);if(fail){fail=false;throw Error('lost response')};return new Response(JSON.stringify({data:{kind:'rename-operation',characterId:'10',accountId:'a',after:'新名字'}}));};
 await api.prepare(cmd,selection);await assert.rejects(api.confirm());await api.confirm();assert.equal(keys.length,2);assert.equal(keys[0],keys[1]);assert.equal(api.state.characters[0].name,'新名字');
});
