import { randomUUID } from 'node:crypto';
import { definitions, providerConfig, providerReady, seedModels } from './catalog.mjs';
import { syncModels, openAICompletion } from './adapters.mjs';
import { GatewayError, estimateTokens } from './streams.mjs';

export function validateRequest(body) {
  if (!body || typeof body !== 'object' || Array.isArray(body)) throw new GatewayError('JSON 객체가 필요합니다.', 400, 'invalid_request');
  if (!Array.isArray(body.messages) || !body.messages.length || body.messages.length > 200) throw new GatewayError('messages 배열은 1~200개 메시지를 포함해야 합니다.', 400, 'invalid_messages');
  for (const message of body.messages) {
    if (!message || !['system', 'developer', 'user', 'assistant', 'tool'].includes(message.role)) throw new GatewayError('지원하지 않는 메시지 역할입니다.', 400, 'invalid_role');
    if (typeof message.content !== 'string' && message.content !== null && !Array.isArray(message.content) && !message.tool_calls) throw new GatewayError('메시지 content가 올바르지 않습니다.', 400, 'invalid_content');
  }
  if (body.n !== undefined && body.n !== 1) throw new GatewayError('현재 n=1만 지원합니다.', 400, 'unsupported_parameter');
  for (const name of ['max_tokens', 'max_completion_tokens']) {
    if (body[name] !== undefined && (!Number.isInteger(body[name]) || body[name] < 1 || body[name] > 32768)) throw new GatewayError(name + '는 1~32768 정수여야 합니다.', 400, 'invalid_parameter');
  }
  if (body.stream !== undefined && typeof body.stream !== 'boolean') throw new GatewayError('stream은 boolean이어야 합니다.', 400, 'invalid_parameter');
  if (body.tools !== undefined && !Array.isArray(body.tools)) throw new GatewayError('tools는 배열이어야 합니다.', 400, 'invalid_tools');
  if (body.tools?.some(tool => tool?.type !== 'function' || !tool.function?.name)) throw new GatewayError('무료 라우터는 클라이언트가 실행하는 function 도구만 지원합니다.', 400, 'unsupported_tools');
  if (body.models !== undefined && (!Array.isArray(body.models) || !body.models.length || body.models.some(m => typeof m !== 'string'))) throw new GatewayError('models는 모델 ID 배열이어야 합니다.', 400, 'invalid_models');
}

export class Engine {
  constructor(store, bridge, { providers = definitions, fetcher = fetch } = {}) {
    this.store = store; this.bridge = bridge; this.providers = providers; this.fetcher = fetcher;
    this.refreshing = new Map();
    store.data.catalog ||= [];
    for (const provider of providers) {
      if (!store.data.models.some(m => m.provider === provider.id)) store.data.models.push(...seedModels(provider));
    }
    store.save();
  }
  provider(id) {
    const p = this.providers.find(p => p.id === id);
    if (!p) throw new GatewayError('알 수 없는 공급처입니다.', 404, 'unknown_provider');
    return p;
  }
  async refresh(id) {
    if (this.refreshing.has(id)) return this.refreshing.get(id);
    const work = (async () => {
      const p = this.provider(id);
      try {
        const models = await syncModels(p, this.store, this.bridge, this.fetcher);
        this.store.data.models = this.store.data.models.filter(m => m.provider !== id).concat(models);
        if(models.catalog)this.store.data.catalog=this.store.data.catalog.filter(m=>m.provider!==id).concat(models.catalog);
        const config = this.store.data.providers[id] ||= {};
        config.lastSync = new Date().toISOString(); config.syncError = null;
        this.store.save();
        return { count: models.length };
      } catch (error) {
        const config = this.store.data.providers[id] ||= {};
        config.syncError = error.message; this.store.save();
        throw error;
      }
    })().finally(() => this.refreshing.delete(id));
    this.refreshing.set(id, work);
    return work;
  }
  modelState(model) {
    const p = this.provider(model.provider);
    const status = providerReady(this.store, p, this.bridge);
    if (status.ready && (p.catalog || p.kind === 'opencode') && Date.now() - Date.parse(model.discoveredAt) > 3600000) return { ...model, ready: false, reason: 'catalog_expired' };
    return { ...model, ...status };
  }
  models() { return this.store.data.models.filter(m => m.free === true).map(m => this.modelState(m)); }
  catalog() {
    const full=new Map(this.store.data.catalog.map(m=>[m.id,m]));
    for(const model of this.store.data.models)if(!full.has(model.id))full.set(model.id,{...model,catalogStatus:'free'});
    return [...full.values()].map(model=>{
      if(!model.free)return {...model,ready:false,reason:model.catalogStatus==='deprecated'?'deprecated':model.catalogStatus==='paid'?'paid_model':'pricing_unverified'};
      const enabled=this.store.data.models.find(m=>m.id===model.id);
      return enabled?{...model,...this.modelState(enabled)}:{...model,ready:false,reason:'catalog_expired'};
    });
  }
  state() {
    const providers = this.providers.map(p => {
      const config = providerConfig(this.store, p);
      return {
        id: p.id, name: p.name, kind: p.kind, quota: p.quota, url: p.url, base: p.base || null,
        enabled: config.enabled, priority: config.priority, freeConfirmed: Boolean(config.freeConfirmed),
        needsConfirmation: Boolean(p.needsConfirmation), keyOptional: Boolean(p.keyOptional),
        hasKey: Boolean(p.env && this.store.key(p)), env: p.env || null,
        limits: { ...p.limits, ...config.limits }, lastSync: config.lastSync || null,
        syncError: config.syncError || null, ...providerReady(this.store, p, this.bridge),
        usage: this.store.buckets(p.id)
      };
    });
    return { providers, models: this.models(), catalog:this.catalog(), logs: this.store.data.logs,
      settings: this.store.data.settings, bridge: this.bridge?.state?.() || { installed: false, ready: false } };
  }
  async candidates(body) {
    // Refresh stale catalog pricing before attempting inference.
    for (const p of this.providers) {
      const config = providerConfig(this.store, p);
      const available = providerReady(this.store, p, this.bridge);
      if (!available.ready || !(p.catalog || p.kind === 'opencode')) continue;
      if (!config.lastSync || Date.now() - Date.parse(config.lastSync) > 1800000) {
        try { await this.refresh(p.id); } catch { /* An expired catalog is never eligible. */ }
      }
    }
    const requested = body.models || [body.model || 'free/auto'];
    const preference = new Map((this.store.data.settings.preferredModels || []).map((id,index) => [id,index]));
    let candidates = [];
    for (const id of requested) {
      if (id === 'free/auto') {
        candidates.push(...this.models().filter(m => m.ready).sort((a, b) => {
          const priority = providerConfig(this.store, this.provider(a.provider)).priority - providerConfig(this.store, this.provider(b.provider)).priority;
          return (preference.get(a.id) ?? 999) - (preference.get(b.id) ?? 999) || priority || a.id.localeCompare(b.id);
        }));
      } else {
        const model = this.models().find(m => m.id === id);
        if (!model) throw new GatewayError('무료 모델 목록에 없는 모델입니다: ' + id, 404, 'model_not_found');
        if (!model.ready && requested.length === 1) throw new GatewayError('이 모델을 사용하려면 공급처 설정을 확인하세요: ' + model.reason, 503, 'provider_not_ready');
        if (model.ready) candidates.push(model);
      }
    }
    if (body.tools?.length || body.messages.some(m => m.role === 'tool' || m.tool_calls)) candidates = candidates.filter(m => m.capabilities.tools);
    const seen = new Set();
    candidates = candidates.filter(m => !seen.has(m.id) && seen.add(m.id));
    if (!candidates.length) {
      const limited = this.models().some(m => /limit|cooldown/.test(m.reason || ''));
      throw new GatewayError(limited ? '현재 무료 공급처 한도에 도달했습니다. 잠시 후 다시 시도하세요.' : '사용 가능한 무료 모델이 없습니다. 공급처를 연결하고 모델 목록을 동기화하세요.', limited ? 429 : 503, limited ? 'rate_limit_exceeded' : 'no_free_models', limited ? 60 : 0);
    }
    // Automatic fallback tries different providers, not dozens of models on a rate-limited provider.
    if (requested.includes('free/auto')) {
      const providerSeen = new Set();
      candidates = candidates.filter(m => !providerSeen.has(m.provider) && providerSeen.add(m.provider));
    }
    return candidates.slice(0, this.store.data.settings.maxFallbacks);
  }
  async *complete(body, signal) {
    validateRequest(body);
    const candidates = await this.candidates(body);
    const id = 'chatcmpl-' + randomUUID(), created = Math.floor(Date.now() / 1000);
    let lastError;
    for (const model of candidates) {
      if (signal.aborted) throw signal.reason;
      const provider = this.provider(model.provider);
      const config = providerConfig(this.store, provider);
      const reservation = this.store.reserve(provider, config);
      if (reservation.reason) { lastError = new GatewayError('공급처 무료 한도에 도달했습니다.', 429, reservation.reason, 60); continue; }
      let started = false, output = '', usage, recorded = false;
      try {
        const adapter = provider.kind === 'opencode'
          ? this.bridge.complete(body, model, { signal })
          : openAICompletion(provider, model, body, { store: this.store, signal, fetcher: this.fetcher });
        for await (const chunk of adapter) {
          if (signal.aborted) throw signal.reason;
          if (chunk.usage) usage = chunk.usage;
          output += chunk.choices?.map(c => c.delta?.content || '').join('') || '';
          started = true;
          yield { ...chunk, id, object: 'chat.completion.chunk', created, model: model.id,
            provider: provider.id, free: true };
        }
        this.store.record(provider, reservation, {
          ok: true, tokens: usage?.total_tokens, estimatedTokens: estimateTokens(body.messages, output),
          durationMs: Date.now() - reservation.startedAt, model: model.id, requestId: id
        });
        recorded = true;
        return;
      } catch (error) {
        const aborted = signal.aborted;
        const timedOut = signal.reason?.name === 'TimeoutError';
        const code = timedOut ? 'request_timeout' : aborted ? 'cancelled' : error.code || 'upstream_error';
        this.store.record(provider, reservation, {
          ok: false, tokens: usage?.total_tokens, estimatedTokens: started ? estimateTokens(body.messages, output) : 0,
          durationMs: Date.now() - reservation.startedAt, model: model.id, error: code, requestId: id
        });
        recorded = true;
        if ([429, 401, 403, 402].includes(error.status) || error.status === 503 && error.retryAfter) {
          this.store.data.providers[provider.id] ||= {};
          this.store.data.providers[provider.id].cooldownUntil = Date.now() + (error.retryAfter || 60) * 1000;
          this.store.save();
        }
        lastError = error instanceof GatewayError ? error : new GatewayError(timedOut ? '요청 시간이 초과되었습니다.' : aborted ? '요청이 취소되었습니다.' : '공급처 연결이 종료되었습니다.', timedOut ? 504 : aborted ? 499 : 502, timedOut ? 'request_timeout' : code);
        if (started || aborted || error.status === 400) throw lastError;
      } finally {
        if (!recorded) this.store.record(provider, reservation, {
          ok: false, tokens: usage?.total_tokens, estimatedTokens: started ? estimateTokens(body.messages, output) : 0,
          durationMs: Date.now() - reservation.startedAt, model: model.id, error: 'cancelled', requestId: id
        });
      }
    }
    throw lastError || new GatewayError('대체할 무료 공급처가 없습니다.', 503, 'no_free_models');
  }
}

export function accumulateCompletion(chunks) {
  const last = chunks.at(-1);
  const message = { role: 'assistant', content: '' };
  const tools = new Map();
  let finishReason = 'stop', usage;
  for (const chunk of chunks) {
    if (chunk.usage) usage = chunk.usage;
    for (const choice of chunk.choices || []) {
      const delta = choice.delta || {};
      if (delta.content) message.content += delta.content;
      if (delta.reasoning_content) message.reasoning_content = (message.reasoning_content || '') + delta.reasoning_content;
      if (delta.refusal) message.refusal = (message.refusal || '') + delta.refusal;
      for (const call of delta.tool_calls || []) {
        const current = tools.get(call.index ?? 0) || { id: '', type: 'function', function: { name: '', arguments: '' } };
        if (call.id) current.id = call.id;
        if (call.type) current.type = call.type;
        current.function.name += call.function?.name || '';
        current.function.arguments += call.function?.arguments || '';
        tools.set(call.index ?? 0, current);
      }
      if (choice.finish_reason) finishReason = choice.finish_reason;
    }
  }
  if (tools.size) { message.tool_calls = [...tools.entries()].sort((a,b) => a[0] - b[0]).map(x => x[1]); if (!message.content) message.content = null; }
  return { id: last.id, object: 'chat.completion', created: last.created, model: last.model,
    provider: last.provider, free: true, choices: [{ index: 0, message, finish_reason: finishReason }],
    ...(usage ? { usage } : {}) };
}
