import { h, mount, icon, toast, confirmDialog, copyText, fmtWhen, loading } from '../ui.js';
import { get, post, put, del } from '../api.js';
import { can, ROLE_LABEL, ROLE_HELP, fail } from '../lib.js';
import { orgHeader } from './org.js';
import { t } from '../i18n.js';

export default async function team({ page, params: [id], orgById, state, navigate, setTitle, refreshOrgs, stale }) {
  const o = orgById(id);
  if (!o) throw new Error(t('Organization not found.'));
  if (!can.manage(o.role)) return navigate(`#/o/${id}`, { replace: true });
  setTitle(`${o.name} · ${t('Team')}`);
  const body = h('div', { class: 'stack' }, loading());
  mount(page, orgHeader(o, '/team'), body);

  async function paint() {
    const [members, invites] = await Promise.all([get(`/v1/orgs/${id}/members`), get(`/v1/orgs/${id}/invites`)]);
    if (stale()) return;
    const assignable = o.role === 'owner' ? ['owner', 'admin', 'organizer', 'reviewer', 'reporter'] : ['organizer', 'reviewer', 'reporter'];
    const inviteRoles = o.role === 'owner' ? ['organizer', 'reviewer', 'reporter', 'admin'] : ['organizer', 'reviewer', 'reporter'];

    const roleSel = h('select', { class: 'input', id: 'ir' },
      inviteRoles.map((r) => h('option', { value: r }, ROLE_LABEL[r])));
    const help = h('p', { class: 'hint' }, ROLE_HELP[roleSel.value]);
    roleSel.addEventListener('change', () => { help.textContent = ROLE_HELP[roleSel.value]; });
    const linkBox = h('div', { class: 'stack-sm' });
    const makeBtn = h('button', { class: 'btn', type: 'submit' }, icon('plus'), t('Create invite link'));

    const invite = h('form', {
      class: 'card stack',
      onSubmit: async (e) => {
        e.preventDefault(); makeBtn.disabled = true;
        try {
          const inv = await post(`/v1/orgs/${id}/invites`, { role: roleSel.value });
          const link = `${location.origin}/#/join/${inv.token}`;
          mount(linkBox,
            h('div', { class: 'banner ok' }, t('Link created. It works once and expires {when}.', { when: fmtWhen(inv.expires_at) })),
            h('input', { class: 'input', readonly: true, value: link, 'aria-label': t('Invite link'), onFocus: (ev) => ev.target.select() }),
            h('div', { class: 'row wrap' },
              h('button', { class: 'btn secondary', type: 'button', onClick: async () => toast((await copyText(link)) ? t('Link copied') : t('Could not copy'), 'ok') }, icon('copy'), t('Copy')),
              navigator.share && h('button', { class: 'btn secondary', type: 'button', onClick: () => navigator.share({ title: t('Join {org}', { org: o.name }), url: link }).catch(() => {}) }, icon('share'), t('Share'))));
          list.replaceWith(pendingList((await get(`/v1/orgs/${id}/invites`)).items));
        } catch (err) { fail(err); }
        makeBtn.disabled = false;
      },
    },
    h('h2', {}, t('Invite someone')),
    h('p', { class: 'muted' }, t('Send them the link. When they open it and sign in, they join.')),
    h('div', { class: 'field' }, h('label', { for: 'ir' }, t('They can')), roleSel, help), makeBtn, linkBox);

    const pendingList = (items) => h('section', { class: 'stack-sm', id: 'pending' },
      items.length > 0 && h('h2', {}, t('Waiting to join')),
      items.map((i) => h('div', { class: 'item' },
        h('div', { class: 'grow' }, h('div', { class: 'title' }, t(ROLE_LABEL[i.role])), h('div', { class: 'small muted' }, t('Expires {when}', { when: fmtWhen(i.expires_at) }))),
        h('button', {
          class: 'btn ghost small', type: 'button',
          onClick: async (ev) => { try { await del(`/v1/orgs/${id}/invites/${i.id}`); ev.target.closest('.item').remove(); } catch (err) { fail(err); } },
        }, t('Cancel')))));
    let list = pendingList(invites.items);

    const rows = members.items.map((m) => {
      const self = m.user_id === state.me.id;
      const editable = !self && (o.role === 'owner' || !['owner', 'admin'].includes(m.role));
      return h('div', { class: 'item' },
        h('div', { class: 'grow who' }, h('div', { class: 'name' }, m.full_name, self && h('span', { class: 'muted' }, t(' (you)')))),
        editable
          ? h('select', {
              class: 'input', style: 'inline-size:auto;min-block-size:38px', 'aria-label': t('Role of {name}', { name: m.full_name }),
              onChange: async (ev) => { try { await put(`/v1/orgs/${id}/members/${m.user_id}`, { role: ev.target.value }); toast(t('Role updated'), 'ok'); } catch (err) { fail(err); paint(); } },
            }, assignable.map((r) => h('option', { value: r, selected: r === m.role }, ROLE_LABEL[r])))
          : h('span', { class: 'chip' }, ROLE_LABEL[m.role]),
        (editable || self) && h('button', {
          class: 'btn ghost small', type: 'button',
          onClick: async () => {
            const ok = await confirmDialog({
              title: self ? t('Leave {org}?', { org: o.name }) : t('Remove {name}?', { name: m.full_name }),
              body: self ? t('You will lose access to its sessions.') : t('They will lose access to this organization.'),
              confirm: self ? t('Leave') : t('Remove'), danger: true,
            });
            if (!ok) return;
            try {
              await del(`/v1/orgs/${id}/members/${m.user_id}`);
              if (self) { await refreshOrgs(); return navigate('#/'); }
              paint();
            } catch (err) { fail(err); }
          },
        }, self ? t('Leave') : t('Remove')));
    });

    mount(body, invite, list, h('section', { class: 'stack-sm' }, h('h2', {}, t('People ({n})', { n: members.items.length })), h('div', { class: 'list' }, rows)));
  }
  await paint();
}
