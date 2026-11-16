export class GatewayError extends Error {
  constructor(message, status = 502, code = 'upstream_error', retryAfter = 0) {
    super(message); this.status = status; this.code = code; this.retryAfter = retryAfter;
  }
}

export async function* sseEvents(body) {
  const decoder = new TextDecoder();
  let buffer = '';
  for await (const bytes of body) {
    buffer += decoder.decode(bytes, { stream: true });
    // Normalize CRLF only after complete lines have arrived.
    while (true) {
      const match = /\r?\n\r?\n/.exec(buffer);
      if (!match) break;
      const block = buffer.slice(0, match.index);
      buffer = buffer.slice(match.index + match[0].length);
      const data = block.split(/\r?\n/).filter(x => x.startsWith('data:')).map(x => x.slice(5).replace(/^ /, '')).join('\n');
      if (data) yield data;
    }
  }
  buffer += decoder.decode();
  const data = buffer.split(/\r?\n/).filter(x => x.startsWith('data:')).map(x => x.slice(5).replace(/^ /, '')).join('\n');
  if (data) yield data;
}

export class AsyncQueue {
  constructor() { this.values = []; this.waiters = []; this.finished = false; }
  push(value) { const resolve = this.waiters.shift(); resolve ? resolve({ value, done: false }) : this.values.push(value); }
  end() { this.finished = true; for (const resolve of this.waiters.splice(0)) resolve({ done: true }); }
  next() {
    if (this.values.length) return Promise.resolve({ value: this.values.shift(), done: false });
    if (this.finished) return Promise.resolve({ done: true });
    return new Promise(resolve => this.waiters.push(resolve));
  }
  [Symbol.asyncIterator]() { return this; }
}

export function estimateTokens(messages, output = '') {
  // This is a local estimate, never an authoritative provider allowance.
  const input = JSON.stringify(messages);
  return Math.ceil((input.length + output.length) / 3);
}

export function retryDelay(header) {
  if (!header) return 60;
  const seconds = Number(header);
  if (Number.isFinite(seconds)) return Math.max(1, Math.min(86400, seconds));
  const date = Date.parse(header);
  return Number.isNaN(date) ? 60 : Math.max(1, Math.min(86400, Math.ceil((date - Date.now()) / 1000)));
}
