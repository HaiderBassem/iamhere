import { h, mount } from '../ui.js';
import { publicPost } from '../api.js';
import { setSession, takeReturn, peekReturn, emailEnabled } from '../auth.js';
import { authLayout, field, passwordField, formError, busy } from '../forms.js';
import { t } from '../i18n.js';

export default async function signin({ page, setTitle }) {
  setTitle(t('Sign in'));
  const going = peekReturn();
  const email = h('input', { class: 'input', id: 'si-email', type: 'email', required: true, maxlength: 254, autocomplete: 'username',
    inputmode: 'email', autocapitalize: 'off', spellcheck: 'false', placeholder: 'name@example.com' });
  const forgot = emailEnabled() ? h('a', { class: 'small', href: '#/forgot' }, t('Forgot password?')) : null;
  const pw = passwordField('si-pw', t('Password'), { labelExtra: forgot });
  const err = formError();
  const btn = h('button', { class: 'btn big', type: 'submit' }, t('Sign in'));

  mount(page, authLayout({
    title: t('Welcome back'),
    subtitle: going?.startsWith('#/c/') ? t('Sign in to check in.') : t('Sign in to your account.'),
    body: h('form', {
      class: 'stack', novalidate: true,
      onSubmit: async (e) => {
        e.preventDefault();
        err.hide();
        if (!email.value.trim() || !pw.input.value) { err.show(t('Enter your email and password.')); return (email.value ? pw.input : email).focus(); }
        await busy(btn, t('Signing in…'), async () => {
          try {
            const res = await publicPost('/v1/auth/signin', { email: email.value.trim(), password: pw.input.value });
            setSession(res.token);
            location.hash = takeReturn() || '#/';
          } catch (e2) {
            err.show(e2.message);
            pw.input.select();
            pw.input.focus();
          }
        });
      },
    }, err, field('si-email', t('Email'), email), pw, btn,
    !emailEnabled() && h('p', { class: 'small muted center' }, t('Forgot your password? Ask the person who runs this service for a reset link.'))),
    footer: [t('New here?'), ' ', h('a', { href: '#/signup' }, t('Create an account'))],
  }));
  email.focus();
}
