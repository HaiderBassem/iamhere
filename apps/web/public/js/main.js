// App shell + router. Views live in ./views and are loaded on demand.
import { initAuth, authMode, isSignedIn, login, signOut } from './auth.js';
import { get } from './api.js';
import { brand } from '../brand.js';
import { h, mount, loading, icon, initials, toast } from './ui.js';

const root = document.getElementById('app');
export const state = { me: null, orgs: null };

// [pattern, view module]. Order matters: first match wins.
const ROUTES = [
  [/^#\/?$/, 'home'],
  [/^#\/login$/, 'login'],
  [/^#\/scan$/, 'scan'],
  [/^#\/c\/([\w-]+)$/, 'checkin'],
  [/^#\/join\/([\w-]+)$/, 'join'],
  [/^#\/me$/, 'profile'],
  [/^#\/new-org$/, 'neworg'],
  [/^#\/o\/([\w-]+)$/, 'org'],
  [/^#\/o\/([\w-]+)\/s\/([\w-]+)$/, 'session'],
  [/^#\/o\/([\w-]+)\/(team|places|settings)$/, 'orgtab'],
];

let cleanup = [];
let token = 0;

export async function refreshOrgs() {
  state.orgs = (await get('/v1/orgs')).items;
  return state.orgs;
}
export const orgById = (id) => state.orgs?.find((o) => o.id === id);

export function navigate(hash, { replace = false } = {}) {
  if (replace) history.replaceState(null, '', hash);
  if (location.hash === hash && !replace) return route();
  location.hash = hash;
  if (replace) route();
}

async function route() {
  const my = ++token;
  cleanup.forEach((fn) => { try { fn(); } catch { /* ignore */ } });
  cleanup = [];
  document.body.classList.remove('present');

  const hash = location.hash || '#/';
  const m = ROUTES.map(([re, view]) => [re.exec(hash), view]).find(([x]) => x);
  if (!m) return navigate('#/', { replace: true });
  const [match, view] = m;

  if (view !== 'login' && !isSignedIn()) {
    if (authMode() === 'oidc') return login(hash);
    return navigate('#/login', { replace: true });
  }

  const page = h('main', {}, loading());
  mount(root, view === 'login' ? null : topbar(), page, view === 'login' ? null : tabbar(hash));
  if (view === 'login') mount(root, page);
  try {
    if (view !== 'login' && !state.me) {
      [state.me, state.orgs] = await Promise.all([get('/v1/me'), get('/v1/orgs').then((r) => r.items)]);
      if (my !== token) return;
      mount(root, topbar(), page, tabbar(hash));
    }
    const mod = await import(`./views/${view}.js`);
    if (my !== token) return;
    const ctx = {
      params: match.slice(1), page, navigate, state, refreshOrgs, orgById,
      onCleanup: (fn) => cleanup.push(fn),
      stale: () => my !== token,
      setTitle: (t) => { document.title = t ? `${t} · ${brand.name}` : brand.name; },
    };
    ctx.setTitle('');
    await mod.default(ctx);
  } catch (err) {
    if (my !== token) return;
    console.error(err);
    mount(page, h('div', { class: 'empty' },
      h('p', {}, err.message || 'Something went wrong.'),
      h('p', { style: 'margin-top:12px' }, h('button', { class: 'btn secondary', onClick: () => route() }, 'Try again'))));
  }
}

function topbar() {
  return h('header', { class: 'topbar' },
    h('a', { class: 'brand', href: '#/' }, h('img', { src: brand.logo, alt: '' }), h('span', {}, brand.name)),
    h('span', { class: 'grow' }),
    h('a', { class: 'avatar', href: '#/me', 'aria-label': 'Your profile' }, initials(state.me?.full_name)));
}

function tabbar(hash) {
  const tab = (href, label, ic, on) => h('a', { href, 'aria-current': on ? 'page' : false }, icon(ic), label);
  return h('nav', { class: 'tabbar', 'aria-label': 'Main' },
    tab('#/', 'Home', 'home', hash === '#/' || hash === '#' || /^#\/(o|new-org)/.test(hash)),
    tab('#/scan', 'Check in', 'scan', hash === '#/scan' || hash.startsWith('#/c/')),
    tab('#/me', 'You', 'user', hash === '#/me'));
}

export { signOut };

async function boot() {
  try {
    await initAuth();
  } catch (err) {
    mount(root, h('main', {}, h('div', { class: 'empty' },
      h('p', {}, err.message),
      h('p', { style: 'margin-top:12px' }, h('a', { class: 'btn', href: '/' }, 'Back to start')))));
    return;
  }
  window.addEventListener('hashchange', route);
  route();
}
boot();
if (!window.isSecureContext && location.hostname !== 'localhost') {
  toast('This page is not secure (HTTPS). Camera and location will not work.', 'bad');
}
