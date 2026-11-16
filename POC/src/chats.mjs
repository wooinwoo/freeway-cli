import { existsSync, readFileSync, writeFileSync, renameSync } from 'node:fs';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { GatewayError } from './streams.mjs';

const bad = message => { throw new GatewayError(message, 400, 'invalid_chat'); };
const text = (value, max, name) => {
  if (typeof value !== 'string' || value.length > max) bad(name + ' 형식이나 길이를 확인하세요.');
  return value;
};
const attachments=value=>{
  if(!Array.isArray(value)||value.length>5)bad('A prompt supports up to five text files.');
  return value.map(file=>{if(!file||typeof file!=='object')bad('Invalid file attachment.');return {path:text(file.path,1000,'File path'),content:text(file.content,200000,'File content')};});
};
function fields(body, previous = {}) {
  const result = { ...previous };
  if (!body || typeof body !== 'object' || Array.isArray(body)) bad('대화 설정 객체가 필요합니다.');
  if (body.title !== undefined) result.title = text(body.title, 120, '제목').trim() || '새 대화';
  if (body.model !== undefined) result.model = text(body.model, 300, '모델 ID');
  if (body.systemPrompt !== undefined) result.systemPrompt = text(body.systemPrompt, 20000, '시스템 지침');
  if (body.draft !== undefined) result.draft = text(body.draft, 150000, '작성 중인 입력');
  if(body.workspace!==undefined)result.workspace=text(body.workspace,4000,'Workspace');
  if(body.draftFiles!==undefined)result.draftFiles=attachments(body.draftFiles);
  if(body.queued!==undefined){
    if(!Array.isArray(body.queued)||body.queued.length>20)bad('The follow-up queue supports up to 20 entries.');
    result.queued=body.queued.map(item=>{
      if(!item||typeof item!=='object'||Array.isArray(item))bad('Invalid queued prompt.');
      return {text:text(item.text,150000,'Queued prompt'),files:attachments(item.files||[])};
    });
    if(JSON.stringify(result.queued).length>2000000)bad('The follow-up queue is too large.');
  }
  if (body.settings !== undefined) {
    const s = body.settings;
    if (!s || typeof s !== 'object' || Array.isArray(s)) bad('생성 설정이 올바르지 않습니다.');
    const maxTokens = s.maxTokens ?? previous.settings?.maxTokens ?? 2048;
    const temperature = s.temperature ?? previous.settings?.temperature ?? 0.7;
    if (!Number.isInteger(maxTokens) || maxTokens < 1 || maxTokens > 32768) bad('출력 토큰은 1~32768 정수입니다.');
    if (!Number.isFinite(temperature) || temperature < 0 || temperature > 2) bad('Temperature는 0~2 사이입니다.');
    result.settings = { maxTokens, temperature };
  }
  if (body.messages !== undefined) {
    if (!Array.isArray(body.messages) || body.messages.length > 198) bad('대화 하나에 메시지는 최대 198개입니다. 새 대화를 시작하세요.');
    const ids = new Set();
    result.messages = body.messages.map(m => {
      if (!m || !['user','assistant'].includes(m.role)) bad('대화에는 사용자와 AI 메시지만 저장할 수 있습니다.');
      const id = text(m.id, 100, '메시지 ID');
      if (!id || ids.has(id)) bad('메시지 ID는 중복될 수 없습니다.');
      ids.add(id);
      const status = m.status ?? 'done';
      if (!['done','streaming','stopped','error'].includes(status)) bad('메시지 상태가 올바르지 않습니다.');
      const message = { id, role:m.role, content:text(m.content, 300000, '메시지'), status };
      if(m.files!==undefined)message.files=attachments(m.files);
      for (const [key,max] of [['model',300],['reasoning',300000],['error',1000]]) if (m[key] !== undefined) message[key] = text(m[key],max,key);
      return message;
    });
    if (JSON.stringify(result.messages).length > 1500000) bad('대화가 너무 큽니다. 내보내기 후 새 대화를 시작하세요.');
  }
  return result;
}

export class Chats {
  constructor(store) {
    this.store = store;
    this.path = join(store.dir, 'chats.json');
    this.data = existsSync(this.path) ? JSON.parse(store.decrypt(JSON.parse(readFileSync(this.path,'utf8')).encrypted)) : [];
    // An interrupted stream must never look as if it is still running after restart.
    let recovered = false;
    for (const chat of this.data) for (const message of chat.messages) if (message.status === 'streaming') {
      message.status = 'stopped'; recovered = true;
    }
    if (recovered) this.save();
  }
  save() {
    const temp = this.path + '.tmp';
    writeFileSync(temp, JSON.stringify({ version:1, encrypted:this.store.encrypt(JSON.stringify(this.data)) }),{mode:0o600});
    renameSync(temp,this.path);
  }
  list() {
    return this.data.map(c => ({id:c.id,title:c.title,model:c.model,workspace:c.workspace||'',hasDraft:Boolean(c.draft||c.draftFiles?.length),queuedCount:c.queued?.length||0,createdAt:c.createdAt,updatedAt:c.updatedAt,
      messageCount:c.messages.length,preview:(c.messages.find(m=>m.role==='user')?.content || '').slice(0,120)}))
      .sort((a,b)=>b.updatedAt.localeCompare(a.updatedAt));
  }
  get(id) {
    const chat = this.data.find(c=>c.id===id);
    if (!chat) throw new GatewayError('대화를 찾지 못했습니다.',404,'chat_not_found');
    return structuredClone(chat);
  }
  create(body) {
    if (this.data.length >= 300) throw new GatewayError('대화가 300개에 도달했습니다. 필요한 대화를 내보내고 정리하세요.',409,'chat_limit');
    const now = new Date().toISOString();
    const chat = fields(body,{id:randomUUID(),title:'새 대화',model:'free/auto',systemPrompt:'',settings:{maxTokens:2048,temperature:0.7},messages:[],revision:1,createdAt:now,updatedAt:now});
    this.data.push(chat); this.save(); return structuredClone(chat);
  }
  update(id,body) {
    const current = this.get(id);
    if (body?.revision !== current.revision) throw new GatewayError('다른 탭에서 대화가 변경되었습니다. 새로고침한 뒤 다시 시도하세요.',409,'chat_conflict');
    const chat = fields(body,current);
    chat.revision++; chat.updatedAt = new Date().toISOString();
    this.data[this.data.findIndex(c=>c.id===id)] = chat;
    this.save(); return structuredClone(chat);
  }
  delete(id) {
    this.get(id);
    this.data = this.data.filter(c=>c.id!==id);this.save();
  }
}
