import test from 'node:test';
import assert from 'node:assert/strict';
import {createPasswordReset} from '../src/api/password-reset.ts';

test('password reset uses one random password and request across concurrent clicks and lost replies',async()=>{
  const calls=[];
  const operation=createPasswordReset('fixture:main','main',async(path,body,key)=>{
    calls.push({path,body,key});
    if(calls.length===1)throw new Error('lost reply');
    return {...body,accountId:'fixture:main',resetAt:123};
  });
  const first=operation.submit();
  assert.equal(operation.submit(),first);
  await assert.rejects(first,/lost reply/);
  const password=await operation.submit();
  assert.match(password,/^[A-Za-z0-9_-]{20}$/);
  assert.deepEqual(calls[0],calls[1]);
  assert.equal(calls[0].path,'/api/v1/accounts/fixture%3Amain/password-reset');
  assert.equal(calls[0].key,calls[0].body.requestId);
  assert.equal(calls[0].body.password,password);
  assert.equal(await operation.submit(),password);
  assert.equal(calls.length,2);
});

test('password result is not returned for an unrelated or missing receipt',async()=>{
  for(const result of [null,{}, {requestId:'other',accountId:'fixture:main',account:'main',resetAt:123}]) {
    const operation=createPasswordReset('fixture:main','main',async()=>result);
    await assert.rejects(operation.submit(),/暂未取得重置结果/);
  }
});

test('separate resets generate independent credentials without accessing browser persistence',async()=>{
  const trap=new Proxy({},{get(){throw new Error('credential entered storage');}});
  globalThis.localStorage=trap;globalThis.sessionStorage=trap;
  try {
    const post=async(path,body)=>({...body,accountId:'fixture:main',resetAt:123});
    const a=await createPasswordReset('fixture:main','main',post).submit();
    const b=await createPasswordReset('fixture:main','main',post).submit();
    assert.notEqual(a,b);
  } finally {delete globalThis.localStorage;delete globalThis.sessionStorage;}
});
