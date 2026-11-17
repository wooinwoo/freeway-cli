import { spawn } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { randomBytes } from 'node:crypto';
import { createServer } from 'node:net';
import { setTimeout as delay } from 'node:timers/promises';
import { AsyncQueue, sseEvents, GatewayError } from './streams.mjs';
import { normalizeModel, catalogModel } from './catalog.mjs';
import { findExecutable } from './executables.mjs';

// Keep a read schema for OpenCode's free route; every read still requires approval and is rejected below.
const toolPermission = { '*': 'deny', read: 'ask' };

function modelError(error) {
  const description = JSON.stringify(error || {});
  if (/free usage (?:limit|exceeded)|free_tier_limit|rate.?limit|quota|too many requests|usage limit reached/i.test(description)) {
    return new GatewayError('OpenCode 무료 사용 한도에 도달했습니다. 다른 무료 공급처를 사용하거나 한도 갱신 후 다시 시도하세요.', 429, 'rate_limit_exceeded', 600);
  }
  const status = error?.data?.statusCode || error?.statusCode;
  return new GatewayError('OpenCode 모델이 요청을 완료하지 못했습니다.', [401,403,429].includes(status) ? status : 502, 'opencode_model_error');
}

export function findOpenCode() {
  const detected = findExecutable('opencode', process.env.OPENCODE_BIN);
  if (detected) return detected;
  const candidates = process.platform === 'win32'
    ? [join(process.env.APPDATA || '', 'npm', 'node_modules', 'opencode-ai', 'bin', 'opencode.exe')]
    : ['/usr/local/bin/opencode', join(process.env.HOME || '', '.opencode', 'bin', 'opencode'), '/opt/homebrew/bin/opencode'];
  return candidates.find(existsSync) || null;
}

async function freePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
  });
}

export class OpenCodeBridge {
  constructor(dir, { fetcher = fetch, runner = spawn } = {}) {
    this.dir = dir; this.fetcher = fetcher; this.runner = runner; this.ready = false; this.child = null;
    this.binary = findOpenCode(); this.version = null; this.base = null;
    this.lastError = null; this.password = randomBytes(24).toString('hex');
  }
  headers() { return { authorization: 'Basic ' + Buffer.from('opencode:' + this.password).toString('base64'), 'content-type': 'application/json' }; }
  state() { return { installed: Boolean(this.binary), ready: this.ready, managed: Boolean(this.child), lastError: this.lastError, version: this.version }; }
  async start() {
    if (this.ready) return this.state();
    if (this.starting) return this.starting;
    this.starting = this.startInternal().finally(() => { this.starting = null; });
    return this.starting;
  }
  async startInternal() {
    if (!this.binary) throw new GatewayError('OpenCode 실행 파일을 찾지 못했습니다. OPENCODE_BIN을 지정하세요.', 409, 'opencode_not_installed');
    mkdirSync(this.dir, { recursive: true });
    const port = await freePort();
    this.base = 'http://127.0.0.1:' + port;
    const config = {
      enabled_providers: ['opencode'], share: 'disabled', compaction: { auto: false },
      permission: toolPermission,
      agent: { title: { disable: true }, summary: { disable: true }, compaction: { disable: true }, freeway: {
        description: 'Conversational gateway without filesystem or command access.',
        mode: 'primary', permission: toolPermission,
        prompt: 'Answer the user conversationally. Do not use any tools, inspect local files, or execute commands.'
      } }
    };
    this.child = this.runner(this.binary, ['serve', '--hostname', '127.0.0.1', '--port', String(port)], {
      cwd: this.dir, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'],
      env: { ...process.env, OPENCODE_SERVER_PASSWORD: this.password, OPENCODE_SERVER_USERNAME: 'opencode',
        OPENCODE_DISABLE_AUTOUPDATE: 'true', OPENCODE_DISABLE_SHARE: 'true',
        OPENCODE_CONFIG_CONTENT: JSON.stringify(config) }
    });
    let processError;
    this.child.on('error', error => { processError = error; this.ready = false; });
    this.child.on('exit', () => { this.ready = false; this.child = null; });
    this.child.stdout.resume();
    this.child.stderr.resume();
    for (let i = 0; i < 160; i++) {
      if (processError) break;
      try {
        const response = await this.fetcher(this.base + '/global/health', { headers: this.headers(), signal: AbortSignal.timeout(1500) });
        if (response.ok) {
          const data = await response.json();
          this.version = data.version || null;
          this.ready = true; this.lastError = null;
          return this.state();
        }
      } catch {}
      await delay(250);
    }
    this.lastError = processError?.message || 'OpenCode 서버 시작 시간이 초과되었습니다.';
    this.stop();
    throw new GatewayError(this.lastError, 503, 'opencode_start_failed');
  }
  stop() {
    const child = this.child;
    this.child = null; this.ready = false;
    child?.kill();
  }
  async models(provider) {
    if (!this.ready) return [];
    let response = await this.fetcher(this.base + '/provider', { headers: this.headers(), signal: AbortSignal.timeout(20000) });
    if (!response.ok) response = await this.fetcher(this.base + '/config/providers', { headers: this.headers(), signal: AbortSignal.timeout(20000) });
    if (!response.ok) throw new GatewayError('OpenCode 모델 목록 조회 실패', 502, 'catalog_failed');
    const data = await response.json();
    const all = data.all || data.providers || [];
    const list = [],catalog=[];
    for (const p of all) {
      // Only OpenCode's own free pool. Other provider keys can be paid.
      if (p.id !== 'opencode') continue;
      for (const model of Object.values(p.models || {})) {
        const entry=catalogModel(provider,model);entry.capabilities={stream:true,tools:false};entry.opencodeProvider=p.id;catalog.push(entry);
        if (model.status === 'deprecated' || model.disabled) continue;
        if (!model.cost || model.cost.input !== 0 || model.cost.output !== 0) continue;
        const normalized = normalizeModel(provider, model);
        normalized.opencodeProvider = p.id;
        normalized.capabilities = { stream: true, tools: false };
        list.push(normalized);
      }
    }
    Object.defineProperty(list,'catalog',{value:catalog});
    return list;
  }
  async *complete(body, model, { signal }) {
    if (!this.ready) throw new GatewayError('OpenCode 서버가 연결되어 있지 않습니다.', 503, 'opencode_disconnected');
    if (body.tools?.length || body.messages.some(m => m.role === 'tool' || m.tool_calls)) {
      throw new GatewayError('OpenCode 브리지는 현재 텍스트 대화만 지원합니다. 도구 호출은 API 공급처를 선택하세요.', 400, 'unsupported_capability');
    }
    if (body.messages.some(m => Array.isArray(m.content) && m.content.some(p => p.type !== 'text'))) {
      throw new GatewayError('OpenCode 브리지는 현재 텍스트 입력만 지원합니다.', 400, 'unsupported_capability');
    }
    const upstreamController = new AbortController();
    signal = AbortSignal.any([signal, upstreamController.signal]);
    const request = async (path, options = {}) => {
      const r = await this.fetcher(this.base + path, { ...options, headers: this.headers() });
      if (!r.ok) throw new GatewayError('OpenCode 요청 실패 (HTTP ' + r.status + ')', r.status, 'opencode_error');
      return r;
    };
    const created = await request('/session', { method: 'POST', body: JSON.stringify({ title: 'Freeway chat',
      permission: Object.entries(toolPermission).map(([permission, action]) => ({ permission, pattern: '*', action }))
    }), signal });
    const session = await created.json();
    const eventController = new AbortController();
    const eventsSignal = AbortSignal.any([signal, eventController.signal]);
    const queue = new AsyncQueue();
    const partTexts = new Map();
    const assistantMessages = new Set();
    let emitted = '', completed = false, failed = false, blockedTool = false, resultReceived = false;
    const fail = error => {
      if (failed) return;
      failed = true; queue.push({ type: 'error', error }); queue.end();
    };
    const abortSession = () => {
      this.fetcher(this.base + '/session/' + session.id + '/abort', {
        method: 'POST', headers: this.headers(), signal: AbortSignal.timeout(3000)
      }).catch(() => {});
    };
    signal.addEventListener('abort', abortSession, { once: true });
    try {
      const events = await request('/event', { signal: eventsSignal });
      const eventTask = (async () => {
        try {
          for await (const raw of sseEvents(events.body)) {
            const envelope = JSON.parse(raw);
            const event = envelope.payload || envelope;
            const props = event.properties || {};
            if (event.type === 'permission.asked' && props.sessionID === session.id) {
              blockedTool = true;
              try {
                if (typeof props.id !== 'string' || !props.id) throw new Error('Missing permission ID');
                await request('/permission/' + encodeURIComponent(props.id) + '/reply', {
                  method: 'POST', signal, body: JSON.stringify({ reply: 'reject' })
                });
                fail(new GatewayError('OpenCode의 도구 실행 요청을 거절했습니다. 이 연결은 텍스트 대화만 지원합니다.', 400, 'unsupported_capability'));
              } catch {
                fail(new GatewayError('OpenCode 도구 권한을 거절하지 못해 요청을 중지했습니다.', 502, 'tool_rejection_failed'));
              }
              abortSession();
              continue;
            }
            if (event.type === 'session.error' && props.sessionID === session.id) fail(modelError(props.error));
            if (event.type === 'session.status' && props.sessionID === session.id && props.status?.type === 'retry') {
              const retry = modelError(props.status);
              fail(retry.status === 429 ? retry : new GatewayError('OpenCode 공급처가 요청을 완료하지 못해 재시도 중입니다. 잠시 후 다시 시도하세요.', 503, 'upstream_retry', 60));
            }
            if (event.type === 'message.updated' && props.info?.sessionID === session.id && props.info.role === 'assistant') {
              assistantMessages.add(props.info.id);
              if (props.info.error) fail(modelError(props.info.error));
            }
            if (event.type === 'message.part.updated') {
              const part = props.part;
              if (part?.sessionID !== session.id || part.type !== 'text' || !assistantMessages.has(part.messageID)) continue;
              const previous = partTexts.get(part.id) || '';
              const text = part.text || '';
              if (text.startsWith(previous)) {
                const delta = text.slice(previous.length);
                if (delta) queue.push({ type: 'text', text: delta });
              }
              partTexts.set(part.id, text);
            }
            if (event.type === 'message.part.delta' && props.sessionID === session.id && props.field === 'text' && assistantMessages.has(props.messageID) && partTexts.has(props.partID)) {
              const delta = props.delta || '';
              partTexts.set(props.partID, (partTexts.get(props.partID) || '') + delta);
              if (delta) queue.push({ type: 'text', text: delta });
            }
          }
        } catch { /* Final message response is the authoritative fallback. */ }
      })();
      const system = body.messages.filter(m => m.role === 'system' || m.role === 'developer').map(m => typeof m.content === 'string' ? m.content : JSON.stringify(m.content)).join('\n\n');
      const history = body.messages.filter(m => m.role !== 'system' && m.role !== 'developer').map(m => {
        const content = Array.isArray(m.content) ? m.content.map(p => p.text || '').join('\n') : m.content || '';
        return '[' + m.role + ']\n' + content;
      }).join('\n\n');
      const postTask = (async () => {
        try {
          const response = await request('/session/' + session.id + '/message', {
            method: 'POST', signal, body: JSON.stringify({
              model: { providerID: model.opencodeProvider, modelID: model.upstreamId },
              agent: 'freeway', system,
              parts: [{ type: 'text', text: 'Continue this conversation and answer the last user message:\n\n' + history }]
            })
          });
          const result = await response.json();
          if (blockedTool) return;
          if (result.info?.error) throw modelError(result.info.error);
          queue.push({ type: 'result', result });
        } catch (error) { queue.push({ type: 'error', error }); }
        finally { if (!blockedTool) queue.end(); }
      })();
      for await (const event of queue) {
        if (event.type === 'error') throw event.error;
        if (event.type === 'text') {
          if (resultReceived) continue;
          emitted += event.text;
          yield { choices: [{ index: 0, delta: { content: event.text }, finish_reason: null }] };
        } else {
          resultReceived = true;
          const text = (event.result.parts || []).filter(p => p.type === 'text').map(p => p.text || '').join('\n');
          if (text.startsWith(emitted) && text.length > emitted.length) {
            yield { choices: [{ index: 0, delta: { content: text.slice(emitted.length) }, finish_reason: null }] };
            emitted = text;
          }
          if (!emitted) throw new GatewayError('OpenCode가 텍스트 응답을 반환하지 않았습니다.', 502, 'empty_completion');
          const t = event.result.info?.tokens;
          const usage = t && {
            prompt_tokens: (t.input || 0) + (t.cache?.read || 0) + (t.cache?.write || 0),
            completion_tokens: (t.output || 0) + (t.reasoning || 0)
          };
          if (usage) usage.total_tokens = usage.prompt_tokens + usage.completion_tokens;
          yield { choices: [{ index: 0, delta: {}, finish_reason: 'stop' }], ...(usage ? { usage } : {}) };
        }
      }
      await postTask;
      completed = true;
      eventController.abort();
      await eventTask;
    } finally {
      signal.removeEventListener('abort', abortSession);
      if (!completed) abortSession();
      upstreamController.abort();
      eventController.abort();
      queue.end();
      this.fetcher(this.base + '/session/' + session.id, { method: 'DELETE', headers: this.headers(), signal: AbortSignal.timeout(3000) }).catch(() => {});
    }
  }
}
