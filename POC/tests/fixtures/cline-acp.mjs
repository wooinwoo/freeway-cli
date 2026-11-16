import { createInterface } from 'node:readline';
const send = message => process.stdout.write(JSON.stringify({ jsonrpc: '2.0', ...message }) + '\n');
let selected, promptId;
createInterface({ input: process.stdin }).on('line', line => {
  const item = JSON.parse(line);
  if (item.method === 'initialize') send({ id: item.id, result: { protocolVersion: 1 } });
  if (item.method === 'session/new') send({ id: item.id, result: { sessionId: 'fixture-session' } });
  if (item.method === 'session/set_model') {
    selected = item.params.modelId;
    send({ id: item.id, result: {} });
  }
  if (item.method === 'session/prompt') {
    if (selected !== 'cline-free/test') { send({ id: item.id, error: { message: 'Paid default would have been called' } }); return; }
    const text = item.params.prompt[0].text;
    if (text.includes('WAIT_FOR_CANCEL')) return;
    if (text.includes('QUOTA')) { send({ id: item.id, error: { message: 'Free usage quota exceeded' } }); return; }
    if (text.includes('PERMISSION')) {
      promptId = item.id;
      send({ id: 90, method: 'session/request_permission', params: { options: [{ optionId: 'allow', kind: 'allow_once' }] } });
      return;
    }
    send({ method: 'session/update', params: { sessionId: 'other-session', update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'WRONG SESSION' } } } });
    const body = text.split('[user]\n')[1];
    send({ method: 'session/update', params: { sessionId: 'fixture-session', update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: body } } } });
    send({ id: item.id, result: { stopReason: 'end_turn' } });
  }
  if (item.id === 90) {
    const denied = item.result?.outcome?.outcome === 'cancelled';
    send({ method: 'session/update', params: { sessionId: 'fixture-session', update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: denied ? 'Permission denied' : 'TOOLS EXECUTED' } } } });
    send({ id: promptId, result: { stopReason: 'end_turn' } });
  }
});
