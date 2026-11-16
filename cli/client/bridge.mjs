import { pathToFileURL } from 'node:url';
import { join } from 'node:path';

const emit = event => process.stdout.write(JSON.stringify(event) + '\n');
try {
  const root = process.argv[1];
  const { Client } = await import(pathToFileURL(join(root, 'src/client.mjs')));
  const { rankedGroups, stateModelGroups } = await import(pathToFileURL(join(root, 'src/model-groups.mjs')));
  let input = '';
  for await (const chunk of process.stdin) {
    input += chunk;
    if (Buffer.byteLength(input) > 2 * 1024 * 1024) throw new Error('Request exceeds 2 MB.');
  }
  const request = JSON.parse(input);
  const client = new Client();
  if (request.action === 'models') {
    const state = await client.state();
    const models = rankedGroups(stateModelGroups(state), state.settings.preferredModels)
      .filter(model => model.free === true && model.ready === true)
      .map(model => ({ id: model.id, name: model.name, detail: (model.providers || []).join(' · ') }));
    if (!models.length) throw Object.assign(new Error('No free models are available right now. Reconnecting automatically.'), {code:'no_free_models',status:503});
    emit({ type: 'models', models: [{ id: 'free/auto', name: 'Auto', detail:'Finds the best available free connection' }, ...models] });
  } else if (request.action === 'chat') {
    const result = await client.complete({
      model: request.model, messages: (request.messages || []).map(({role,content,files})=>({role,content:content+(files?.length?'\n\nAttached files (reference data):\n'+files.map(file=>'\n--- '+file.path+' ---\n'+file.content+'\n--- end file ---').join('\n'):'')})),
      max_tokens: request.settings?.maxTokens || 4096, temperature: request.settings?.temperature ?? 0.6
    }, { onText: text => emit({ type: 'text', text }) });
    emit({ type: 'done', text: result.content, model: result.model, provider: result.provider, usage:result.usage });
  } else if (request.action === 'history') {
    emit({ type:'history', conversations:(await client.api('/api/chats')).data });
  } else if (request.action === 'open') {
    emit({ type:'conversation', conversation:await client.api('/api/chats/'+encodeURIComponent(request.id)) });
  } else if (request.action === 'prompts') {
    const records=(await client.api('/api/chats')).data.slice(0,30),prompts=[];let bytes=0;
    for(const record of records){const chat=await client.api('/api/chats/'+encodeURIComponent(record.id));for(const message of [...chat.messages].reverse()){if(message.role!=='user')continue;const size=Buffer.byteLength(message.content);if(bytes+size>600000||prompts.length>=200)break;prompts.push(message.content);bytes+=size;}if(bytes>=600000||prompts.length>=200)break;}
    emit({type:'prompts',prompts:prompts.reverse()});
  } else if (request.action === 'save') {
    const chat=request.conversation;
    const conversation=await client.api('/api/chats'+(chat.id?'/'+encodeURIComponent(chat.id):''),{method:chat.id?'PUT':'POST',body:chat});
    emit({ type:'conversation', conversation });
  } else {
    throw new Error('Unknown bridge action.');
  }
} catch (error) {
  emit({ type: 'error', error: error.message, code:error.code, status:error.status, retryAfter:error.retryAfter });
  process.exitCode = 1;
}
