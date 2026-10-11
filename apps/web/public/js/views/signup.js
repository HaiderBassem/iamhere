import { h, mount, icon } from '../ui.js';
import { publicPost } from '../api.js';
import { setSession, takeReturn, peekReturn } from '../auth.js';
import { authLayout, field, passwordField, formError, busy } from '../forms.js';
import { t } from '../i18n.js';
import { heldScan, setHeldResult, dropHeld } from '../held.js';

export default async function signup({ page, setTitle }) {
  setTitle(t('Create your account'));
  const going = peekReturn();
  const held = going?.startsWith('#/c/') ? heldScan() : null;
  const name = h('input', { class: 'input', id: 'su-name', required: true, maxlength: 200, autocomplete: 'name', placeholder: t('e.g. Sara Ahmed') });
  const email = h('input', { class: 'input', id: 'su-email', type: 'email', required: true, maxlength: 254, autocomplete: 'email',
    inputmode: 'email', autocapitalize: 'off', spellcheck: 'false', placeholder: 'name@example.com' });
  const pw = passwordField('su-pw', t('Password'), { fresh: true });
  const err = formError();
  const btn = h('button', { class: 'btn big', type: 'submit' }, t('Create account'));

  mount(page, authLayout({
    title: t('Create your account'),
    subtitle: held
      ? t('Create your account and you’re checked in. No need to scan again.')
      : going?.startsWith('#/c/')
      ? t('One account checks you in everywhere: every class, workshop and event. It takes 20 seconds.')
      : going?.startsWith('#/join/')
        ? t('Create your account to join the team. It takes 20 seconds.')
        : t('One account for every class, workshop and event you attend or run.'),
    body: h('form', {
      class: 'stack', novalidate: true,
      onSubmit: async (e) => {
        e.preventDefault();
        err.hide();
        if (!name.value.trim()) { err.show(t('Enter your name.')); return name.focus(); }
        if (!email.validity.valid || !email.value.trim()) { err.show(t('Enter a valid email address, like name@example.com.')); return email.focus(); }
        if (pw.input.value.length < 8) { err.show(t('Use at least 8 characters for the password.')); return pw.input.focus(); }
        await busy(btn, t('Creating your account…'), async () => {
          try {
            const body = { name: name.value.trim(), email: email.value.trim(), password: pw.input.value };
            const claim = heldScan()?.claim;            // re-read: it may have run out while typing
            if (held && claim) body.claim = claim;
            const res = await publicPost('/v1/auth/signup', body);
            setSession(res.token);
            const back = takeReturn();
            if (body.claim) { setHeldResult({ checkin: res.checkin, error: res.checkin_error }); location.hash = '#/c/held'; return; }
            dropHeld();
            location.hash = back || '#/';
          } catch (e2) {
            err.show(e2.message);
            (e2.code === 'email_taken' || e2.code === 'invalid_email' ? email : e2.code === 'weak_password' ? pw.input : name).focus();
          }
        });
      },
    }, err,
    held && h('div', { class: 'auth-context' }, h('span', { class: 'feature-icon' }, icon('check')),
      h('span', { class: 'stack-xs' }, h('span', { class: 'small muted' }, t('Checking in to')), h('b', {}, held.title), h('span', { class: 'small muted' }, held.org))),
    field('su-name', t('Full name'), name, t('Organizers see this name on attendance lists.')),
    field('su-email', t('Email'), email),
    pw, btn,
    h('p', { class: 'small muted center' }, t('No spam. Your email is only used to sign in and to reset your password.'))),
    footer: [t('Already have an account?'), ' ', h('a', { href: '#/signin' }, t('Sign in'))],
  }));
  name.focus();
}
