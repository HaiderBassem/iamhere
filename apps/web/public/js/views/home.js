import { h, mount, icon, fmtWhen } from '../ui.js';
import { get } from '../api.js';
import { STATUS, ROLE_LABEL, can } from '../lib.js';

export default async function home({ page, state, refreshOrgs, setTitle }) {
  setTitle('');
  const [orgs, hist] = await Promise.all([refreshOrgs(), get('/v1/me/attendance?limit=8')]);
  const first = state.me.full_name.split(/\s+/)[0];

  mount(page, h('div', { class: 'stack' },
    h('h1', {}, `Hi ${first} \u{1F44B}`),

    h('a', { class: 'card hero stack-sm', href: '#/scan', style: 'text-decoration:none' },
      h('div', { class: 'row' }, icon('scan'), h('h2', {}, 'Check in')),
      h('p', { class: 'muted' }, 'Scan the code on the screen. Or use your phone’s camera app: it opens here by itself.'),
      h('span', { class: 'btn light big', style: 'margin-top:6px' }, 'Scan to check in')),

    h('section', { class: 'stack-sm' },
      h('div', { class: 'row between' }, h('h2', {}, 'Your organizations'),
        orgs.length > 0 && h('a', { class: 'btn ghost small', href: '#/new-org' }, icon('plus'), 'New')),
      orgs.length === 0
        ? h('div', { class: 'empty stack-sm' },
            h('p', {}, 'Taking attendance for a class, team or event?'),
            h('a', { class: 'btn', href: '#/new-org' }, 'Set up your organization'))
        : h('div', { class: 'list' }, orgs.map((o) =>
            h('a', { class: 'item', href: `#/o/${o.id}` },
              h('div', { class: 'grow' }, h('div', { class: 'title' }, o.name), h('div', { class: 'small muted' }, ROLE_LABEL[o.role])),
              can.start(o.role) && h('span', { class: 'chip brand' }, 'Start attendance'))))),

    hist.items.length > 0 && h('section', { class: 'stack-sm' },
      h('h2', {}, 'Your recent check-ins'),
      h('div', { class: 'list' }, hist.items.map((r) => {
        const st = STATUS[r.status] || STATUS.pending;
        return h('div', { class: 'item' },
          h('div', { class: 'grow' }, h('div', { class: 'title' }, r.session_title),
            h('div', { class: 'small muted' }, `${r.organization_name} · ${fmtWhen(r.checked_in_at)}`)),
          h('span', { class: 'chip ' + st.cls }, st.label));
      })))));
}
