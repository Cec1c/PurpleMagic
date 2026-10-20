// Runs only when explicitly invoked against preview_gateway_live.py's disposable database.
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { build } from 'esbuild';
import { pathToFileURL } from 'node:url';
const dir = process.env.PURPLEMAGIC_LIVE_FIXTURE;
if (!dir) throw new Error('Set PURPLEMAGIC_LIVE_FIXTURE to the disposable preview state directory');
const processInfo = JSON.parse(await fs.readFile(path.join(dir,'process.json'),'utf8'));
assert.match(processInfo.container,/^dnf90-gateway-preview-/);
assert.equal(processInfo.status,'running');
const origin = `https://127.0.0.1:${processInfo.port}`;
await build({entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',outfile:path.join(dir,'console-api.mjs')});
const {ConsoleAPI} = await import(pathToFileURL(path.join(dir,'console-api.mjs')));
const storage = new Map();
globalThis.sessionStorage = {getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)};
const api = new ConsoleAPI(()=>{});
const cookies = new Map();
api.http.fetcher = async (url,opts={}) => {
 const headers = {...opts.headers,Origin:origin,Cookie:[...cookies].map(([k,v])=>`${k}=${v}`).join('; ')};
 const res = await fetch(new URL(url,origin),{...opts,headers});
 for(const raw of res.headers.getSetCookie()) {const [k,...v] = raw.split(';')[0].split('=');cookies.set(k,v.join('='));}
 return res;
};
const evidence = [];
const pass = (name,detail={})=>{evidence.push({name,...detail});console.log('PASS',name);};
const selection = {accounts:[],characters:['10']};
async function commit(command,sel=selection) {
 await api.prepare(command,sel);
 const pending=api.meta.pending;
 await api.confirm();
 assert.equal(pending.completed,true);
 api.cancel();
 return pending;
}
try {
 await api.login(JSON.parse(await fs.readFile(path.join(dir,'login-key.json'),'utf8')).key);
 assert.equal(api.state.characters.length,8);
 assert.equal(api.state.characters.find(c=>c.id==='40').online,true);
 await api.select('10');
 pass('login-directory-presence');
 for(const tab of ['bag','equipped','petbag','warehouse','account','pets','avatars','emblems']) {
  await api.inventory(tab);pass('inventory-read-'+tab,{count:api.state.items.length});
 }
 await api.inventory('bag');
 let item=api.state.items.find(i=>i.slot===9 && i.remote.container==='equipment');
 await api.item(item.key);item=api.state.items.find(i=>i.key===item.key);
 await commit({kind:'item',key:item.key,version:item.version,patch:{count:item.count,enhance:7,amplify:0,forge:item.forge,bound:item.bound}});
 await api.inventory('bag');await api.item(item.key);
 assert.equal(api.state.items.find(i=>i.key===item.key).enhance,7);
 pass('equipment-preview-commit-readback');
 await api.growth();
 const c=api.state.characters.find(c=>c.id==='10');
 await commit({kind:'growth',characterId:'10',level:c.level,job:c.job,sp:120,tp:7});
 await api.growth();assert.equal(api.state.characters.find(c=>c.id==='10').sp,120);assert.equal(api.state.characters.find(c=>c.id==='10').tp,7);
 pass('multi-step-sp-tp');
 await api.select('20');await api.growth();
 const other=api.state.characters.find(c=>c.id==='20');
 assert.ok(api.meta.jobs.some(j=>j.value==='0:1'));
 await commit({kind:'growth',characterId:'20',level:86,job:'0:1',sp:150,tp:9});
 await api.growth();await api.character();
 assert.equal(api.state.characters.find(c=>c.id==='20').level,86);
 assert.equal(api.meta.resources.profession.balance.growType,1);
 assert.equal(api.meta.resources.skillPoints.balance.bonusSp,150);
 pass('combined-level-profession-skill-points');
 await api.select('10');
 const a=api.state.accounts.find(a=>a.id==='preview:1');
 await commit({kind:'currency',characterId:'10',gold:123456,points:3000,pointsText:'3000',tokens:a.tokens});
 assert.equal(api.state.characters.find(c=>c.id==='10').gold,123456);
 assert.equal(api.state.accounts.find(a=>a.id==='preview:1').rawPoints,'3000');
 pass('gold-currency-readback');
 await api.catalog('100050050');assert.ok(api.meta.catalog.some(i=>i.id==='100050050'));
 const mail=await commit({kind:'mail',recipients:[],title:'兰紫接入验收',body:'真实接口与一次性数据库',gold:321,items:[{id:'100050050',name:'幽魂魅影长袍',count:1,enhance:0}]},{accounts:['preview:1'],characters:['10']});
 assert.equal(mail.recipients.length,2);
 assert.equal(mail.steps[0].result.sent,2);
 await api.mailbox();const received=api.state.mails.find(m=>m.title==='兰紫接入验收');assert.ok(received);
 await api.mail(received.id);assert.equal(api.state.mails.find(m=>m.id===received.id).items[0].id,'100050050');
 pass('account-all-roles-dedup-mail');
 await commit({kind:'delete-mail',ids:[received.id]});await api.mailbox();assert.ok(!api.state.mails.some(m=>m.id===received.id));
 pass('mailbox-delete-readback');
 await api.quests('active');assert.ok(api.meta.quests.length);
 const quest=api.meta.quests[0];
 await commit({kind:'quest',characterId:'10',ids:[quest.id],status:'completed'});
 await api.quests('completed');assert.equal(api.state.questStates['10'][quest.id],'completed');
 await commit({kind:'quest',characterId:'10',ids:[quest.id],status:'active'});
 pass('quest-complete-reset');
 const benefit=await commit({kind:'benefit-create',name:'兰紫自动福利验收',gold:100,enabled:true,scope:'future'});
 const rule=benefit.steps[0].result;assert.ok(rule.id);
 await api.benefits();assert.equal(api.state.benefits.find(b=>b.id===rule.id).enabled,true);
 pass('benefit-create-enable',{id:rule.id});
 let delivered=false;
 for(let attempt=0;attempt<15;attempt++) {
  await api.mailbox();
  if(api.state.mails.some(m=>m.title==='兰紫自动福利验收')) {delivered=true;break;}
  await new Promise(resolve=>setTimeout(resolve,1000));
 }
 assert.ok(delivered,'automatic benefit must arrive without manually running a job');
 let proofTask;
 for(let attempt=0;attempt<15;attempt++) {
  let cursor="", sent=0;
  do { const page=await api.http.get(`/api/v1/benefits/${rule.id}/recipients?limit=100&cursor=${encodeURIComponent(cursor)}`); sent+=page.data.filter(r=>r.state==="sent").length; cursor=page.page?.nextCursor||""; } while(cursor);
  proofTask={sent};
  if(proofTask.sent===8) break;
  await new Promise(resolve=>setTimeout(resolve,1000));
 }
 assert.equal(proofTask.sent,8);
 await commit({kind:'benefit-toggle',id:rule.id});await api.benefits();
 await commit({kind:'benefit-toggle',id:rule.id});await api.mailbox();
 assert.equal(api.state.mails.filter(m=>m.title==='兰紫自动福利验收').length,1);
 pass('automatic-benefit-delivery-and-no-repeat');
 await api.hub(JSON.parse(await fs.readFile(path.join(dir,'hub-token.json'),'utf8')).token);
 assert.ok(api.meta.hub);
 const original=structuredClone(api.meta.hub.published.profile);
 await commit({kind:'news',id:'',title:'兰紫接口验收公告',body:'公告正文',published:true});await api.hub();assert.equal(api.state.news[0].title,'兰紫接口验收公告');
 pass('hub-news-publication');
 await commit({kind:'news',id:'0',title:'编辑草稿验收',body:'草稿内容',published:false});await api.hub();
 const draft=api.state.news.find(n=>!n.published);assert.ok(draft.id.startsWith('draft-'));
 await commit({kind:'news',id:draft.id,title:draft.title,body:draft.body,published:true});await api.hub();
 assert.equal(api.state.news.length,1);assert.equal(api.state.news[0].title,'编辑草稿验收');
 pass('news-draft-edit-publish-no-duplicate');
 await commit({kind:'sources',download:'https://downloads.preview.test',gameHost:'updated.preview.test',gamePort:10012,source:{id:'primary',name:'本地验收源',endpoint:'https://downloads.preview.test',publicKey:original.UpdatePublicKey,certificateSha256:'',enabled:true}});
 await api.hub();assert.equal(api.state.sources.gameHost,'updated.preview.test');assert.equal(api.state.sources.download,'https://downloads.preview.test');
 assert.deepEqual(api.meta.hub.published.profile,{...original,GameHost:'updated.preview.test',BootstrapPort:10012});
 pass('hub-source-connection-profile-preserved');
 await api.history();assert.ok(api.state.receipts.some(r=>r.target==='金币'));
 pass('domain-history');
 await fs.writeFile(path.join(dir,'integration-receipt.json'),JSON.stringify({passed:true,evidence},null,2));
} catch(e) {
 await fs.writeFile(path.join(dir,'integration-receipt.json'),JSON.stringify({passed:false,error:e.message,pending:api.meta.pending,evidence},null,2));
 throw e;
}
