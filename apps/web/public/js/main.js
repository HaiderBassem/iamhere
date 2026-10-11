// App shell + router. Views live in ./views and are loaded on demand.
import { loadConfig, isSignedIn, rememberReturn, signOut, emailEnabled, currentToken } from './auth.js';
import { get, post } from './api.js';
import { brand } from '../brand.js';
import { h, mount, loading, icon, initials, toast } from './ui.js';
import { t, initI18n } from './i18n.js';
import { holdScan } from './held.js';

const root = document.getElementById('app');
export const state = { me: null, orgs: null, account: null };

// [pattern, view module, public?]. Order matters: first match wins.
const ROUTES = [
  [/^#\/?$/, 'home'],
  [/^#\/signin$/, 'signin', true],
  [/^#\/signup$/, 'signup', true],
  [/^#\/forgot$/, 'forgot', true],
  [/^#\/reset\/([\w-]+)$/, 'reset', true],
  [/^#\/verify\/([\w-]+)$/, 'verify', true],
  [/^#\/privacy$/, 'privacy', true],
  [/^#\/scan$/, 'scan'],
  [/^#\/c\/([\w-]+)$/, 'checkin'],
  [/^#\/join\/([\w-]+)$/, 'join'],
  [/^#\/me$/, 'profile'],
  [/^#\/new-org$/, 'neworg'],
  [/^#\/o\/([\w-]+)$/, 'org'],
  [/^#\/o\/([\w-]+)\/s\/([\w-]+)$/, 'session'],
  [/^#\/o\/([\w-]+)\/(team|places|settings|report)$/, 'orgtab'],
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
  window.scrollTo(0, 0);

  const hash = location.hash || '#/';
  const m = ROUTES.map(([re, view, pub]) => [re.exec(hash), view, pub]).find(([x]) => x);
  if (!m) return navigate('#/', { replace: true });
  let [match, view, pub] = m;

  const signedIn = isSignedIn();
  if (view === 'home' && !signedIn) { view = 'landing'; pub = true; }
  if (!pub && !signedIn) {
    rememberReturn(hash);                       // come back here after signing in (a check-in link, an invite)
    // a check-in link: hold the scan while the account is created, so there is no second scan
    if (view === 'checkin') { mount(root, h('main', { class: 'public' }, loading())); await holdScan(match[1]); if (my !== token) return; }
    return navigate(/^#\/(c|join)\//.test(hash) ? '#/signup' : '#/signin', { replace: true });
  }
  if (signedIn && ['signin', 'signup', 'forgot'].includes(view)) return navigate('#/', { replace: true });

  // what is cached about "me" belongs to one sign-in: someone else signing in on this page starts fresh
  if (state.token !== currentToken()) Object.assign(state, { me: null, orgs: null, account: null, token: currentToken() });
  const page = h('main', { id: 'main', class: pub ? 'public' : '' }, loading());
  if (pub) mount(root, page);
  else mount(root, topbar(hash), page, tabbar(hash));
  try {
    if (!pub && !state.me) {
      [state.me, state.orgs, state.account] = await Promise.all([
        get('/v1/me'), get('/v1/orgs').then((r) => r.items), get('/v1/auth/account').catch(() => null),
      ]);
      if (my !== token) return;
      mount(root, topbar(hash), banner(), page, tabbar(hash));
    } else if (!pub) {
      mount(root, topbar(hash), banner(), page, tabbar(hash));
    }
    const mod = await import(`./views/${view}.js`);
    if (my !== token) return;
    const ctx = {
      params: match.slice(1), page, navigate, state, refreshOrgs, orgById,
      onCleanup: (fn) => cleanup.push(fn),
      stale: () => my !== token,
      setTitle: (title) => { document.title = title ? `${title} · ${brand.name}` : `${brand.name} · ${t(brand.tagline)}`; },
    };
    ctx.setTitle('');
    await mod.default(ctx);
  } catch (err) {
    if (my !== token) return;
    console.error(err);
    mount(page, h('div', { class: 'empty' },
      h('p', {}, err.message || t('Something went wrong.')),
      h('p', { style: 'margin-top:12px' }, h('button', { class: 'btn secondary', onClick: () => route() }, t('Try again')))));
  }
}

function topbar(hash) {
  const link = (href, label, on) => h('a', { href, 'aria-current': on ? 'page' : false }, label);
  const menuBtn = h('button', { class: 'avatar', type: 'button', 'aria-haspopup': 'menu', 'aria-expanded': 'false', 'aria-label': t('Your account') },
    initials(state.me?.full_name));
  const menu = h('div', { class: 'menu', role: 'menu', hidden: true },
    h('div', { class: 'menu-head' }, h('b', {}, state.me?.full_name || ''), h('span', { class: 'small muted' }, state.account?.email || '')),
    h('a', { href: '#/me', role: 'menuitem' }, icon('user'), t('Your account')),
    h('a', { href: '#/new-org', role: 'menuitem' }, icon('plus'), t('New organization')),
    h('button', { type: 'button', role: 'menuitem', onClick: () => signOut() }, icon('logout'), t('Sign out')));
  menuBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    const open = menu.hidden;
    closeMenus();
    menu.hidden = !open;
    menuBtn.setAttribute('aria-expanded', String(open));
    if (open) menu.querySelector('a')?.focus();
  });
  return h('header', { class: 'topbar' },
    h('a', { class: 'skip', href: '#main', onClick: (e) => { e.preventDefault(); document.getElementById('main')?.focus(); } }, t('Skip to content')),
    h('a', { class: 'brand', href: '#/' }, h('img', { src: brand.logo, alt: '' }), h('span', {}, brand.name)),
    h('nav', { class: 'topnav', 'aria-label': t('Main') },
      link('#/', t('Home'), hash === '#/' || hash === '#' || /^#\/(o|new-org)/.test(hash)),
      link('#/scan', t('Check in'), hash === '#/scan' || hash.startsWith('#/c/'))),
    h('span', { class: 'grow' }),
    h('div', { class: 'menu-wrap' }, menuBtn, menu));
}

function tabbar(hash) {
  const tab = (href, label, ic, on) => h('a', { href, 'aria-current': on ? 'page' : false }, icon(ic), label);
  return h('nav', { class: 'tabbar', 'aria-label': t('Main') },
    tab('#/', t('Home'), 'home', hash === '#/' || hash === '#' || /^#\/(o|new-org)/.test(hash)),
    tab('#/scan', t('Check in'), 'scan', hash === '#/scan' || hash.startsWith('#/c/')),
    tab('#/me', t('You'), 'user', hash === '#/me'));
}

// A gentle reminder to confirm the email address (it is how a forgotten
// password comes back). Shown only when the server can send email.
function banner() {
  const a = state.account;
  if (!a || a.email_verified || !emailEnabled()) return null;
  const btn = h('button', { class: 'linkish', type: 'button' }, t('Send the link again'));
  btn.addEventListener('click', async () => {
    btn.disabled = true;
    try { await post('/v1/auth/email/resend'); toast(t('Sent. Check your inbox.'), 'ok'); } catch (err) { toast(err.message, 'bad'); btn.disabled = false; }
  });
  return h('div', { class: 'notice', role: 'status' }, icon('mail'),
    h('span', {}, t('Please confirm your email. We sent a link to {email}.', { email: a.email }), ' ', btn));
}

// one page-wide listener closes any open menu (outside click, Escape)
function closeMenus() {
  document.querySelectorAll('.menu').forEach((m) => { m.hidden = true; });
  document.querySelectorAll('[aria-haspopup="menu"]').forEach((b) => b.setAttribute('aria-expanded', 'false'));
}
document.addEventListener('click', closeMenus);
document.addEventListener('keydown', (e) => { if (e.key === 'Escape') closeMenus(); });

export { signOut };

async function boot() {
  initI18n();
  try {
    await loadConfig();
  } catch (err) {
    mount(root, h('main', { class: 'public' }, h('div', { class: 'empty' },
      h('p', {}, err.message),
      h('p', { style: 'margin-top:12px' }, h('a', { class: 'btn', href: '/' }, t('Try again'))))));
    return;
  }
  window.addEventListener('hashchange', route);
  route();
}
boot();
if (!window.isSecureContext && location.hostname !== 'localhost') {
  toast(t('This page is not secure (HTTPS). Camera and location will not work.'), 'bad');
}
