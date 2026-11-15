#!/usr/bin/env node
import { createInterface } from 'node:readline/promises';
import { readFileSync } from 'node:fs';
import { spawn, spawnSync } from 'node:child_process';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { Client, appRoot } from './src/client.mjs';
import { runMcp } from './src/mcp.mjs';
import { findOpenCode } from './src/opencode.mjs';
import { runTuiSession } from './src/tui.mjs';
import { modelItems } from './src/model-menu.mjs';
import { cellWidth, clip } from './src/terminal-ui.mjs';
import { modelReference, rankedGroups, stateModelGroups } from './src/model-groups.mjs';
import { defaultProfile } from './src/profile.mjs';

const version=JSON.parse(readFileSync(join(appRoot,'package.json'),'utf8')).version;
export function parseArgs(args){
  const options={prompt:[],command:null};
  const commands=new Set(['chat','ask','run','agent','models','priority','history','status','serve','mcp','mcp-config','setup','opencode','connect']);
  const values={'--model':'model','-m':'model','--system':'system','--max-tokens':'maxTokens','--temperature':'temperature','--url':'base','--session':'session','--timeout':'timeout'};
  for(let i=0;i<args.length;i++){
    const a=args[i];if(a==='--'){options.prompt.push(...args.slice(i+1));break;}
    if(values[a]){if(args[i+1]===undefined)throw new Error(a+' 뒤에 값을 입력하세요.');options[values[a]]=args[++i];continue;}
    if(a==='--json')options.json=true;else if(a==='--stream-json')options.streamJson=true;else if(a==='--save')options.save=true;else if(a==='--no-save')options.save=false;
    else if(a==='--plain')options.plain=true;else if(a==='--no-start')options.autoStart=false;else if(a==='--help'||a==='-h')options.help=true;else if(a==='--version'||a==='-v')options.version=true;
    else if(a.startsWith('-'))throw new Error('알 수 없는 옵션: '+a);
    else if(!options.command&&!options.prompt.length&&commands.has(a))options.command=a;else options.prompt.push(a);
  }
  if(options.maxTokens!==undefined&&(!Number.isInteger(Number(options.maxTokens))||Number(options.maxTokens)<1||Number(options.maxTokens)>32768))throw new Error('--max-tokens는 1~32768입니다.');
  if(options.temperature!==undefined&&(!Number.isFinite(Number(options.temperature))||Number(options.temperature)<0||Number(options.temperature)>2))throw new Error('--temperature는 0~2입니다.');
  if(options.timeout!==undefined&&(!Number.isFinite(Number(options.timeout))||Number(options.timeout)<1||Number(options.timeout)>300))throw new Error('--timeout은 1~300초입니다.');
  return options;
}
function help(){process.stdout.write(`Freeway ${version} — 무료 AI CLI\n\n  freeway                         대화 시작\n  freeway "질문"                  한 번 질문\n  freeway agent --json "작업"      서브에이전트용 JSON 출력\n  Get-Content task.txt | freeway agent --json\n  freeway models                  무료 모델 목록\n  freeway history                 저장한 대화 목록\n  freeway --session ID            이전 대화 이어가기\n  freeway mcp                     MCP stdio 서버\n  freeway mcp-config              AI 도구 연결 설정 출력\n  freeway status                  연결 상태\n  freeway serve                   서버를 화면에서 직접 실행\n\n옵션: --model ID, --max-tokens N, --temperature N, --system "지침"\n      --json, --stream-json, --save, --no-save, --timeout 초, --no-start\n대화 명령: /model ID, /models, /history, /open ID, /new, /exit\n`);}
async function readInput(){let text='';process.stdin.setEncoding('utf8');for await(const chunk of process.stdin){text+=chunk;if(text.length>150000)throw new Error('입력은 150,000자 이하로 전달하세요.');}return text.trim();}
function config(){const command=[process.execPath,join(appRoot,'cli.mjs'),'mcp'];return {opencode:{mcp:{servers:{freeway:{type:'local',command,timeout:{startup:30000,execution:180000}}}}},opencodeV1:{mcp:{freeway:{type:'local',command,enabled:true}}},standard:{mcpServers:{freeway:{command:process.execPath,args:[join(appRoot,'cli.mjs'),'mcp']}}}};}
async function listModels(client,json=false){
  const s=await client.state();if(json){console.log(JSON.stringify({models:rankedGroups(stateModelGroups(s),s.settings.preferredModels)}));return;}
  const items=modelItems(s),columns=process.stdout.columns||100,digits=Math.max(2,String(items.length).length);
  const width=Math.max(12,Math.min(38,Math.floor(columns*.44)-5,Math.max(18,...items.map(i=>cellWidth(i.label))))),detailWidth=Math.max(1,columns-width-digits-5);
  for(const [index,m]of items.entries()){const label=clip(m.label,width);process.stdout.write(String(index+1).padStart(digits)+'. '+label+' '.repeat(width-cellWidth(label)+2)+clip(m.detail,detailWidth)+'\n');}
}
async function listHistory(client){const h=await client.api('/api/chats');for(const c of h.data)process.stdout.write(c.id+'  '+c.title+'\n');if(!h.data.length)process.stdout.write('저장한 대화가 없습니다.\n');}
export async function main(args=process.argv.slice(2)){
  const o=parseArgs(args);if(o.version){console.log(version);return;}if(o.help){help();return;}
  if(o.command==='mcp-config'){console.log(JSON.stringify(config(),null,2));return;}
  if(o.command==='setup'||o.command==='opencode'){
    if(o.command==='setup'&&o.prompt.join(' ')!=='opencode')throw new Error('사용법: freeway setup opencode');
    const binary=findOpenCode();if(!binary)throw new Error('OpenCode 실행 파일을 찾지 못했습니다. OPENCODE_BIN을 설정하세요.');
    if(o.command==='setup'){console.log('준비 완료. freeway opencode로 실행하면 freeway_delegate 서브에이전트 도구가 연결됩니다.');return;}
    const detected=spawnSync(binary,['--version'],{encoding:'utf8',windowsHide:true}).stdout?.trim()||'1';
    const major=Number(detected.match(/\d+/)?.[0]||1),entry=config();
    let inherited={};if(process.env.OPENCODE_CONFIG_CONTENT){try{inherited=JSON.parse(process.env.OPENCODE_CONFIG_CONTENT);}catch{throw new Error('기존 OPENCODE_CONFIG_CONTENT가 올바른 JSON이 아닙니다.');}}
    const mcp={...inherited.mcp};if(major>=2)mcp.servers={...mcp.servers,...entry.opencode.mcp.servers};else Object.assign(mcp,entry.opencodeV1.mcp);
    const child=spawn(binary,o.prompt,{stdio:'inherit',windowsHide:true,env:{...process.env,OPENCODE_CONFIG_CONTENT:JSON.stringify({...inherited,mcp})}});
    await new Promise((resolve,reject)=>{child.on('error',reject);child.on('exit',code=>{process.exitCode=code||0;resolve();});});return;
  }
  if(o.command==='mcp'){await runMcp(new Client(o),version);return;}
  if(o.command==='serve'){
    const child=spawn(process.execPath,[join(appRoot,'server.mjs')],{cwd:appRoot,stdio:'inherit',windowsHide:true});
    await new Promise((resolve,reject)=>{child.on('error',reject);child.on('exit',code=>{process.exitCode=code||0;resolve();});});return;
  }
  const client=new Client(o);
  if(o.command==='connect'){if(o.prompt.join(' ')!=='opencode')throw new Error('사용법: freeway connect opencode');const result=await client.api('/api/opencode/start',{method:'POST',body:{}});console.log('OpenCode: '+result.count+'개 무료 모델 연결됨');return;}
  if(o.command==='models'){await listModels(client,o.json);return;}
  if(o.command==='history'){await listHistory(client);return;}
  const state=await client.state();const profile=state.settings.chatDefaults||{model:'free/auto',maxTokens:4096,temperature:0.6};
  if(o.command==='priority'){
    const groups=stateModelGroups(state), routes=groups.flatMap(g=>g.routes);
    let preferred=state.settings.preferredModels||[];
    if(o.prompt.length){
      preferred=o.prompt.length===1&&o.prompt[0]==='reset'?defaultProfile.preferredModels.filter(id=>groups.some(g=>g.free&&g.id===modelReference(id,routes))):o.prompt;
      preferred=[...new Set(preferred.map(id=>modelReference(id,routes)))];
      await client.api('/api/settings',{method:'PUT',body:{preferredModels:preferred}});
    }
    const order=rankedGroups(groups,preferred).filter(m=>m.free).map((m,index)=>({rank:index+1,id:m.id,name:m.name,ready:m.ready}));
    if(o.json)console.log(JSON.stringify({appliesTo:'free/auto',models:order}));
    else {for(const m of order)console.log(m.rank+'. '+m.name+'  '+m.id);console.log('\nAuto 적용: freeway --model free/auto\n순서 설정: freeway priority MODEL_ID MODEL_ID ...');}return;
  }
  let model=o.model||profile.model;let system=o.system||'';let maxTokens=Number(o.maxTokens||profile.maxTokens),temperature=Number(o.temperature??profile.temperature);
  if(o.command==='status'){console.log(JSON.stringify({server:client.base,model,maxTokens,readyModels:stateModelGroups(state).filter(m=>m.free&&m.ready).length,readyRoutes:state.models.filter(m=>m.ready).length},null,2));return;}
  let chat=o.session?await client.api('/api/chats/'+o.session):null;
  if(chat){model=o.model||chat.model;system=o.system??chat.systemPrompt;maxTokens=Number(o.maxTokens||chat.settings.maxTokens);temperature=Number(o.temperature??chat.settings.temperature);}
  if(!o.model)model=modelReference(model,state.catalog||state.models);
  let history=chat?.messages.filter(m=>m.role==='user'||m.status==='done').map(m=>({role:m.role,content:m.content}))||[];
  const machine=o.json||o.command==='agent';let controller;
  const interactive=process.stdin.isTTY&&!o.prompt.length&&!['agent','ask','run'].includes(o.command)&&!machine&&!o.streamJson;
  const save=o.save??(interactive||Boolean(o.session));
  if(interactive&&!o.plain)return runTuiSession({client,state,model,system,maxTokens,temperature,chat,save,version,timeout:Number(o.timeout||150)});
  async function persist(messages){
    if(!save)return;
    if(!chat)chat=await client.api('/api/chats',{method:'POST',body:{title:messages.find(m=>m.role==='user')?.content.replace(/\s+/g,' ').slice(0,45)||'CLI 대화',model,systemPrompt:system,settings:{maxTokens,temperature}}});
    chat=await client.api('/api/chats/'+chat.id,{method:'PUT',body:{revision:chat.revision,model,systemPrompt:system,settings:{maxTokens,temperature},messages}});
  }
  async function ask(prompt){
    if(!prompt.trim())return;
    if(history.length>194)throw new Error('대화가 길어졌습니다. /new로 새 대화를 시작하세요.');
    const messages=[...history,{role:'user',content:prompt}];
    const stored=chat?.messages.map(m=>({...m}))||history.map(m=>({id:crypto.randomUUID(),...m,status:'done'}));
    const user={id:crypto.randomUUID(),role:'user',content:prompt,status:'done'};stored.push(user);
    const assistant={id:crypto.randomUUID(),role:'assistant',content:'',status:'streaming'};
    await persist([...stored,assistant]);
    controller=new AbortController();
    try{
      const result=await client.complete({model,messages:system?[{role:'system',content:system},...messages]:messages,max_tokens:maxTokens,temperature},{signal:AbortSignal.any([controller.signal,AbortSignal.timeout(Number(o.timeout||150)*1000)]),onText:text=>{assistant.content+=text;if(!machine&&!o.streamJson)process.stdout.write(text);},onChunk:chunk=>{if(o.streamJson)process.stdout.write(JSON.stringify({type:'chunk',...chunk})+'\n');}});
      assistant.content=result.content;assistant.model=result.model;assistant.status='done';history=[...messages,{role:'assistant',content:result.content}];await persist([...stored,assistant]);
      if(machine||o.streamJson)console.log(JSON.stringify({...result,...(chat?{session:chat.id}:{}),...(o.streamJson?{type:'result'}:{})}));
      else{process.stdout.write('\n');if(interactive)process.stderr.write('['+result.model+']\n');}
    }catch(error){
      assistant.status=controller.signal.aborted?'stopped':'error';assistant.error=controller.signal.aborted?'응답이 중지되었습니다.':error.message;
      if(save)await persist([...stored,assistant]).catch(()=>{});
      throw error;
    }finally{controller=null;}
  }
  if(!interactive){
    const prompt=o.prompt.join(' ')||(!process.stdin.isTTY?await readInput():'');if(!prompt){if(machine)throw new Error('질문을 인자나 표준 입력으로 전달하세요.');help();return;}
    const interrupt=()=>controller?.abort();process.on('SIGINT',interrupt);
    try{await ask(prompt);}finally{process.off('SIGINT',interrupt);}return;
  }
  process.stderr.write('Freeway '+version+' · '+model+'\n/exit 종료 · /new 새 대화 · /models 모델 목록\n');
  const rl=createInterface({input:process.stdin,output:process.stdout});let closed=false;
  rl.on('close',()=>{closed=true;});rl.on('SIGINT',()=>{if(controller)controller.abort();else rl.close();});
  try{while(!closed){
    let input;try{input=(await rl.question('\nyou> ')).trim();}catch{break;}if(!input)continue;
    if(['/exit','/quit'].includes(input))break;
    try{
      if(input==='/models'){await listModels(client);continue;}
      if(input==='/history'){await listHistory(client);continue;}
      if(input==='/new'){chat=null;history=[];process.stderr.write('새 대화를 시작합니다.\n');continue;}
      if(input==='/model'){console.log(model);continue;}
      if(input.startsWith('/model ')){const id=input.slice(7).trim();const state=await client.state();if(id!=='free/auto'&&![...state.models,...stateModelGroups(state)].some(m=>m.id===id&&m.ready&&m.free))throw new Error('연결된 무료 모델 ID를 선택하세요.');model=id;console.log('모델: '+model);continue;}
      if(input.startsWith('/open ')){chat=await client.api('/api/chats/'+input.slice(6).trim());history=chat.messages.filter(m=>m.role==='user'||m.status==='done').map(m=>({role:m.role,content:m.content}));model=chat.model;system=chat.systemPrompt;maxTokens=chat.settings.maxTokens;temperature=chat.settings.temperature;console.log('대화: '+chat.title);continue;}
      if(input==='/help'){help();continue;}
      await ask(input);
    }catch(error){process.stderr.write('\n'+error.message+'\n');}
  }}finally{rl.close();}
}

if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url))main().catch(error=>{
  const json=process.argv.includes('--json')||process.argv.includes('--stream-json')||process.argv[2]==='agent';
  if(json)console.log(JSON.stringify({ok:false,error:{message:error.message,code:error.code||'cli_error'}}));else process.stderr.write('Freeway: '+error.message+'\n');
  process.exitCode=error.name==='AbortError'?130:1;
});
