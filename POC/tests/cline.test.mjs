import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { ClineBridge } from '../src/cline.mjs';

const fixture = fileURLToPath(new URL('./fixtures/cline-acp.mjs', import.meta.url));
const provider = { id: 'cline', proof: 'cli-free-catalog' };
function setup(t) {
  const dir = mkdtempSync(join(tmpdir(), 'freeway-cline-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const bridge = new ClineBridge(dir, { fetcher: async () => Response.json({ free: [{ id: 'cline-free/test', name: 'Test' }],
    recommended: [{ id: 'paid/default' }], clinePass: [{ id: 'subscription/model' }] }),
    runner: (binary, args, options) => {
      assert.equal(args[args.indexOf('--auto-approve') + 1], 'false');
      assert.ok(args.includes('--acp'));
      assert.equal(options.env.CLINE_PROVIDER, 'cline');
      assert.ok(!args.some(arg => arg.includes('SECRET_PROMPT')));
      return spawn(process.execPath, [fixture], options);
    } });
  bridge.ready = true;
  return { bridge, dir };
}
async function request(bridge, text, signal = AbortSignal.timeout(3000)) {
  const [model] = await bridge.models(provider);
  let content = '';
  for await (const chunk of bridge.complete({ messages: [{ role: 'user', content: text }] }, model, { signal })) content += chunk.choices[0].delta.content || '';
  return content;
}
test('only the official free array is registered', async t => {
  const { bridge } = setup(t);
  const models = await bridge.models(provider);
  assert.deepEqual(models.map(m => m.upstreamId), ['cline-free/test']);
  await assert.rejects(async () => { for await (const _ of bridge.complete({ messages: [] }, { upstreamId: 'paid/default' }, { signal: AbortSignal.timeout(1000) })) {} }, { code: 'not_free_model' });
});
test('ACP selects the free model before transmitting Unicode conversation on stdin', async t => {
  const original = process.env.CLINE_PROVIDER;
  process.env.CLINE_PROVIDER = 'openrouter';
  t.after(() => { if (original === undefined) delete process.env.CLINE_PROVIDER; else process.env.CLINE_PROVIDER = original; });
  const { bridge, dir } = setup(t);
  assert.equal(await request(bridge, 'SECRET_PROMPT 안녕 🌏\nSecond line'), 'SECRET_PROMPT 안녕 🌏\nSecond line');
  assert.deepEqual(readdirSync(dir), []);
  assert.equal(bridge.children.size, 0);
});
test('tool permissions are denied and free quota errors retain 429 status', async t => {
  const { bridge } = setup(t);
  assert.equal(await request(bridge, 'PERMISSION'), 'Permission denied');
  await assert.rejects(request(bridge, 'QUOTA'), { status: 429, code: 'rate_limit_exceeded' });
});
test('cancellation stops the process and removes the transient workspace', async t => {
  const { bridge, dir } = setup(t);
  await assert.rejects(request(bridge, 'WAIT_FOR_CANCEL', AbortSignal.timeout(200)), { name: 'TimeoutError' });
  assert.equal(bridge.children.size, 0);
  assert.deepEqual(readdirSync(dir), []);
});
