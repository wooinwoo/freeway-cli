import { mkdirSync, readFileSync, writeFileSync, renameSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { randomBytes, createCipheriv, createDecipheriv, timingSafeEqual } from 'node:crypto';

export function safeEqual(a, b) {
  const x = Buffer.from(String(a || '')), y = Buffer.from(String(b || ''));
  return x.length === y.length && timingSafeEqual(x, y);
}

export class Store {
  constructor(dir) {
    this.dir = dir;
    mkdirSync(dir, { recursive: true });
    const secretPath = join(dir, 'vault.key');
    if (!existsSync(secretPath)) writeFileSync(secretPath, randomBytes(32), { mode: 0o600 });
    this.secret = readFileSync(secretPath);
    if (this.secret.length !== 32) throw new Error('Invalid local vault key');
    this.path = join(dir, 'state.json');
    this.data = existsSync(this.path) ? JSON.parse(readFileSync(this.path, 'utf8')) : {
      version: 1, routerKey: 'fw_' + randomBytes(28).toString('hex'),
      providers: {}, models: [], usage: {}, logs: [],
      settings: { allowedOrigins: [], timeoutMs: 120000, maxFallbacks: 3 }
    };
    this.save();
  }
  save() {
    const temp = this.path + '.tmp';
    writeFileSync(temp, JSON.stringify(this.data, null, 2), { mode: 0o600 });
    renameSync(temp, this.path);
  }
  encrypt(value) {
    if (!value) return '';
    const iv = randomBytes(12);
    const cipher = createCipheriv('aes-256-gcm', this.secret, iv);
    const body = Buffer.concat([cipher.update(value, 'utf8'), cipher.final()]);
    return Buffer.concat([iv, cipher.getAuthTag(), body]).toString('base64');
  }
  decrypt(value) {
    if (!value) return '';
    const raw = Buffer.from(value, 'base64');
    const decipher = createDecipheriv('aes-256-gcm', this.secret, raw.subarray(0, 12));
    decipher.setAuthTag(raw.subarray(12, 28));
    return Buffer.concat([decipher.update(raw.subarray(28)), decipher.final()]).toString('utf8');
  }
  key(provider) {
    const config = this.data.providers[provider.id] || {};
    return config.secret ? this.decrypt(config.secret) : process.env[provider.env] || '';
  }
  configure(id, patch) {
    const config = this.data.providers[id] ||= {};
    for (const field of ['enabled', 'priority', 'freeConfirmed']) {
      if (patch[field] !== undefined) config[field] = patch[field];
    }
    if (patch.apiKey !== undefined) config.secret = this.encrypt(patch.apiKey.trim());
    if (patch.clearKey) config.secret = '';
    if (patch.limits) config.limits = patch.limits;
    delete config.cooldownUntil;
    this.save();
  }
  buckets(id, now = Date.now()) {
    const iso = new Date(now).toISOString();
    const usage = this.data.usage[id] ||= {};
    for (const [period, key] of [['minute', iso.slice(0, 16)], ['hour', iso.slice(0, 13)], ['day', iso.slice(0, 10)]]) {
      if (usage[period]?.key !== key) usage[period] = { key, requests: 0, tokens: 0, estimatedTokens: 0 };
    }
    usage.total ||= { requests: 0, succeeded: 0, failed: 0, tokens: 0, estimatedTokens: 0 };
    return usage;
  }
  capacity(provider, config) {
    const usage = this.buckets(provider.id);
    const limits = { ...provider.limits, ...config.limits };
    for (const period of ['minute', 'hour', 'day']) {
      const limit = limits[period];
      if (limit && usage[period].requests >= limit) return period + '_request_limit';
    }
    if (limits.dayTokens && usage.day.tokens + usage.day.estimatedTokens >= limits.dayTokens) return 'daily_token_limit';
    if (config.cooldownUntil > Date.now()) return 'cooldown';
    return null;
  }
  reserve(provider, config) {
    const reason = this.capacity(provider, config);
    if (reason) return { reason };
    const usage = this.buckets(provider.id);
    for (const period of ['minute', 'hour', 'day']) usage[period].requests++;
    usage.total.requests++;
    this.save();
    return { startedAt: Date.now(), bucketKeys: Object.fromEntries(['minute', 'hour', 'day'].map(p => [p, usage[p].key])) };
  }
  record(provider, reservation, { ok, tokens, estimatedTokens = 0, model, error, durationMs, requestId }) {
    const usage = this.buckets(provider.id);
    const reported = Number.isFinite(tokens) ? Math.max(0, tokens) : 0;
    const estimated = Number.isFinite(tokens) ? 0 : Math.max(0, estimatedTokens);
    for (const period of ['minute', 'hour', 'day']) {
      if (usage[period].key === reservation.bucketKeys[period]) {
        usage[period].tokens += reported;
        usage[period].estimatedTokens += estimated;
      }
    }
    usage.total[ok ? 'succeeded' : 'failed']++;
    usage.total.tokens += reported;
    usage.total.estimatedTokens += estimated;
    this.data.logs.unshift({
      id: requestId, time: new Date().toISOString(), provider: provider.id, model,
      ok, tokens: Number.isFinite(tokens) ? reported : null,
      estimatedTokens: estimated, durationMs, error: error || null
    });
    this.data.logs = this.data.logs.slice(0, 200);
    this.save();
  }
}
