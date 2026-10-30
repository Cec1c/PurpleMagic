let api;
const pending = new Set(), recent = new Map(), attempts = new Map();
export function setup(value) { api = value; }
export function available() {
  const cfg = api.config();
  try { const url = new URL(cfg.endpoint); if (url.protocol !== 'http:' || !['127.0.0.1','[::1]'].includes(url.hostname) || url.pathname !== '/' || url.search || url.hash || url.username || url.password || String(cfg.token).length < 32) throw Error(); }
  catch { return {ok:false, reason:'请配置网关回环地址和绑定凭据'}; }
  return {ok:true};
}
export function dispose() { pending.clear(); recent.clear(); attempts.clear(); }

// Parse only the received OneBot event. QQ identity never comes from text or AI.
export function parseCommand(event, selfId) {
  if (event?.post_type !== 'message' || !['group','private'].includes(event.message_type)) return null;
  const qq = String(event.user_id ?? '');
  if (!/^[1-9]\d{4,11}$/.test(qq) || qq === String(selfId)) return null;
  const segments = Array.isArray(event.message) ? event.message : null;
  let text, mentioned;
  if (segments) {
    mentioned = segments.some(s => s.type === 'at' && String(s.data?.qq) === String(selfId));
    text = segments.filter(s => s.type === 'text').map(s => s.data?.text ?? '').join('').trim();
  } else {
    const raw = String(event.raw_message ?? event.message ?? '');
    mentioned = [...raw.matchAll(/\[CQ:at,qq=(\d+)\]/g)].some(m => m[1] === String(selfId));
    text = raw.replace(/\[CQ:(?:at|reply),[^\]]*\]/g,'').trim();
  }
  if (!/^\/绑定(?:\s|$)/u.test(text)) return null;
  // Consume even an unmentioned binding command so its code never enters AI.
  const direct = /^\/绑定\s+([0-9]{6})$/u.exec(text);
  // Transitional support for commands copied by previously released launchers.
  const legacy = /^\/绑定\s+([A-Za-z0-9_]{3,20})\s+([0-9]{6}|[a-f0-9]{32})$/u.exec(text);
  const match = direct ? {code:direct[1]} : legacy ? {code:legacy[2], ...(legacy[2].length===32?{account:legacy[1]}:{})} : null;
  return {qq, group:String(event.group_id ?? ''), kind:event.message_type, mentioned, match, eventId:String(event.message_id ?? ''), time:Number(event.time)};
}

async function request(path,body) {
  const cfg=api.config();
  if(available().ok!==true)throw Error('绑定插件配置未完成，请联系管理员');
  const response=await api.fetch(cfg.endpoint.replace(/\/$/,'')+'/api/bot/qq-binding/'+path,{
    method:body?'POST':'GET',redirect:'error',signal:AbortSignal.timeout(8000),
    headers:{Authorization:'Bearer '+cfg.token,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined
  });
  const raw=await response.text();if(raw.length>16384)throw Error('绑定服务返回异常');
  let data;try{data=JSON.parse(raw)}catch{throw Error('绑定服务暂不可用，请稍后重试')}
  if(!response.ok||data.ok!==true)throw Error(data.message||'绑定未完成，请重新发送指令');
  return data.data;
}

export const providers = {
  'moderation.qq-tick': processModeration,
  'moderation.qq-event': processGroupEvent,
  'message.account-binding': async ({event,onebot,sender}) => {
    const cmd=parseCommand(event,onebot.selfId);if(!cmd)return {handled:false};
    if(cmd.kind==='group'&&!cmd.mentioned)return {handled:true};
    const now=Date.now();
    if(!Number.isFinite(cmd.time)||Math.abs(now-cmd.time*1000)>120000)return {handled:true};
    for(const [k,t] of recent)if(now-t>600000)recent.delete(k);
    for(const [k,v] of attempts)if(now-v.start>600000)attempts.delete(k);
    const key=cmd.kind+':'+cmd.group+':'+cmd.qq+':'+cmd.eventId;
    if(!cmd.eventId||pending.has(cmd.qq)||recent.has(key))return {handled:true};
    const bucket=attempts.get(cmd.qq)??{start:now,count:0};
    if(bucket.count>=6||pending.size>=16)return {handled:true};bucket.count++;attempts.set(cmd.qq,bucket);
    if(attempts.size>4096)attempts.delete(attempts.keys().next().value);
    if(recent.size>4096)recent.delete(recent.keys().next().value);
    pending.add(cmd.qq);recent.set(key,now);
    const chat=cmd.kind+':'+(cmd.kind==='group'?cmd.group:cmd.qq);
    try {
      const cfg=await request('settings');
      if(!cfg.enabled&&!cfg.allowBinding)throw Error('当前服务器未启用 QQ 绑定');
      if(cmd.kind==='group'&&!cfg.groups.includes(cmd.group))return {handled:true};
      let group=cmd.kind==='group'?cmd.group:'';
      if(cmd.kind==='private') {
        for(const id of cfg.groups) {
          try {const member=await onebot.call('get_group_member_info',{group_id:Number(id),user_id:Number(cmd.qq),no_cache:true},4000);
            if(String(member?.user_id)===cmd.qq && (member.group_id==null||String(member.group_id)===id)){group=id;break}}
          catch { /* A successful membership response is required. */ }
        }
        if(!group)throw Error('请先加入允许绑定的 QQ 群，再发送绑定指令');
      }
      if(!cmd.match)throw Error('请在登录器获取绑定码，然后发送：/绑定 六位绑定码');
      await request('complete',{...cmd.match,qq:cmd.qq,group,revision:cfg.revision});
      await sender.sendTextBatch(chat,['绑定成功，现在可以返回 PurpleMagic 登录游戏。']);
    } catch(e) {
      // Never log event bodies, binding codes or authentication headers.
      const known=String(e?.message??'');
      const text=/^[\u4e00-\u9fff]/u.test(known)&&known.length<160?known:'绑定服务暂不可用，请稍后重试';
      try{await sender.sendTextBatch(chat,[text])}catch{api.warn('绑定结果消息未送达')}
    } finally {pending.delete(cmd.qq)}
    return {handled:true};
  }
};

let moderationBusy=false;
const numberPattern=/^[1-9]\d{4,11}$/;
async function kickBlacklisted(onebot,qq,group) {
  if(!numberPattern.test(qq)||!numberPattern.test(group)||qq===String(onebot.selfId))throw Error('permission_denied');
  const members=await onebot.call('get_group_member_list',{group_id:Number(group),no_cache:true},6000);
  if(!Array.isArray(members))throw Error('qq_api_failed');
  const self=members.find(m=>String(m.user_id)===String(onebot.selfId));
  if(!self||!['admin','owner'].includes(self.role))throw Error('permission_denied');
  const member=members.find(m=>String(m.user_id)===qq);
  if(!member)return 'not_in_group';
  if(member.role==='owner'||(member.role==='admin'&&self.role!=='owner'))throw Error('permission_denied');
  await onebot.call('set_group_kick',{group_id:Number(group),user_id:Number(qq),reject_add_request:true},6000);
  return 'kicked';
}
async function processModeration({onebot}) {
  if(moderationBusy||!onebot.connected)return;
  moderationBusy=true;
  try {
    const jobs=await request('jobs',{});
    if(!Array.isArray(jobs)||jobs.length>4)throw Error('invalid jobs');
    for(const job of jobs) {
      if(!/^[a-f0-9]{64}$/.test(job.id)||!/^[a-f0-9]{32}$/.test(job.lease))continue;
      let success=false,cause='qq_api_failed';
      try {
        const cfg=await request('settings');
        if(!cfg.groups.includes(job.group))throw Error('permission_denied');
        const blocked=await request('blocked',{qq:job.qq});
        if(!blocked.blocked)throw Error('permission_denied');
        cause=await kickBlacklisted(onebot,job.qq,job.group);success=true;
      }catch(e){if(e?.message==='permission_denied')cause='permission_denied';}
      await request('ack',{id:job.id,lease:job.lease,success,cause});
    }
  }finally{moderationBusy=false;}
}
async function processGroupEvent({event,onebot}) {
  const join=event?.post_type==='request'&&event.request_type==='group'&&event.sub_type==='add';
  const entered=event?.post_type==='notice'&&event.notice_type==='group_increase';
  if(!join&&!entered)return {handled:false};
  const qq=String(event.user_id??''),group=String(event.group_id??'');
  if(!numberPattern.test(qq)||!numberPattern.test(group)||qq===String(onebot.selfId))return {handled:false};
  if(!Number.isFinite(Number(event.time))||Math.abs(Date.now()-Number(event.time)*1000)>120000)return {handled:false};
  const cfg=await request('settings');if(!cfg.groups.includes(group))return {handled:false};
  if(!(await request('blocked',{qq})).blocked)return {handled:false};
  if(join&&typeof event.flag==='string'&&event.flag.length<=512){
    await onebot.call('set_group_add_request',{flag:event.flag,sub_type:'add',approve:false,reason:'已在黑名单中'},6000);
  }else if(entered){await kickBlacklisted(onebot,qq,group);}
  return {handled:true};
}
