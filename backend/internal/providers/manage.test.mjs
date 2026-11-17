import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { createGateway } from '../../../POC/server.mjs';
import { Client } from '../../../POC/src/client.mjs';
import { definitions } from '../../../POC/src/catalog.mjs';
import { run, providers } from './manage.mjs';

async function setup(t, { upstreamStatus = 200, reply = 'FREEWAY_PROVIDER_OK' } = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'freeway-provider-commands-'));
  const calls = [];
  const bridge = {
    ready: true, state: () => ({ ready: true }), states: () => ({}),
    forProvider() { return this; }, async start() { return this.state(); }, stop() {},
    async models(p) { return [{ id: `${p.id}/fixture`, upstreamId: 'fixture', provider: p.id,
      name: 'Fixture', free: true, discoveredAt: new Date().toISOString(), capabilities: { stream: true, tools: false } }]; },
    async *complete(body, model) {
      calls.push({ provider: model.provider, body });
      yield { choices: [{ delta: { content: 'FREEWAY_PROVIDER_OK' }, finish_reason: 'stop' }] };
    }
  };
  const fetcher = async (url, options = {}) => {
    const p = definitions.find(p => p.base && url.startsWith(p.base + '/'));
    assert.ok(p, 'only known provider endpoints are contacted');
    calls.push({ provider: p.id, url, authorization: options.headers?.authorization,
      body: options.body && JSON.parse(options.body) });
    if (url.endsWith('/models')) return Response.json({ data: [
      { id: 'fixture-a:free', pricing: { prompt: '0', completion: '0' } },
      { id: 'fixture-b:free', pricing: { prompt: '0', completion: '0' } },
      { id: 'paid', pricing: { prompt: '1', completion: '1' } }
    ] });
    if (upstreamStatus !== 200) return new Response('upstream error', { status: upstreamStatus });
    return Response.json({ choices: [{ message: { content: reply }, finish_reason: 'stop' }] });
  };
  const app = createGateway({ dataDir: dir, providers: definitions.map(p => ({ ...p, env: 'FREEWAY_FIXTURE_' + p.id.toUpperCase() })), bridge, fetcher });
  const address = await app.listen(0);
  t.after(async () => { await app.close(); rmSync(dir, { recursive: true, force: true }); });
  const client = new Client({ base: `http://127.0.0.1:${address.port}`, autoStart: false });
  return { app, calls, deps: { client, env: {}, interactive: false } };
}

test('all eleven providers have authentication metadata and commands without starting a server', async () => {
  const result = await run(['list']);
  assert.equal(result.providers.length, 11);
  assert.deepEqual(providers.map(p => p.id).sort(), definitions.map(p => p.id).sort());
  for (const p of result.providers) {
    assert.ok(p.authUrl.startsWith('https://'));
    assert.ok(p.commands.auth.includes(p.id));
    assert.ok(p.commands.models.includes(p.id));
  }
});

test('API authentication imports the key without exposing it or claiming inference success', async t => {
  const { app, calls, deps } = await setup(t);
  const result = await run(['auth', 'openrouter', '--from-env'], {
    ...deps, env: { OPENROUTER_API_KEY: 'private-fixture-key' }
  });
  assert.equal(result.results[0].status, 'configured');
  assert.equal(result.results[0].inferenceVerified, false);
  assert.equal(app.store.key(definitions.find(p => p.id === 'openrouter')), 'private-fixture-key');
  assert.equal(calls[0].authorization, 'Bearer private-fixture-key');
  assert.ok(!JSON.stringify(result).includes('private-fixture-key'));
  assert.ok(!app.store.data.providers.openrouter.secret.includes('private-fixture-key'));
});

test('bulk authentication reports missing keys separately and does not silently confirm paid accounts', async t => {
  const { app, deps } = await setup(t);
  const result = await run(['auth', 'all', '--from-env'], deps);
  assert.equal(result.results.length, 11);
  assert.equal(result.ok, false);
  assert.equal(result.results.find(p => p.id === 'groq').status, 'needs_key');
  assert.equal(result.results.find(p => p.id === 'cline').status, 'configured');
  assert.notEqual(app.store.data.providers.groq?.freeConfirmed, true);
});

test('seed model registration never counts as an authenticated request', async t => {
  const { calls, deps } = await setup(t);
  const result = await run(['auth', 'groq', '--from-env'], {
    ...deps, env: { GROQ_API_KEY: 'fixture-groq-key' }
  });
  assert.equal(result.results[0].inferenceVerified, false);
  assert.equal(calls.length, 0);
  const probe = await run(['probe', 'groq'], deps);
  assert.equal(probe.results[0].status, 'needs_free_confirmation');
  assert.equal(calls.length, 0);
});

test('replacing an API key invalidates the previous free-account confirmation', async t => {
  const { app, calls, deps } = await setup(t);
  await run(['auth', 'groq', '--from-env', '--confirm-free'], {
    ...deps, env: { GROQ_API_KEY: 'free-account-key' }
  });
  assert.equal(app.store.data.providers.groq.freeConfirmed, true);
  await run(['auth', 'groq', '--from-env'], { ...deps, env: { GROQ_API_KEY: 'different-account-key' } });
  assert.equal(app.store.data.providers.groq.freeConfirmed, false);
  const result = await run(['probe', 'groq'], deps);
  assert.equal(result.results[0].status, 'needs_free_confirmation');
  assert.equal(calls.length, 0);
});

test('unconfirmed NVIDIA and Vikasit are also excluded by the shared gateway', async t => {
  for (const id of ['nvidia', 'vikasit']) {
    const { app, deps } = await setup(t);
    app.store.configure('kilo', { enabled: false });
    const p = providers.find(p => p.id === id);
    await run(['auth', id, '--from-env'], { ...deps, env: { [p.env]: 'fixture-key' } });
    assert.equal(app.engine.state().providers.find(p => p.id === id).ready, false);
    assert.ok(app.engine.models().filter(m => m.provider === id).every(m => !m.ready));
    await assert.rejects(app.engine.candidates({ model: 'free/auto', messages: [{ role: 'user', content: 'Hello' }] }),
      { code: 'no_free_models' });
  }
});

test('model selection persists, preserves settings, and probes the exact provider/model', async t => {
  const { app, calls, deps } = await setup(t);
  await run(['auth', 'kilo'], deps);
  const models = await run(['models', 'kilo'], deps);
  assert.deepEqual(models.results[0].models.map(m => m.id), ['kilo/fixture-a:free', 'kilo/fixture-b:free']);
  assert.ok(models.results[0].models.every(m => m.commands.select.at(-1) === m.upstreamId));
  app.store.data.settings.chatDefaults = { model: 'free/auto', maxTokens: 777, temperature: 0.4 };
  app.store.save();
  await run(['select', 'kilo', 'fixture-b:free'], deps);
  assert.equal(app.store.data.settings.chatDefaults.model, 'kilo/fixture-b:free');
  assert.equal(app.store.data.settings.chatDefaults.maxTokens, 777);
  const probe = await run(['probe', 'kilo'], deps);
  assert.equal(probe.results[0].status, 'passed');
  assert.equal(calls.at(-1).body.model, 'fixture-b:free');
  assert.equal(calls.at(-1).provider, 'kilo');
  assert.deepEqual(calls.at(-1).body.messages, [{ role: 'user', content: 'Reply with exactly FREEWAY_PROVIDER_OK. Do not use tools.' }]);
});

test('ask uses the selected model and preserves a Unicode prompt with option-like text', async t => {
  const { calls, deps } = await setup(t);
  await run(['auth', 'kilo'], deps);
  await run(['select', 'kilo', 'fixture-b:free'], deps);
  const result = await run(['ask', 'kilo', '--', '안녕 🌏 --model paid'], deps);
  assert.equal(result.results[0].status, 'passed');
  assert.equal(calls.at(-1).body.model, 'fixture-b:free');
  assert.equal(calls.at(-1).body.messages[0].content, '안녕 🌏 --model paid');
});

test('quota and authentication errors never fall back to another provider', async t => {
  for (const upstreamStatus of [401, 403, 429]) {
    const { calls, deps } = await setup(t, { upstreamStatus });
    await run(['auth', 'kilo'], deps);
    const result = await run(['probe', 'kilo'], deps);
    assert.equal(result.ok, false);
    assert.equal(result.results[0].error.httpStatus, upstreamStatus);
    assert.equal(result.results[0].status, upstreamStatus === 429 ? 'quota_exhausted' : 'failed');
    assert.equal(calls.filter(c => c.body).length, 1);
  }
});

test('a successful HTTP response with the wrong assistant answer fails the probe', async t => {
  const { deps } = await setup(t, { reply: 'I could not finish the request.' });
  await run(['auth', 'kilo'], deps);
  const result = await run(['probe', 'kilo'], deps);
  assert.equal(result.ok, false);
  assert.equal(result.results[0].status, 'unexpected_response');
  assert.equal(result.results[0].inferenceVerified, false);
});

test('stdin authentication accepts a key without putting it in argv or output', async t => {
  const { app, deps } = await setup(t);
  const result = await run(['auth', 'openrouter', '--stdin'], {
    ...deps, readStdin: async () => 'stdin-private-key'
  });
  assert.equal(result.ok, true);
  assert.equal(app.store.key(definitions.find(p => p.id === 'openrouter')), 'stdin-private-key');
  assert.ok(!JSON.stringify(result).includes('stdin-private-key'));
});

test('the actual command accepts a key on stdin and emits a single secret-free JSON result', async t => {
  const { app, deps } = await setup(t);
  const child = spawn(process.execPath, [fileURLToPath(new URL('./manage.mjs', import.meta.url)),
    'auth', 'openrouter', '--stdin'], { env: { ...process.env, FREEWAY_URL: deps.client.base }, stdio: ['pipe', 'pipe', 'pipe'] });
  let stdout = '', stderr = '';
  child.stdout.on('data', chunk => { stdout += chunk; });
  child.stderr.on('data', chunk => { stderr += chunk; });
  const closed = new Promise((resolve, reject) => { child.once('error', reject); child.once('close', resolve); });
  child.stdin.end('subprocess-private-key\n');
  assert.equal(await closed, 0);
  assert.equal(JSON.parse(stdout).ok, true);
  assert.equal(app.store.key(definitions.find(p => p.id === 'openrouter')), 'subprocess-private-key');
  assert.ok(!stdout.includes('subprocess-private-key'));
  assert.ok(!stderr.includes('subprocess-private-key'));
});

test('paid, unknown and other-provider models are rejected before inference', async t => {
  const { calls, deps } = await setup(t);
  await run(['auth', 'kilo'], deps);
  for (const id of ['paid', 'missing', 'openrouter/fixture-a:free', 'free/auto']) {
    await assert.rejects(run(['select', 'kilo', id], deps), /무료 모델/);
    const result = await run(['probe', 'kilo', '--model', id], deps);
    assert.equal(result.results[0].status, 'failed');
  }
  assert.ok(calls.every(c => !c.body));
});

test('CLI authentication uses official argv and probes through its text-only bridge', async t => {
  const { deps, calls } = await setup(t);
  const logins = [];
  const result = await run(['auth', 'cline', '--login'], {
    ...deps, login: async (p) => { logins.push(p.loginArgs); }
  });
  assert.deepEqual(logins, [['auth', 'cline']]);
  assert.equal(result.results[0].status, 'configured');
  const probe = await run(['probe', 'cline', '--model', 'fixture'], deps);
  assert.equal(probe.results[0].status, 'passed');
  assert.equal(calls.at(-1).body.model, 'cline/fixture');
});

test('argument and secret validation fails before sending changes', async t => {
  const { deps, calls } = await setup(t);
  for (const args of [['auth', 'unknown'], ['auth', 'kilo', '--key', 'secret'],
    ['auth', 'all', '--stdin'], ['probe', 'all', '--model', 'fixture'], ['status', 'kilo', '--confirm-free']]) {
    await assert.rejects(run(args, deps));
  }
  const result = await run(['auth', 'openrouter', '--from-env'], {
    ...deps, env: { OPENROUTER_API_KEY: 'secret\nheader-injection' }
  });
  assert.equal(result.results[0].status, 'failed');
  assert.ok(!JSON.stringify(result).includes('header-injection'));
  assert.equal(calls.length, 0);
});
