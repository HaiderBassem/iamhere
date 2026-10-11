import { h, mount, icon, toast, fmtWhen, uuid } from '../ui.js';
import { get, post } from '../api.js';
import { can, MODES, ROLE_LABEL, modeLabel, fail, getPosition, defaultTitle } from '../lib.js';
import { t, tn } from '../i18n.js';

export function orgHeader(org, active) {
  const tabs = [['', t('Sessions')], ['/report', t('Reports')],
    ...(can.manage(org.role) ? [['/team', t('Team')], ['/places', t('Places')], ['/settings', t('Settings')]] : [])];
  return h('div', {},
    h('a', { class: 'back', href: '#/' }, icon('back'), t('Home')),
    h('div', { class: 'page-head' }, h('h1', {}, org.name), h('p', { class: 'muted small' }, t('Your role: {role}', { role: t(ROLE_LABEL[org.role]) }))),
    tabs.length > 1 && h('nav', { class: 'tabs', 'aria-label': org.name },
      tabs.map(([p, label]) => h('a', { href: `#/o/${org.id}${p}`, 'aria-current': active === p ? 'page' : false }, label))));
}

export default async function org({ page, params: [id], orgById, state, navigate, setTitle, refreshOrgs, stale }) {
  let o = orgById(id);
  if (!o) { await refreshOrgs(); o = orgById(id); }
  if (!o) throw new Error(t('Organization not found.'));
  setTitle(o.name);

  const [settings, venues, sessions] = await Promise.all([
    get(`/v1/orgs/${id}/settings`), get(`/v1/orgs/${id}/venues`), get(`/v1/orgs/${id}/sessions?limit=15`),
  ]);
  if (stale()) return;

  const body = h('div', { class: 'stack' });
  mount(page, orgHeader(o, ''), body);
  if (can.start(o.role)) body.append(startCard());
  body.append(sessionsList(sessions));

  function startCard() {
    let mode = settings.verification_mode === 'standard' ? 'standard' : 'strict';
    const title = h('input', { class: 'input', id: 't', value: defaultTitle(), maxlength: 200, autocomplete: 'off' });
    const venueSel = h('select', { class: 'input', id: 'v' },
      h('option', { value: '' }, t('Use my current location')),
      venues.items.map((v) => h('option', { value: v.id }, v.reference_count ? v.name : t('{place} (no photos yet)', { place: v.name }))));
    if (venues.items.length === 1) venueSel.value = venues.items[0].id;
    const note = h('div', { class: 'stack-sm' });
    const segBtns = {};
    const seg = h('div', { class: 'seg', role: 'group', 'aria-label': t('Check-in mode') },
      Object.entries(MODES).map(([k, m]) => (segBtns[k] = h('button', { type: 'button', onClick: () => { mode = k; paint(); } }, m.label, h('small', {}, m.help)))));
    const placeField = h('div', { class: 'field' }, h('label', { for: 'v' }, t('Place')), venueSel);
    const go = h('button', { class: 'btn big', type: 'submit' }, icon('scan'), t('Start & show QR'));
    const idem = uuid();

    function paint() {
      for (const [k, b] of Object.entries(segBtns)) b.setAttribute('aria-pressed', String(k === mode));
      placeField.hidden = mode !== 'strict';
      mount(note);
      if (mode === 'strict') {
        const v = venues.items.find((x) => x.id === venueSel.value);
        note.append(h('div', { class: 'hint' }, v
          ? (v.reference_count
              ? t('Attendees share their location and photograph the screen. Compared with {photos} of {place}, today’s photo and what it learned from earlier check-ins.', { photos: tn('{n} saved photo', '{n} saved photos', v.reference_count), place: v.name })
              : t('{place} has no saved photos yet. After starting, take today’s photo of the room from where attendees sit.', { place: v.name }))
          : t('Your location marks the place. After starting, take today’s photo of the room from where attendees sit.')));
      }
    }
    venueSel.addEventListener('change', paint);
    paint();

    return h('form', {
      class: 'card stack',
      onSubmit: async (e) => {
        e.preventDefault();
        go.disabled = true;
        try {
          const req = { title: title.value.trim() || defaultTitle(), mode };
          if (mode === 'strict') {
            if (venueSel.value) req.venue_id = venueSel.value;
            else {
              toast(t('Getting your location…'));
              const p = await getPosition();
              req.anchor = { lat: p.lat, lon: p.lon, accuracy_m: p.accuracy_m };
            }
          }
          const s = await post(`/v1/orgs/${id}/sessions`, req, { 'Idempotency-Key': idem });
          navigate(`#/o/${id}/s/${s.id}`);
        } catch (err) { fail(err); go.disabled = false; }
      },
    },
    h('h2', {}, t('Start attendance')),
    h('div', { class: 'field' }, h('label', { for: 't' }, t('What is it for?')), title),
    seg, placeField, note, go);
  }

  function sessionsList(first) {
    const list = h('div', { class: 'list' });
    const more = h('button', { class: 'btn secondary', type: 'button' }, t('Show more'));
    let cursor = first.next_cursor;
    const render = (items) => items.forEach((s) => list.append(
      h('a', { class: 'item', href: `#/o/${id}/s/${s.id}` },
        h('div', { class: 'grow' }, h('div', { class: 'title' }, s.title),
          h('div', { class: 'small muted' }, `${fmtWhen(s.starts_at)} · ${modeLabel(s.verification_mode)}`)),
        h('div', { class: 'row' },
          s.needs_review_count > 0 && h('span', { class: 'chip warn' }, tn('{n} to review', '{n} to review', s.needs_review_count)),
          h('span', { class: 'chip ok' }, tn('{n} in', '{n} in', s.verified_count)),
          s.status === 'active' && h('span', { class: 'chip brand' }, t('Live'))))));
    render(first.items);
    more.hidden = !cursor;
    more.addEventListener('click', async () => {
      more.disabled = true;
      try {
        const next = await get(`/v1/orgs/${id}/sessions?limit=25&cursor=${encodeURIComponent(cursor)}`);
        render(next.items); cursor = next.next_cursor; more.hidden = !cursor;
      } catch (err) { fail(err); }
      more.disabled = false;
    });
    return h('section', { class: 'stack-sm' }, h('h2', {}, t('Sessions')),
      first.items.length ? list : h('div', { class: 'empty' }, t('No sessions yet. Start one above.')), more);
  }
}
