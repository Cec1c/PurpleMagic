import {test,beforeEach} from 'node:test';
import assert from 'node:assert/strict';
import {setup,providers,dispose} from '../plugins/purplemagic-binding/index.js';
const self='567890123',qq='234567890',g1='123456789',g2='123456788';
let checks,results,calls,lists,counts,onebot,due,warnings;
beforeEach(()=>{
 checks=[{accountId:'fixture:main',qq,revision:3,settingsRevision:4,lease:'a'.repeat(32),groups:[g1,g2]}];
 results=[];calls=[];due=[];warnings=[];
 lists=new Map([[g1,[{user_id:self}]],[g2,[{user_id:self},{user_id:qq}]]]);counts=new Map([[g1,1],[g2,2]]);
 onebot={connected:true,selfId:self,call:async(action,p)=>{calls.push([action,p]);const group=String(p.group_id);if(lists.get(group) instanceof Error)throw lists.get(group);if(action==='get_group_info')return {group_id:group,member_count:counts.get(group)};if(action==='get_group_member_list')return lists.get(group);throw Error('unexpected QQ write');}};
 setup({config:()=>({endpoint:'http://127.0.0.1:8782',token:'t'.repeat(32)}),warn:m=>warnings.push(m),fetch:async(url,opt)=>{
  const path=url.split('/').at(-1);if(path==='check-result')results.push(JSON.parse(opt.body));if(path==='check-due')due.push(JSON.parse(opt.body));
  return new Response(JSON.stringify({ok:true,data:path==='checks'?checks:path==='jobs'?[]:path==='settings'?{groups:[g1,g2]}:{saved:true}}));
 }});
});
test('membership in any allowed group preserves binding and updates group',async()=>{
 await providers['moderation.qq-tick']({onebot});assert.equal(results[0].outcome,'member');assert.equal(results[0].group,g2);assert.equal(results[0].revision,3);
});
test('absence requires complete successful lists from every allowed group',async()=>{
 lists.set(g2,[{user_id:self}]);counts.set(g2,1);await providers['moderation.qq-tick']({onebot});assert.equal(results[0].outcome,'absent');
});
test('timeout or truncated member list is unknown rather than absence',async()=>{
 lists.set(g2,Error('offline'));await providers['moderation.qq-tick']({onebot});assert.equal(results[0].outcome,'unknown');
 results=[];lists.set(g2,[{user_id:self}]);await providers['moderation.qq-tick']({onebot});assert.equal(results[0].outcome,'unknown');
});
test('bot removed, empty list and invalid group info never invalidate players',async()=>{
 lists.set(g2,[]);counts.set(g2,0);await providers['moderation.qq-tick']({onebot});assert.equal(results[0].outcome,'unknown');
});
test('batch reuses group snapshots and performs no QQ write',async()=>{
 checks.push({...checks[0],accountId:'fixture:other',qq:'234567891'});await providers['moderation.qq-tick']({onebot});assert.equal(calls.length,4);assert.equal(results.length,2);
});
test('leaving queues a fresh check rather than deleting the binding',async()=>{
 const event={post_type:'notice',notice_type:'group_decrease',user_id:qq,group_id:g1,time:Date.now()/1000};
 await providers['moderation.qq-event']({event,onebot});assert.deepEqual(due,[{qq}]);assert.equal(calls.length,0);
});
test('offline or disposed plugin does not poll membership',async()=>{
 onebot.connected=false;await providers['moderation.qq-tick']({onebot});assert.equal(results.length,0);onebot.connected=true;dispose();await providers['moderation.qq-tick']({onebot});assert.equal(results.length,0);
});
