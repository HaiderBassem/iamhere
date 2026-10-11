import { h, mount } from '../ui.js';
import { devLogin, authMode } from '../auth.js';
import { brand } from '../../brand.js';

// Only reachable in development (name-only login). Production redirects to the
// identity provider instead.
export default async function login({ page, navigate, setTitle }) {
  setTitle('Sign in');
  if (authMode() !== 'dev') return navigate('#/', { replace: true });
  const name = h('input', { class: 'input', id: 'name', autocomplete: 'name', required: true, placeholder: 'Your full name', maxlength: 200 });
  mount(page, h('div', { class: 'stack', style: 'max-inline-size:420px;margin:8vh auto 0' },
    h('div', { class: 'center stack-sm' },
      h('img', { src: brand.logo, alt: '', width: 64, height: 64, style: 'margin:0 auto;border-radius:16px' }),
      h('h1', {}, brand.name), h('p', { class: 'muted' }, brand.tagline)),
    h('form', {
      class: 'card stack',
      onSubmit: (e) => {
        e.preventDefault();
        if (!name.value.trim()) return;
        devLogin(name.value);
        let back = '#/';
        try { back = sessionStorage.getItem('iah.return') || '#/'; sessionStorage.removeItem('iah.return'); } catch { /* ignore */ }
        // set the destination without firing a navigation (which would start
        // requests that the reload then aborts), then start fresh signed in
        history.replaceState(null, '', back);
        location.reload();
      },
    },
    h('div', { class: 'banner' }, 'Development sign-in. Production uses your organization’s account.'),
    h('div', { class: 'field' }, h('label', { for: 'name' }, 'What’s your name?'), name),
    h('button', { class: 'btn big', type: 'submit' }, 'Continue'))));
  name.focus();
}
