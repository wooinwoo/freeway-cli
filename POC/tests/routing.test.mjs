import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Store } from '../src/store.mjs';
import { Engine } from '../src/engine.mjs';
import { GatewayError } from '../src/streams.mjs';

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
