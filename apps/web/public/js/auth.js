// Sign-in. Production: OpenID Connect (Keycloak) with the authorization-code
// flow + PKCE, no client secret and no cookies. Development (server started
// with IAH_DEV_AUTH=1): a name-only login so the app can be tried in seconds.

const KEY = 'iah.auth';      // sessionStorage: tokens die with the tab
const FLOW = 'iah.flow';     // sessionStorage: PKCE verifier/state in flight
const DEV = 'iah.dev';       // localStorage: dev identity
let config = null;
let discovery = null;

export async function loadConfig() {
  if (!config) {
    const res = await fetch('/v1/public-config');
    if (!res.ok) throw new Error('config');
    config = await res.json();
  }
  return config;
}
export const authMode = () => config?.auth;

const b64url = (buf) => btoa(String.fromCharCode(...new Uint8Array(buf))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
const rand = (n = 32) => b64url(crypto.getRandomValues(new Uint8Array(n)));
const read = (k, store = sessionStorage) => { try { return JSON.parse(store.getItem(k)); } catch { return null; } };
const write = (k, v, store = sessionStorage) => { try { store.setItem(k, JSON.stringify(v)); } catch { /* private mode */ } };

async function disco() {
  if (!discovery) {
    const res = await fetch(config.issuer.replace(/\/$/, '') + '/.well-known/openid-configuration');
    if (!res.ok) throw new Error('Sign-in service is unavailable.');
    discovery = await res.json();
  }
  return discovery;
}

/** Call once at start. Completes a returning login redirect if there is one. */
export async function initAuth() {
  await loadConfig();
  if (config.auth !== 'oidc') return;
  const q = new URLSearchParams(location.search);
  if (q.get('error')) {
    history.replaceState(null, '', location.pathname + location.hash);
    throw new Error(q.get('error_description') || 'Sign-in was cancelled.');
  }
  if (!q.get('code')) return;
  const flow = read(FLOW);
  sessionStorage.removeItem(FLOW);
  history.replaceState(null, '', location.pathname);   // never leave the code in the URL
  if (!flow || flow.state !== q.get('state')) throw new Error('Sign-in could not be verified. Please try again.');
  await tokenRequest({ grant_type: 'authorization_code', code: q.get('code'), redirect_uri: redirectUri(), code_verifier: flow.verifier });
  location.hash = flow.returnTo || '#/';
}

const redirectUri = () => location.origin + '/';

async function tokenRequest(params) {
  const d = await disco();
  const res = await fetch(d.token_endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ client_id: config.client_id, ...params }),
  });
  const t = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(t.error_description || 'Sign-in failed.');
  const prev = read(KEY) || {};
  write(KEY, {
    access: t.access_token,
    refresh: t.refresh_token || prev.refresh,
    id: t.id_token || prev.id,
    exp: Date.now() + (t.expires_in || 300) * 1000,
  });
}

/** Sends the browser to the identity provider; never returns. */
export async function login(returnTo = location.hash || '#/') {
  const d = await disco();
  const verifier = rand(48), state = rand(16);
  write(FLOW, { verifier, state, returnTo });
  const challenge = b64url(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier)));
  location.assign(d.authorization_endpoint + '?' + new URLSearchParams({
    client_id: config.client_id, redirect_uri: redirectUri(), response_type: 'code',
    scope: 'openid profile email', code_challenge: challenge, code_challenge_method: 'S256', state,
  }));
  return new Promise(() => {});
}

export function isSignedIn() {
  if (config?.auth === 'dev') return !!read(DEV, localStorage);
  const t = read(KEY);
  return !!(t && (t.refresh || t.exp > Date.now()));
}

/** A valid bearer token, refreshing or re-authenticating as needed. */
export async function getToken() {
  if (config.auth === 'dev') {
    const d = read(DEV, localStorage);
    if (!d) return new Promise(() => { location.hash = '#/login'; });
    return `dev:${d.sub}:${encodeURIComponent(d.name)}`;   // header values must be Latin-1
  }
  const t = read(KEY);
  if (t && t.exp - 30000 > Date.now()) return t.access;
  if (t?.refresh) {
    try { await tokenRequest({ grant_type: 'refresh_token', refresh_token: t.refresh }); return read(KEY).access; }
    catch { /* refresh expired: fall through to a fresh login */ }
  }
  return login();
}

export async function reauth() {
  sessionStorage.removeItem(KEY);
  if (config.auth === 'oidc') await login();
}

export function devLogin(name) {
  const prev = read(DEV, localStorage);
  write(DEV, { sub: prev?.sub || 'dev-' + rand(9), name: name.trim() }, localStorage);
}

export async function signOut() {
  const t = read(KEY);
  sessionStorage.removeItem(KEY);
  if (config.auth === 'dev') { localStorage.removeItem(DEV); location.hash = '#/login'; location.reload(); return; }
  const d = await disco();
  if (d.end_session_endpoint) {
    const p = new URLSearchParams({ post_logout_redirect_uri: redirectUri(), client_id: config.client_id });
    if (t?.id) p.set('id_token_hint', t.id);
    location.assign(d.end_session_endpoint + '?' + p);
  } else location.assign('/');
}
