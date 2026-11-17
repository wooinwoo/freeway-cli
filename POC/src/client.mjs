import { spawn } from 'node:child_process';
import { mkdirSync, openSync, closeSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';
import { sseEvents } from './streams.mjs';

export const appRoot=dirname(dirname(fileURLToPath(import.meta.url)));
export class Client {
  constructor({base=process.env.FREEWAY_URL||'http://127.0.0.1:4141',autoStart=true,fetcher=fetch}={}){
    const url=new URL(base);
    if(url.protocol!=='http:'||!['127.0.0.1','localhost','[::1]'].includes(url.hostname)||url.pathname!=='/'||url.username||url.password||url.search||url.hash)throw new Error('FREEWAY_URL은 로컬 http://127.0.0.1:포트 형식이어야 합니다.');
    this.base=url.origin;this.autoStart=autoStart;this.fetcher=fetcher;this.cookie=null;this.connected=false;
  }
  async health(){try{const r=await this.fetcher(this.base+'/health',{signal:AbortSignal.timeout(1500)});if(!r.ok)return false;const d=await r.json();return d.name==='Freeway'&&d.status==='ok';}catch{return false;}}
  async connect(){
    if(this.connected)return;
    if(this.connecting)return this.connecting;
    this.connecting=(async()=>{
      if(!await this.health()){
        if(!this.autoStart)throw new Error('Freeway 서버가 꺼져 있습니다. freeway serve로 실행하세요.');
        const dir=process.env.FREEWAY_DATA_DIR||join(appRoot,'.data');mkdirSync(dir,{recursive:true});
        const fd=openSync(join(dir,'server.log'),'a');
        const child=spawn(process.execPath,[join(appRoot,'server.mjs')],{cwd:appRoot,detached:true,windowsHide:true,stdio:['ignore',fd,fd],env:{...process.env,PORT:String(new URL(this.base).port||80)}});
        let launchError;child.once('error',error=>{launchError=error;});child.unref();closeSync(fd);
        if(child.pid)writeFileSync(join(dir,'server-'+(new URL(this.base).port||80)+'.pid'),String(child.pid));
        let healthy=false;for(let i=0;i<100;i++){if(launchError)throw new Error('Freeway 서버 실행 실패: '+launchError.message);if(await this.health()){healthy=true;break;}await delay(200);}
        if(!healthy)throw new Error('Freeway 서버를 시작하지 못했습니다. freeway serve로 오류를 확인하세요.');
      }
      const root=await this.fetcher(this.base+'/',{signal:AbortSignal.timeout(5000)});
      this.cookie=root.headers.get('set-cookie')?.split(';')[0];await root.text();
      if(!this.cookie)throw new Error('Freeway 로컬 인증에 실패했습니다.');
      this.connected=true;
    })().finally(()=>{this.connecting=null;});return this.connecting;
  }
  async api(path,{method='GET',body,signal}={},retry=true){
    await this.connect();
    const response=await this.fetcher(this.base+path,{method,signal,headers:{cookie:this.cookie,origin:this.base,'content-type':'application/json'},...(body!==undefined?{body:JSON.stringify(body)}:{})});
    if(response.status===401&&retry){await response.body?.cancel();this.connected=false;await this.connect();return this.api(path,{method,body,signal},false);}
    const result=await response.json();if(!response.ok){const error=new Error(result.error?.message||'Freeway 요청 실패');error.status=response.status;error.code=result.error?.code;throw error;}return result;
  }
  async state(){
    const state=await this.api('/api/state');
    if(!state.models.some(m=>m.ready)&&state.providers.some(p=>p.id==='kilo'&&p.enabled)){
      await this.api('/api/providers/kilo/sync',{method:'POST',body:{}}).catch(()=>{});return this.api('/api/state');
    }
    return state;
  }
  async complete(body,{signal,onText,onChunk}={}){
    await this.connect();const start=Date.now();
    const response=await this.fetcher(this.base+'/v1/chat/completions',{method:'POST',signal,headers:{cookie:this.cookie,'content-type':'application/json'},body:JSON.stringify({...body,stream:true})});
    if(!response.ok){const result=await response.json();const error=new Error(result.error?.message||'모델 요청 실패');error.status=response.status;error.code=result.error?.code;error.retryAfter=result.error?.retryAfter;throw error;}
    let content='',reasoning='',model=body.model,modelGroup,provider,usage,done=false;
    for await(const raw of sseEvents(response.body)){
      if(raw==='[DONE]'){done=true;break;}
      const chunk=JSON.parse(raw);if(chunk.error){const error=new Error(chunk.error.message);error.code=chunk.error.code;error.status=chunk.error.status;error.retryAfter=chunk.error.retryAfter;throw error;}
      model=chunk.model||model;modelGroup=chunk.model_group||modelGroup;provider=chunk.provider||provider;if(chunk.usage)usage=chunk.usage;
      for(const choice of chunk.choices||[]){const delta=choice.delta||{};if(delta.content){content+=delta.content;onText?.(delta.content);}reasoning+=delta.reasoning_content||delta.reasoning||'';}
      onChunk?.(chunk);
    }
    if(!done)throw new Error('응답이 완료되기 전에 연결이 끊겼습니다.');
    if(!content.trim())throw new Error(reasoning?'생각 과정만 반환되었습니다. 출력 토큰을 늘리거나 모델을 변경하세요.':'모델이 텍스트 답변을 반환하지 않았습니다.');
    return {ok:true,model,...(modelGroup?{modelGroup}:{}),provider,content,...(usage?{usage}:{}),elapsedMs:Date.now()-start};
  }
}
