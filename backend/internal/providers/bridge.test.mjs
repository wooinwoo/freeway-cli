import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { createGateway } from '../../../POC/server.mjs';
import { Client } from '../../../POC/src/client.mjs';
import { definitions } from '../../../POC/src/catalog.mjs';
import { execute, errorEvent } from './bridge.mjs';
import { run } from './manage.mjs';
import { GatewayError } from '../../../POC/src/streams.mjs';

async function setup(t) {
  const dir = mkdtempSync('/tmp/freeway-provider-package-');
  const calls = [];
  const provider = { ...definitions.find(p => p.id === 'kilo'), env: 'FREEWAY_PACKAGE_TEST_UNUSED_KEY' };
  const app = createGateway({ dataDir: dir, providers: [provider], fetcher: async (url, options) => {
    calls.push({ url, body: options.body && JSON.parse(options.body) });
    if (url.endsWith('/models')) return Response.json({ data: [
      { id: 'free-model', isFree: true, pricing: { prompt: '0', completion: '0' },
        supported_parameters: ['reasoning'], opencode: { reasoning_options: [{ type: 'effort', values: ['low', 'high'] }] } },
      { id: 'paid-model', isFree: false, pricing: { prompt: '0', completion: '0' } }
    ] });
    return Response.json({ choices: [{ message: { content: 'PACKAGE_OK' }, finish_reason: 'stop' }],
      usage: { total_tokens: 8, cost: 0 } });
  } });
  const address = await app.listen(0);
  const client = new Client({ base: 'http://127.0.0.1:' + address.port, autoStart: false });
  t.after(async () => { await app.close(); rmSync(dir, { recursive: true, force: true }); });
  return { app, client, calls };
}

test('backend package lists all providers without contacting a gateway', async () => {
  const result = await execute({ action: 'providers' });
  assert.equal(result.length, 11);
});

test('bridge errors conform to Go string codes and integer retry seconds even for DOM exceptions', () => {
  assert.deepEqual(errorEvent(new DOMException('private timeout detail', 'TimeoutError')), {
    type: 'error', error: { code: 'request_timeout', status: 504, retryAfter: 0 }
  });
  assert.deepEqual(errorEvent(new DOMException('private cancellation detail', 'AbortError')), {
    type: 'error', error: { code: 'cancelled', status: 499, retryAfter: 0 }
  });
  assert.deepEqual(errorEvent({ code: 23, status: '504', message: 'secret' }), {
    type: 'error', error: { code: 'bridge_failed', status: 502, retryAfter: 0 }
  });
  assert.deepEqual(errorEvent({ code: 'rate_limit_exceeded', status: 429, retryAfter: 2.5 }), {
    type: 'error', error: { code: 'rate_limit_exceeded', status: 429, retryAfter: 3 }
  });
});

test('backend authentication reuses encrypted credentials without returning the key', async t => {
  const { app, client } = await setup(t);
  const result = await execute({ action: 'authenticate', provider: 'kilo', key: 'secret-package-fixture' }, { client });
  assert.equal(result.status, 'configured');
  assert.equal(result.inferenceVerified, false);
  assert.equal(app.store.key(app.engine.provider('kilo')), 'secret-package-fixture');
  assert(!JSON.stringify(result).includes('secret-package-fixture'));
});

test('backend model discovery and streaming transmit the exact model and reasoning setting', async t => {
  const { client, calls } = await setup(t);
  const models = await execute({ action: 'models', provider: 'kilo' }, { client });
  assert.equal(models.length, 1);
  assert.deepEqual(models[0].reasoning, { parameter: 'reasoning', efforts: ['low', 'high'] });
  const events = [];
  const result = await execute({ action: 'complete', provider: 'kilo', model: models[0].id,
    reasoningEffort: 'high', messages: [{ role: 'user', content: '안녕하세요' }] }, { client, emit: e => events.push(e) });
  const actual = calls.find(c => c.url.endsWith('/chat/completions')).body;
  assert.equal(actual.model, 'free-model');
  assert.deepEqual(actual.reasoning, { effort: 'high' });
  assert.equal(actual.reasoning_effort, undefined);
  assert.equal(result.provider, 'kilo');
  assert.equal(result.model, models[0].id);
  assert.equal(result.reasoningEffort, 'high');
  assert.equal(result.content, 'PACKAGE_OK');
  assert.deepEqual(events, [{ type: 'text', text: 'PACKAGE_OK' }]);
});

test('unsupported depth and paid or other-provider models fail before inference', async t => {
  const { client, calls } = await setup(t);
  for (const input of [
    { model: 'free-model', reasoningEffort: 'max', code: 'unsupported_reasoning' },
    { model: 'paid-model', code: 'model_not_found' },
    { model: 'opencode/free-model', code: 'model_not_found' }
  ]) {
    await assert.rejects(execute({ action: 'complete', provider: 'kilo', messages: [{ role: 'user', content: 'test' }], ...input }, { client }), { code: input.code });
  }
  assert(calls.every(c => c.url.endsWith('/models')));
});

test('quota inspection consumes no inference and never calls local counts an upstream allowance', async t => {
  const { app, client, calls } = await setup(t);
  app.store.configure('kilo', { limits: { dayTokens: 1000 } });
  app.store.buckets('kilo').hour.requests = 7;
  app.store.buckets('kilo').day.tokens = 100;
  app.store.buckets('kilo').day.estimatedTokens = 50;
  app.store.save();
  const result = await execute({ action: 'quota', provider: 'kilo' }, { client });
  assert.equal(result.source, 'local_gateway');
  assert.equal(result.authoritative, false);
  assert.equal(result.providerRemaining, null);
  assert.equal(result.localRemaining.hour, 193);
  assert.equal(result.usage.hour.requests, 7);
  assert.equal(result.localTokenRemaining.day, 850);
  assert.equal(calls.length, 0);
});

test('HTTP reasoning validation rejects unsupported values before reserving usage', async t => {
  const { app, client, calls } = await setup(t);
  await execute({ action: 'models', provider: 'kilo' }, { client });
  await assert.rejects(client.complete({ model: 'kilo/free-model', reasoning_effort: 'max',
    messages: [{ role: 'user', content: 'test' }] }), { code: 'unsupported_reasoning' });
  assert.equal(app.store.buckets('kilo').total.requests, 0);
  assert(calls.every(c => c.url.endsWith('/models')));
});

test('CLI exposes reasoning and quota through the same tested implementation', async t => {
  const { client, calls } = await setup(t);
  const deps = { client, interactive: false };
  const models = await run(['models', 'kilo'], deps);
  assert.deepEqual(models.results[0].models[0].reasoning.efforts, ['low', 'high']);
  const result = await run(['ask', 'kilo', '--model', 'free-model', '--reasoning-effort', 'low', '--', '안녕'], deps);
  assert.equal(result.ok, true);
  assert.deepEqual(calls.find(c => c.url.endsWith('/chat/completions')).body.reasoning, { effort: 'low' });
  const before = calls.length;
  const quota = await run(['quota', 'kilo'], deps);
  assert.equal(quota.results[0].providerRemaining, null);
  assert.equal(quota.results[0].localRemaining.hour, 199);
  assert.equal(calls.length, before);
});

test('a real gateway preserves quota status and retry time after streaming has started', async t => {
  const dir = mkdtempSync('/tmp/freeway-late-quota-');
  const provider = definitions.find(p => p.id === 'opencode');
  const bridge = { ready: true, state: () => ({ ready: true }), stop() {},
    async models() { return [{ id: 'opencode/free-test', provider: 'opencode', upstreamId: 'free-test', free: true,
      discoveredAt: new Date().toISOString(), capabilities: { stream: true, tools: false } }]; },
    async *complete() {
      yield { choices: [{ delta: { content: 'partial' } }] };
      throw new GatewayError('quota exhausted', 429, 'rate_limit_exceeded', 60);
    } };
  const app = createGateway({ dataDir: dir, providers: [provider], bridge });
  app.store.configure('opencode', { enabled: true });
  await app.engine.refresh('opencode');
  const address = await app.listen(0);
  t.after(async () => { await app.close(); rmSync(dir, { recursive: true, force: true }); });
  const client = new Client({ base: 'http://127.0.0.1:' + address.port, autoStart: false });
  let partial = '';
  await assert.rejects(client.complete({ model: 'opencode/free-test', messages: [{ role: 'user', content: 'test' }] },
    { signal: AbortSignal.timeout(2000), onText: text => { partial += text; } }), { code: 'rate_limit_exceeded', status: 429, retryAfter: 60 });
  assert.equal(partial, 'partial');
});
