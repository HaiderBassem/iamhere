import { h, mount, icon, toast } from '../ui.js';
import { publicPost } from '../api.js';
import { setSession } from '../auth.js';
import { authLayout, passwordField, formError, busy } from '../forms.js';
import { t } from '../i18n.js';

export default async function reset({ page, params: [token], setTitle }) {
  setTitle(t('Choose a new password'));
  history.replaceState(null, '', '#/reset/' + token);   // keep it out of other places' history
  const pw = passwordField('rs-pw', t('New password'), { fresh: true });
  const err = formError();
  const btn = h('button', { class: 'btn big', type: 'submit' }, t('Save and sign in'));

  const expired = () => mount(page, authLayout({
    title: t('This link has expired'),
    body: h('div', { class: 'stack center' },
      h('div', { class: 'big-icon warn', style: 'margin-inline:auto' }, icon('warn')),
      h('p', { class: 'muted' }, t('Reset links work once, for one hour. Ask for a new one.')),
      h('a', { class: 'btn big', href: '#/forgot' }, t('Send a new link'))),
  }));

  mount(page, authLayout({
    title: t('Choose a new password'),
    subtitle: t('You’ll be signed out everywhere else.'),
    body: h('form', {
      class: 'stack', novalidate: true,
      onSubmit: async (e) => {
        e.preventDefault();
        err.hide();
        if (pw.input.value.length < 8) { err.show(t('Use at least 8 characters for the password.')); return pw.input.focus(); }
        await busy(btn, t('Saving…'), async () => {
          try {
            const res = await publicPost('/v1/auth/password/reset', { token, password: pw.input.value });
            setSession(res.token);
            toast(t('Password changed. You’re signed in.'), 'ok');
            location.hash = '#/';
          } catch (e2) {
            if (e2.code === 'link_expired') return expired();
            err.show(e2.message);
            pw.input.focus();
          }
        });
      },
    }, err, pw, btn),
  }));
  pw.input.focus();
}
