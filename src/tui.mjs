import { TerminalUI, commands } from './terminal-ui.mjs';
import { writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { spawn } from 'node:child_process';
import { chatToMarkdown } from '../public/chat-render.mjs';

export async function runTuiSession({client,state,model,system,maxTokens,temperature,chat:initialChat,save=true,version,timeout=150}){
  let chat=initialChat,controller,requestTask,exiting=false;
  const ui=new TerminalUI({version,onAction:action});
  ui.messages=chat?.messages.map(m=>({...m,status:m.status==='streaming'?'stopped':m.status}))||[];
  ui.title=chat?.title||'새 대화';ui.model=model;let stateCache=state;
  let finish;const finished=new Promise(resolve=>{finish=resolve;});
  const modelLabel=id=>id==='free/auto'?'품질 우선 Auto':stateCache.models.find(m=>m.id===id)?.name||id.replace(/^kilo\//,'');
  function chrome(){ui.model=model;ui.modelName=modelLabel(model);ui.modelNames=new Map(stateCache.models.map(m=>[m.id,m.name]));ui.saveLabel=save?(chat?'로컬 저장됨 · '+chat.id.slice(0,8):'대화 자동 저장'):'이 세션 저장 안 함';ui.render();}
  async function persist(){
    if(!save)return;
    if(!chat)chat=await client.api('/api/chats',{method:'POST',body:{title:ui.messages.find(m=>m.role==='user')?.content.replace(/\s+/g,' ').slice(0,45)||'새 대화',model,systemPrompt:system,settings:{maxTokens,temperature}}});
    chat=await client.api('/api/chats/'+chat.id,{method:'PUT',body:{revision:chat.revision,title:chat.title,model,systemPrompt:system,settings:{maxTokens,temperature},messages:ui.messages.map(m=>({...m}))}});
    ui.title=chat.title;chrome();
  }
  async function generate(text,{retry=false}={}){
    if(ui.busy)return;ui.busy=true;ui.startedAt=Date.now();ui.scrollOffset=0;
    if(retry){let last=ui.messages.length-1;while(last>=0&&ui.messages[last].role!=='user')last--;if(last<0){ui.busy=false;ui.notify('다시 생성할 질문이 없어요.');return;}ui.messages=ui.messages.slice(0,last+1);}
    else if(ui.messages.length>194){ui.busy=false;ui.notify('대화가 길어졌어요. /new로 새 대화를 시작하세요.',true);return;}
    else ui.messages.push({id:crypto.randomUUID(),role:'user',content:text,status:'done'});
    const messages=ui.messages.filter(m=>m.role==='user'||m.status==='done'&&m.content).map(m=>({role:m.role,content:m.content}));
    const assistant={id:crypto.randomUUID(),role:'assistant',content:'',reasoning:'',status:'streaming'};ui.messages.push(assistant);controller=new AbortController();ui.render();
    requestTask=(async()=>{
      try{
        await persist();
        const result=await client.complete({model,messages:system?[{role:'system',content:system},...messages]:messages,max_tokens:maxTokens,temperature},{signal:AbortSignal.any([controller.signal,AbortSignal.timeout(timeout*1000)]),onText:text=>{assistant.content+=text;ui.schedule();},onChunk:chunk=>{if(chunk.model)assistant.model=chunk.model;for(const c of chunk.choices||[])assistant.reasoning+=c.delta?.reasoning_content||c.delta?.reasoning||'';ui.schedule();}});
        assistant.content=result.content;assistant.model=result.model;assistant.status='done';ui.usage=result.usage;ui.notify('답변 완료 · '+(result.elapsedMs/1000).toFixed(1)+'초');
      }catch(e){assistant.status=controller.signal.aborted?'stopped':'error';if(!controller.signal.aborted)assistant.error=e.message;ui.notify(controller.signal.aborted?'응답을 중지했어요.':e.message,!controller.signal.aborted);}
      finally{
        try{await persist();}catch(e){ui.notify('저장 실패: '+e.message,true);ui.saveLabel='저장 실패 · /save 재시도';}
        ui.busy=false;controller=null;requestTask=null;chrome();if(exiting)finish();
      }
    })();
    return requestTask;
  }
  async function modelPicker(){
    stateCache=await client.state();const rank=new Map((stateCache.settings.preferredModels||[]).map((id,index)=>[id,index]));
    const models=stateCache.models.filter(m=>m.ready).sort((a,b)=>(rank.get(a.id)??999)-(rank.get(b.id)??999)||a.name.localeCompare(b.name));
    ui.openModal('모델 선택 · 성능 우선',[{label:'품질 우선 Auto'+(model==='free/auto'?'  ✓':''),detail:'연결된 무료 모델에서 우선순위에 따라 자동 선택',value:'free/auto'},...models.map(m=>({label:(rank.has(m.id)?'추천 '+(rank.get(m.id)+1)+' · ':'')+m.name+(model===m.id?'  ✓':''),detail:m.id,value:m.id}))],{placeholder:'모델 또는 공급처 검색',onSelect:async item=>{model=item.value;chrome();if(chat)await persist();ui.notify(modelLabel(model)+' 선택됨');}});
  }
  async function historyPicker(){
    const records=(await client.api('/api/chats')).data;
    if(!records.length){ui.notify('아직 저장한 대화가 없어요.');return;}
    ui.openModal('이전 대화',records.map(c=>({label:c.title,detail:new Date(c.updatedAt).toLocaleString('ko-KR')+' · '+c.messageCount+'개 메시지',value:c.id})),{placeholder:'대화 제목 검색',onSelect:async item=>{
      chat=await client.api('/api/chats/'+item.value);ui.messages=chat.messages.map(m=>({...m,status:m.status==='streaming'?'stopped':m.status}));ui.title=chat.title;model=chat.model;system=chat.systemPrompt;maxTokens=chat.settings.maxTokens;temperature=chat.settings.temperature;ui.scrollOffset=0;chrome();
    }});
  }
  function settingsPicker(){
    ui.openModal('대화 설정',[
      {label:'출력 길이 · 4,096 토큰',detail:'일반 대화와 짧은 코드 답변',value:4096},
      {label:'출력 길이 · 8,192 토큰',detail:'추론과 긴 코드 답변에 여유 있게',value:8192},
      {label:'출력 길이 · 16,384 토큰',detail:'긴 답변 · 무료 한도 사용량이 늘어날 수 있어요',value:16384},
      {label:'현재 설정 보기',detail:'Temperature '+temperature+' · '+maxTokens+' tokens · '+(system?'시스템 지침 사용':'기본 지침'),value:'current'}
    ],{placeholder:'설정 검색',onSelect:async item=>{if(item.value==='current'){ui.notify('Temperature '+temperature+' · '+maxTokens+' tokens');return;}maxTokens=item.value;if(chat)await persist();ui.notify('출력 한도 '+maxTokens.toLocaleString()+' 토큰');}});
  }
  async function action(name,value){
    if(name==='stop'){controller?.abort();return;}
    if(name==='exit'){exiting=true;if(ui.busy){controller?.abort();await requestTask;}finish();return;}
    if(ui.busy)return;
    if(name==='send'){await generate(value);return;}
    if(name==='retry'){await generate('',{retry:true});return;}
    if(name==='new'){chat=null;ui.messages=[];ui.title='새 대화';ui.scrollOffset=0;ui.setInput('');chrome();return;}
    if(name==='models'){await modelPicker();return;}
    if(name==='history'){await historyPicker();return;}
    if(name==='settings'){settingsPicker();return;}
    if(name==='model'){if(!value){await modelPicker();return;}stateCache=await client.state();if(value!=='free/auto'&&!stateCache.models.some(m=>m.id===value&&m.ready))throw new Error('연결된 무료 모델 ID를 선택하세요.');model=value;chrome();if(chat)await persist();return;}
    if(name==='open'){const records=(await client.api('/api/chats')).data;const found=records.find(c=>c.id===value);if(!found)throw new Error('대화 ID를 찾지 못했습니다. /history에서 선택하세요.');chat=await client.api('/api/chats/'+value);ui.messages=chat.messages;ui.title=chat.title;model=chat.model;system=chat.systemPrompt;maxTokens=chat.settings.maxTokens;temperature=chat.settings.temperature;chrome();return;}
    if(name==='save'){await persist();ui.notify('대화를 저장했어요.');return;}
    if(name==='copy'){
      const content=[...ui.messages].reverse().find(m=>m.role==='assistant'&&m.content)?.content;if(!content)throw new Error('복사할 답변이 없어요.');
      if(process.platform!=='win32')throw new Error('/export로 답변을 저장하세요.');
      await new Promise((resolve,reject)=>{const child=spawn('clip.exe',[],{windowsHide:true,stdio:['pipe','ignore','pipe']});child.on('error',reject);child.on('close',code=>code?reject(new Error('클립보드 복사 실패')):resolve());child.stdin.end(Buffer.from(content,'utf16le'));});ui.notify('마지막 답변을 복사했어요.');return;
    }
    if(name==='export'){
      if(!ui.messages.length)throw new Error('내보낼 대화가 없어요.');const file=resolve(value||'freeway-'+new Date().toISOString().replace(/[:.]/g,'-')+'.md');
      writeFileSync(file,chatToMarkdown({title:chat?.title||'Freeway 대화',model,messages:ui.messages}),{flag:'wx'});ui.notify('저장됨: '+file);return;
    }
    if(name==='help'){
      ui.openModal('도움말',[
        {label:'Enter · 전송 / Ctrl+O · 줄바꿈',detail:'여러 줄 입력과 붙여넣기를 지원해요. Ctrl+J도 지원합니다.'},
        {label:'Ctrl+P · 모델 / Ctrl+L · 이전 대화',detail:'방향키로 선택하고 Enter로 확인해요.'},
        {label:'PgUp / PgDn · 대화 스크롤',detail:'긴 답변의 앞부분을 다시 읽을 수 있어요.'},
        {label:'Esc · 생성 중지 / 메뉴 닫기',detail:'Ctrl+R로 마지막 답변을 다시 생성해요.'},
        ...commands.map(([label,detail])=>({label,detail}))
      ],{placeholder:'도움말 검색',onSelect:item=>{if(item.label.startsWith('/'))return action(item.label.slice(1));}});return;
    }
    throw new Error('알 수 없는 명령어예요. /help로 확인하세요.');
  }
  const terminate=()=>action('exit');process.on('SIGTERM',terminate);
  try{ui.start();chrome();await finished;}
  finally{process.off('SIGTERM',terminate);ui.close();}
}
