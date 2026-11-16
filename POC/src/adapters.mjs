import { GatewayError, sseEvents, retryDelay } from './streams.mjs';
import { freeModel, normalizeModel, seedModels, catalogModel } from './catalog.mjs';

const allowed = ['messages', 'temperature', 'top_p', 'max_tokens', 'max_completion_tokens', 'stop', 'tools', 'tool_choice', 'response_format', 'seed', 'frequency_penalty', 'presence_penalty', 'parallel_tool_calls', 'user', 'reasoning_effort'];

export async function syncModels(provider, store, bridge, fetcher = fetch) {
  if (['opencode', 'cli'].includes(provider.kind)) return bridge.models(provider);
  if (!provider.catalog) return seedModels(provider);
  const key = store.key(provider);
  if (!key && !provider.keyOptional) throw new GatewayError('먼저 공급처 API 키를 저장하세요.', 409, 'missing_key');
  const response = await fetcher(provider.base + '/models', {
    headers: key ? { authorization: 'Bearer ' + key } : {},
    signal: AbortSignal.timeout(20000)
  });
  if (!response.ok) throw new GatewayError('모델 목록 조회 실패 (HTTP ' + response.status + ')', 502, 'catalog_failed');
  const data = await response.json();
  const models = Array.isArray(data) ? data : data.data || data.models || [];
  if (!Array.isArray(models)) throw new GatewayError('공급처 모델 목록 형식을 읽을 수 없습니다.', 502, 'invalid_catalog');
  const eligible=models.filter(m => freeModel(provider, m)&&m.status!=='deprecated'&&!m.disabled).map(m => normalizeModel(provider, m));
  Object.defineProperty(eligible,'catalog',{value:models.map(m=>catalogModel(provider,m))});
  return eligible;
}

export async function* openAICompletion(provider, model, body, { store, signal, fetcher = fetch }) {
  const key = store.key(provider);
  const request = { model: model.upstreamId, stream: true, stream_options: { include_usage: true } };
  for (const field of allowed) if (body[field] !== undefined) request[field] = body[field];
  if (request.max_tokens === undefined && request.max_completion_tokens === undefined) request.max_tokens = 2048;
  const response = await fetcher(provider.base + '/chat/completions', {
    method: 'POST', signal,
    headers: { 'content-type': 'application/json', ...(key ? { authorization: 'Bearer ' + key } : {}) },
    body: JSON.stringify(request)
  });
  if (!response.ok) {
    await response.body?.cancel().catch(() => {});
    throw new GatewayError(provider.name + ' 요청 실패 (HTTP ' + response.status + ')', response.status,
      response.status === 429 ? 'rate_limit_exceeded' : 'upstream_error',
      response.status === 429 ? retryDelay(response.headers.get('retry-after')) : 0);
  }
  if (response.headers.get('content-type')?.includes('application/json')) {
    const result = await response.json();
    if (result.error) throw new GatewayError(provider.name + ' 응답 오류', 502, 'upstream_error');
    const choice = result.choices?.[0];
    if (!choice?.message) throw new GatewayError('공급처 응답 형식이 올바르지 않습니다.', 502, 'invalid_upstream_response');
    yield { choices: [{ index: 0, delta: choice.message, finish_reason: choice.finish_reason || 'stop' }], usage: result.usage };
    return;
  }
  let done = false, sawChoice = false;
  for await (const raw of sseEvents(response.body)) {
    if (raw === '[DONE]') { done = true; break; }
    let chunk;
    try { chunk = JSON.parse(raw); } catch { throw new GatewayError('공급처 스트림을 해석하지 못했습니다.', 502, 'invalid_stream'); }
    if (chunk.error) throw new GatewayError(provider.name + ' 스트림 응답 오류', 502, 'upstream_stream_error');
    if (chunk.choices?.length) sawChoice = true;
    yield chunk;
  }
  if (!sawChoice || !done) throw new GatewayError('공급처 스트림이 완료 전에 종료되었습니다.', 502, 'incomplete_stream');
}
