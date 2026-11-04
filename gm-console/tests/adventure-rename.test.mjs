import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';
import {createRequire} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';
const {outputFiles}=await build({entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',write:false});
const {ConsoleAPI}=await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
const storage=new Map();
globalThis.sessionStorage={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
const selection={accounts:[],characters:[]};
const cmd={kind:'rename-adventure',accountId:'a',expectedName:'旧冒险团',newName:' 新冒险团 '};
function fixture(){storage.clear();const api=new ConsoleAPI(()=>{});api.current='10';api.state.accounts=[{id:'a',name:'登录账号',points:100,adventureName:'旧冒险团'},{id:'b',name:'另一个账号',adventureName:'另一个团'}];api.state.characters=[{id:'10',accountId:'a',name:'角色甲'},{id:'11',accountId:'a',name:'角色乙'}];api.meta.features['adventure.rename']=true;api.meta.resources.adventureName={accountId:'a',name:'旧冒险团'};api.character=async()=>{};api.enter=async()=>{};return api;}
const receipt={kind:'adventure-rename-operation',accountId:'a',before:'旧冒险团',after:'新冒险团'};
test('adventure rename previews and confirms once, preserving login and character names',async()=>{
 const api=fixture(),calls=[];
 api.http.fetcher=async(path,init)=>{calls.push({path,body:JSON.parse(init.body),key:init.headers['Idempotency-Key']});return new Response(JSON.stringify({data:path.endsWith('/preview')?{receipt}:receipt}));};
 await api.prepare(cmd,selection);assert.equal(calls.length,1);assert.match(api.meta.pending.summary,/旧冒险团 → 新冒险团/);
 await api.confirm();assert.equal(calls.length,2);assert.match(calls[1].path,/accounts\/a\/adventure-rename-operations$/);assert.equal(calls[1].body.newName,'新冒险团');assert.ok(calls[1].key);
 assert.equal(api.state.accounts[0].adventureName,'新冒险团');assert.equal(api.state.accounts[0].name,'登录账号');assert.equal(api.state.accounts[0].points,100);assert.equal(api.state.accounts[1].adventureName,'另一个团');assert.equal(api.state.characters[0].name,'角色甲');assert.equal(api.meta.resources.adventureName.name,'新冒险团');
});
test('adventure rename rejects switched account, stale name and missing capability before preview',async()=>{
 for(const change of [a=>{a.state.characters[0].accountId='b'},a=>{a.meta.resources.adventureName.name='已变更'},a=>{a.meta.features['adventure.rename']=false},a=>{delete a.meta.resources.adventureName}]){const api=fixture();change(api);let calls=0;api.http.fetcher=async()=>{calls++;throw Error('unexpected request')};await assert.rejects(api.prepare(cmd,selection));assert.equal(calls,0);}
});
test('adventure rename retries original key after a lost response',async()=>{
 const api=fixture(),keys=[];let fail=true;
 api.http.fetcher=async(path,init)=>{if(path.endsWith('/preview'))return new Response(JSON.stringify({data:{receipt}}));keys.push(init.headers['Idempotency-Key']);if(fail){fail=false;throw Error('lost response')};return new Response(JSON.stringify({data:receipt}));};
 await api.prepare(cmd,selection);await assert.rejects(api.confirm());await api.confirm();assert.equal(keys.length,2);assert.equal(keys[0],keys[1]);assert.equal(api.meta.resources.adventureName.name,'新冒险团');
});
test('growth reads adventure by account and ignores a response after character switch',async()=>{
 const api=fixture();api.meta.features['adventure.read']=true;let resolve,started;
 const ready=new Promise(r=>started=r);
 api.http.fetcher=async path=>{assert.match(path,/accounts\/a\/adventure-name$/);return new Promise(r=>{resolve=r;started()})};
 const loading=api.growth();await ready;api.current='11';resolve(new Response(JSON.stringify({data:{accountId:'a',name:'过时响应'}})));await loading;assert.equal(api.meta.resources.adventureName.name,'旧冒险团');
});
test('adventure history uses the account scope and its pagination cursor',async()=>{
 const api=fixture();api.meta.features['adventure.read']=true;let calls=0;
 api.http.fetcher=async path=>{calls++;assert.match(path,/accounts\/a\/adventure-rename-operations/);if(calls===2)assert.match(path,/cursor=next/);return new Response(JSON.stringify({data:[{...receipt,requestId:String(calls),createdAt:'2026-11-04T00:00:00Z'}],page:calls===1?{nextCursor:'next'}:{}}));};
 await api.history();await api.history(true);assert.equal(calls,2);assert.equal(api.state.receipts.length,2);assert.match(api.state.receipts[0].summary,/旧冒险团 → 新冒险团/);
});

const rendered=await build({stdin:{contents:'export {GmPage} from "./src/pages/Gm"; export {LiveContext} from "./src/api/context";',resolveDir:process.cwd(),loader:'tsx'},bundle:true,platform:'node',format:'cjs',packages:'external',write:false});
const page={exports:{}};
new Function('require','module','exports',rendered.outputFiles[0].text)(createRequire(import.meta.url),page,page.exports);
test('GM renders the separate adventure form only with its capability and shows account-wide scope',()=>{
 const api=fixture();api.meta.resources.adventureName={accountId:'a',name:'旧冒险团'};const character={...api.state.characters[0],online:false,level:90,job:'剑魂',sp:0,tp:0};
 const render=()=>renderToStaticMarkup(createElement(page.exports.LiveContext.Provider,{value:api},createElement(page.exports.GmPage,{state:{...api.state,api:api.meta},character,request(){}})));
 const html=render();assert.match(html,/冒险团改名/);assert.match(html,/value="旧冒险团"/);assert.match(html,/所有角色共用此名称/);assert.match(html,/账号 登录账号/);
 api.meta.features['adventure.rename']=false;assert.doesNotMatch(render(),/冒险团改名/);
});
