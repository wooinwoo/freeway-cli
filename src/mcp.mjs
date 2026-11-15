import { createInterface } from 'node:readline';
import { rankedGroups, stateModelGroups } from './model-groups.mjs';

const delegate={name:'freeway_delegate',description:'Delegate a self-contained reasoning, coding review, writing or summarization task to a free hosted AI subagent. Returns advice/text; does not execute commands or edit files. Pass all relevant context in the task. Requests may be rate limited.',inputSchema:{type:'object',properties:{task:{type:'string',minLength:1,maxLength:150000},system:{type:'string',maxLength:20000},model:{type:'string'},max_tokens:{type:'integer',minimum:1,maximum:32768}},required:['task'],additionalProperties:false},annotations:{title:'Freeway AI subagent',readOnlyHint:true,destructiveHint:false,idempotentHint:false,openWorldHint:true}};
const models={name:'freeway_models',description:'List connected free AI models and the configured default model.',inputSchema:{type:'object',properties:{},additionalProperties:false},annotations:{readOnlyHint:true,destructiveHint:false}};

export async function runMcp(client,version,{input=process.stdin,output=process.stdout}={}){
  const rl=createInterface({input,crlfDelay:Infinity});const running=new Map();let initialized=false;
  const send=value=>output.write(JSON.stringify(value)+'\n');
  const error=(id,code,message)=>send({jsonrpc:'2.0',id,error:{code,message}});
  async function handle(request){
    if(!request||request.jsonrpc!=='2.0'||typeof request.method!=='string'){error(request?.id??null,-32600,'Invalid request');return;}
    if(request.method==='notifications/cancelled'){running.get(request.params?.requestId)?.abort();return;}
    if(request.id===undefined)return;
    const id=request.id;
    if(running.has(id)){error(id,-32600,'Duplicate request ID');return;}
    if(request.method==='initialize'){
      initialized=true;const supported=['2025-11-25','2025-06-18','2025-03-26','2024-11-05'];
      send({jsonrpc:'2.0',id,result:{protocolVersion:supported.includes(request.params?.protocolVersion)?request.params.protocolVersion:'2025-06-18',capabilities:{tools:{listChanged:false}},serverInfo:{name:'freeway',version},instructions:'Use freeway_delegate for self-contained reasoning or review tasks. This server provides text advice, not filesystem or shell access.'}});return;
    }
    if(request.method==='ping'){send({jsonrpc:'2.0',id,result:{}});return;}
    if(!initialized){error(id,-32002,'Initialize the server first');return;}
    if(request.method==='tools/list'){send({jsonrpc:'2.0',id,result:{tools:[delegate,models]}});return;}
    if(request.method!=='tools/call'){error(id,-32601,'Method not found');return;}
    if(running.size>=3){error(id,-32000,'Too many concurrent tasks; retry after a task finishes');return;}
    const controller=new AbortController();running.set(id,controller);
    try{
      const name=request.params?.name,args=request.params?.arguments||{};let result;
      if(name==='freeway_models'){
        const state=await client.state();result={defaultModel:state.settings.chatDefaults?.model||'free/auto',models:rankedGroups(stateModelGroups(state),state.settings.preferredModels).filter(m=>m.free&&m.ready).map(m=>({id:m.id,name:m.name,providers:m.providers}))};
      }else if(name==='freeway_delegate'){
        if(typeof args.task!=='string'||!args.task.trim()||args.task.length>150000||args.system!==undefined&&(typeof args.system!=='string'||args.system.length>20000)||args.model!==undefined&&typeof args.model!=='string'||args.max_tokens!==undefined&&(!Number.isInteger(args.max_tokens)||args.max_tokens<1||args.max_tokens>32768))throw new Error('Invalid task arguments');
        const state=await client.state(),profile=state.settings.chatDefaults||{model:'free/auto',maxTokens:4096,temperature:0.6};
        const messages=[...(args.system?[{role:'system',content:args.system}]:[]),{role:'user',content:args.task}];
        result=await client.complete({model:args.model||profile.model,messages,max_tokens:args.max_tokens||profile.maxTokens,temperature:profile.temperature},{signal:AbortSignal.any([controller.signal,AbortSignal.timeout(150000)])});
      }else{error(id,-32602,'Unknown tool');return;}
      send({jsonrpc:'2.0',id,result:{content:[{type:'text',text:name==='freeway_delegate'?result.content:JSON.stringify(result)}],structuredContent:result,isError:false}});
    }catch(e){send({jsonrpc:'2.0',id,result:{content:[{type:'text',text:e.message}],isError:true}});}
    finally{running.delete(id);}
  }
  rl.on('line',line=>{if(!line.trim())return;if(line.length>200000){error(null,-32600,'Message too large');return;}let request;try{request=JSON.parse(line);}catch{error(null,-32700,'Parse error');return;}handle(request).catch(()=>error(request.id??null,-32603,'Internal error'));});
  await new Promise(resolve=>rl.on('close',resolve));for(const controller of running.values())controller.abort();
}
