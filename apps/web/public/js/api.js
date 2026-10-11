import { getToken, reauth } from './auth.js';

export class ApiError extends Error {
  constructor(status, code, message) { super(message); this.status = status; this.code = code; }
}

async function call(method, path, body, headers = {}) {
  const token = await getToken();
  let res;
  try {
    res = await fetch(path, {
      method,
      headers: { Authorization: 'Bearer ' + token, ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}), ...headers },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  } catch {
    throw new ApiError(0, 'offline', 'No connection. Check your internet and try again.');
  }
  if (res.status === 401) { await reauth(); throw new ApiError(401, 'unauthenticated', 'Please sign in.'); }
  return res;
}

export async function api(method, path, body, headers) {
  const res = await call(method, path, body, headers);
  if (res.status === 204) return null;
  const isJSON = (res.headers.get('content-type') || '').includes('json');
  const data = isJSON ? await res.json() : null;
  if (!res.ok) throw new ApiError(res.status, data?.error || 'error', data?.message || 'Something went wrong. Please try again.');
  return data;
}
export const get = (p) => api('GET', p);
export const post = (p, b, h) => api('POST', p, b === undefined ? {} : b, h);
export const put = (p, b) => api('PUT', p, b);
export const patch = (p, b) => api('PATCH', p, b);
export const del = (p) => api('DELETE', p);

/** Downloads an authenticated file (CSV) through a temporary link. */
export async function download(path, filename) {
  const res = await call('GET', path);
  if (!res.ok) {
    const data = await res.json().catch(() => null);
    throw new ApiError(res.status, data?.error || 'error', data?.message || 'Download failed.');
  }
  const url = URL.createObjectURL(await res.blob());
  const a = Object.assign(document.createElement('a'), { href: url, download: filename });
  document.body.append(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);
}

/**
 * Server-sent events over fetch (EventSource cannot send an Authorization
 * header). Resolves when the stream ends; rejects on failure. onEvent(name, data).
 */
export async function stream(path, onEvent, signal) {
  const res = await call('GET', path);
  if (!res.ok) {
    const data = await res.json().catch(() => null);
    throw new ApiError(res.status, data?.error || 'error', data?.message || 'Stream failed.');
  }
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  signal?.addEventListener('abort', () => reader.cancel().catch(() => {}));
  let buf = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += value;
    let i;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i); buf = buf.slice(i + 2);
      let name = 'message', data = '';
      for (const line of block.split('\n')) {
        if (line.startsWith('event:')) name = line.slice(6).trim();
        else if (line.startsWith('data:')) data += line.slice(5).trim();
      }
      if (data) { try { onEvent(name, JSON.parse(data)); } catch { /* ignore malformed */ } }
    }
  }
}
