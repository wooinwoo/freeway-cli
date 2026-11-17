import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenCodeBridge, findOpenCode } from '../src/opencode.mjs';
import { findExecutable } from '../src/executables.mjs';
import { mkdirSync, mkdtempSync, writeFileSync, chmodSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { EventEmitter } from 'node:events';

test('OpenCode retains only an approval-gated read schema and blocks all other tools', async t => {
  const dir = mkdtempSync(join(tmpdir(), 'freeway-opencode-config-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  let config;
  const child = Object.assign(new EventEmitter(), { stdout: { resume() {} }, stderr: { resume() {} }, kill() {} });
  const bridge = new OpenCodeBridge(dir, {
    runner: (_binary, _args, options) => { config = JSON.parse(options.env.OPENCODE_CONFIG_CONTENT); return child; },
    fetcher: async () => Response.json({ version: '1.18.35' })
  });
  bridge.binary = process.execPath;
  try {
    await bridge.start();
    assert.deepEqual(config.permission, { '*': 'deny', read: 'ask' });
    assert.deepEqual(config.agent.freeway.permission, { '*': 'deny', read: 'ask' });
    assert.equal(config.agent.freeway.steps, undefined);
    assert.equal(config.agent.freeway.tools, undefined);
  } finally { bridge.stop(); }
});

function protocolBridge(events, { replyStatus = 200, wait = false, fastResult = false } = {}) {
  const calls = [];
  const bridge = new OpenCodeBridge('/tmp/freeway-opencode-protocol-fixture', { fetcher: async (url, options = {}) => {
    const path = new URL(url).pathname;
    calls.push({ path, body: options.body && JSON.parse(options.body) });
    if (path === '/session') {
      assert.deepEqual(JSON.parse(options.body).permission, [
        { permission: '*', pattern: '*', action: 'deny' },
        { permission: 'read', pattern: '*', action: 'ask' }
      ]);
      return Response.json({ id: 'session-1' });
    }
    if (path === '/event') return new Response(events.map(event => 'data: ' + JSON.stringify(event) + '\n\n').join(''));
    if (path === '/permission/request-1/reply') return Response.json({}, { status: replyStatus });
    if (path.endsWith('/message')) {
      const body = JSON.parse(options.body);
      assert.equal(body.tools, undefined, 'legacy tool flags can rewrite session permissions and must not be sent');
      if (wait) return new Promise((resolve, reject) => {
        if (options.signal.aborted) reject(options.signal.reason);
        else options.signal.addEventListener('abort', () => reject(options.signal.reason), { once: true });
      });
      if (!fastResult) await new Promise(resolve => setImmediate(resolve));
      return Response.json({ info: { tokens: { input: 4, output: 2 } }, parts: [{ type: 'text', text: '안녕 🌏' }] });
    }
    return Response.json({});
  } });
  bridge.base = 'http://127.0.0.1:1'; bridge.ready = true;
  return { bridge, calls };
}

const freeModel = { upstreamId: 'free-test', opencodeProvider: 'opencode' };
const permissionEvent = { type: 'permission.asked', properties: {
  id: 'request-1', sessionID: 'session-1', permission: 'read', patterns: ['canary.txt']
} };

test('OpenCode sends a supported reasoning variant without weakening tool permissions', async () => {
  const { bridge, calls } = protocolBridge([]);
  const model = { ...freeModel, reasoning: { parameter: 'variant', efforts: ['low', 'high'] } };
  for await (const _ of bridge.complete({ reasoning_effort: 'high', messages: [{ role: 'user', content: 'test' }] }, model,
    { signal: AbortSignal.timeout(1000) })) {}
  assert.equal(calls.find(c => c.path.endsWith('/message')).body.variant, 'high');
  const before = calls.length;
  await assert.rejects(async () => {
    for await (const _ of bridge.complete({ reasoning_effort: 'max', messages: [] }, model,
      { signal: AbortSignal.timeout(1000) })) {}
  }, { code: 'unsupported_reasoning' });
  assert.equal(calls.length, before);
});

test('OpenCode streams conversation without executing tools and preserves usage', async () => {
  const { bridge } = protocolBridge([
    { type: 'message.updated', properties: { info: { id: 'assistant-1', role: 'assistant', sessionID: 'session-1' } } },
    { type: 'message.part.updated', properties: { part: { id: 'reasoning-1', messageID: 'assistant-1', sessionID: 'session-1', type: 'reasoning', text: '' } } },
    { type: 'message.part.delta', properties: { partID: 'reasoning-1', messageID: 'assistant-1', sessionID: 'session-1', field: 'text', delta: 'Internal reasoning must stay out of the answer.' } },
    { type: 'message.part.updated', properties: { part: { id: 'part-1', messageID: 'assistant-1', sessionID: 'session-1', type: 'text', text: '안녕 ' } } }
  ]);
  const chunks = [];
  for await (const chunk of bridge.complete({ messages: [{ role: 'user', content: 'Hello' }] }, freeModel,
    { signal: AbortSignal.timeout(1000) })) chunks.push(chunk);
  assert.equal(chunks.map(c => c.choices[0].delta.content || '').join(''), '안녕 🌏');
  assert.equal(chunks.at(-1).usage.total_tokens, 6);
});

test('OpenCode rejects read permission before aborting and deleting its session', async () => {
  const { bridge, calls } = protocolBridge([permissionEvent], { wait: true });
  await assert.rejects(async () => {
    for await (const _ of bridge.complete({ messages: [{ role: 'user', content: 'Read a canary file' }] }, freeModel,
      { signal: AbortSignal.timeout(1000) })) {}
  }, { code: 'unsupported_capability' });
  assert.deepEqual(calls.find(c => c.path === '/permission/request-1/reply').body, { reply: 'reject' });
  assert.ok(calls.some(c => c.path === '/session/session-1/abort'));
  assert.ok(calls.some(c => c.path === '/session/session-1' && !c.body));
});

test('OpenCode fails closed when replying to a permission request fails', async () => {
  const { bridge, calls } = protocolBridge([permissionEvent], { replyStatus: 500, wait: true });
  await assert.rejects(async () => {
    for await (const _ of bridge.complete({ messages: [{ role: 'user', content: 'Read a canary file' }] }, freeModel,
      { signal: AbortSignal.timeout(1000) })) {}
  }, { code: 'tool_rejection_failed' });
  assert.ok(calls.some(c => c.path.endsWith('/abort')));
  assert.ok(!calls.some(c => c.body?.reply === 'once' || c.body?.reply === 'always'));
});

test('OpenCode ignores permission requests belonging to another session', async () => {
  const { bridge, calls } = protocolBridge([{ ...permissionEvent, properties: { ...permissionEvent.properties, sessionID: 'another-session' } }]);
  for await (const _ of bridge.complete({ messages: [{ role: 'user', content: 'Hello' }] }, freeModel,
    { signal: AbortSignal.timeout(1000) })) {}
  assert.ok(!calls.some(c => c.path.startsWith('/permission/')));
});

test('OpenCode ignores late deltas after receiving the authoritative final response', async () => {
  const { bridge } = protocolBridge([
    { type: 'message.updated', properties: { info: { id: 'assistant-1', role: 'assistant', sessionID: 'session-1' } } },
    { type: 'message.part.updated', properties: { part: { id: 'part-1', messageID: 'assistant-1', sessionID: 'session-1', type: 'text', text: '안녕 ' } } }
  ], { fastResult: true });
  let text = '';
  for await (const chunk of bridge.complete({ messages: [{ role: 'user', content: 'Hello' }] }, freeModel,
    { signal: AbortSignal.timeout(1000) })) text += chunk.choices[0].delta.content || '';
  assert.equal(text, '안녕 🌏');
});

test('OpenCode executable discovery includes the current PATH', t => {
  if (process.platform === 'win32') return;
  const dir = mkdtempSync(join(tmpdir(), 'freeway-bin-'));
  const original = process.env.PATH, override = process.env.OPENCODE_BIN;
  t.after(() => { process.env.PATH = original; if (override === undefined) delete process.env.OPENCODE_BIN; else process.env.OPENCODE_BIN = override; rmSync(dir, { recursive: true, force: true }); });
  delete process.env.OPENCODE_BIN;
  const binary = join(dir, 'opencode');
  writeFileSync(binary, '#!/bin/sh\nexit 0\n'); chmodSync(binary, 0o700);
  process.env.PATH = dir;
  assert.equal(findExecutable('opencode'), binary);
  assert.equal(findOpenCode(), binary);
});
test('Windows npm shims resolve to the packaged native executable', t => {
  const platform = Object.getOwnPropertyDescriptor(process, 'platform');
  const original = process.env.PATH;
  const dir = mkdtempSync(join(tmpdir(), 'freeway-windows-bin-'));
  t.after(() => { Object.defineProperty(process, 'platform', platform); process.env.PATH = original; rmSync(dir, { recursive: true, force: true }); });
  writeFileSync(join(dir, 'opencode.cmd'), 'exit 1');
  const nativeDir = join(dir, 'node_modules', 'opencode-ai', 'bin');
  mkdirSync(nativeDir, { recursive: true });
  const native = join(nativeDir, 'opencode.exe');
  writeFileSync(native, 'fixture');
  Object.defineProperty(process, 'platform', { value: 'win32', configurable: true });
  process.env.PATH = dir;
  assert.equal(findExecutable('opencode'), native);
  assert.equal(findExecutable('opencode', join(dir, 'opencode.cmd')), null);
});
test('OpenCode free usage exhaustion becomes a rate limit without trying a paid model', async () => {
  const bridge = new OpenCodeBridge('/tmp/unused', { fetcher: async (url, options) => {
    if (url.endsWith('/session')) return Response.json({ id: 'session-1' });
    if (url.endsWith('/event')) return new Response('data: ' + JSON.stringify({ type: 'session.status', properties: {
      sessionID: 'session-1', status: { type: 'retry', message: 'Free usage exceeded, subscribe to Go', action: { reason: 'free_tier_limit' } }
    } }) + '\n\n');
    if (url.endsWith('/message')) return new Promise((resolve, reject) => options.signal.addEventListener('abort', () => reject(options.signal.reason), { once: true }));
    return Response.json({});
  } });
  bridge.base = 'http://127.0.0.1:1'; bridge.ready = true;
  await assert.rejects(async () => {
    for await (const _ of bridge.complete({ messages: [{ role: 'user', content: 'test' }] }, { upstreamId: 'free-test', opencodeProvider: 'opencode' }, { signal: AbortSignal.timeout(1000) })) {}
  }, { status: 429, code: 'rate_limit_exceeded' });
});
