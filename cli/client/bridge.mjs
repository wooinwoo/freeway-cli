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
      .map(model => ({ id: model.id, name: model.name }));
    if (!models.length) throw new Error('No free models are ready. Connect a provider using the POC CLI, then reopen /model.');
    emit({ type: 'models', models: [{ id: 'free/auto', name: 'Auto · Free models' }, ...models] });
  } else if (request.action === 'chat') {
    const result = await client.complete({
      model: request.model, messages: request.messages, max_tokens: 4096, temperature: 0.6
    }, { onText: text => emit({ type: 'text', text }) });
    emit({ type: 'done', text: result.content, model: result.model, provider: result.provider });
  } else {
    throw new Error('Unknown bridge action.');
  }
} catch (error) {
  emit({ type: 'error', error: error.message });
  process.exitCode = 1;
}
