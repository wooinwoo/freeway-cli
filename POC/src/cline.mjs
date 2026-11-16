import { spawn } from 'node:child_process';
import { mkdirSync, readFileSync, mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { homedir } from 'node:os';
import { createInterface } from 'node:readline';
import { findExecutable } from './executables.mjs';
import { normalizeModel } from './catalog.mjs';
import { GatewayError, AsyncQueue } from './streams.mjs';

const clineCatalog = 'https://api.cline.bot/api/v1/ai/cline/recommended-models';
const textOnly = 'You are a conversational assistant. Answer the last user message using the provided conversation. Do not use tools, access files, execute commands, or delegate tasks.';

export class ClineBridge {
  constructor(dir, { fetcher = fetch, runner = spawn } = {}) {
    this.id = 'cline'; this.dir = dir; this.fetcher = fetcher; this.runner = runner;
    this.binary = findExecutable('cline', process.env.CLINE_BIN);
    this.ready = false; this.lastError = null; this.children = new Map(); this.freeIds = new Set();
  }
  state() { return { installed: Boolean(this.binary), ready: this.ready, lastError: this.lastError }; }
  async start() {
    if (!this.binary) throw new GatewayError('Install Cline CLI and set CLINE_BIN if it is not on PATH.', 409, 'cli_not_installed');
    let authenticated = false;
    try {
      const settings = JSON.parse(readFileSync(join(homedir(), '.cline', 'data', 'settings', 'providers.json'), 'utf8'));
      authenticated = Boolean(settings.providers?.cline?.settings?.auth?.accessToken);
    } catch {}
    if (!authenticated) {
      this.ready = false;
      throw new GatewayError('Sign in first: cline auth cline', 409, 'cli_login_required');
    }
    mkdirSync(this.dir, { recursive: true });
    this.ready = true; this.lastError = null;
    return this.state();
  }
  stop() { for (const stop of this.children.values()) stop(); this.ready = false; }
  async models(provider) {
    if (!this.ready) return [];
    const response = await this.fetcher(clineCatalog, { signal: AbortSignal.timeout(20000) });
    if (!response.ok) throw new GatewayError('Cline free catalog is unavailable.', 502, 'catalog_failed');
    const data = await response.json();
    if (!Array.isArray(data.free)) throw new GatewayError('Invalid Cline free catalog.', 502, 'invalid_catalog');
    // Only the official free array. Recommended and ClinePass entries can be paid.
    const models = data.free.filter(m => typeof m.id === 'string' && m.id.length > 0);
    this.freeIds = new Set(models.map(m => m.id));
    return models.map(m => ({ ...normalizeModel(provider, m), capabilities: { stream: true, tools: false } }));
  }
  async *complete(body, model, { signal }) {
    if (!this.ready) throw new GatewayError('CLI provider is disconnected.', 503, 'cli_disconnected');
    if (!this.freeIds.has(model.upstreamId)) throw new GatewayError('Model is not in the current free CLI catalog.', 400, 'not_free_model');
    if (body.tools?.length || body.messages.some(m => m.tool_calls || m.role === 'tool' || Array.isArray(m.content) && m.content.some(p => p.type !== 'text'))) {
      throw new GatewayError('CLI providers support text conversations only.', 400, 'unsupported_capability');
    }
    signal.throwIfAborted();
    const workspace = mkdtempSync(join(this.dir, 'request-'));
    const history = body.messages.map(m => `[${m.role}]\n${Array.isArray(m.content) ? m.content.map(p => p.text || '').join('\n') : m.content || ''}`).join('\n\n');
    const env = { ...process.env };
    env.CLINE_PROVIDER = 'cline';
    delete env.CLINE_API_KEY;
    const args = ['--acp', '--auto-approve', 'false', '--provider', 'cline',
        '--compaction', 'off', '--retries', '1', '--timeout', '120', '--cwd', workspace,
        '--hooks-dir', workspace, '--system', textOnly];
    const queue = new AsyncQueue();
    let child, lines, closed, stderr = '', emitted = '', sessionId, terminal = false, failure;
    const stop = () => {
      if (!child?.pid || child.exitCode !== null || child.signalCode !== null) return;
      if (process.platform === 'win32') {
        this.runner('taskkill', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' }).on('error', () => child.kill());
      } else {
        try { process.kill(-child.pid, 'SIGTERM'); } catch { child.kill(); }
        const force = setTimeout(() => { try { process.kill(-child.pid, 'SIGKILL'); } catch {} }, 1500);
        force.unref();
        child.once('close', () => clearTimeout(force));
      }
    };
    try {
      child = this.runner(this.binary, args, { cwd: workspace, env, detached: process.platform !== 'win32', windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
      this.children.set(child, stop);
      closed = new Promise(resolve => child.once('close', code => { if (!terminal && code && !failure) failure = cliError(stderr || `Cline exited with code ${code}`); queue.end(); resolve(); }));
      child.on('error', error => { failure = error; queue.end(); });
      const send = message => child.stdin.write(JSON.stringify({ jsonrpc: '2.0', ...message }) + '\n');
      const abort = () => { failure = signal.reason; stop(); };
      signal.addEventListener('abort', abort, { once: true });
      child.once('close', () => signal.removeEventListener('abort', abort));
      child.stdin.on('error', error => { if (!terminal) { failure = error; stop(); } });
      child.stderr.on('data', chunk => { stderr = (stderr + chunk).slice(-8000); });
      lines = createInterface({ input: child.stdout, crlfDelay: Infinity });
      lines.on('line', line => {
        try {
          const item = JSON.parse(line);
          if (item.error) throw cliError(item.error.data?.message || item.error.message || 'Cline request failed.');
          if (item.id === 1 && item.result) send({ id: 2, method: 'session/new', params: { cwd: workspace, mcpServers: [] } });
          if (item.id === 2 && item.result) {
            sessionId = item.result.sessionId;
            if (typeof sessionId !== 'string') throw new GatewayError('Cline returned no session ID.', 502, 'invalid_stream');
            // ACP ignores the CLI model flag. Confirm the free model before sending any prompt.
            send({ id: 3, method: 'session/set_model', params: { sessionId, modelId: model.upstreamId } });
          }
          if (item.id === 3 && item.result) send({ id: 4, method: 'session/prompt', params: { sessionId,
            prompt: [{ type: 'text', text: textOnly + '\nThis is a text-only chat, not a coding task. Do not execute a command to print an answer.\n\n' + history }] } });
          if (item.method === 'session/request_permission') send({ id: item.id, result: { outcome: { outcome: 'cancelled' } } });
          else if (item.method && item.id !== undefined) send({ id: item.id, error: { code: -32601, message: 'Client tools are disabled.' } });
          if (item.method === 'session/update' && item.params?.sessionId === sessionId) {
            const update = item.params.update;
            if (update?.sessionUpdate === 'agent_message_chunk' && update.content?.type === 'text') queue.push(update.content.text);
          }
          if (item.id === 4 && item.result) {
            if (item.result.stopReason !== 'end_turn') throw cliError(`Cline run ended: ${item.result.stopReason}`);
            terminal = true; queue.end(); stop();
          }
        } catch (error) {
          failure = error instanceof GatewayError ? error : new GatewayError('Invalid CLI response stream.', 502, 'invalid_stream');
          queue.end(); stop();
        }
      });
      if (signal.aborted) abort();
      else send({ id: 1, method: 'initialize', params: { protocolVersion: 1, clientCapabilities: {} } });
      for await (const text of queue) {
        emitted += text;
        yield { choices: [{ index: 0, delta: { content: text }, finish_reason: null }] };
      }
      await closed;
      signal.throwIfAborted();
      if (failure) throw failure;
      if (!terminal) throw new GatewayError('CLI response ended before completion.', 502, 'incomplete_stream');
      if (!emitted.trim()) throw new GatewayError('CLI returned no text.', 502, 'empty_completion');
      yield { choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] };
    } finally {
      if (child) { stop(); await closed; this.children.delete(child); }
      lines?.close(); queue.end(); rmSync(workspace, { recursive: true, force: true });
    }
  }
}

function cliError(message) {
  const text = String(message);
  if (/quota|rate.?limit|free.*limit|capacity|RESOURCE_EXHAUSTED|429/i.test(text)) return new GatewayError('CLI free quota is exhausted. Try another provider.', 429, 'rate_limit_exceeded', 600);
  if (/auth|sign.?in|login|credential|401/i.test(text)) return new GatewayError('CLI login is required. Reauthenticate the provider.', 401, 'cli_login_required');
  return new GatewayError(text.slice(0, 400), 502, 'cli_error');
}
