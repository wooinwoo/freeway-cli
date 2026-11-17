export const definitions = [
  { id: 'kilo', name: 'Kilo', kind: 'openai', base: 'https://api.kilo.ai/api/gateway', env: 'KILO_API_KEY', keyOptional: true, priority: 10, limits: { hour: 200 }, quota: '시간당 200요청 / IP', proof: 'catalog-zero', catalog: true, url: 'https://kilo.ai/docs/gateway/usage-and-billing' },
  { id: 'opencode', name: 'OpenCode', kind: 'opencode', priority: 20, limits: {}, quota: '무료 모델별 한도', proof: 'catalog-zero', url: 'https://opencode.ai/v1/docs/server/' },
  { id: 'cline', name: 'Cline CLI', kind: 'cli', priority: 21, limits: {}, quota: 'Cline 공식 무료 프로모션 한도', proof: 'cli-free-catalog', url: 'https://docs.cline.bot/getting-started/free-models' },
  { id: 'nvidia', name: 'NVIDIA NIM', kind: 'openai', base: 'https://integrate.api.nvidia.com/v1', env: 'NVIDIA_API_KEY', priority: 30, limits: { minute: 40 }, quota: '대부분 모델 최대 40요청 / 분', proof: 'free-prototype', catalog: true, needsConfirmation: true, url: 'https://build.nvidia.com/' },
  { id: 'zai', name: 'Z.ai', kind: 'openai', base: 'https://api.z.ai/api/paas/v4', env: 'ZAI_API_KEY', priority: 40, limits: {}, quota: '지정 Flash 모델 입출력 $0', proof: 'allowlist', seed: ['glm-4.7-flash', 'glm-4.5-flash'], url: 'https://docs.z.ai/guides/overview/pricing' },
  { id: 'requesty', name: 'Requesty', kind: 'openai', base: 'https://router.requesty.ai/v1', env: 'REQUESTY_API_KEY', priority: 50, limits: { day: 200 }, quota: '무료 모델 일 200요청', proof: 'catalog-zero', catalog: true, url: 'https://www.requesty.ai/models/free' },
  { id: 'vikasit', name: 'Vikasit', kind: 'openai', base: 'https://api.vikasit.ai/v1', env: 'VIKASIT_API_KEY', priority: 60, limits: { dayTokens: 2000000 }, quota: 'Nova 일 200만 토큰 · 계정 확인', proof: 'allowlist', seed: ['vikasit-nova'], needsConfirmation: true, url: 'https://vikasit.ai/' },
  { id: 'openrouter', name: 'OpenRouter', kind: 'openai', base: 'https://openrouter.ai/api/v1', env: 'OPENROUTER_API_KEY', priority: 70, limits: { minute: 20, day: 50 }, quota: '무결제 기준 일 50요청', proof: 'suffix-free', catalog: true, url: 'https://openrouter.ai/collections/free-models/' },
  { id: 'groq', name: 'Groq', kind: 'openai', base: 'https://api.groq.com/openai/v1', env: 'GROQ_API_KEY', priority: 80, limits: { minute: 30, day: 1000, dayTokens: 200000 }, quota: '무료 계정 한도 확인 필요', proof: 'free-account', seed: ['openai/gpt-oss-120b', 'openai/gpt-oss-20b'], needsConfirmation: true, url: 'https://console.groq.com/docs/rate-limits' },
  { id: 'gemini_api', name: 'Gemini API', kind: 'openai', base: 'https://generativelanguage.googleapis.com/v1beta/openai', env: 'GEMINI_API_KEY', priority: 90, limits: {}, quota: '프로젝트별 무료 한도', proof: 'free-account', seed: ['gemini-2.5-flash', 'gemini-2.5-flash-lite'], needsConfirmation: true, url: 'https://aistudio.google.com/docs/pricing' },
  { id: 'novita', name: 'Novita', kind: 'openai', base: 'https://api.novita.ai/openai', env: 'NOVITA_API_KEY', priority: 100, limits: {}, quota: 'Apodex Mini 무료 · 계정 확인', proof: 'allowlist', seed: ['apodex/apodex-1.1-mini'], needsConfirmation: true, url: 'https://novita.ai/pricing' }
];

export function freeModel(provider, model) {
  const id = model.id || model.name || '';
  if (model.isFree === false) return false;
  if (!id || model.architecture?.modality && !model.architecture.modality.includes('text')) return false;
  const pricing = model.pricing || model.cost;
  const isZero = value => (typeof value === 'number' || typeof value === 'string' && value.trim() !== '') && Number(value) === 0;
  const zero = pricing && pricing.prompt !== undefined && pricing.completion !== undefined
    ? isZero(pricing.prompt) && isZero(pricing.completion)
    : pricing && pricing.input !== undefined && pricing.output !== undefined
      && isZero(pricing.input) && isZero(pricing.output);
  if (provider.proof === 'catalog-zero') return Boolean(zero);
  if (provider.proof === 'suffix-free') return id.endsWith(':free') || id === 'openrouter/free';
  if (provider.proof === 'free-prototype') return true;
  return provider.seed?.includes(id) || false;
}

export function normalizeModel(provider, model) {
  const upstreamId = model.id || model.name;
  return {
    id: provider.id + '/' + upstreamId, provider: provider.id, upstreamId,
    name: model.name || upstreamId, context: model.context_length || model.limit?.context || null,
    free: true, freeBasis: provider.proof, discoveredAt: new Date().toISOString(),
    capabilities: { stream: true, tools: model.supported_parameters?.includes('tools') ?? (provider.kind !== 'opencode') }
  };
}

export function catalogModel(provider, model) {
  const pricing=model.pricing||model.cost;
  const input=pricing?.prompt??pricing?.input,output=pricing?.completion??pricing?.output;
  const numeric=value=>value!==null&&value!==undefined&&value!==''&&Number.isFinite(Number(value))?Number(value):null;
  const entry=normalizeModel(provider,model);
  entry.free=freeModel(provider,model)&&model.status!=='deprecated'&&!model.disabled;
  entry.pricing={input:numeric(input),output:numeric(output),unit:model.pricing?'per_token':'per_million_tokens'};
  entry.catalogStatus=model.status==='deprecated'||model.disabled?'deprecated':entry.free?'free':model.isFree===false||numeric(input)>0||numeric(output)>0?'paid':'unverified';
  return entry;
}

export function seedModels(provider) {
  return (provider.seed || []).map(id => normalizeModel(provider, { id }));
}

export function providerConfig(store, provider) {
  const config = store.data.providers[provider.id] || {};
  return { enabled: provider.id === 'kilo', priority: provider.priority, ...config };
}

export function providerReady(store, provider, bridge) {
  const config = providerConfig(store, provider);
  if (!config.enabled) return { ready: false, reason: 'disabled' };
  if (provider.needsConfirmation && !config.freeConfirmed) return { ready: false, reason: 'confirm_free_account' };
  if (['opencode', 'cli'].includes(provider.kind) && !bridge?.ready) return { ready: false, reason: 'connect_' + provider.id };
  if (!['opencode', 'cli'].includes(provider.kind) && !provider.keyOptional && !store.key(provider)) return { ready: false, reason: 'missing_key' };
  const cap = store.capacity(provider, config);
  if (cap) return { ready: false, reason: cap };
  return { ready: true, reason: null };
}
