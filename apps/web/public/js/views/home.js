import { h, mount, icon, fmtWhen } from '../ui.js';
import { get } from '../api.js';
import { STATUS, ROLE_LABEL, can } from '../lib.js';
import { t, tn } from '../i18n.js';

export default async function home({ page, state, refreshOrgs, setTitle }) {
  setTitle(t('Home'));
  const [orgs, hist] = await Promise.all([refreshOrgs(), get('/v1/me/attendance?limit=20')]);
  const first = state.me.full_name.split(/\s+/)[0];
  const today = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long' }).format(new Date());
  const orgCount = new Set(hist.items.map((r) => r.organization_name)).size;

  const orgCard = (o) => h('div', { class: 'org-card' },
    h('a', { class: 'org-link', href: `#/o/${o.id}` },
      h('span', { class: 'org-mark', 'aria-hidden': 'true' }, [...o.name.trim()][0]?.toUpperCase() || '?'),
      h('span', { class: 'stack-xs' }, h('b', {}, o.name), h('span', { class: 'small muted' }, t(ROLE_LABEL[o.role])))),
    can.start(o.role) && h('a', { class: 'btn small', href: `#/o/${o.id}` }, icon('qr'), t('Start')));

  mount(page, h('div', { class: 'stack-lg' },
    h('div', { class: 'stack-xs' }, h('span', { class: 'eyebrow' }, today), h('h1', {}, t('Hi {name}', { name: first }))),

    h('a', { class: 'card hero checkin-hero', href: '#/scan' },
      h('div', { class: 'stack-sm' },
        h('h2', {}, t('Check in')),
        h('p', { class: 'muted' }, t('Scan the code on the screen. Your phone’s camera app works too: it opens here by itself.'))),
      h('span', { class: 'btn light' }, icon('scan'), t('Scan to check in'))),

    h('section', { class: 'stack-sm' },
      h('div', { class: 'row between' }, h('h2', {}, t('Your organizations')),
        orgs.length > 0 && h('a', { class: 'btn ghost small', href: '#/new-org' }, icon('plus'), t('New'))),
      orgs.length === 0
        ? h('div', { class: 'card flat soft row wrap' },
            h('span', { class: 'feature-icon' }, icon('building')),
            h('div', { class: 'grow stack-xs' }, h('b', {}, t('Run sessions too?')),
              h('span', { class: 'muted small' }, t('Take attendance for a class, a team or an event. Set up takes a minute.'))),
            h('a', { class: 'btn secondary', href: '#/new-org' }, t('Set up an organization')))
        : h('div', { class: 'org-grid' }, orgs.map(orgCard))),

    h('section', { class: 'stack-sm' },
      h('div', { class: 'row between' }, h('h2', {}, t('Your attendance')),
        hist.items.length > 0 && h('span', { class: 'small muted' },
          tn('{n} check-in', '{n} check-ins', hist.items.length) + ' · ' + tn('{n} organization', '{n} organizations', orgCount))),
      hist.items.length === 0
        ? h('div', { class: 'empty' }, icon('calendar'), h('p', {}, t('Your check-ins will appear here, from every organization, in one place.')))
        : h('ul', { class: 'list' }, hist.items.map((r) => {
            const st = STATUS[r.status] || STATUS.pending;
            return h('li', { class: 'item' },
              h('span', { class: 'dot ' + st.cls }),
              h('div', { class: 'grow' }, h('div', { class: 'title' }, r.session_title),
                h('div', { class: 'small muted' }, `${r.organization_name} · ${fmtWhen(r.checked_in_at)}`)),
              h('span', { class: 'chip ' + st.cls }, t(st.label)));
          })))));
}
