import { h, mount, icon } from '../ui.js';
import { orgForm } from '../forms.js';
import { t } from '../i18n.js';

export default async function neworg({ page, navigate, refreshOrgs, setTitle }) {
  setTitle(t('New organization'));
  const form = orgForm({ refreshOrgs, onCreated: (org) => navigate(`#/o/${org.id}`) });
  mount(page, h('div', { class: 'stack narrow' },
    h('a', { class: 'back', href: '#/' }, icon('back'), t('Home')),
    h('div', { class: 'page-head' }, h('h1', {}, t('New organization')),
      h('p', { class: 'muted' }, t('Just a name. You can invite people and add places later.'))),
    h('div', { class: 'card' }, form)));
  form.focusFirst();
}
