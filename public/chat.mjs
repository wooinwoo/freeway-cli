import { escapeHTML as esc, renderMarkdown, chatToMarkdown } from './chat-render.mjs';

const $=selector=>document.querySelector(selector);
const icons={
  plus:'<path d="M12 5v14M5 12h14"/>',search:'<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4 4"/>',
  panel:'<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16M5.5 8h1M5.5 11h1"/>',
  settings:'<path d="M4 7h16M4 17h16"/><circle cx="9" cy="7" r="3" fill="currentColor" stroke="none"/><circle cx="15" cy="17" r="3" fill="currentColor" stroke="none"/>',
  network:'<rect x="8" y="3" width="8" height="6" rx="1.5"/><rect x="2" y="16" width="7" height="5" rx="1.5"/><rect x="15" y="16" width="7" height="5" rx="1.5"/><path d="M12 9v4M5.5 16v-3h13v3"/>',
  export:'<path d="M12 3v12m-4-4 4 4 4-4M5 15v5h14v-5"/>',send:'<path d="M12 20V4m-6 6 6-6 6 6"/>',stop:'<rect x="5" y="5" width="14" height="14" rx="2" fill="currentColor" stroke="none"/>',
  copy:'<rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V4H4v12h4"/>',retry:'<path d="M20 10a8 8 0 1 0-2 8M20 4v6h-6"/>',
  edit:'<path d="m14 5 5 5M4 20l4.5-1 12-12a2.8 2.8 0 0 0-4-4l-12 12L4 20Z"/>',trash:'<path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7"/>',
  arrowDown:'<path d="M12 4v16m-6-6 6 6 6-6"/>',check:'<path d="m5 12 4 4L19 6"/>'
};
const icon=name=>'<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'+icons[name]+'</svg>';
for(const [id,name] of Object.entries({'new-chat-icon':'plus','search-icon':'search','providers-icon':'network','dashboard-icon':'settings','close-sidebar':'panel','toggle-sidebar':'panel','chat-settings':'settings','export-chat':'export','send-message':'send','rename-chat':'edit','scroll-bottom':'arrowDown'}))$('#'+id).innerHTML=icon(name);

let state,history=[],active=null,busy=false,generation=null,saveTail=Promise.resolve(),saveError=false;
let selectedModel='free/auto',defaults={maxTokens:2048,temperature:0.7,systemPrompt:''},toastTimer,textAction,deleteId,autoFollow=true,renderTimer,autoSaveTimer;
let pendingProfile;
const safeLocal={get(key){try{return localStorage.getItem('freeway.'+key);}catch{return null;}},set(key,value){try{localStorage.setItem('freeway.'+key,value);}catch{}},remove(key){try{localStorage.removeItem('freeway.'+key);}catch{}}};
try{defaults={...defaults,...JSON.parse(safeLocal.get('chat-defaults')||'{}')};}catch{}
if(safeLocal.get('sidebar-collapsed')==='true'&&innerWidth>760)document.body.classList.add('sidebar-collapsed');

function toast(message){$('#toast').textContent=message;$('#toast').classList.add('show');clearTimeout(toastTimer);toastTimer=setTimeout(()=>$('#toast').classList.remove('show'),4200);}
async function api(path,options={},retry=true){
  const response=await fetch(path,{...options,headers:{'content-type':'application/json',...options.headers}});
  if(response.status===401&&retry){await fetch('/').then(r=>r.text());return api(path,options,false);}
  const data=await response.json();
  if(!response.ok){const e=new Error(data.error?.message||'요청을 처리하지 못했습니다.');e.status=response.status;throw e;}
  return data;
}
const modelName=id=>id==='free/auto'?'Freeway Auto':state?.models.find(m=>m.id===id)?.name||id.split('/').slice(1).join('/')||id;
const providerName=id=>state?.providers.find(p=>p.id===id)?.name||id;
function saveStatus(text,error=false){$('#save-state').textContent=text;$('#save-state').classList.toggle('error',error);$('#save-state').title=error?'클릭하여 다시 저장':'';}
function getDraft(){return safeLocal.get('draft.'+(active?.id||'new'))||'';}
function storeDraft(){safeLocal.set('draft.'+(active?.id||'new'),$('#message-input').value);}
function resizeInput(){const input=$('#message-input');input.style.height='auto';input.style.height=Math.min(input.scrollHeight,180)+'px';updateSend();}
function updateSend(){const button=$('#send-message');button.disabled=!busy&&!$('#message-input').value.trim();button.classList.toggle('stop',busy);button.setAttribute('aria-label',busy?'응답 중지':'메시지 보내기');button.innerHTML=icon(busy?'stop':'send');$('#choose-model').disabled=busy;$('#composer-model').disabled=busy;$('#chat-settings').disabled=busy;}
function statusNote(){
  const chosen=state?.models.find(m=>m.id===selectedModel);
  let note='';
  if(selectedModel!=='free/auto'&&!chosen?.ready)note='선택한 모델이 현재 연결되지 않았어요. 다른 모델을 선택하거나 공급처를 연결해주세요.';
  if(saveError)note='대화 저장에 실패했어요. 상단의 저장 상태를 눌러 다시 저장해주세요.';
  $('#composer-note').hidden=!note;$('#composer-note').textContent=note;
}
function updateChrome(){
  $('#selected-model-name').textContent=modelName(selectedModel);
  $('#composer-model-name').textContent=selectedModel==='free/auto'?'무료 경로 자동 선택':modelName(selectedModel);
  $('#context-count').textContent=active?.messages.length?Math.ceil(active.messages.length/2)+'번의 대화':'';
  $('#export-chat').disabled=!active?.messages.length;
  $('#conversation-title').textContent=active?.title||'';
  document.title=(active?.messages.length?active.title+' — ':'')+'Freeway 챗';
  const ready=state?.models.filter(m=>m.ready).length||0,providers=state?.providers.filter(p=>p.ready).length||0;
  $('#availability').textContent=ready?ready+'개 무료 모델이 연결되어 있어요':'공급처를 연결하고 대화를 시작하세요';
  $('#provider-status').textContent=providers+'개 공급처 · '+ready+'개 모델 준비';
  statusNote();updateSend();
}
async function refreshState(){state=await api('/api/state');updateChrome();}
function loadModelProfile(){
  const profile=state.settings.chatDefaults,version=String(state.settings.chatProfileVersion||'');
  if(!profile||!version||safeLocal.get('model-profile')===version)return;
  defaults={...defaults,maxTokens:profile.maxTokens,temperature:profile.temperature};
  selectedModel=profile.model;pendingProfile={...profile,version};
  safeLocal.set('chat-defaults',JSON.stringify(defaults));safeLocal.set('last-model',profile.model);
}
function upsertHistory(chat){
  const summary={id:chat.id,title:chat.title,model:chat.model,updatedAt:chat.updatedAt,createdAt:chat.createdAt,messageCount:chat.messages.length,preview:chat.messages.find(m=>m.role==='user')?.content.slice(0,120)||''};
  history=[summary,...history.filter(c=>c.id!==chat.id)].sort((a,b)=>b.updatedAt.localeCompare(a.updatedAt));renderHistory();
}
function renderHistory(){
  const query=$('#history-search').value.trim().toLocaleLowerCase();
  const entries=history.filter(c=>(c.title+' '+c.preview).toLocaleLowerCase().includes(query));
  if(!entries.length){$('#history-list').innerHTML='<p class="history-empty">'+(query?'검색 결과가 없어요.':history.length?'표시할 대화가 없어요.':'아직 나눈 대화가 없어요.<br>첫 이야기를 시작해보세요.')+'</p>';return;}
  const today=new Date().toLocaleDateString('en-CA');let previous='';
  $('#history-list').innerHTML=entries.map(c=>{
    const days=(Date.now()-Date.parse(c.updatedAt))/86400000;
    const group=new Date(c.updatedAt).toLocaleDateString('en-CA')===today?'오늘':days<7?'최근 7일':'이전 대화';
    const label=group!==previous?'<div class="history-label">'+group+'</div>':'';previous=group;
    return label+'<div class="history-item '+(active?.id===c.id?'active':'')+'"><button type="button" data-open-chat="'+esc(c.id)+'" title="'+esc(c.title)+'"'+(active?.id===c.id?' aria-current="true"':'')+'>'+esc(c.title)+'</button><button type="button" class="icon-btn history-delete" data-delete-chat="'+esc(c.id)+'" aria-label="'+esc(c.title)+' 삭제">'+icon('trash')+'</button></div>';
  }).join('');
}
function modelLabel(message){return message.model?providerName(message.model.split('/')[0])+' · '+modelName(message.model):busy?'답변을 준비하고 있어요':'';}
function messageMarkup(m,index){
  if(m.role==='user')return '<article class="message user" data-message-id="'+esc(m.id)+'"><div class="user-bubble">'+esc(m.content)+'</div><div class="user-tools"><button class="icon-btn" data-edit-message="'+esc(m.id)+'" aria-label="메시지 수정" '+(busy?'disabled':'')+'>'+icon('edit')+'</button><button class="icon-btn" data-copy-message="'+esc(m.id)+'" aria-label="내 메시지 복사">'+icon('copy')+'</button></div></article>';
  const last=index===active.messages.length-1;
  return '<article class="message assistant" data-message-id="'+esc(m.id)+'"><div class="assistant-header"><span class="assistant-avatar">↗</span><strong>Freeway</strong><span class="assistant-model">'+esc(modelLabel(m))+'</span></div><div class="assistant-body"><details class="reasoning" '+(m.reasoning?'':'hidden')+'><summary>생각 과정</summary><pre>'+esc(m.reasoning||'')+'</pre></details><div class="answer">'+renderMarkdown(m.content)+'</div><div class="typing" '+(m.status==='streaming'?'':'hidden')+'><i></i><i></i><i></i><span>'+(m.content?'답변을 이어 쓰고 있어요':'답변을 생성하는 중…')+'</span></div><div class="message-error" '+(m.error?'':'hidden')+'><span>'+esc(m.error||'')+'</span>'+(last?'<button data-regenerate>다시 시도</button>':'')+'</div></div><div class="message-actions" '+(m.status==='streaming'?'hidden':'')+'><button class="icon-btn" data-copy-message="'+esc(m.id)+'" aria-label="답변 복사" '+(!m.content?'disabled':'')+'>'+icon('copy')+'</button>'+(last?'<button class="icon-btn" data-regenerate aria-label="답변 다시 생성" '+(busy?'disabled':'')+'>'+icon('retry')+'</button>':'')+'<span class="message-status">'+(m.status==='stopped'?'응답 중지됨':m.status==='error'?'응답 실패':'')+'</span></div></article>';
}
function renderConversation(){
  const hasMessages=Boolean(active?.messages.length);
  $('#welcome').hidden=hasMessages;$('#conversation').hidden=!hasMessages;
  $('#messages').innerHTML=hasMessages?active.messages.map(messageMarkup).join(''):'';
  $('#stream-announcement').textContent='';updateChrome();
  autoFollow=true;requestAnimationFrame(()=>scrollEnd());
}
function scrollEnd(){const view=$('#chat-view');view.scrollTop=view.scrollHeight;$('#scroll-bottom').hidden=true;autoFollow=true;}
function paintStreaming(){
  clearTimeout(renderTimer);renderTimer=null;
  const m=active?.messages.at(-1);if(!m||m.role!=='assistant')return;
  const article=$('[data-message-id="'+m.id+'"]');if(!article)return;
  const answer=article.querySelector('.answer');answer.innerHTML=renderMarkdown(m.content);
  article.querySelector('.assistant-model').textContent=modelLabel(m);
  const reasoning=article.querySelector('.reasoning');reasoning.hidden=!m.reasoning;reasoning.querySelector('pre').textContent=m.reasoning||'';
  const typing=article.querySelector('.typing');typing.hidden=m.status!=='streaming';typing.querySelector('span').textContent=m.content?'답변을 이어 쓰고 있어요':'답변을 생성하는 중…';
  if(autoFollow)scrollEnd();
}
function schedulePaint(){if(!renderTimer)renderTimer=setTimeout(paintStreaming,70);}
function snapshot(chat){return {revision:chat.revision,title:chat.title,model:chat.model,settings:{...chat.settings},systemPrompt:chat.systemPrompt,messages:chat.messages.map(m=>({...m}))};}
function saveChat(chat=active){
  if(!chat)return Promise.resolve();
  const data=snapshot(chat);saveStatus('저장 중…');
  const task=saveTail.catch(()=>{}).then(async()=>{
    data.revision=chat.revision;
    const result=await api('/api/chats/'+chat.id,{method:'PUT',body:JSON.stringify(data)});
    chat.revision=result.revision;chat.updatedAt=result.updatedAt;saveError=false;
    if(active?.id===chat.id){saveStatus('저장됨');statusNote();}
    upsertHistory(chat);return result;
  });
  saveTail=task;
  return task.catch(error=>{if(active?.id===chat.id){saveError=true;saveStatus('저장 실패 · 재시도',true);statusNote();}throw error;});
}
function scheduleSave(){if(autoSaveTimer)return;autoSaveTimer=setTimeout(()=>{autoSaveTimer=null;saveChat().catch(()=>{});},1500);}
async function stopGeneration(){if(generation){generation.controller.abort();await generation.task;}}
function closeSidebar(){document.body.classList.remove('sidebar-open');}
function setLocation(id){const target=id?'#chat/'+id:location.pathname;historyReplace(target);}
function historyReplace(url){window.history.replaceState(null,'',url);}
async function newChat(){
  await stopGeneration();await saveTail.catch(()=>{});
  if(saveError&&active){await saveChat();}
  storeDraft();active=null;selectedModel=safeLocal.get('last-model')||'free/auto';
  safeLocal.remove('active-chat');setLocation(null);saveStatus('');saveError=false;renderHistory();renderConversation();
  $('#message-input').value=getDraft();resizeInput();closeSidebar();$('#message-input').focus();
}
async function openChat(id){
  if(active?.id===id){closeSidebar();return;}
  await stopGeneration();await saveTail.catch(()=>{});if(saveError&&active)await saveChat();storeDraft();
  const chat=await api('/api/chats/'+id);
  let recovered=false;for(const m of chat.messages)if(m.status==='streaming'){m.status='stopped';recovered=true;}
  active=chat;selectedModel=chat.model;saveError=false;safeLocal.set('active-chat',id);setLocation(id);
  renderHistory();renderConversation();$('#message-input').value=getDraft();resizeInput();saveStatus('저장됨');closeSidebar();
  if(recovered)await saveChat();
}
async function ensureChat(){
  if(active)return active;
  active=await api('/api/chats',{method:'POST',body:JSON.stringify({model:selectedModel,systemPrompt:defaults.systemPrompt,settings:{maxTokens:defaults.maxTokens,temperature:defaults.temperature}})});
  safeLocal.set('active-chat',active.id);setLocation(active.id);upsertHistory(active);return active;
}
function payloadMessages(chat){
  const result=chat.messages.filter(m=>m.role==='user'||m.status!=='streaming'&&m.status!=='error'&&m.content).map(m=>({role:m.role,content:m.content}));
  if(chat.systemPrompt.trim())result.unshift({role:'system',content:chat.systemPrompt});
  return result;
}
function startGeneration({text,truncate}={}){
  if(busy)return;
  busy=true;const controller=new AbortController();generation={controller,task:null};updateSend();
  generation.task=(async()=>{
    let assistant,chat;
    try{
      chat=await ensureChat();
      if((truncate??chat.messages.length)+(text?2:1)>198)throw new Error('대화가 길어졌어요. 새 대화를 시작해주세요.');
      if(truncate!==undefined)chat.messages=chat.messages.slice(0,truncate);
      if(text)chat.messages.push({id:crypto.randomUUID(),role:'user',content:text,status:'done'});
      if(chat.title==='새 대화')chat.title=chat.messages.find(m=>m.role==='user')?.content.replace(/\s+/g,' ').trim().slice(0,45)||'새 대화';
      chat.model=selectedModel;
      const messages=payloadMessages(chat);
      assistant={id:crypto.randomUUID(),role:'assistant',content:'',reasoning:'',status:'streaming'};chat.messages.push(assistant);
      renderConversation();await saveChat(chat);
      safeLocal.remove('draft.new');safeLocal.remove('draft.'+chat.id);
      if(controller.signal.aborted)throw new DOMException('Stopped','AbortError');
      const response=await fetch('/v1/chat/completions',{method:'POST',headers:{'content-type':'application/json'},signal:controller.signal,body:JSON.stringify({model:chat.model,messages,max_tokens:chat.settings.maxTokens,temperature:chat.settings.temperature,stream:true})});
      if(!response.ok){const result=await response.json();throw new Error(result.error?.message||'모델이 요청을 처리하지 못했어요.');}
      const reader=response.body.getReader(),decoder=new TextDecoder();let buffer='',finished=false;
      while(!finished){
        const {done,value}=await reader.read();if(done)break;
        buffer+=decoder.decode(value,{stream:true});let boundary;
        while((boundary=buffer.indexOf('\n\n'))>=0){
          const block=buffer.slice(0,boundary);buffer=buffer.slice(boundary+2);
          const data=block.split('\n').filter(l=>l.startsWith('data:')).map(l=>l.slice(5).trimStart()).join('\n');
          if(!data)continue;if(data==='[DONE]'){finished=true;break;}
          const chunk=JSON.parse(data);if(chunk.error)throw new Error(chunk.error.message);
          if(chunk.model)assistant.model=chunk.model;
          for(const choice of chunk.choices||[]){assistant.content+=choice.delta?.content||'';assistant.reasoning+=choice.delta?.reasoning_content||choice.delta?.reasoning||'';}
          schedulePaint();scheduleSave();
        }
      }
      if(!finished)throw new Error('답변이 완료되기 전에 연결이 끊겼어요. 다시 시도해주세요.');
      if(!assistant.content.trim())throw new Error(assistant.reasoning?'생각 과정만 반환됐어요. 출력 토큰을 늘리거나 다른 모델로 다시 시도해주세요.':'모델이 텍스트 답변을 반환하지 않았어요. 다른 모델로 다시 시도해주세요.');
      assistant.status='done';
    }catch(error){
      if(assistant){assistant.status=controller.signal.aborted?'stopped':'error';if(!controller.signal.aborted)assistant.error=error.message;}
      else {toast(error.message);if(text){$('#message-input').value=text;resizeInput();storeDraft();}}
    }finally{
      clearTimeout(autoSaveTimer);autoSaveTimer=null;clearTimeout(renderTimer);renderTimer=null;
      if(chat&&assistant){try{await saveChat(chat);}catch(error){toast(error.message);}}
      busy=false;generation=null;renderConversation();updateSend();
      $('#stream-announcement').textContent=assistant?.status==='done'?'답변이 완료되었습니다.':assistant?.status==='stopped'?'응답을 중지했습니다.':'';
      refreshState().catch(()=>{});
    }
  })();
  return generation.task;
}
async function submitMessage(event){event?.preventDefault();if(busy){generation?.controller.abort();return;}
  const text=$('#message-input').value.trim();if(!text)return;
  $('#message-input').value='';storeDraft();resizeInput();await startGeneration({text});
}
async function regenerate(){if(busy||!active)return;let index=active.messages.length-1;while(index>=0&&active.messages[index].role!=='user')index--;if(index<0)return;await startGeneration({truncate:index+1});}
function openModels(){if(busy)return;$('#model-search').value='';renderModels();$('#model-dialog').showModal();$('#model-search').focus();}
function renderModels(){
  const q=$('#model-search').value.toLocaleLowerCase();
  let result='';
  if(!q||'freeway auto 자동 무료'.includes(q))result='<button class="model-option '+(selectedModel==='free/auto'?'selected':'')+'" data-select-model="free/auto"><span class="model-monogram">↗</span><span class="model-option-info"><strong>Freeway Auto</strong><small>연결한 무료 공급처에서 자동 선택</small></span><span class="model-check">'+(selectedModel==='free/auto'?'✓':'')+'</span></button>';
  const models=[...(state?.models||[])].sort((a,b)=>Number(b.ready)-Number(a.ready)||a.provider.localeCompare(b.provider)||a.name.localeCompare(b.name)).filter(m=>(m.name+' '+m.id+' '+providerName(m.provider)).toLocaleLowerCase().includes(q));
  let group='';for(const m of models){const label=providerName(m.provider)+(m.ready?'':' · 연결 필요');if(label!==group){result+='<div class="model-section-label">'+esc(label)+'</div>';group=label;}
    result+='<button class="model-option '+(selectedModel===m.id?'selected':'')+'" data-select-model="'+esc(m.id)+'" '+(!m.ready?'disabled':'')+'><span class="model-monogram">'+esc(m.provider.slice(0,2).toUpperCase())+'</span><span class="model-option-info"><strong>'+esc(m.name)+'</strong><small>'+esc(m.id)+'</small></span><span class="model-check">'+(selectedModel===m.id?'✓':'')+'</span></button>';
  }
  $('#model-list').innerHTML=result||'<p class="history-empty">검색 결과가 없어요.</p>';
}
async function selectModel(id){selectedModel=id;safeLocal.set('last-model',id);if(active){active.model=id;await saveChat();}updateChrome();$('#model-dialog').close();$('#message-input').focus();}
function openSettings(){if(busy)return;const settings=active?.settings||defaults;$('#max-tokens').value=settings.maxTokens;$('#temperature').value=settings.temperature;$('#system-prompt').value=active?.systemPrompt??defaults.systemPrompt;$('#settings-error').textContent='';$('#settings-provider-note').textContent=selectedModel.startsWith('opencode/')?'OpenCode는 텍스트 대화를 지원하며, 출력 토큰과 Temperature는 OpenCode 자체 설정을 따릅니다.':'모델과 공급처에 따라 지원하는 설정이 다를 수 있어요.';$('#settings-dialog').showModal();}
function openTextDialog(mode,id){
  textAction={mode,id};$('#text-dialog-error').textContent='';
  const editing=mode==='edit';$('#text-dialog-title').textContent=editing?'메시지 수정':'대화 이름 변경';$('#text-dialog-label').textContent=editing?'수정할 메시지':'대화 이름';
  $('#text-dialog-description').textContent=editing?'이 메시지 이후의 답변을 새로 생성합니다.':'';
  const input=$('#text-dialog-input');input.maxLength=editing?150000:120;input.rows=editing?6:2;input.value=editing?active.messages.find(m=>m.id===id).content:active.title;
  $('#text-dialog-submit').textContent=editing?'수정하고 다시 보내기':'저장';$('#text-dialog').showModal();input.focus();if(!editing)input.select();
}
function askDelete(id){deleteId=id;$('#delete-description').textContent='“'+(history.find(c=>c.id===id)?.title||'대화')+'”';$('#delete-dialog').showModal();}
async function copy(text,button){try{await navigator.clipboard.writeText(text);if(button){const previous=button.innerHTML;button.innerHTML=button.hasAttribute('data-copy-code')?'복사됨':icon('check');setTimeout(()=>{if(button.isConnected)button.innerHTML=previous;},1500);}else toast('복사했어요.');}catch{toast('클립보드에 접근하지 못했어요. 텍스트를 선택해 복사해주세요.');}}

$('#composer').addEventListener('submit',submitMessage);
$('#message-input').addEventListener('input',()=>{resizeInput();storeDraft();});
$('#message-input').addEventListener('keydown',event=>{if(event.key==='Enter'&&!event.shiftKey&&!event.isComposing){event.preventDefault();if(!busy)submitMessage();}});
$('#new-chat').addEventListener('click',()=>newChat().catch(e=>toast(e.message)));
$('#history-search').addEventListener('input',renderHistory);
$('#history-list').addEventListener('click',event=>{const open=event.target.closest('[data-open-chat]'),del=event.target.closest('[data-delete-chat]');if(open)openChat(open.dataset.openChat).catch(e=>toast(e.message));if(del)askDelete(del.dataset.deleteChat);});
$('#choose-model').addEventListener('click',openModels);$('#composer-model').addEventListener('click',openModels);$('#model-search').addEventListener('input',renderModels);
$('#model-list').addEventListener('click',event=>{const button=event.target.closest('[data-select-model]');if(button&&!button.disabled)selectModel(button.dataset.selectModel).catch(e=>toast(e.message));});
$('#chat-settings').addEventListener('click',openSettings);
$('#settings-form').addEventListener('submit',async event=>{event.preventDefault();const button=event.submitter;button.disabled=true;try{const settings={maxTokens:Number($('#max-tokens').value),temperature:Number($('#temperature').value)},systemPrompt=$('#system-prompt').value;defaults={...settings,systemPrompt};safeLocal.set('chat-defaults',JSON.stringify(defaults));if(active){active.settings=settings;active.systemPrompt=systemPrompt;await saveChat();}$('#settings-dialog').close();toast('대화 설정을 저장했어요.');}catch(e){$('#settings-error').textContent=e.message;}finally{button.disabled=false;}});
$('#rename-chat').addEventListener('click',()=>{if(active&&!busy)openTextDialog('rename');});
$('#text-dialog-form').addEventListener('submit',async event=>{event.preventDefault();const value=$('#text-dialog-input').value.trim();if(!value)return;event.submitter.disabled=true;try{if(textAction.mode==='rename'){active.title=value;await saveChat();updateChrome();$('#text-dialog').close();}else{const index=active.messages.findIndex(m=>m.id===textAction.id);$('#text-dialog').close();await startGeneration({truncate:index,text:value});}}catch(e){$('#text-dialog-error').textContent=e.message;}finally{$('#text-dialog-submit').disabled=false;}});
$('#confirm-delete').addEventListener('click',async()=>{const button=$('#confirm-delete');button.disabled=true;try{if(active?.id===deleteId)await stopGeneration();await saveTail.catch(()=>{});await api('/api/chats/'+deleteId,{method:'DELETE'});history=history.filter(c=>c.id!==deleteId);safeLocal.remove('draft.'+deleteId);if(active?.id===deleteId){active=null;saveError=false;safeLocal.remove('active-chat');setLocation(null);saveStatus('');renderConversation();$('#message-input').value=getDraft();resizeInput();}renderHistory();$('#delete-dialog').close();toast('대화를 삭제했어요.');}catch(e){toast(e.message);}finally{button.disabled=false;}});
$('#messages').addEventListener('click',event=>{const code=event.target.closest('[data-copy-code]'),copier=event.target.closest('[data-copy-message]'),retry=event.target.closest('[data-regenerate]'),edit=event.target.closest('[data-edit-message]');if(code)copy(code.closest('.code-block').querySelector('code').textContent,code);if(copier){const m=active?.messages.find(m=>m.id===copier.dataset.copyMessage);if(m)copy(m.content,copier);}if(retry)regenerate();if(edit&&!busy)openTextDialog('edit',edit.dataset.editMessage);});
$('#export-chat').addEventListener('click',()=>{if(!active)return;const blob=new Blob([chatToMarkdown(active)],{type:'text/markdown;charset=utf-8'}),url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download=active.title.replace(/[<>:"/\\|?*\x00-\x1f]/g,'_').slice(0,70)+'.md';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);});
$('#suggestions').addEventListener('click',event=>{const button=event.target.closest('[data-prompt]');if(button){$('#message-input').value=button.dataset.prompt.replace(/\\n/g,'\n');resizeInput();storeDraft();$('#message-input').focus();}});
$('#save-state').addEventListener('click',()=>{if(saveError)saveChat().catch(e=>toast(e.message));});
$('#scroll-bottom').addEventListener('click',scrollEnd);
$('#chat-view').addEventListener('scroll',()=>{const view=$('#chat-view');autoFollow=view.scrollHeight-view.scrollTop-view.clientHeight<100;$('#scroll-bottom').hidden=autoFollow||$('#welcome').hidden===false;},{passive:true});
function toggleSidebar(){if(innerWidth<=760){document.body.classList.toggle('sidebar-open');return;}document.body.classList.toggle('sidebar-collapsed');safeLocal.set('sidebar-collapsed',String(document.body.classList.contains('sidebar-collapsed')));}
$('#toggle-sidebar').addEventListener('click',toggleSidebar);$('#close-sidebar').addEventListener('click',()=>innerWidth<=760?closeSidebar():toggleSidebar());$('#sidebar-scrim').addEventListener('click',closeSidebar);
for(const dialog of document.querySelectorAll('dialog'))dialog.addEventListener('click',event=>{const close=event.target.closest('[data-close]');if(close){dialog.close();return;}if(event.target===dialog){const r=dialog.getBoundingClientRect();if(event.clientX<r.left||event.clientX>r.right||event.clientY<r.top||event.clientY>r.bottom)dialog.close();}});
window.addEventListener('keydown',event=>{if(event.key==='Escape')closeSidebar();if((event.ctrlKey||event.metaKey)&&event.key.toLowerCase()==='k'){event.preventDefault();newChat().catch(e=>toast(e.message));}if(event.key==='/'&&!event.ctrlKey&&!event.metaKey&&!document.querySelector('dialog[open]')&&!event.target.matches('input,textarea')){event.preventDefault();if(innerWidth<=760)document.body.classList.add('sidebar-open');$('#history-search').focus();}});
window.addEventListener('hashchange',()=>{const id=location.hash.match(/^#chat\/([a-f0-9-]{36})$/)?.[1];if(id&&id!==active?.id)openChat(id).catch(e=>toast(e.message));});
window.addEventListener('pagehide',()=>{storeDraft();if(busy&&active){const m=active.messages.at(-1);if(m?.status==='streaming')m.status='stopped';fetch('/api/chats/'+active.id,{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify(snapshot(active)),keepalive:true}).catch(()=>{});generation?.controller.abort();}});
try{
  await refreshState();loadModelProfile();history=(await api('/api/chats')).data;renderHistory();
  const saved=location.hash.match(/^#chat\/([a-f0-9-]{36})$/)?.[1]||safeLocal.get('active-chat');
  if(saved&&history.some(c=>c.id===saved))await openChat(saved);
  else {selectedModel=safeLocal.get('last-model')||'free/auto';renderConversation();$('#message-input').value=getDraft();resizeInput();}
  if(pendingProfile){
    selectedModel=pendingProfile.model;
    if(active){active.model=selectedModel;active.settings={maxTokens:pendingProfile.maxTokens,temperature:pendingProfile.temperature};await saveChat();}
    safeLocal.set('model-profile',pendingProfile.version);pendingProfile=null;updateChrome();
  }
}catch(error){$('#chat-view').innerHTML='<div class="fatal"><h1>챗을 불러오지 못했어요.</h1><p>'+esc(error.message)+'</p><button class="button" id="reload-app">다시 연결</button></div>';$('#reload-app').addEventListener('click',()=>location.reload());}
setInterval(()=>{if(!document.hidden)refreshState().catch(()=>{});},30000);
