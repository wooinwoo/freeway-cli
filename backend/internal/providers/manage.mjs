#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { emitKeypressEvents } from 'node:readline';
import { Client } from '../../../POC/src/client.mjs';
import { definitions } from '../../../POC/src/catalog.mjs';
import { findExecutable } from '../../../POC/src/executables.mjs';

const root = dirname(fileURLToPath(import.meta.url));
export const providers = definitions.map(p => ({ ...p,
  ...JSON.parse(readFileSync(join(root, p.id, 'auth.json'), 'utf8')) }));
const marker = 'FREEWAY_PROVIDER_OK';
const script = 'backend/internal/providers/manage.mjs';
const commands = new Set(['list', 'auth', 'status', 'quota', 'models', 'select', 'probe', 'ask']);

class UsageError extends Error {}
function parse(args) {
  const options = { command: args[0] || 'help', positional: [], timeout: 45, maxTokens: 256 };
  const flags = { '--from-env': ['fromEnv', ['auth']], '--stdin': ['stdin', ['auth']],
    '--login': ['login', ['auth']], '--confirm-free': ['confirmFree', ['auth']] };
  const values = { '--model': ['model', ['probe', 'ask']], '--timeout': ['timeout', ['probe', 'ask']],
    '--max-tokens': ['maxTokens', ['probe', 'ask']], '--reasoning-effort': ['reasoningEffort', ['probe', 'ask']] };
  if (['--help', '-h', 'help'].includes(options.command)) return { command: 'help' };
  if (!commands.has(options.command)) throw new UsageError('지원 명령: list, auth, status, quota, models, select, probe, ask.');
  for (let i = 1; i < args.length; i++) {
    const a = args[i];
    if (a === '--') { options.positional.push(...args.slice(i + 1)); break; }
    if (a === '--json') continue;
    const entry = flags[a] || values[a];
    if (entry) {
      if (!entry[1].includes(options.command)) throw new UsageError('이 명령에서 지원하지 않는 옵션입니다. --help를 확인하세요.');
      if (flags[a]) options[entry[0]] = true;
      else {
        if (args[i + 1] === undefined || args[i + 1].startsWith('--')) throw new UsageError('옵션 뒤에 값을 입력하세요.');
        options[entry[0]] = args[++i];
      }
    } else if (a.startsWith('-')) throw new UsageError('알 수 없는 옵션입니다. 키는 옵션 인수로 받지 않습니다.');
    else options.positional.push(a);
  }
  const [id = 'all', ...rest] = options.positional;
  options.id = id;
  if (id !== 'all' && !providers.some(p => p.id === id)) throw new UsageError('공급처 ID를 list 명령에서 확인하세요.');
  const expected = options.command === 'select' ? 1 : options.command === 'ask' ? null : 0;
  if (options.command === 'list' && options.positional.length || expected !== null && rest.length !== expected) throw new UsageError('명령 인수를 --help에서 확인하세요.');
  if (['select', 'ask'].includes(options.command) && id === 'all' || id === 'all' && (options.stdin || options.model || options.login)) throw new UsageError('이 옵션은 업체 하나를 지정해야 합니다.');
  if (options.command === 'ask' && !rest.length) throw new UsageError('ask 뒤에 업체 ID와 질문을 입력하세요.');
  if (options.fromEnv && options.stdin) throw new UsageError('--from-env와 --stdin 중 하나만 사용하세요.');
  if (!Number.isInteger(Number(options.timeout)) || Number(options.timeout) < 1 || Number(options.timeout) > 300) throw new UsageError('--timeout은 1~300초입니다.');
  if (!Number.isInteger(Number(options.maxTokens)) || Number(options.maxTokens) < 1 || Number(options.maxTokens) > 32768) throw new UsageError('--max-tokens는 1~32768입니다.');
  options.prompt = rest.join(' ');
  options.selected = options.command === 'select' ? rest[0] : undefined;
  return options;
}

function failure(error) {
  const code = error.name === 'TimeoutError' ? 'request_timeout' : error.name === 'AbortError' ? 'cancelled' : error.code || 'connection_failed';
  const status = error.status || null;
  return { status: status === 429 ? 'quota_exhausted' : ['missing_key', 'needs_key'].includes(code) ? 'needs_key'
    : code === 'needs_free_confirmation' ? 'needs_free_confirmation' : ['cli_login_required', 'needs_terminal'].includes(code) ? 'needs_login' : 'failed',
    error: { code, httpStatus: status } };
}
function problem(code) { return Object.assign(new Error(code), { code }); }

// Secret input stays on stdin; raw mode prevents terminal echo and shell-history storage.
async function readSecret(label) {
  if (!process.stdin.isTTY) throw problem('needs_key');
  process.stderr.write(`${label} (Enter: 건너뛰기): `);
  emitKeypressEvents(process.stdin);
  const raw = process.stdin.isRaw;
  process.stdin.setRawMode(true);
  process.stdin.resume();
  try {
    return await new Promise((resolve, reject) => {
      let value = '';
      const finish = (error) => {
        process.stdin.off('keypress', keypress);
        error ? reject(error) : resolve(value);
      };
      const keypress = (text, key = {}) => {
        if (key.ctrl && key.name === 'c') return finish(Object.assign(new Error('cancelled'), { name: 'AbortError' }));
        if (key.name === 'return' || key.name === 'enter') return finish();
        if (key.name === 'backspace') value = Array.from(value).slice(0, -1).join('');
        else if (!key.ctrl && !key.meta && text && !/[\x00-\x1f\x7f]/.test(text)) value += text;
        if (value.length > 4096) finish(problem('invalid_key'));
      };
      process.stdin.on('keypress', keypress);
    });
  } finally {
    process.stdin.setRawMode(raw);
    process.stdin.pause();
    process.stderr.write('\n');
  }
}

async function readStdin() {
  let value = '';
  for await (const chunk of process.stdin) {
    value += chunk;
    if (value.length > 4098) throw problem('invalid_key');
  }
  return value.trim();
}

async function login(p) {
  if (!process.stdin.isTTY) throw problem('needs_terminal');
  const binary = findExecutable(p.id, process.env[p.id.toUpperCase() + '_BIN']);
  if (!binary) throw problem('cli_not_installed');
  const child = spawn(binary, p.loginArgs, { stdio: ['inherit', 2, 2], shell: false });
  await new Promise((resolve, reject) => {
    child.once('error', () => reject(problem('cli_login_failed')));
    child.once('exit', code => code === 0 ? resolve() : reject(problem('cli_login_failed')));
  });
}

function publicProvider(p) {
  return { id: p.id, name: p.name, kind: p.kind, base: p.base || null, env: p.env || null,
    transport: p.kind === 'openai' ? 'http' : 'cli',
    protocol: p.kind === 'openai' ? 'openai_compatible' : p.kind === 'opencode' ? 'opencode_session' : 'acp',
    requiresCLI: p.kind !== 'openai',
    authUrl: p.authUrl, docsUrl: p.docsUrl, freeBasis: p.proof, quota: p.quota,
    modelSource: p.catalog || p.kind !== 'openai' ? 'remote_catalog' : 'documented_allowlist',
    commands: Object.fromEntries(['auth', 'status', 'quota', 'models', 'probe'].map(cmd => [cmd, [process.execPath, join(root, 'manage.mjs'), cmd, p.id]])) };
}

export async function run(args, deps = {}) {
  const options = parse(args);
  if (options.command === 'help') return { ok: true, usage: [
    `node ${script} list`, `node ${script} auth PROVIDER|all [--from-env|--stdin] [--confirm-free]`,
    `node ${script} auth cline --login`, `node ${script} status [PROVIDER|all]`, `node ${script} quota [PROVIDER|all]`,
    `node ${script} models [PROVIDER|all]`, `node ${script} select PROVIDER MODEL_ID`,
    `node ${script} probe [PROVIDER|all] [--model MODEL_ID] [--timeout 45] [--max-tokens 256] [--reasoning-effort VALUE]`,
    `node ${script} ask PROVIDER [--model MODEL_ID] [--reasoning-effort VALUE] -- "질문"`
  ], notes: ['API 키는 숨김 입력·환경변수·표준입력으로만 받습니다.', '인증 구성과 모델 조회는 실제 추론 성공이 아닙니다.',
    'probe는 실제 무료 사용량을 소비하며 정확한 응답 표식을 검사합니다.', 'list는 서버를 시작하지 않습니다. 다른 명령은 기존 POC 게이트웨이를 사용합니다.'] };
  if (options.command === 'list') return { ok: true, providers: providers.map(publicProvider) };
  const client = deps.client || new Client();
  const env = deps.env || process.env;
  const interactive = deps.interactive ?? Boolean(process.stdin.isTTY);
  const signal = () => deps.signal ? AbortSignal.any([deps.signal, AbortSignal.timeout(Number(options.timeout) * 1000)]) : AbortSignal.timeout(Number(options.timeout) * 1000);
  const api = (path, method = 'GET', body) => client.api(path, { method, body, signal: signal() });
  const state = () => api('/api/state');
  const results = [];
  for (const p of providers.filter(p => options.id === 'all' || p.id === options.id)) {
    deps.signal?.throwIfAborted();
    try {
      let snapshot = await state();
      let connection = snapshot.providers.find(item => item.id === p.id);
      if (options.command === 'auth') {
        if (p.kind !== 'openai') {
          if (options.stdin || options.fromEnv && options.id !== 'all') throw new UsageError('CLI 계정은 auth 업체 --login으로 인증하세요.');
          if (options.login) await (deps.login || login)(p);
          try { await api(`/api/providers/${p.id}/connect`, 'POST', {}); }
          catch (error) {
            if (p.id !== 'cline' || error.code !== 'cli_login_required' || !interactive || options.login) throw error;
            await (deps.login || login)(p);
            await api(`/api/providers/${p.id}/connect`, 'POST', {});
          }
        } else {
          if (options.login) throw new UsageError('API 공급처는 키 입력 또는 --from-env로 인증하세요.');
          const patch = { enabled: true };
          let key;
          if (options.stdin) key = await (deps.readStdin || readStdin)();
          else if (options.fromEnv) key = env[p.env];
          else if (!connection.hasKey && !p.keyOptional && interactive) {
            process.stderr.write(`${p.name}: ${p.authUrl}\n`);
            key = await (deps.readSecret || readSecret)(p.env);
          }
          if (key !== undefined) {
            if (typeof key !== 'string' || !key.trim() || key.length > 4096 || /[\r\n\x00-\x1f\x7f]/.test(key)) throw problem('invalid_key');
            patch.apiKey = key.trim();
            patch.freeConfirmed = Boolean(options.confirmFree);
          }
          if (!patch.apiKey && !connection.hasKey && !p.keyOptional) throw problem('needs_key');
          if (options.confirmFree) patch.freeConfirmed = true;
          await api(`/api/providers/${p.id}`, 'PUT', patch);
          await api(`/api/providers/${p.id}/sync`, 'POST', {});
        }
        snapshot = await state();
        connection = snapshot.providers.find(item => item.id === p.id);
        results.push({ id: p.id, status: 'configured', ready: connection.ready,
          reason: connection.reason, inferenceVerified: false,
          modelCount: snapshot.models.filter(m => m.provider === p.id).length });
        continue;
      }
      if (options.command === 'status') {
        results.push({ id: p.id, enabled: connection.enabled, ready: connection.ready,
          reason: connection.reason, hasKey: connection.hasKey, freeConfirmed: connection.freeConfirmed,
          modelCount: snapshot.models.filter(m => m.provider === p.id).length,
          selectedModel: snapshot.settings.chatDefaults?.model || 'free/auto' });
        continue;
      }
      if (options.command === 'quota') {
        const localRemaining = {};
        const localTokenRemaining = {};
        for (const period of ['minute', 'hour', 'day']) {
          if (connection.limits[period]) localRemaining[period] = Math.max(0, connection.limits[period] - (connection.usage[period]?.requests || 0));
        }
        if (connection.limits.dayTokens) localTokenRemaining.day = Math.max(0, connection.limits.dayTokens
          - (connection.usage.day?.tokens || 0) - (connection.usage.day?.estimatedTokens || 0));
        results.push({ id: p.id, provider: p.id, source: 'local_gateway', authoritative: false,
          observedAt: new Date().toISOString(), ready: connection.ready, reason: connection.reason,
          limits: connection.limits, usage: connection.usage, localRemaining, localTokenRemaining, providerRemaining: null,
          lastFailure: snapshot.logs.find(log => log.provider === p.id && !log.ok) || null });
        continue;
      }
      // A catalog query does not prove authentication for allowlist-only providers.
      if (connection.enabled) {
        if (p.kind !== 'openai') await api(`/api/providers/${p.id}/connect`, 'POST', {});
        else await api(`/api/providers/${p.id}/sync`, 'POST', {});
        snapshot = await state();
        connection = snapshot.providers.find(item => item.id === p.id);
      }
      const models = snapshot.models.filter(m => m.provider === p.id && m.free === true);
      if (options.command === 'models') {
        results.push({ id: p.id, status: 'listed', source: publicProvider(p).modelSource,
          ready: connection.ready, reason: connection.reason,
          models: models.map(m => ({ id: m.id, upstreamId: m.upstreamId, name: m.name, ready: m.ready, reason: m.reason,
            reasoning: m.reasoning || { parameter: null, efforts: [] },
            commands: {
              select: [process.execPath, join(root, 'manage.mjs'), 'select', p.id, m.upstreamId],
              probe: [process.execPath, join(root, 'manage.mjs'), 'probe', p.id, '--model', m.upstreamId],
              ask: [process.execPath, join(root, 'manage.mjs'), 'ask', p.id, '--model', m.upstreamId, '--', 'Hello']
            } })) });
        continue;
      }
      if (['probe', 'ask'].includes(options.command)) {
        if (p.kind === 'openai' && !p.keyOptional && !connection.hasKey) throw problem('needs_key');
        if (!connection.enabled) throw problem('disabled');
        if (p.needsConfirmation && !connection.freeConfirmed) throw problem('needs_free_confirmation');
      }
      const requested = options.selected || options.model;
      const saved = snapshot.settings.chatDefaults?.model;
      const model = requested ? models.find(m => m.id === requested || m.upstreamId === requested)
        : models.find(m => m.id === saved && m.ready) || models.find(m => m.ready);
      if (!model) {
        if (requested || options.command === 'select') throw new UsageError('해당 업체의 등록된 무료 모델을 지정하세요.');
        throw problem(connection.reason || 'no_free_models');
      }
      if (options.command === 'select') {
        const defaults = snapshot.settings.chatDefaults || { maxTokens: 4096, temperature: 0.6 };
        await api('/api/settings', 'PUT', { chatDefaults: { ...defaults, model: model.id } });
        results.push({ id: p.id, status: 'selected', model: model.id });
        continue;
      }
      if (!model.ready) throw problem(model.reason || 'provider_not_ready');
      if (options.reasoningEffort !== undefined && !model.reasoning?.efforts?.includes(options.reasoningEffort)) throw problem('unsupported_reasoning');
      const result = await client.complete({ model: model.id, max_tokens: Number(options.maxTokens),
        ...(options.reasoningEffort !== undefined ? { reasoning_effort: options.reasoningEffort } : {}),
        messages: [{ role: 'user', content: options.command === 'probe'
          ? `Reply with exactly ${marker}. Do not use tools.` : options.prompt }] }, { signal: signal() });
      if (result.provider !== p.id || result.model !== model.id) throw problem('unexpected_route');
      const passed = options.command === 'ask' || result.content.trim() === marker;
      results.push({ id: p.id, status: passed ? 'passed' : 'unexpected_response', model: result.model,
        content: result.content, elapsedMs: result.elapsedMs, usage: result.usage, inferenceVerified: passed });
    } catch (error) {
      if (error instanceof UsageError && options.command === 'select') throw error;
      if (error.name === 'AbortError') throw error;
      results.push({ id: p.id, ...failure(error), inferenceVerified: false });
    }
  }
  return { ok: results.every(r => !r.error && r.status !== 'unexpected_response'), command: options.command, results };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const controller = new AbortController();
  process.once('SIGINT', () => controller.abort());
  try {
    const result = await run(process.argv.slice(2), { signal: controller.signal });
    console.log(JSON.stringify(result, null, 2));
    if (!result.ok) process.exitCode = 1;
  } catch (error) {
    console.log(JSON.stringify({ ok: false, ...failure(error),
      ...(error instanceof UsageError ? { message: error.message } : {}) }));
    process.exitCode = 1;
  }
}
