// Sign-in state for the built-in accounts. The API hands out an opaque bearer
// token when someone signs up or signs in; it is kept in localStorage so the
// person stays signed in on this device (30 days from last use), and sent in
// the Authorization header (never a cookie, so no CSRF).

const KEY = 'iah.session';
const RETURN = 'iah.return';     // where to go back to after signing in
let config = null;
let memoryToken = null;          // when storage is blocked (private mode): this tab only

export async function loadConfig() {
  if (!config) {
    const res = await fetch('/v1/public-config');
    if (!res.ok) throw new Error('The service is not reachable. Check your connection and try again.');
    config = await res.json();
  }
  return config;
}
/** Whether emailed links (reset password, confirm email) work on this server. */
export const emailEnabled = () => !!config?.email;

const read = () => { try { return localStorage.getItem(KEY) || memoryToken; } catch { return memoryToken; } };

export const isSignedIn = () => !!read();
/** Identifies the signed-in session (cached data must belong to it). */
export const currentToken = () => read();

/** Stores the token from sign-up / sign-in / password reset. */
export function setSession(token) {
  memoryToken = token;
  try { localStorage.setItem(KEY, token); } catch { /* private mode: this tab only */ }
}

/** Whether this page was reached by being sent back to it after signing in or up
 *  (e.g. a check-in link opened before having an account). Asks once. */
export function arrivedFromSignIn(hash) {
  try {
    const r = sessionStorage.getItem('iah.returned');
    sessionStorage.removeItem('iah.returned');
    return r === hash;
  } catch { return false; }
}

/** Remembers where the person was going (a check-in link, an invite) before signing in. */
export function rememberReturn(hash) {
  if (!hash || /^#\/?$|^#\/(signin|signup|forgot|reset|verify)/.test(hash)) return;
  try { sessionStorage.setItem(RETURN, hash); } catch { /* private mode */ }
}
export function takeReturn() {
  try {
    const r = sessionStorage.getItem(RETURN);
    sessionStorage.removeItem(RETURN);
    if (r) sessionStorage.setItem('iah.returned', r);
    return r;
  } catch { return null; }
}
export function peekReturn() {
  try { return sessionStorage.getItem(RETURN); } catch { return null; }
}

/** The bearer token; without one, goes to sign in and never resolves. */
export async function getToken() {
  const tok = read();
  if (tok) return tok;
  rememberReturn(location.hash);
  location.hash = '#/signin';
  return new Promise(() => {});
}

/** The token was refused (expired, signed out elsewhere): start over at sign in. */
export async function reauth() {
  clear();
  rememberReturn(location.hash);
  location.hash = '#/signin';
}

function clear() {
  memoryToken = null;
  try { localStorage.removeItem(KEY); } catch { /* ignore */ }
}

/** Signs out this device, then shows the start page. */
export async function signOut() {
  const tok = read();
  if (tok) {
    await fetch('/v1/auth/signout', { method: 'POST', headers: { Authorization: 'Bearer ' + tok } }).catch(() => {});
  }
  clear();
  location.hash = '#/';
  location.reload();
}

// keep tabs in step: signing out in one tab signs out the others
window.addEventListener('storage', (e) => {
  if (e.key === KEY && !e.newValue) { location.hash = '#/'; location.reload(); }
});
