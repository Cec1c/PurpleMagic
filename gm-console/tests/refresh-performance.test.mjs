import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
const {outputFiles}=await build({entryPoints:['src/api/console.ts'],bundle:true,platform:'node',format:'esm',write:false});
const {ConsoleAPI,itemDTO}=await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
globalThis.sessionStorage={getItem:()=>null,setItem(){},removeItem(){}};
const response=data=>new Response(JSON.stringify({data}));
const selection={accounts:[],characters:[]};
function fixture(){const a=new ConsoleAPI(()=>{});a.current='10';a.sectionId='gm';a.state.characters=[{id:'10',accountId:'a',name:'初始名字'}];a.meta.features['characters.rename']=true;return a;}

test('two consecutive renames each make only preview + commit; unrelated read failures cannot obscure success',async()=>{
 const a=fixture(),calls=[];
 a.http.fetcher=async(path,init)=>{assert.equal(init.method,'POST');const body=JSON.parse(init.body);calls.push(path);const receipt={kind:'rename-operation',accountId:'a',characterId:'10',before:body.expectedName,after:body.newName};return response(path.endsWith('/preview')?{receipt}:receipt)};
 for(const name of ['第一名字','第二名字']){
  await a.prepare({kind:'rename-character',characterId:'10',expectedName:a.state.characters[0].name,newName:name},selection);
  await a.confirm();assert.equal(a.state.characters[0].name,name);assert.equal(a.meta.error,'');
 }
 assert.equal(calls.length,4);
});

test('opened item reuses its options and commits into its row without reloading six containers or character assets',async()=>{
 const a=fixture(),c=a.state.characters[0];a.sectionId='inventory';
 const v={container:'materials',slot:201,itemId:'101',count:'15',bound:false,revision:'old',pvfChecksum:'hash',attributes:{}};
 const item=itemDTO(v,c,'bag');item.remote.options={item:v,canDelete:true,ownerScope:'character',quantity:{min:'1',max:'999'}};
 const unchanged=itemDTO({...v,slot:202},c,'bag');a.state.items=[item,unchanged];
 const calls=[];a.http.fetcher=async(path,init)=>{calls.push(path);assert.equal(init.method,'POST');return response(path.endsWith('/preview')?{planToken:'plan',receipt:{}}:{kind:'inventory-operation',ownerScope:'character',characterId:'10',accountId:'a',changes:[{container:'materials',slot:201,after:{...v,count:'4',name:'物品'},revision:'new'}]})};
 await a.prepare({kind:'item',key:item.key,version:item.version,patch:{count:4,bound:item.bound,enhance:item.enhance,amplify:item.amplify,forge:item.forge}},selection);
 await a.confirm();assert.equal(calls.length,2);assert.equal(a.state.items[0].count,4);assert.equal(a.state.items[1],unchanged);assert.equal(a.state.items[0].remote.revision,'new');
});

test('system mode persists through a single dedicated request without re-previewing pending operations',async()=>{
 const a=fixture();a.meta.pending={id:'keep',steps:[{id:'signed',body:{planToken:'keep'}}]};const before=JSON.stringify(a.meta.pending),calls=[];
 a.http.fetcher=async(path,init)=>{calls.push(path);return response({carbonMode:JSON.parse(init.body).carbonMode})};
 await a.systemSettings(true);assert.equal(a.meta.systemSettings.carbonMode,true);assert.equal(JSON.stringify(a.meta.pending),before);assert.deepEqual(calls,['/api/v1/settings/system']);
});

test('preview and player writes keep immutable catalog cache; changing PVF invalidates it',async()=>{
 const a=fixture(),calls=[];a.http.fetcher=async(path)=>{calls.push(path);return response({items:[]})};
 await a.http.get('/api/v1/items?q=x');await a.http.post('/api/v1/characters/10/inventory-operations/preview',{});await a.http.get('/api/v1/items?q=x');
 assert.equal(calls.filter(p=>p.startsWith('/api/v1/items')).length,1);
 await a.http.post('/api/v1/settings/pvf',{mode:'memory'});await a.http.get('/api/v1/items?q=x');assert.equal(calls.filter(p=>p.startsWith('/api/v1/items')).length,2);
});

test('refresh targets only the changed growth, balance or paused delivery list',async()=>{
 const {outputFiles}=await build({entryPoints:['src/api/refresh.ts'],bundle:true,platform:'node',format:'esm',write:false});
 const {refreshOperation}=await import('data:text/javascript;base64,'+Buffer.from(outputFiles[0].text).toString('base64'));
 const a=fixture(),calls=[];
 a.growth=async names=>calls.push(['growth',names]);a.balances=async names=>calls.push(['balances',names]);a.tasks=async()=>calls.push(['tasks']);
 await refreshOperation(a,{steps:[{done:true,path:'/api/v1/characters/10/skill-point-operations'},{done:true,path:'/api/v1/accounts/a/currency-adjustments'},{done:true,path:'/api/v1/tasks/x/pause'}]});
 assert.deepEqual(calls,[['growth',['skillPoints']],['balances',['currency']],['tasks']]);
});
