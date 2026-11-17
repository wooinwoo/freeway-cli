import { createServer } from 'node:http';
import { readFileSync, existsSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { randomBytes } from 'node:crypto';
import { Store, safeEqual } from './src/store.mjs';
import { definitions, providerConfig } from './src/catalog.mjs';
import { Engine, accumulateCompletion } from './src/engine.mjs';
import { ProviderBridges } from './src/provider-bridges.mjs';
import { GatewayError } from './src/streams.mjs';
import { Chats } from './src/chats.mjs';
import { defaultProfile } from './src/profile.mjs';
import { modelReference } from './src/model-groups.mjs';

const root = dirname(fileURLToPath(import.meta.url));
if (existsSync(join(root, '.env'))) process.loadEnvFile(join(root, '.env'));
const assets = { '/': ['chat.html', 'text/html; charset=utf-8'], '/admin': ['index.html', 'text/html; charset=utf-8'], '/chat.mjs': ['chat.mjs','text/javascript; charset=utf-8'], '/chat-render.mjs': ['chat-render.mjs','text/javascript; charset=utf-8'], '/chat.css': ['chat.css','text/css; charset=utf-8'], '/app.mjs': ['app.mjs', 'text/javascript; charset=utf-8'], '/style.css': ['style.css', 'text/css; charset=utf-8'], '/favicon.svg': ['favicon.svg', 'image/svg+xml'] };

async function readBody(request) {
  let size = 0;
  const chunks = [];
  for await (const chunk of request) {
    size += chunk.length;
    if (size > 2 * 1024 * 1024) throw new GatewayError('요청 본문은 2MB 이하만 지원합니다.', 413, 'body_too_large');
    chunks.push(chunk);
  }
  try { return JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}'); }
  catch { throw new GatewayError('올바른 JSON을 보내주세요.', 400, 'invalid_json'); }
}

function json(response, data, status = 200) {
  response.writeHead(status, { 'content-type': 'application/json; charset=utf-8', 'cache-control': 'no-store' });
  response.end(JSON.stringify(data));
}

export function createGateway(options = {}) {
  const store = options.store || new Store(options.dataDir || process.env.FREEWAY_DATA_DIR || join(root, '.data'));
  if (!options.providers && !store.data.settings.chatDefaults) {
    Object.assign(store.data.settings,structuredClone(defaultProfile));store.save();
  }
  if(!options.providers&&!store.data.settings.catalogVersion){
    store.data.settings.preferredModels=[...new Set([...defaultProfile.preferredModels,...store.data.settings.preferredModels||[]])].slice(0,20);
    store.data.settings.catalogVersion=1;store.save();
  }
  const bridge = options.bridge || new ProviderBridges(store.dir);
  if(!options.providers&&bridge.binary&&store.data.providers.opencode?.enabled===undefined)store.configure('opencode',{enabled:true});
  const engine = new Engine(store, bridge, { providers: options.providers || definitions, fetcher: options.fetcher || fetch });
  if(!options.providers&&!store.data.settings.groupVersion){
    const catalog=engine.catalog();
    store.data.settings.preferredModels=[...new Set((store.data.settings.preferredModels||[]).map(id=>modelReference(id,catalog)))];
    if(store.data.settings.chatDefaults)store.data.settings.chatDefaults.model=modelReference(store.data.settings.chatDefaults.model,catalog);
    store.data.settings.groupVersion=1;store.save();
  }
  const chats = new Chats(store);
  const sessionSecret = randomBytes(32).toString('hex');
  const researchPath = join(root, 'research.json');
  const research = existsSync(researchPath) ? JSON.parse(readFileSync(researchPath, 'utf8')) : { candidates: [] };
  const server = createServer(async (request, response) => {
    response.setHeader('x-content-type-options', 'nosniff');
    response.setHeader('referrer-policy', 'no-referrer');
    response.setHeader('content-security-policy', "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'");
    const controller = new AbortController();
    response.on('close', () => { if (!response.writableEnded) controller.abort(new Error('client_disconnected')); });
    request.on('aborted', () => controller.abort(new Error('client_disconnected')));
    try {
      const host = request.headers.host || '';
      const parsedHost = new URL('http://' + host);
      if (!['127.0.0.1', 'localhost', '[::1]'].includes(parsedHost.hostname) || Number(parsedHost.port || 80) !== server.address().port) {
        throw new GatewayError('허용되지 않은 Host입니다.', 403, 'invalid_host');
      }
      const origin = 'http://' + host;
      const path = new URL(request.url, origin).pathname;
      const requestOrigin = request.headers.origin;
      const ownOrigin = !requestOrigin || requestOrigin === origin;
      const allowedOrigin = requestOrigin && store.data.settings.allowedOrigins.includes(requestOrigin);
      if (requestOrigin && !ownOrigin && !allowedOrigin) throw new GatewayError('허용되지 않은 Origin입니다.', 403, 'invalid_origin');
      if (allowedOrigin || requestOrigin === origin) {
        response.setHeader('access-control-allow-origin', requestOrigin);
        response.setHeader('vary', 'Origin');
        response.setHeader('access-control-allow-headers', 'authorization, content-type');
        response.setHeader('access-control-allow-methods', 'GET, POST, PUT, OPTIONS');
        response.setHeader('access-control-expose-headers', 'x-freeway-provider, x-request-id, retry-after');
      }
      if (request.method === 'OPTIONS') { response.writeHead(204); response.end(); return; }
      const cookie = request.headers.cookie?.split(';').map(x => x.trim()).find(x => x.startsWith('freeway_session='))?.slice('freeway_session='.length);
      const admin = ownOrigin && safeEqual(cookie, sessionSecret);
      const bearer = request.headers.authorization?.match(/^Bearer (.+)$/i)?.[1];
      const apiAuthorized = admin || safeEqual(bearer, store.data.routerKey);
      if (request.method === 'GET' && assets[path]) {
        const [name, type] = assets[path];
        if ((path === '/' || path === '/admin') && ownOrigin) response.setHeader('set-cookie', 'freeway_session=' + sessionSecret + '; HttpOnly; SameSite=Strict; Path=/; Max-Age=86400');
        response.writeHead(200, { 'content-type': type, 'cache-control': 'no-store' });
        response.end(readFileSync(join(root, 'public', name)));
        return;
      }
      if (path === '/health' && request.method === 'GET') { json(response, { status: 'ok', name: 'Freeway', version: '0.1.0' }); return; }
      if (path.startsWith('/api/')) {
        if (!admin) throw new GatewayError('로컬 관리 화면을 먼저 열어주세요.', 401, 'admin_auth_required');
        if (!['GET', 'HEAD'].includes(request.method) && (!requestOrigin || requestOrigin !== origin)) throw new GatewayError('관리 변경은 같은 출처에서만 가능합니다.', 403, 'admin_origin_required');
        if (path === '/api/state' && request.method === 'GET') { json(response, { ...engine.state(), baseURL: origin + '/v1', researchCount: research.candidates.length }); return; }
        if (path === '/api/research' && request.method === 'GET') { json(response, research); return; }
        if (path === '/api/chats') {
          if (request.method === 'GET') { json(response,{data:chats.list()});return; }
          if (request.method === 'POST') { json(response,chats.create(await readBody(request)),201);return; }
        }
        const chatMatch = /^\/api\/chats\/([a-f0-9-]{36})$/.exec(path);
        if (chatMatch) {
          if (request.method === 'GET') { json(response,chats.get(chatMatch[1]));return; }
          if (request.method === 'PUT') { json(response,chats.update(chatMatch[1],await readBody(request)));return; }
          if (request.method === 'DELETE') { chats.delete(chatMatch[1]);json(response,{ok:true});return; }
        }
        if (path === '/api/key' && request.method === 'GET') { json(response, { key: store.data.routerKey }); return; }
        if (path === '/api/key/rotate' && request.method === 'POST') {
          store.data.routerKey = 'fw_' + randomBytes(28).toString('hex'); store.save();
          json(response, { key: store.data.routerKey }); return;
        }
        if (path === '/api/settings' && request.method === 'PUT') {
          const body = await readBody(request);
          const settings = { ...store.data.settings };
          if (body.allowedOrigins !== undefined) {
            if (!Array.isArray(body.allowedOrigins) || body.allowedOrigins.length > 20 || body.allowedOrigins.some(x => {
              try { const u = new URL(x); return u.origin !== x || !['http:', 'https:'].includes(u.protocol); } catch { return true; }
            })) throw new GatewayError('Origin은 http(s)://주소:포트 형식으로 입력하세요.', 400, 'invalid_origins');
            settings.allowedOrigins = body.allowedOrigins;
          }
          if (body.timeoutMs !== undefined) {
            if (!Number.isInteger(body.timeoutMs) || body.timeoutMs < 1000 || body.timeoutMs > 300000) throw new GatewayError('요청 제한 시간은 1~300초입니다.', 400, 'invalid_timeout');
            settings.timeoutMs = body.timeoutMs;
          }
          const known = new Set([...engine.models(),...engine.modelGroups().filter(m=>m.free)].map(m => m.id));
          if (body.preferredModels !== undefined) {
            if (!Array.isArray(body.preferredModels) || body.preferredModels.length > 20 || body.preferredModels.some(id => !known.has(id))) throw new GatewayError('우선 모델은 등록된 무료 모델 ID여야 합니다.',400,'invalid_preferred_models');
            settings.preferredModels = [...new Set(body.preferredModels.map(id=>modelReference(id,engine.catalog())))];
          }
          if (body.chatDefaults !== undefined) {
            const d = body.chatDefaults;
            if (!d || typeof d !== 'object' || Array.isArray(d) || d.model !== 'free/auto' && !known.has(d.model) || !Number.isInteger(d.maxTokens) || d.maxTokens < 1 || d.maxTokens > 32768 || !Number.isFinite(d.temperature) || d.temperature < 0 || d.temperature > 2) throw new GatewayError('기본 대화 설정과 무료 모델을 확인하세요.',400,'invalid_chat_defaults');
            settings.chatDefaults = {model:d.model,maxTokens:d.maxTokens,temperature:d.temperature};
            settings.chatProfileVersion = Date.now();
          }
          store.data.settings = settings;
          store.save(); json(response, store.data.settings); return;
        }
        if (path === '/api/opencode/start' && request.method === 'POST') {
          await bridge.start();
          store.configure('opencode', { enabled: true });
          const result = await engine.refresh('opencode');
          json(response, { ...bridge.state(), ...result }); return;
        }
        const connect = /^\/api\/providers\/(opencode|cline)\/connect$/.exec(path);
        if (connect && request.method === 'POST') {
          await bridge.start(connect[1]);
          store.configure(connect[1], { enabled: true });
          json(response, { ...(bridge.forProvider?.(connect[1]) || bridge).state(), ...await engine.refresh(connect[1]) }); return;
        }
        const match = /^\/api\/providers\/([a-z0-9_-]+)(\/sync)?$/.exec(path);
        if (match) {
          const provider = engine.provider(match[1]);
          if (request.method === 'PUT' && !match[2]) {
            const body = await readBody(request);
            for (const field of ['enabled', 'freeConfirmed', 'clearKey']) if (body[field] !== undefined && typeof body[field] !== 'boolean') throw new GatewayError(field + '는 boolean이어야 합니다.', 400, 'invalid_config');
            if (body.priority !== undefined && (!Number.isInteger(body.priority) || body.priority < 0 || body.priority > 1000)) throw new GatewayError('우선순위는 0~1000 정수입니다.', 400, 'invalid_config');
            if (body.apiKey !== undefined && (typeof body.apiKey !== 'string' || body.apiKey.length > 4096 || /[\r\n]/.test(body.apiKey))) throw new GatewayError('API 키 형식이 올바르지 않습니다.', 400, 'invalid_key');
            if (body.limits !== undefined) {
              if (!body.limits || typeof body.limits !== 'object' || Array.isArray(body.limits) || Object.entries(body.limits).some(([key, value]) => !['minute', 'hour', 'day', 'dayTokens'].includes(key) || !Number.isInteger(value) || value < 1 || value > 1000000000)) throw new GatewayError('한도는 양의 정수여야 합니다.', 400, 'invalid_limits');
            }
            store.configure(provider.id, body);
            json(response, { ok: true }); return;
          }
          if (request.method === 'POST' && match[2]) { json(response, await engine.refresh(provider.id)); return; }
        }
        throw new GatewayError('관리 경로를 찾지 못했습니다.', 404, 'not_found');
      }
      if (path.startsWith('/v1/')) {
        if (!apiAuthorized) throw new GatewayError('유효한 Freeway API 키가 필요합니다.', 401, 'invalid_api_key');
        if (request.method === 'GET' && path === '/v1/models') {
          json(response, { object: 'list', data: [
            { id: 'free/auto', object: 'model', created: 0, owned_by: 'freeway', ready: engine.models().some(m => m.ready) },
            ...engine.modelGroups().filter(m=>m.free).map(m => ({ id: m.id, object: 'model', created: 0, owned_by: 'freeway',
              name:m.name, providers:m.providers, routes:m.routes.filter(r=>r.free).map(r=>({id:r.id,provider:r.provider,ready:r.ready})),
              context_length: m.context, pricing: { prompt: '0', completion: '0' }, ready: m.ready, capabilities: m.capabilities }))
          ] }); return;
        }
        if (request.method === 'POST' && path === '/v1/chat/completions') {
          const body = await readBody(request);
          const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(store.data.settings.timeoutMs)]);
          const chunks = engine.complete(body, signal);
          if (body.stream) {
            for await (const chunk of chunks) {
              if (controller.signal.aborted) break;
              if (!response.headersSent) {
                response.writeHead(200, { 'content-type': 'text/event-stream; charset=utf-8', 'cache-control': 'no-cache, no-transform',
                  'x-accel-buffering': 'no', 'x-freeway-provider': chunk.provider, 'x-request-id': chunk.id });
              }
              if (!response.write('data: ' + JSON.stringify(chunk) + '\n\n')) {
                await new Promise((resolve, reject) => {
                  const cleanup = () => { response.off('drain', drain); response.off('close', closed); };
                  const drain = () => { cleanup(); resolve(); };
                  const closed = () => { cleanup(); reject(new GatewayError('연결이 닫혔습니다.', 499, 'cancelled')); };
                  response.once('drain', drain); response.once('close', closed);
                });
              }
            }
            if (!response.destroyed) response.end('data: [DONE]\n\n');
          } else {
            const all = [];
            for await (const chunk of chunks) all.push(chunk);
            const result = accumulateCompletion(all);
            response.setHeader('x-freeway-provider', result.provider);
            response.setHeader('x-request-id', result.id);
            json(response, result);
          }
          return;
        }
      }
      throw new GatewayError('요청한 경로를 찾지 못했습니다.', 404, 'not_found');
    } catch (error) {
      if (response.destroyed) return;
      const status = error.status || (error.name === 'TimeoutError' ? 504 : 500);
      const payload = { error: { message: error instanceof GatewayError ? error.message : status === 504 ? '요청 시간이 초과되었습니다.' : '게이트웨이 내부 오류가 발생했습니다.',
        type: status === 401 ? 'authentication_error' : 'gateway_error', code: error.code || 'internal_error', status,
        ...(error.retryAfter ? { retryAfter: error.retryAfter } : {}) } };
      if (error.retryAfter && !response.headersSent) response.setHeader('retry-after', String(error.retryAfter));
      if (response.headersSent) response.end('data: ' + JSON.stringify(payload) + '\n\ndata: [DONE]\n\n');
      else json(response, payload, status >= 400 && status <= 599 ? status : 502);
    }
  });
  return {
    server, store, bridge, engine, chats,
    async listen(port = 4141) {
      await new Promise((resolve, reject) => { server.once('error', reject); server.listen(port, '127.0.0.1', resolve); });
      return server.address();
    },
    async close() {
      bridge.stop?.();
      server.closeAllConnections();
      await new Promise(resolve => server.close(resolve));
    }
  };
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  const app = createGateway();
  const port = Number(process.env.PORT || 4141);
  await app.listen(port);
  console.log('Freeway 실행 중: http://127.0.0.1:' + port);
  console.log('API 주소: http://127.0.0.1:' + port + '/v1');
  console.log('API 키는 관리 화면의 연결 메뉴에서 확인할 수 있습니다.');
  for (const provider of app.engine.providers) {
    if (!['opencode', 'cli'].includes(provider.kind) && providerConfig(app.store, provider).enabled) app.engine.refresh(provider.id).catch(() => {});
  }
  if (providerConfig(app.store, app.engine.provider('opencode')).enabled) {
    app.bridge.start().then(() => app.engine.refresh('opencode')).catch(() => {});
  }
  for (const id of ['cline']) {
    if (providerConfig(app.store, app.engine.provider(id)).enabled) app.bridge.start(id).then(() => app.engine.refresh(id)).catch(() => {});
  }
  for (const name of ['SIGINT', 'SIGTERM']) process.once(name, async () => { await app.close(); process.exit(0); });
}
