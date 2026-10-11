import { h, mount, toast, confirmDialog, initials, icon } from '../ui.js';
import { patch, del, post } from '../api.js';
import { signOut, emailEnabled } from '../auth.js';
import { fail } from '../lib.js';
import { field, passwordField, formError, busy } from '../forms.js';
import { t, tn } from '../i18n.js';

export default async function profile({ page, state, setTitle }) {
  setTitle(t('Your account'));
  const me = state.me;
  const acct = state.account;

  // ---- details
  const name = h('input', { class: 'input', id: 'nm', value: me.full_name, required: true, maxlength: 200, autocomplete: 'name' });
  const phone = h('input', { class: 'input', id: 'ph', type: 'tel', value: me.phone || '', placeholder: '+964 750 123 4567', autocomplete: 'tel', inputmode: 'tel' });
  const save = h('button', { class: 'btn', type: 'submit' }, t('Save changes'));
  const details = h('form', {
    class: 'card stack',
    onSubmit: async (e) => {
      e.preventDefault();
      await busy(save, t('Saving…'), async () => {
        try {
          Object.assign(state.me, await patch('/v1/me', { full_name: name.value.trim(), phone: phone.value.trim() }));
          toast(t('Saved'), 'ok');
        } catch (err) { fail(err); }
      });
    },
  },
  h('div', { class: 'stack-xs' }, h('h2', {}, t('Your details')),
    h('p', { class: 'muted small' }, t('Your name is what organizers see on attendance lists, in every organization.'))),
  field('nm', t('Full name'), name),
  field('ph', t('Phone (optional)'), phone, t('Include the country code, for example +964.')),
  h('div', {}, save));

  // ---- sign-in & security
  const cur = passwordField('pw-cur', t('Current password'));
  const next = passwordField('pw-new', t('New password'), { fresh: true });
  const pwErr = formError();
  const pwBtn = h('button', { class: 'btn', type: 'submit' }, t('Change password'));
  const pwForm = h('form', {
    class: 'stack', hidden: true, novalidate: true,
    onSubmit: async (e) => {
      e.preventDefault();
      pwErr.hide();
      if (next.input.value.length < 8) { pwErr.show(t('Use at least 8 characters for the password.')); return next.input.focus(); }
      await busy(pwBtn, t('Saving…'), async () => {
        try {
          await post('/v1/auth/password', { current: cur.input.value, new: next.input.value });
          toast(t('Password changed. Other devices were signed out.'), 'ok');
          pwForm.reset(); pwForm.hidden = true; pwOpen.hidden = false;
        } catch (err) { pwErr.show(err.message); }
      });
    },
  }, pwErr, cur, next, h('div', { class: 'row' }, pwBtn,
    h('button', { class: 'btn ghost', type: 'button', onClick: () => { pwForm.reset(); pwForm.hidden = true; pwOpen.hidden = false; } }, t('Cancel'))));
  const pwOpen = h('button', { class: 'btn secondary', type: 'button', onClick: () => { pwForm.hidden = false; pwOpen.hidden = true; cur.input.focus(); } },
    t('Change password'));

  const resend = h('button', { class: 'linkish', type: 'button' }, t('Send the link again'));
  resend.addEventListener('click', async () => {
    resend.disabled = true;
    try { await post('/v1/auth/email/resend'); toast(t('Sent. Check your inbox.'), 'ok'); } catch (err) { fail(err); resend.disabled = false; }
  });

  const security = h('section', { class: 'card stack' },
    h('div', { class: 'stack-xs' }, h('h2', {}, t('Sign-in & security')),
      h('p', { class: 'muted small' }, t('One account for every organization you attend or run.'))),
    acct && h('div', { class: 'kv' },
      h('span', { class: 'muted' }, t('Email')),
      h('span', { class: 'row wrap' }, h('b', {}, acct.email),
        acct.email_verified
          ? h('span', { class: 'chip ok' }, icon('check'), t('Confirmed'))
          : emailEnabled() && [h('span', { class: 'chip warn' }, t('Not confirmed')), resend])),
    h('div', { class: 'kv' }, h('span', { class: 'muted' }, t('Password')), h('span', {}, pwOpen)),
    pwForm,
    h('div', { class: 'kv' }, h('span', { class: 'muted' }, t('Devices')),
      h('button', {
        class: 'btn secondary', type: 'button',
        onClick: async (e) => {
          try {
            const r = await post('/v1/auth/signout-others');
            toast(r.signed_out ? tn('Signed out of {n} other device', 'Signed out of {n} other devices', r.signed_out) : t('No other devices were signed in.'), 'ok');
            e.target.disabled = true;
          } catch (err) { fail(err); }
        },
      }, t('Sign out of other devices'))));

  mount(page, h('div', { class: 'stack-lg narrow' },
    h('div', { class: 'row' }, h('span', { class: 'avatar xl' }, initials(me.full_name)),
      h('div', { class: 'stack-xs' }, h('h1', {}, me.full_name), acct && h('p', { class: 'muted small' }, acct.email))),
    details,
    security,
    h('button', { class: 'btn secondary big', type: 'button', onClick: () => signOut() }, icon('logout'), t('Sign out')),
    h('section', { class: 'card flat danger-zone stack-sm' },
      h('h3', {}, t('Delete my account')),
      h('p', { class: 'muted small' }, t('Removes your name, email, phone and password. Your past check-ins stay in your organizers’ records without your name.')),
      h('button', {
        class: 'btn danger small', style: 'justify-self:start', type: 'button',
        onClick: async () => {
          if (!(await confirmDialog({ title: t('Delete your account?'), body: t('This cannot be undone.'), confirm: t('Delete my account'), cancel: t('Cancel'), danger: true }))) return;
          try { await del('/v1/me'); await signOut(); } catch (err) { fail(err); }
        },
      }, t('Delete account')))));
}
