import { h, mount, icon } from '../ui.js';
import { publicPost } from '../api.js';
import { authLayout, field, formError, busy } from '../forms.js';
import { t } from '../i18n.js';

export default async function forgot({ page, setTitle }) {
  setTitle(t('Reset your password'));
  const email = h('input', { class: 'input', id: 'fg-email', type: 'email', required: true, maxlength: 254, autocomplete: 'email',
    inputmode: 'email', autocapitalize: 'off', spellcheck: 'false', placeholder: 'name@example.com' });
  const err = formError();
  const btn = h('button', { class: 'btn big', type: 'submit' }, t('Send reset link'));

  const sent = (addr) => mount(page, authLayout({
    title: t('Check your inbox'),
    body: h('div', { class: 'stack center' },
      h('div', { class: 'big-icon brand', style: 'margin-inline:auto' }, icon('mail')),
      h('p', {}, t('If {email} has an account, a link to choose a new password is on its way. It works for one hour.', { email: addr })),
      h('p', { class: 'small muted' }, t('Nothing after a few minutes? Check the spam folder, or try again.'))),
    footer: h('a', { href: '#/signin' }, t('Back to sign in')),
  }));

  mount(page, authLayout({
    title: t('Reset your password'),
    subtitle: t('Enter the email you signed up with. We’ll send you a link to choose a new password.'),
    body: h('form', {
      class: 'stack', novalidate: true,
      onSubmit: async (e) => {
        e.preventDefault();
        err.hide();
        if (!email.validity.valid || !email.value.trim()) { err.show(t('Enter a valid email address, like name@example.com.')); return email.focus(); }
        await busy(btn, t('Sending…'), async () => {
          try {
            await publicPost('/v1/auth/password/forgot', { email: email.value.trim() });
            sent(email.value.trim());
          } catch (e2) { err.show(e2.message); }
        });
      },
    }, err, field('fg-email', t('Email'), email), btn),
    footer: h('a', { href: '#/signin' }, t('Back to sign in')),
  }));
  email.focus();
}
