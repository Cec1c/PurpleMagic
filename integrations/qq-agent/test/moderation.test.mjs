import {test,beforeEach} from 'node:test';
import assert from 'node:assert/strict';
import {setup,providers} from '../plugins/purplemagic-binding/index.js';
const self='567890123',qq='234567890',group='123456789';
let calls,acks,members,blocked,jobs,onebot;
beforeEach(()=>{
 calls=[];acks=[];members=[{user_id:self,role:'admin'},{user_id:qq,role:'member'}];blocked=true;
 jobs=[{id:'a'.repeat(64),lease:'b'.repeat(32),qq,group}];
 onebot={selfId:self,connected:true,call:async(action,args)=>{calls.push({action,args});if(action==='get_group_member_list')return members;return {};}};
 setup({config:()=>({endpoint:'http://127.0.0.1:8782',token:'t'.repeat(32)}),fetch:async(url,opt)=>{
  const path=url.split('/').at(-1);if(path==='ack')acks.push(JSON.parse(opt.body));
  return new Response(JSON.stringify({ok:true,data:path==='jobs'?jobs:path==='settings'?{groups:[group]}:path==='blocked'?{blocked}:{saved:true}}));
 }});
});
test('approved task kicks exact bound QQ and rejects rejoin',async()=>{
 await providers['moderation.qq-tick']({onebot});
 assert.deepEqual(calls[1],{action:'set_group_kick',args:{group_id:Number(group),user_id:Number(qq),reject_add_request:true}});
 assert.equal(acks[0].success,true);assert.equal(acks[0].cause,'kicked');
});
test('missing admin permission reports retry and never kicks',async()=>{
 members[0].role='member';await providers['moderation.qq-tick']({onebot});
 assert.equal(calls.length,1);assert.equal(acks[0].success,false);assert.equal(acks[0].cause,'permission_denied');
});
test('nonmember remains blacklisted without fake kick',async()=>{
 members.pop();await providers['moderation.qq-tick']({onebot});assert.equal(calls.length,1);assert.equal(acks[0].cause,'not_in_group');
});
test('no blacklist entry means no QQ mutation',async()=>{
 blocked=false;await providers['moderation.qq-tick']({onebot});assert.equal(calls.length,0);assert.equal(acks[0].success,false);
});
test('blacklisted join request is rejected with its exact flag',async()=>{
 const result=await providers['moderation.qq-event']({onebot,event:{post_type:'request',request_type:'group',sub_type:'add',user_id:qq,group_id:group,flag:'fixture-flag',time:Date.now()/1000}});
 assert.equal(result.handled,true);assert.deepEqual(calls[0],{action:'set_group_add_request',args:{flag:'fixture-flag',sub_type:'add',approve:false,reason:'已在黑名单中'}});
});
test('ordinary group requests and unrelated groups are untouched',async()=>{
 blocked=false;assert.equal((await providers['moderation.qq-event']({onebot,event:{post_type:'request',request_type:'group',sub_type:'add',user_id:qq,group_id:group,flag:'fixture',time:Date.now()/1000}})).handled,false);assert.equal(calls.length,0);
});
