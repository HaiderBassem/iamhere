import { h, mount, icon } from '../ui.js';
import { orgForm } from '../forms.js';
import { t } from '../i18n.js';

// First steps after creating an account: what brings you here?
export default async function start({ page, state, navigate, refreshOrgs, setTitle }) {
  setTitle(t('Welcome'));
  const first = state.me.full_name.split(/\s+/)[0];

  const choice = (ic, title, text, onClick) => h('button', { class: 'choice', type: 'button', onClick },
    h('span', { class: 'choice-icon' }, icon(ic)),
    h('span', { class: 'stack-sm' }, h('b', {}, title), h('span', { class: 'muted small' }, text)),
    icon('arrow'));

  function ask() {
    mount(page, h('div', { class: 'onboard stack' },
      h('div', { class: 'stack-sm center' },
        h('span', { class: 'eyebrow' }, t('Your account is ready')),
        h('h1', {}, t('Welcome, {name}!', { name: first })),
        h('p', { class: 'muted' }, t('What brings you here? You can do both, any time.'))),
      h('div', { class: 'stack-sm' },
        choice('scan', t('I’m checking in'), t('To classes, workshops or events. Scan the code on the screen, that’s all.'), () => navigate('#/')),
        choice('building', t('I run sessions'), t('Take attendance for a class, a team or an event. Set up takes a minute.'), organize))));
  }

  function organize() {
    const form = orgForm({ refreshOrgs, submitLabel: t('Create and continue'), onCreated: (org) => navigate(`#/o/${org.id}`) });
    mount(page, h('div', { class: 'onboard stack' },
      h('button', { class: 'back', type: 'button', onClick: ask }, icon('back'), t('Back')),
      h('div', { class: 'stack-sm' },
        h('span', { class: 'eyebrow' }, t('Step 1 of 2')),
        h('h1', {}, t('Name your organization')),
        h('p', { class: 'muted' }, t('A class, a school, a company: whatever you take attendance for. Next you’ll start your first session.'))),
      h('div', { class: 'card' }, form)));
    form.focusFirst();
  }

  ask();
}
