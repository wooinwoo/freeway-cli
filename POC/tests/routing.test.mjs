import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Store } from '../src/store.mjs';
import { Engine } from '../src/engine.mjs';
import { GatewayError } from '../src/streams.mjs';
import { definitions, normalizeModel } from '../src/catalog.mjs';

test('reasoning selection keeps compatible Auto and model-group routes before applying the fallback budget', async t => {
  const dir = mkdtempSync(join(tmpdir(), 'freeway-reasoning-routes-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const providers = ['first', 'second'].map((id, index) => ({ id, kind: 'openai', keyOptional: true, priority: index, limits: {} }));
  const store = new Store(dir);
  for (const p of providers) store.configure(p.id, { enabled: true });
  store.data.settings.maxFallbacks = 1;
  store.data.models = providers.map((p, index) => normalizeModel(p, { id: 'vendor/same-model', name: 'Same Model',
    supported_parameters: index ? ['reasoning'] : [],
    opencode: { reasoning_options: index ? [{ type: 'effort', values: ['high'] }] : [] } }));
  const engine = new Engine(store, null, { providers });
  const group = engine.modelGroups().find(g => g.routes.length === 2);
  assert.ok(group);
  for (const model of ['free/auto', group.id]) {
    const candidates = await engine.candidates({ model, reasoning_effort: 'high', messages: [{ role: 'user', content: 'test' }] });
    assert.deepEqual(candidates.map(m => m.id), ['second/vendor/same-model']);
  }
  await assert.rejects(engine.candidates({ model: 'first/vendor/same-model', reasoning_effort: 'high', messages: [] }), { code: 'unsupported_reasoning' });
  await assert.rejects(engine.candidates({ model: 'free/auto', reasoning_effort: 'max', messages: [] }), { code: 'unsupported_reasoning' });
});

test('Kilo explicitly paid models stay out of free routes even with zero token prices', async t => {
  const dir = mkdtempSync(join(tmpdir(), 'freeway-kilo-catalog-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const provider = definitions.find(p => p.id === 'kilo');
  const paidIds = ['google/lyria-3-pro-preview', 'google/lyria-3-clip-preview', 'recursive/flint-v1.0', 'humansand/ampersand-v0.1'];
  const models = [
    ...paidIds.map(id => ({ id, isFree: false, pricing: { prompt: '0', completion: '0' } })),
    { id: 'verified-free', isFree: true, pricing: { prompt: '0', completion: '0' } },
    { id: 'charged', isFree: true, pricing: { prompt: '1', completion: '1' } }
  ];
  const store = new Store(dir);
  let requests = 0;
  const engine = new Engine(store, null, { providers: [provider], fetcher: async url => {
    requests++;
    assert.equal(url, provider.base + '/models');
    return Response.json({ data: models });
  } });
  await engine.refresh('kilo');
  assert.deepEqual(engine.models().map(m => m.upstreamId), ['verified-free']);
  for (const id of paidIds) {
    const entry = engine.catalog().find(m => m.upstreamId === id);
    assert.equal(entry.free, false);
    assert.equal(entry.catalogStatus, 'paid');
    assert.equal(entry.ready, false);
    await assert.rejects(engine.candidates({ model: 'kilo/' + id, messages: [{ role: 'user', content: 'test' }] }), { code: 'model_not_found' });
  }
  assert.equal(requests, 1);
});

test('Auto falls back from exhausted OpenCode to Cline and records a cooldown', async t => {
  const dir = mkdtempSync(join(tmpdir(), 'freeway-routing-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const providers = [
    { id: 'opencode', kind: 'opencode', priority: 1, limits: {} },
    { id: 'cline', kind: 'cli', priority: 2, limits: {} }
  ];
  const store = new Store(dir);
  for (const provider of providers) store.configure(provider.id, { enabled: true });
  const calls = [];
  const bridges = Object.fromEntries(providers.map(provider => [provider.id, {
    ready: true,
    async models() { return [{ id: provider.id + '/free-test', provider: provider.id, upstreamId: 'free-test',
      free: true, name: 'Free Test', discoveredAt: new Date().toISOString(), capabilities: { stream: true, tools: false } }]; },
    async *complete() {
      calls.push(provider.id);
      if (provider.id === 'opencode') throw new GatewayError('Free quota exhausted', 429, 'rate_limit_exceeded', 600);
      yield { choices: [{ index: 0, delta: { content: 'CLINE_FALLBACK_OK' }, finish_reason: 'stop' }] };
    }
  }]));
  const engine = new Engine(store, { forProvider: id => bridges[id] }, { providers });
  await engine.refresh('opencode');
  await engine.refresh('cline');
  store.data.settings.preferredModels = ['opencode/free-test', 'cline/free-test'];
  const chunks = [];
  for await (const chunk of engine.complete({ model: 'free/auto', messages: [{ role: 'user', content: 'test' }] }, AbortSignal.timeout(1000))) chunks.push(chunk);
  assert.deepEqual(calls, ['opencode', 'cline']);
  assert.equal(chunks[0].provider, 'cline');
  assert.equal(chunks[0].free, true);
  assert.equal(chunks[0].choices[0].delta.content, 'CLINE_FALLBACK_OK');
  assert.ok(store.data.providers.opencode.cooldownUntil > Date.now());
});

function routingFixture(t, { failingModels, healthyModel = 'healthy-free', maxFallbacks = 3, failureStatus = 503 }) {
  const dir = mkdtempSync(join(tmpdir(), 'freeway-routing-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const providers = [
    { id: 'opencode', kind: 'opencode', priority: 1, limits: {} },
    { id: 'cline', kind: 'cli', priority: 2, limits: {} }
  ];
  const store = new Store(dir), calls = [];
  store.data.settings.maxFallbacks = maxFallbacks;
  for (const provider of providers) store.configure(provider.id, { enabled: true });
  const bridges = Object.fromEntries(providers.map(provider => [provider.id, {
    ready: true,
    async models() {
      return (provider.id === 'opencode' ? failingModels : [healthyModel]).map(upstreamId => ({
        id: provider.id + '/' + upstreamId, provider: provider.id, upstreamId,
        free: true, name: upstreamId, discoveredAt: new Date().toISOString(),
        capabilities: { stream: true, tools: false }
      }));
    },
    async *complete(_body, model) {
      calls.push(model.id);
      if (provider.id === 'opencode') throw new GatewayError('Upstream unavailable', failureStatus, 'upstream_error', failureStatus === 503 ? 60 : 0);
      yield { choices: [{ index: 0, delta: { content: 'HEALTHY_ROUTE_OK' }, finish_reason: 'stop' }] };
    }
  }]));
  const engine = new Engine(store, { forProvider: id => bridges[id] }, { providers });
  return { engine, store, calls, async connect() {
    for (const provider of providers) await engine.refresh(provider.id);
    store.data.settings.preferredModels = [...failingModels.map(id => 'opencode/' + id), 'cline/' + healthyModel];
  } };
}

async function completeAuto(engine) {
  const chunks = [];
  for await (const chunk of engine.complete({ model: 'free/auto', messages: [{ role: 'user', content: 'test' }] }, AbortSignal.timeout(1000))) chunks.push(chunk);
  return chunks;
}

test('Auto reaches a healthy route beyond models skipped after a provider cooldown', async t => {
  const fixture = routingFixture(t, { failingModels: ['first-free', 'second-free', 'third-free'] });
  await fixture.connect();
  const chunks = await completeAuto(fixture.engine);
  assert.deepEqual(fixture.calls, ['opencode/first-free', 'cline/healthy-free']);
  assert.equal(chunks[0].choices[0].delta.content, 'HEALTHY_ROUTE_OK');
  assert.equal(fixture.store.data.logs.length, 2);
});

test('Fallback limit still bounds the number of distinct models actually requested', async t => {
  const fixture = routingFixture(t, { failingModels: ['first-free', 'second-free', 'third-free'], maxFallbacks: 2, failureStatus: 502 });
  await fixture.connect();
  await assert.rejects(completeAuto(fixture.engine), { code: 'upstream_error' });
  assert.deepEqual(fixture.calls, ['opencode/first-free', 'opencode/second-free']);
});

test('Alternate free routes for the same model use one fallback slot', async t => {
  const fixture = routingFixture(t, { failingModels: ['nvidia/nemotron-test'], healthyModel: 'nvidia/nemotron-test', maxFallbacks: 1, failureStatus: 502 });
  await fixture.connect();
  const chunks = await completeAuto(fixture.engine);
  assert.deepEqual(fixture.calls, ['opencode/nvidia/nemotron-test', 'cline/nvidia/nemotron-test']);
  assert.equal(chunks[0].provider, 'cline');
});
