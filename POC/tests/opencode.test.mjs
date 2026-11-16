import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenCodeBridge, findOpenCode } from '../src/opencode.mjs';
import { findExecutable } from '../src/executables.mjs';
import { mkdirSync, mkdtempSync, writeFileSync, chmodSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';

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
