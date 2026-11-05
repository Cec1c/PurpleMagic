import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
const {outputFiles}=await build({entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',write:false});
const {ConsoleAPI}=await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
const storage=new Map();
globalThis.sessionStorage={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
function fixture(){storage.clear();const api=new ConsoleAPI(()=>{});api.current='10';api.state.characters=[{id:'10',accountId:'a',name:'角色'}];api.state.accounts=[{id:'a',name:'账号'}];api.meta.features['premium.write']=true;api.meta.features['premium.read']=true;return api;}
const command={kind:'premium-grant',accountId:'a',days:30},selection={accounts:[],characters:[]};
test('premium grant confirms once, retries same identity, updates only account contract',async()=>{
 const api=fixture(),requests=[];let fail=true;
 api.http.fetcher=async(path,init)=>{requests.push({path,key:init.headers['Idempotency-Key']});if(fail){fail=false;throw Error('response lost')};return new Response(JSON.stringify({data:{kind:'premium-grant',accountId:'a',expireAt:2000000000,active:true}}));};
 await api.prepare(command,selection);assert.equal(requests.length,0);await assert.rejects(api.confirm());await api.confirm();
 assert.equal(requests.length,2);assert.equal(requests[0].key,requests[1].key);assert.equal(requests[0].path,'/api/v1/accounts/a/premium-grants');assert.equal(api.meta.resources.premium.expireAt,2000000000);
});
test('premium rejects invalid days and stale account selection before network',async()=>{
 const api=fixture();api.http.fetcher=async()=>{throw Error('unexpected network')};
 for(const days of [0,-1,3651,1.5])await assert.rejects(api.prepare({...command,days},selection));
 await assert.rejects(api.prepare({...command,accountId:'other'},selection));
});
