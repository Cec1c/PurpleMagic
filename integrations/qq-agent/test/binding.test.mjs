import {test,beforeEach} from 'node:test';
import assert from 'node:assert/strict';
import {setup,dispose,parseCommand,providers} from '../plugins/purplemagic-binding/index.js';
const code='a'.repeat(32),self='567890123',qq='234567890',group='123456789';
let sent,calls,fetches,policy,member;
const run=providers['message.account-binding'];
function event(kind='group',extra={}){return {post_type:'message',message_type:kind,user_id:Number(qq),group_id:Number(group),message_id:123,time:Math.floor(Date.now()/1000),message:[{type:'at',data:{qq:self}},{type:'text',data:{text:` /绑定 player ${code}`}}],...extra};}
function ctx(e){return {event:e,onebot:{selfId:self,call:async(action,args)=>{calls.push({action,args});if(member instanceof Error)throw member;return member}},sender:{sendTextBatch:async(chat,text)=>sent.push({chat,text})}};}
beforeEach(()=>{
 dispose();sent=[];calls=[];fetches=[];policy={enabled:true,groups:[group],revision:3};member={user_id:Number(qq),group_id:Number(group)};
 setup({config:()=>({endpoint:'http://127.0.0.1:8782',token:'t'.repeat(32)}),warn:()=>{},fetch:async(url,options)=>{
   fetches.push({url,options});return new Response(JSON.stringify({ok:true,data:url.endsWith('settings')?policy:{qq}}),{status:200});
 }});
});
test('group command requires real mention and uses event QQ',async()=>{
 const result=await run(ctx(event()));assert.equal(result.handled,true);assert.equal(fetches.length,2);
 const body=JSON.parse(fetches[1].options.body);assert.equal(body.qq,qq);assert.equal(body.group,group);assert.equal(body.code,code);assert.equal(body.revision,3);
 assert.equal(sent.length,1);assert.ok(!sent[0].text[0].includes(code));assert.equal(calls.length,0);
});
test('unmentioned command is consumed without sending or calling a model',async()=>{
 const e=event('group',{message:[{type:'at',data:{qq:'678901234'}},{type:'text',data:{text:` /绑定 player ${code}`}}]});
 assert.equal((await run(ctx(e))).handled,true);assert.equal(fetches.length,0);assert.equal(sent.length,0);
});
test('private binding requires fresh allowed-group membership',async()=>{
 await run(ctx(event('private')));assert.equal(calls[0].action,'get_group_member_info');assert.equal(calls[0].args.no_cache,true);assert.equal(fetches.length,2);
});
test('private nonmember and failed QQ query cannot consume code',async()=>{
 member=Error('offline');await run(ctx(event('private')));assert.equal(fetches.length,1);assert.match(sent[0].text[0],/加入/);
});
test('unlisted group has no reply and never binds',async()=>{
 policy.groups=['345678901'];await run(ctx(event()));assert.equal(fetches.length,1);assert.equal(sent.length,0);
});
test('repeated and stale events do not repeat writes',async()=>{
 const e=event();await run(ctx(e));await run(ctx(e));await run(ctx(event('group',{message_id:124,time:1})));assert.equal(fetches.length,2);
});
test('ordinary messages and forwarded instructions do not match',()=>{
 assert.equal(parseCommand(event('group',{message:[{type:'text',data:{text:'hello'}}]}),self),null);
 assert.equal(parseCommand(event('group',{message:[{type:'forward',data:{text:`/绑定 player ${code}`}}]}),self),null);
});
test('malformed binding command receives instructions without write',async()=>{
 await run(ctx(event('group',{message:[{type:'at',data:{qq:self}},{type:'text',data:{text:'/绑定 player'}}]})));assert.equal(fetches.length,1);assert.match(sent[0].text[0],/获取绑定码/);
});
test('OneBot string CQ format uses exact QQ mention',()=>{
 const parsed=parseCommand(event('group',{message:`[CQ:at,qq=${self}] /绑定 player ${code}`}),self);assert.ok(parsed.mentioned);assert.equal(parsed.match[1],'player');
});
