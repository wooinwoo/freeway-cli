import { fileURLToPath } from 'node:url';
import { resolve } from 'node:path';
import { Client } from '../../../POC/src/client.mjs';
import { run, providers } from './manage.mjs';

const problem = (code, status = 400) => Object.assign(new Error(code), { code, status });
const providerFor = id => providers.find(p => p.id === id) || (() => { throw problem('unknown_provider', 404); })();

function unwrap(result) {
  if (!result.ok) {
    const error = result.results?.find(r => r.error)?.error;
    throw problem(error?.code || 'provider_failed', error?.httpStatus || 409);
  }
  return result.results[0];
}

export function errorEvent(error) {
  const code = error?.name === 'TimeoutError' ? 'request_timeout' : error?.name === 'AbortError' ? 'cancelled'
    : typeof error?.code === 'string' && error.code ? error.code : 'bridge_failed';
  const fallback = code === 'request_timeout' ? 504 : code === 'cancelled' ? 499 : code === 'rate_limit_exceeded' ? 429 : 502;
  const status = Number.isInteger(error?.status) && error.status >= 400 && error.status <= 599 ? error.status : fallback;
  const retryAfter = Number.isFinite(error?.retryAfter) && error.retryAfter > 0 ? Math.ceil(error.retryAfter) : 0;
  return { type: 'error', error: { code, status, retryAfter } };
}

// Backend operations reuse the existing gateway's credentials, adapters and usage ledger.
export async function execute(input, { client, emit = () => {} } = {}) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw problem('invalid_request');
  if (input.action === 'providers') return (await run(['list'])).providers;
  const provider = providerFor(input.provider);
  client ||= new Client({ base: input.baseURL || 'http://127.0.0.1:4141', autoStart: false });
  const deps = { client, interactive: false };
  if (input.action === 'authenticate') {
    const args = ['auth', provider.id];
    if (input.key !== undefined) {
      if (provider.kind !== 'openai') throw problem('cli_login_required', 409);
      args.push('--stdin');
      deps.readStdin = async () => input.key;
    }
    if (input.confirmFree === true) args.push('--confirm-free');
    return unwrap(await run(args, deps));
  }
  if (input.action === 'quota') return unwrap(await run(['quota', provider.id], deps));
  if (!['models', 'complete'].includes(input.action)) throw problem('unknown_action');
  const listed = unwrap(await run(['models', provider.id], deps));
  const models = listed.models;
  if (input.action === 'models') return models;
  const model = models.find(m => m.id === input.model || m.upstreamId === input.model);
  if (!model) throw problem('model_not_found', 404);
  if (!model.ready) throw problem(model.reason || 'provider_not_ready', 503);
  if (input.reasoningEffort !== undefined && !model.reasoning.efforts.includes(input.reasoningEffort)) throw problem('unsupported_reasoning');
  const body = { model: model.id, messages: input.messages,
    max_tokens: input.maxTokens ?? 2048,
    ...(input.reasoningEffort !== undefined ? { reasoning_effort: input.reasoningEffort } : {}) };
  const result = await client.complete(body, { signal: AbortSignal.timeout(120000), onChunk: chunk => {
    if (chunk.provider !== provider.id || chunk.model !== model.id) throw problem('unexpected_route', 502);
    for (const choice of chunk.choices || []) {
      if (choice.delta?.content) emit({ type: 'text', text: choice.delta.content });
    }
  } });
  if (result.provider !== provider.id || result.model !== model.id) throw problem('unexpected_route', 502);
  return { ...result, reasoningEffort: input.reasoningEffort || null };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const emit = event => process.stdout.write(JSON.stringify(event) + '\n');
  try {
    let raw = '';
    for await (const chunk of process.stdin) {
      raw += chunk;
      if (Buffer.byteLength(raw) > 2 * 1024 * 1024) throw problem('request_too_large', 413);
    }
    const result = await execute(JSON.parse(raw), { emit });
    emit({ type: 'result', result });
  } catch (error) {
    // Raw upstream/child errors may contain secrets; expose only normalized codes.
    emit(errorEvent(error));
    process.exitCode = 1;
  }
}
