import { emitKeypressEvents } from 'node:readline';

export const ansi = {reset:'\x1b[0m',accent:'\x1b[38;2;153;210;167m',text:'\x1b[38;2;222;232;224m',muted:'\x1b[38;2;116;140;123m',border:'\x1b[38;2;61;83;68m',code:'\x1b[38;2;167;203;215m',warning:'\x1b[38;2;223;178;120m',bold:'\x1b[1m',selected:'\x1b[48;2;38;62;44m'};
const color=(name,text)=>ansi[name]+text+ansi.reset;
const segmenter=new Intl.Segmenter('ko',{granularity:'grapheme'});
export const graphemes=text=>[...segmenter.segment(text)].map(s=>({text:s.segment,index:s.index}));
export function cleanTerminal(value){return String(value??'').replace(/\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g,'').replace(/\x1b\[[0-?]*[ -/]*[@-~]/g,'').replace(/\x1b./g,'').replace(/[\x00-\x08\x0b-\x1f\x7f\u202a-\u202e\u2066-\u2069]/g,'').replace(/\t/g,'  ');}
export function cellWidth(text){let width=0;for(const {text:s} of graphemes(cleanTerminal(text))){const p=s.codePointAt(0);if(s==='\n'||/^\p{Mark}+$/u.test(s))continue;width+=p>=0x1100&&(p<=0x115f||p===0x2329||p===0x232a||p>=0x2e80&&p<=0xa4cf||p>=0xac00&&p<=0xd7a3||p>=0xf900&&p<=0xfaff||p>=0xfe10&&p<=0xfe6f||p>=0xff01&&p<=0xff60||p>=0xffe0&&p<=0xffe6||p>=0x1f000)?2:1;}return width;}
export function clip(text,width){let out='',used=0;for(const {text:s} of graphemes(cleanTerminal(text))){const w=cellWidth(s);if(used+w>width)break;out+=s;used+=w;}return out;}
export function clipStyled(text,width){let out='',used=0,full=false;for(const token of text.match(/\x1b\[[0-?]*[ -/]*[@-~]|[^\x1b]+/g)||[]){if(token.startsWith('\x1b')){out+=token;continue;}for(const {text:s} of graphemes(token)){const w=cellWidth(s);if(used+w>width){full=true;break;}used+=w;out+=s;}if(full)break;}return out+ansi.reset;}
const pad=(s,n)=>s+' '.repeat(Math.max(0,n-cellWidth(s)));
export function wrapInput(text,width){
  const lines=[{text:'',start:0,end:0}];let col=0;
  for(const {text:s,index} of graphemes(text)){
    if(s==='\n'){lines.at(-1).end=index;lines.push({text:'',start:index+1,end:index+1});col=0;continue;}
    const w=cellWidth(s);if(col+w>width){lines.at(-1).end=index;lines.push({text:'',start:index,end:index});col=0;}
    lines.at(-1).text+=s;lines.at(-1).end=index+s.length;col+=w;
  }
  return lines;
}
export const wrap=(text,width)=>wrapInput(cleanTerminal(text),Math.max(1,width)).map(l=>l.text);
const inline=text=>cleanTerminal(text).replace(/\*\*([^*]+)\*\*/g,(_,s)=>color('text',s)).replace(/`([^`]+)`/g,(_,s)=>color('code',s));
function markdown(text,width){
  const output=[];let code=false;
  for(const original of cleanTerminal(text).split('\n')){
    if(/^\s*```/.test(original)){if(code)output.push(color('border','╰'+'─'.repeat(Math.max(1,width-1))));else output.push(color('border','╭─ '+(original.trim().slice(3)||'code')+' '+'─'.repeat(Math.max(1,width-10))));code=!code;continue;}
    if(code){for(const line of wrap(original,width-3))output.push(color('border','│ ')+color('code',line));continue;}
    const heading=/^(#{1,6})\s+(.+)/.exec(original);
    for(const line of wrap(heading?heading[2]:original,width))output.push(heading?ansi.bold+color('accent',line):color('text',inline(line)));
  }
  return output;
}
export const commands=[['/new','새 대화'],['/models','모델 선택'],['/priority','Auto 모델 우선순위'],['/connect opencode','OpenCode 모델 연결'],['/history','이전 대화'],['/settings','대화 설정'],['/retry','마지막 답변 다시 생성'],['/copy','마지막 답변 복사'],['/export','Markdown으로 내보내기'],['/help','키보드 도움말'],['/exit','종료']];

export class TerminalUI {
  constructor({input=process.stdin,output=process.stdout,version='0.4.0',onAction=()=>{}}={}){
    this.stdin=input;this.stdout=output;this.version=version;this.onAction=onAction;this.input='';this.cursor=0;this.messages=[];this.model='';this.modelName='';this.title='새 대화';this.busy=false;this.modal=null;this.scrollOffset=0;this.notice='';this.noticeUntil=0;this.closed=false;this.paste=false;this.pasteBuffer='';
  }
  start(){
    emitKeypressEvents(this.stdin);this.wasRaw=this.stdin.isRaw;this.stdin.setRawMode?.(true);this.stdin.resume();
    this.stdout.write('\x1b[?1049h\x1b[?25l\x1b[?2004h');
    this.keyHandler=(str,key)=>{try{this.key(str,key||{});}catch(e){this.notify(e.message,true);}};
    this.resizeHandler=()=>this.render();this.stdin.on('keypress',this.keyHandler);this.stdout.on('resize',this.resizeHandler);
    this.timer=setInterval(()=>{if(this.busy||this.noticeUntil&&Date.now()>this.noticeUntil)this.render();},100);
    this.render();return this;
  }
  close(){if(this.closed)return;this.closed=true;clearInterval(this.timer);clearTimeout(this.renderTimer);this.stdin.off('keypress',this.keyHandler);this.stdout.off('resize',this.resizeHandler);this.stdin.setRawMode?.(this.wasRaw||false);this.stdin.pause();this.stdout.write('\x1b[0m\x1b[?2004l\x1b[?25h\x1b[?1049l');}
  notify(message,error=false){this.notice=message;this.noticeError=error;this.noticeUntil=Date.now()+4500;this.render();}
  schedule(){if(!this.renderTimer)this.renderTimer=setTimeout(()=>{this.renderTimer=null;this.render();},50);}
  dispatch(action,value){Promise.resolve(this.onAction(action,value)).catch(e=>this.notify(e.message,true));}
  setInput(text){this.input=text;this.cursor=text.length;this.render();}
  openModal(title,items,{placeholder='검색어 입력',onSelect,columns=['NAME','DESCRIPTION'],selectedValue}={}){this.modal={title,items,index:Math.max(0,items.findIndex(i=>i.value===selectedValue)),query:'',placeholder,onSelect,columns};this.render();}
  filtered(){const m=this.modal;return m?m.items.filter(item=>(item.label+' '+(item.detail||'')+' '+(item.value||'')+' '+(item.search||'')).toLocaleLowerCase().includes(m.query.toLocaleLowerCase())):[];}
  selectItem(item){if(!item)return;if(item.disabled){this.notify(item.disabledReason||'현재 선택할 수 없는 항목입니다.',true);return;}const callback=this.modal.onSelect;this.modal=null;Promise.resolve(callback?.(item)).catch(e=>this.notify(e.message,true));}
  insert(text){text=cleanTerminal(text);if(!text)return;if(this.modal){this.modal.query+=text.replace(/\n/g,'');this.modal.index=0;}else if(!this.busy&&this.input.length+text.length<=150000){this.input=this.input.slice(0,this.cursor)+text+this.input.slice(this.cursor);this.cursor+=text.length;}this.schedule();}
  key(str,key){
    if(key.sequence==='\x1b[200~'){this.paste=true;this.pasteBuffer='';return;}
    if(key.sequence==='\x1b[201~'){this.paste=false;this.insert(this.pasteBuffer);this.pasteBuffer='';return;}
    if(this.paste){this.pasteBuffer+=key.name==='return'||key.name==='enter'?'\n':str||'';return;}
    if(key.ctrl&&key.name==='c'){if(this.busy){this.dispatch('stop');return;}if(Date.now()-(this.lastInterrupt||0)<1800){this.dispatch('exit');return;}this.lastInterrupt=Date.now();this.notify('종료하려면 Ctrl+C를 한 번 더 누르세요.');return;}
    if(key.ctrl&&key.name==='d'&&!this.input){this.dispatch('exit');return;}
    if(key.name==='escape'){if(this.modal)this.modal=null;else if(this.busy)this.dispatch('stop');else this.input='',this.cursor=0;this.render();return;}
    if(key.ctrl&&key.name==='p'&&!this.busy){this.dispatch('models');return;}
    if(key.ctrl&&['h','l'].includes(key.name)&&!this.busy){this.dispatch('history');return;}
    if(key.ctrl&&key.name==='n'&&!this.busy){this.dispatch('new');return;}
    if(key.ctrl&&key.name==='r'&&!this.busy){this.dispatch('retry');return;}
    if(key.name==='pageup'||key.ctrl&&key.name==='u'){this.scrollOffset+=Math.max(3,Math.floor((this.stdout.rows||30)/2));this.render();return;}
    if(key.name==='pagedown'||key.ctrl&&key.name==='d'){this.scrollOffset=Math.max(0,this.scrollOffset-Math.max(3,Math.floor((this.stdout.rows||30)/2)));this.render();return;}
    if(this.modal){
      const matches=this.filtered();if(key.name==='up')this.modal.index=Math.max(0,this.modal.index-1);
      else if(key.name==='down')this.modal.index=Math.min(matches.length-1,this.modal.index+1);
      else if(!this.modal.query&&/^[1-9]$/.test(str||'')&&matches[Number(str)-1])this.selectItem(matches[Number(str)-1]);
      else if(key.name==='return')this.selectItem(matches[this.modal.index]);
      else if(key.name==='backspace'){this.modal.query=graphemes(this.modal.query).slice(0,-1).map(s=>s.text).join('');this.modal.index=0;}
      else if(str&&!key.ctrl&&!key.meta)this.insert(str);
      this.schedule();return;
    }
    if(this.busy)return;
    if(key.name==='enter'||key.ctrl&&['j','o'].includes(key.name)||key.name==='return'&&(key.shift||key.meta)||key.sequence==='\x1b[13;2u'){this.insert('\n');return;}
    if(key.name==='return'){
      const text=this.input.trim();if(!text)return;this.input='';this.cursor=0;this.scrollOffset=0;this.render();
      if(text.startsWith('/')){const [command,...rest]=text.split(/\s+/);this.dispatch(command.slice(1),rest.join(' '));}else this.dispatch('send',text);return;
    }
    const segments=graphemes(this.input),before=segments.filter(s=>s.index<this.cursor).at(-1),after=segments.find(s=>s.index>=this.cursor);
    if(key.name==='backspace'&&before){this.input=this.input.slice(0,before.index)+this.input.slice(this.cursor);this.cursor=before.index;}
    else if(key.name==='delete'&&after)this.input=this.input.slice(0,this.cursor)+this.input.slice(after.index+after.text.length);
    else if(key.name==='left')this.cursor=before?.index??0;
    else if(key.name==='right')this.cursor=after?after.index+after.text.length:this.input.length;
    else if(key.name==='home'||key.ctrl&&key.name==='a')this.cursor=0;
    else if(key.name==='end'||key.ctrl&&key.name==='e')this.cursor=this.input.length;
    else if(key.name==='up'){this.scrollOffset+=3;}
    else if(key.name==='down'){this.scrollOffset=Math.max(0,this.scrollOffset-3);}
    else if(key.name==='tab'&&this.input.startsWith('/')){const match=commands.find(([c])=>c.startsWith(this.input.trim()));if(match)this.setInput(match[0]);}
    else if(str&&!key.ctrl&&!key.meta)this.insert(str);
    this.schedule();
  }
  thread(width){
    const lines=[];for(const m of this.messages){
      lines.push('');lines.push(m.role==='user'?color('accent','  ● YOU'):color('accent','  ✦ FREEWAY')+color('muted',m.model?'  '+this.shortModel(m.model):''));lines.push('');
      const content=m.role==='user'?wrap(m.content,width-5).map(s=>color('text',s)):markdown(m.content,width-5);
      lines.push(...content.map(s=>'    '+s));
      if(m.reasoning&&!m.content&&m.status==='streaming')lines.push(color('muted','    추론을 진행하고 있어요…'));
      if(m.error)for(const line of wrap(m.error,width-7))lines.push(color('warning','    ! '+line));
      if(m.status==='stopped')lines.push(color('muted','    응답이 중지되었습니다. Ctrl+R로 다시 생성'));
    }
    if(this.busy){const spinner=['⠋','⠙','⠹','⠸','⠼','⠴','⠦','⠧','⠇','⠏'][Math.floor(Date.now()/100)%10];lines.push('',color('accent','    '+spinner+' ')+color('muted','답변 생성 중  '+Math.floor((Date.now()-this.startedAt)/1000)+'s  ·  Esc 중지'));}
    return lines;
  }
  shortModel(id){return this.modelNames?.get(id)||id.replace(/^kilo\//,'').replace(/^opencode\//,'').replace(/^nvidia\//,'').replace(/:free$/,'');}
  render(){
    if(this.closed)return;
    const width=Math.max(35,this.stdout.columns||100),height=Math.max(12,this.stdout.rows||30),inner=width-8;
    const input=this.modal?this.modal.query:this.input;const inputLines=wrapInput(input,inner),cursor=this.modal?input.length:this.cursor;
    let cursorLine=0;for(let i=0;i<inputLines.length;i++)if(inputLines[i].start<=cursor)cursorLine=i;
    const inputRows=this.modal?1:Math.max(2,Math.min(inputLines.length,Math.min(6,Math.max(2,height-13))));const firstInput=Math.max(0,cursorLine-inputRows+1);
    const bodyHeight=Math.max(2,height-inputRows-7),frame=[];
    const brand=ansi.bold+color('accent','↗ freeway')+color('muted','  /  AI workspace');
    frame.push('  '+brand+' '.repeat(Math.max(1,width-cellWidth(brand)-cellWidth('v'+this.version)-5))+color('muted','v'+this.version));
    frame.push('  '+color('text',clip(this.modelName||this.shortModel(this.model),width-22))+color('accent','  FREE')+color('muted','  ·  '+(this.busy?'생성 중':this.title==='새 대화'?'새 대화':clip(this.title,Math.max(10,width-60)))));frame.push('');
    let body=[];
    if(this.modal){
      const matches=this.filtered();this.modal.index=Math.max(0,Math.min(this.modal.index,matches.length-1));
      const nameWidth=Math.max(12,Math.min(38,Math.floor(width*.44)-5,Math.max(18,...matches.map(item=>cellWidth(item.label)))));
      const detailWidth=Math.max(1,width-nameWidth-14);
      body.push(color('accent','    '+this.modal.title),color('muted','    ↑ ↓  Enter  ·  1-9 빠른 선택  ·  Esc 닫기'),'');
      body.push('          '+color('muted',pad(clip(this.modal.columns[0],nameWidth),nameWidth)+'  '+clip(this.modal.columns[1],detailWidth)));
      body.push(color('border','    '+'─'.repeat(width-8)));
      const visible=Math.max(1,bodyHeight-6),start=Math.max(0,this.modal.index-visible+1);
      if(!matches.length)body.push(color('muted','    검색 결과가 없어요.'));
      for(let i=start;i<Math.min(matches.length,start+visible);i++){
        const item=matches[i],selected=i===this.modal.index;
        const prefix='    '+(selected?'›':' ')+' '+String(i+1).padStart(2)+'. ';
        body.push((selected?ansi.selected:'')+color(selected?'accent':'muted',prefix)+color(item.disabled?'muted':selected?'accent':'text',pad(clip(item.label,nameWidth),nameWidth))+'  '+color('muted',clip(item.detail||'',detailWidth))+ansi.reset);
      }
      if(matches.length)body.push(color('muted','          '+(this.modal.index+1)+' / '+matches.length+(matches[this.modal.index]?.value?'  ·  '+clip(String(matches[this.modal.index].value),width-24):'')));
    }else if(!this.messages.length){
      const text=[color('accent','↗'),ansi.bold+color('text','무엇을 함께 만들어볼까요?'),'',color('muted','생각을 정리하고, 코드를 검토하고, 답을 찾아보세요.'),'',color('accent','/models')+color('muted',' 모델 선택     ')+color('accent','/history')+color('muted',' 대화 이어가기')];
      body=Array(Math.max(0,Math.floor((bodyHeight-text.length)/2))).fill('');
      body.push(...text.map(line=>' '.repeat(Math.max(2,Math.floor((width-cellWidth(line))/2)))+line));
    }else{
      const all=this.thread(width);this.scrollOffset=Math.min(this.scrollOffset,Math.max(0,all.length-bodyHeight));const end=Math.max(0,all.length-this.scrollOffset);body=all.slice(Math.max(0,end-bodyHeight),end);
    }
    if(!this.modal&&this.input.startsWith('/')&&!this.input.includes('\n')&&!this.busy){const matches=commands.filter(([c])=>c.startsWith(this.input.trim()));const menu=matches.slice(0,4).map(([c,d])=>'    '+color('accent',pad(c,12))+color('muted',d));body=body.slice(0,Math.max(0,bodyHeight-menu.length-1));body.push('',...menu);}
    frame.push(...body.slice(0,bodyHeight));while(frame.length<3+bodyHeight)frame.push('');
    const label=this.modal?' '+this.modal.placeholder+' ':this.busy?' 응답 생성 중 · Esc로 중지 ':' 메시지 ';const borderWidth=width-4;
    frame.push('  '+color('border','╭─')+color('muted',label)+color('border','─'.repeat(Math.max(0,borderWidth-cellWidth(label)-3))+'╮'));
    for(let row=0;row<inputRows;row++){const placeholder=!input&&!row;const text=clip(placeholder?(this.modal?'검색…':this.busy?'응답을 기다리는 중…':'무엇이든 물어보세요. / 를 입력하면 명령어가 나와요.'):(inputLines[firstInput+row]?.text||''),inner);frame.push('  '+color('border','│ ')+color(placeholder?'muted':'text',text)+' '.repeat(Math.max(0,inner-cellWidth(text)))+color('border',' │'));}
    frame.push('  '+color('border','╰'+'─'.repeat(borderWidth-2)+'╯'));
    frame.push('  '+color('muted',clip(this.modal?'↑↓ 이동  ·  Enter 선택  ·  Esc 닫기':'Enter 전송  ·  Ctrl+O 줄바꿈  ·  Ctrl+P 모델  ·  Ctrl+L 대화  ·  PgUp/PgDn 스크롤',width-5)));
    const notice=Date.now()<this.noticeUntil?this.notice:'';if(!notice)this.noticeUntil=0;
    frame.push('  '+color(notice&&this.noticeError?'warning':'muted',clip(notice||(this.usage?'이번 응답 '+this.usage.total_tokens+' tokens  ·  ':'')+(this.saveLabel||'무료 AI · 개인 워크스페이스')+(this.scrollOffset?'  ·  ↓ 최신 답변':''),width-5)));
    this.lastFrame=frame.slice(0,height).map(line=>clipStyled(line,width-1));
    const cleanFrame=this.lastFrame.map(line=>line+'\x1b[K').join('\r\n');
    const line=inputLines[cursorLine],cursorColumn=cellWidth((line?.text||'').slice(0,Math.max(0,cursor-(line?.start||0))));
    const cursorRow=3+bodyHeight+1+(cursorLine-firstInput)+1;
    this.stdout.write('\x1b[?25l\x1b[H'+cleanFrame+'\x1b[J'+(!this.busy?'\x1b['+cursorRow+';'+(5+cursorColumn)+'H\x1b[?25h':''));
  }
}
