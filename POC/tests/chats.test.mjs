import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Store } from '../src/store.mjs';
import { Chats } from '../src/chats.mjs';

function fixture(t) {
  const dir=mkdtempSync(join(tmpdir(),'freeway-chat-'));
  t.after(()=>rmSync(dir,{recursive:true,force:true}));
  const store=new Store(dir);return {dir,store,chats:new Chats(store)};
}

test('drafts and conversation survive restart in the encrypted chat store', t=>{
  const {dir,store,chats}=fixture(t);
  const draft='아직 보내지 않은 초안\nconsole.log("🌏")';
  const chat=chats.create({title:'개발 대화',draft,messages:[{id:'user-1',role:'user',content:'안녕하세요',status:'done'}]});
  const raw=readFileSync(join(dir,'chats.json'),'utf8');
  assert.ok(!raw.includes(draft)&&!raw.includes('안녕하세요'));
  assert.equal(new Chats(store).get(chat.id).draft,draft);
  assert.equal(new Chats(store).get(chat.id).messages[0].content,'안녕하세요');
});

test('clearing a draft is persisted and stale windows cannot overwrite it',t=>{
  const {chats}=fixture(t);
  const original=chats.create({draft:'초안'});
  const changed=chats.update(original.id,{revision:original.revision,draft:''});
  assert.equal(changed.draft,'');
  assert.throws(()=>chats.update(original.id,{revision:original.revision,draft:'오래된 창'}),{code:'chat_conflict'});
  assert.equal(chats.get(original.id).draft,'');
  assert.throws(()=>chats.update(original.id,{revision:changed.revision,draft:'x'.repeat(150001)}),{code:'invalid_chat'});
});

test('workspace, draft files and queued follow-ups survive encrypted restart',t=>{
 const {store,chats}=fixture(t);
 const file={path:'hello.go',content:'package main\n// 🌏'};
 const chat=chats.create({title:'UX audit',workspace:'C:/projects/demo',draftFiles:[file],queued:[{text:'next question',files:[file]}],messages:[{id:'u1',role:'user',content:'Read @hello.go',files:[file]}]});
 const restored=new Chats(store).get(chat.id);
 assert.deepEqual(restored.draftFiles,[file]);assert.deepEqual(restored.queued,[{text:'next question',files:[file]}]);
 assert.deepEqual(restored.messages[0].files,[file]);assert.equal(restored.workspace,'C:/projects/demo');
 const [summary]=chats.list();assert.equal(summary.hasDraft,true);assert.equal(summary.queuedCount,1);assert.equal(summary.workspace,restored.workspace);
 for(const queued of [[null],Array(21).fill({text:'q'}),[{text:'q',files:Array(6).fill(file)}]]) {
  assert.throws(()=>chats.update(chat.id,{revision:chat.revision,queued}),{code:'invalid_chat'});
 }
 const cleared=chats.update(chat.id,{revision:chat.revision,draftFiles:[],queued:[]});
 assert.deepEqual(new Chats(store).get(chat.id).queued,[]);assert.equal(cleared.revision,2);
});
