import { h, mount, icon, loading, toast } from '../ui.js';
import { post } from '../api.js';
import { t } from '../i18n.js';

export default async function join({ page, params: [token], navigate, refreshOrgs, setTitle, stale }) {
  setTitle(t('Join'));
  mount(page, loading());
  try {
    const org = await post('/v1/invites/accept', { token });
    await refreshOrgs();
    if (stale()) return;
    toast(t('You joined {org}', { org: org.name }), 'ok');
    navigate(`#/o/${org.id}`, { replace: true });
  } catch (err) {
    if (stale()) return;
    mount(page, h('div', { class: 'result' },
      h('div', { class: 'big-icon warn' }, icon('warn')),
      h('h1', {}, err.code === 'already_member' ? t('You are already in') : t('This invitation does not work')),
      h('p', { class: 'muted' }, err.code === 'invite_invalid' ? t('It may have expired or already been used. Ask for a new link.') : err.message),
      h('a', { class: 'btn big', style: 'max-inline-size:360px', href: '#/' }, t('Go to home'))));
  }
}
