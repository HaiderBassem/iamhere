import { h, mount, icon, loading } from '../ui.js';
import { publicPost } from '../api.js';
import { isSignedIn } from '../auth.js';
import { authLayout } from '../forms.js';
import { t } from '../i18n.js';

export default async function verify({ page, params: [token], state, setTitle, stale }) {
  setTitle(t('Confirm your email'));
  mount(page, loading());
  let ok = true, message = '';
  try { await publicPost('/v1/auth/email/confirm', { token }); } catch (err) { ok = false; message = err.message; }
  if (stale()) return;
  if (ok && state.account) state.account.email_verified = true;
  const next = isSignedIn() ? ['#/', t('Continue')] : ['#/signin', t('Sign in')];
  mount(page, authLayout({
    title: ok ? t('Email confirmed') : t('This link has expired'),
    body: h('div', { class: 'stack center' },
      h('div', { class: 'big-icon ' + (ok ? 'ok' : 'warn'), style: 'margin-inline:auto' }, icon(ok ? 'check' : 'warn')),
      h('p', { class: 'muted' }, ok ? t('Thanks! If you ever forget your password, a reset link can reach you here.') : message),
      !ok && isSignedIn() && h('p', { class: 'small muted' }, t('You can ask for a new link from the banner at the top of the app.')),
      h('a', { class: 'btn big', href: next[0] }, next[1])),
  }));
}
