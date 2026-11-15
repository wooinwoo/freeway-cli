export const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

function inline(value) {
  const tokens=[];
  const token=html=>'\uE000'+(tokens.push(html)-1)+'\uE001';
  let s=String(value).replace(/`([^`\n]+)`/g,(_,code)=>token('<code>'+escapeHTML(code)+'</code>'));
  s=s.replace(/\[([^\]\n]+)\]\(([^\s)]+)\)/g,(_,label,url)=>{
    if(!/^https?:\/\//i.test(url)&&!/^mailto:[^\s<>]+$/i.test(url))return label;
    return token('<a href="'+escapeHTML(url)+'" target="_blank" rel="noopener noreferrer">'+escapeHTML(label)+'</a>');
  });
  s=escapeHTML(s).replace(/\*\*([^*\n]+)\*\*/g,'<strong>$1</strong>').replace(/__([^_\n]+)__/g,'<strong>$1</strong>')
    .replace(/(^|[\s(])\*([^*\n]+)\*(?=$|[\s.,!?):])/g,'$1<em>$2</em>')
    .replace(/~~([^~\n]+)~~/g,'<del>$1</del>');
  return s.replace(/\uE000(\d+)\uE001/g,(_,i)=>tokens[Number(i)]||'');
}
const cells=line=>line.trim().replace(/^\|/,'').replace(/\|$/,'').split('|').map(c=>c.trim());
const divider=line=>cells(line).length>0&&cells(line).every(c=>/^:?-{3,}:?$/.test(c));
const listLine=line=>/^\s{0,6}(?:[-+*]|\d+[.)])\s+/.test(line);
const boundary=line=>/^\s*$|^\s{0,3}(?:```|~~~)|^#{1,6}\s+|^>\s?|^\s*(?:---+|\*\*\*+|___+)\s*$/.test(line)||listLine(line);

export function renderMarkdown(value,depth=0) {
  const lines=String(value||'').replace(/\r\n?/g,'\n').split('\n');
  const out=[];
  for(let i=0;i<lines.length;){
    const line=lines[i];
    if(!line.trim()){i++;continue;}
    const fence=/^\s{0,3}(`{3,}|~{3,})([^\s`]*)/.exec(line);
    if(fence){
      const code=[];i++;
      while(i<lines.length&&!new RegExp('^\\s{0,3}'+fence[1][0]+'{'+fence[1].length+',}\\s*$').test(lines[i]))code.push(lines[i++]);
      if(i<lines.length)i++;
      out.push('<div class="code-block"><div class="code-heading"><span>'+escapeHTML(fence[2]||'code')+'</span><button type="button" data-copy-code aria-label="코드 복사">복사</button></div><pre><code>'+escapeHTML(code.join('\n'))+'</code></pre></div>');continue;
    }
    const heading=/^(#{1,6})\s+(.+?)\s*#*\s*$/.exec(line);
    if(heading){out.push('<h'+heading[1].length+'>'+inline(heading[2])+'</h'+heading[1].length+'>');i++;continue;}
    if(/^\s*(?:---+|\*\*\*+|___+)\s*$/.test(line)){out.push('<hr>');i++;continue;}
    if(/^>\s?/.test(line)&&depth<3){
      const quote=[];while(i<lines.length&&/^>\s?/.test(lines[i]))quote.push(lines[i++].replace(/^>\s?/,''));
      out.push('<blockquote>'+renderMarkdown(quote.join('\n'),depth+1)+'</blockquote>');continue;
    }
    if(i+1<lines.length&&line.includes('|')&&divider(lines[i+1])){
      const headings=cells(line);i+=2;const rows=[];
      while(i<lines.length&&lines[i].trim()&&lines[i].includes('|'))rows.push(cells(lines[i++]));
      out.push('<div class="markdown-table"><table><thead><tr>'+headings.map(c=>'<th>'+inline(c)+'</th>').join('')+'</tr></thead><tbody>'+rows.map(row=>'<tr>'+headings.map((_,j)=>'<td>'+inline(row[j]||'')+'</td>').join('')+'</tr>').join('')+'</tbody></table></div>');continue;
    }
    if(listLine(line)){
      const ordered=/^\s*\d+[.)]\s+/.test(line),tag=ordered?'ol':'ul',items=[];
      while(i<lines.length&&listLine(lines[i])&&/^\s*\d+[.)]\s+/.test(lines[i])===ordered)items.push(inline(lines[i++].replace(/^\s*(?:[-+*]|\d+[.)])\s+/,'')));
      out.push('<'+tag+'>'+items.map(item=>'<li>'+item+'</li>').join('')+'</'+tag+'>');continue;
    }
    const paragraph=[line];i++;
    while(i<lines.length&&!boundary(lines[i])&&!(i+1<lines.length&&lines[i].includes('|')&&divider(lines[i+1])))paragraph.push(lines[i++]);
    out.push('<p>'+paragraph.map(inline).join('<br>')+'</p>');
  }
  return out.join('');
}

export function chatToMarkdown(chat) {
  const lines=['# '+chat.title,'',chat.model?'모델: '+chat.model:''];
  for(const m of chat.messages)lines.push('','## '+(m.role==='user'?'나':'Freeway')+(m.model?' · '+m.model:''),'',m.content||'',...(m.error?['','> '+m.error]:[]));
  return lines.join('\n').trim()+'\n';
}
