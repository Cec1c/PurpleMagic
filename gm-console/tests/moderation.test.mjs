import test from 'node:test';
import assert from 'node:assert/strict';
import {duration,fatigue,nextSort,banState,suspicious,readPendingBan,pendingBanKey} from '../src/moderation.ts';
import {build} from 'esbuild';
import {createRequire} from 'node:module';
import {createElement} from 'react';
import {renderToStaticMarkup} from 'react-dom/server';

test('moderation recovers a confirmed request unchanged after a lost reply',()=>{
 const p={change:{requestId:'same-durable-request',kind:'ip',target:'192.0.2.12',reason:'test',expiresAt:null,revision:3,revoke:false},label:'192.0.2.12',confirmed:true};
 const storage={getItem:k=>k===pendingBanKey?JSON.stringify(p):null};
 assert.deepEqual(readPendingBan(storage),p);
 assert.equal(readPendingBan({getItem:()=>'{invalid'}),null);
 assert.equal(readPendingBan({getItem:()=>JSON.stringify({...p,confirmed:false})}),null);
});
test('expiry, timing and thresholds retain distinct meanings',()=>{
 assert.equal(duration(1234),'1.2 秒');assert.equal(duration(0),'—');
 const ban={revoked:false,expiresAt:'2026-10-01T00:00:00Z'};
 assert.equal(banState(ban,Date.parse('2026-10-02')),'已到期');
 assert.equal(banState({...ban,revoked:true}),'已解除');
 assert.equal(suspicious({fastestMs:0,peakHourly:1},30,60),false);
 assert.equal(suspicious({fastestMs:60000,peakHourly:65},30,60),true);
});
test('anti-cheat renders its tabs without a selected character',async()=>{
 const {outputFiles}=await build({entryPoints:['src/App.tsx'],bundle:true,platform:'node',format:'cjs',write:false,packages:'external',loader:{'.css':'empty'},define:{'import.meta.env.VITE_CONSOLE_MODE':'"demo"'}});
 const compiled={exports:{}};new Function('require','module','exports',outputFiles[0].text)(createRequire(import.meta.url),compiled,compiled.exports);
 globalThis.location={hash:'#anti-cheat',search:'?demo=1'};
 globalThis.localStorage={getItem:()=>null,setItem(){}};globalThis.sessionStorage={getItem:()=>null};
 const html=renderToStaticMarkup(createElement(compiled.exports.App));
 assert.match(html,/href="#anti-cheat"[^>]*aria-current="page"/);
 for(const text of ['通关统计','账号封禁','IP 与封禁记录','单小时峰值','平均','消耗疲劳','平均每次','aria-sort="descending"'])assert.ok(html.includes(text),text);
 assert.doesNotMatch(html,/identity-person/);
});

test('sort direction toggles and fatigue distinguishes unknown from a free clear',()=>{
 assert.deepEqual(nextSort({sortBy:'count',sortOrder:'desc'},'count'),{sortBy:'count',sortOrder:'asc'});
 assert.deepEqual(nextSort({sortBy:'count',sortOrder:'asc'},'count'),{sortBy:'count',sortOrder:'desc'});
 assert.deepEqual(nextSort({sortBy:'count',sortOrder:'desc'},'averageMs'),{sortBy:'averageMs',sortOrder:'asc'});
 assert.equal(fatigue(null),'—');assert.equal(fatigue(undefined),'—');assert.equal(fatigue(0),'0');assert.equal(fatigue(2.5,true),'2.5');
});
