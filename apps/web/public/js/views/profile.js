import { h, mount, toast, confirmDialog, initials } from '../ui.js';
import { patch, del } from '../api.js';
import { signOut } from '../auth.js';
import { fail } from '../lib.js';

export default async function profile({ page, state, setTitle }) {
  setTitle('You');
  const me = state.me;
  const name = h('input', { class: 'input', id: 'nm', value: me.full_name, required: true, maxlength: 200, autocomplete: 'name' });
  const phone = h('input', { class: 'input', id: 'ph', type: 'tel', value: me.phone || '', placeholder: '+964 750 123 4567', autocomplete: 'tel', inputmode: 'tel' });
  const save = h('button', { class: 'btn', type: 'submit' }, 'Save');

  mount(page, h('div', { class: 'stack' },
    h('div', { class: 'row' }, h('span', { class: 'avatar', style: 'inline-size:56px;block-size:56px;font-size:1.2rem' }, initials(me.full_name)),
      h('div', {}, h('h1', {}, me.full_name), me.email && h('p', { class: 'muted small' }, me.email))),
    h('form', {
      class: 'card stack',
      onSubmit: async (e) => {
        e.preventDefault(); save.disabled = true;
        try {
          Object.assign(state.me, await patch('/v1/me', { full_name: name.value.trim(), phone: phone.value.trim() }));
          toast('Saved', 'ok');
        } catch (err) { fail(err); }
        save.disabled = false;
      },
    },
    h('h2', {}, 'Your details'),
    h('p', { class: 'muted small' }, 'Your name is what organizers see on attendance lists. Your phone number is optional.'),
    h('div', { class: 'field' }, h('label', { for: 'nm' }, 'Full name'), name),
    h('div', { class: 'field' }, h('label', { for: 'ph' }, 'Phone (optional)'), phone, h('span', { class: 'hint' }, 'Include the country code, for example +964.')), save),
    h('button', { class: 'btn secondary big', type: 'button', onClick: () => signOut() }, 'Sign out'),
    h('section', { class: 'card flat stack-sm' },
      h('h3', {}, 'Delete my account'),
      h('p', { class: 'muted small' }, 'Removes your name, phone and email. Your past check-ins stay in your organizers’ records without your name.'),
      h('button', {
        class: 'btn danger small', style: 'justify-self:start', type: 'button',
        onClick: async () => {
          if (!(await confirmDialog({ title: 'Delete your account?', body: 'This cannot be undone.', confirm: 'Delete my account', danger: true }))) return;
          try { await del('/v1/me'); await signOut(); } catch (err) { fail(err); }
        },
      }, 'Delete account'))));
}
