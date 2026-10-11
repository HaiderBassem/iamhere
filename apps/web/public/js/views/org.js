import { h, mount, icon, toast, fmtWhen, fmtDay, uuid } from '../ui.js';
import { get, post } from '../api.js';
import { can, MODES, ROLE_LABEL, modeLabel, fail, getPosition } from '../lib.js';

export function orgHeader(org, active) {
  const tabs = [['', 'Attendance'], ...(can.manage(org.role) ? [['/team', 'Team'], ['/places', 'Places'], ['/settings', 'Settings']] : [])];
  return h('div', {},
    h('a', { class: 'back', href: '#/' }, 'All organizations'),
    h('div', { class: 'page-head' }, h('h1', {}, org.name), h('p', { class: 'muted small' }, `You are ${/^[aeiou]/i.test(ROLE_LABEL[org.role]) ? 'an' : 'a'} ${ROLE_LABEL[org.role].toLowerCase()}`)),
    tabs.length > 1 && h('nav', { class: 'tabs', 'aria-label': org.name },
      tabs.map(([p, label]) => h('a', { href: `#/o/${org.id}${p}`, 'aria-current': active === p ? 'page' : false }, label))));
}

const defaultTitle = () => `Class – ${fmtDay(new Date())}`;

export default async function org({ page, params: [id], orgById, state, navigate, setTitle, refreshOrgs, stale }) {
  let o = orgById(id);
  if (!o) { await refreshOrgs(); o = orgById(id); }
  if (!o) throw new Error('Organization not found.');
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
      h('option', { value: '' }, 'Use my current location'),
      venues.items.map((v) => h('option', { value: v.id }, `${v.name}${v.reference_count ? '' : ' (no photos yet)'}`)));
    if (venues.items.length === 1) venueSel.value = venues.items[0].id;
    const note = h('div', { class: 'stack-sm' });
    const segBtns = {};
    const seg = h('div', { class: 'seg', role: 'group', 'aria-label': 'Check-in mode' },
      Object.entries(MODES).map(([k, m]) => (segBtns[k] = h('button', { type: 'button', onClick: () => { mode = k; paint(); } }, m.label, h('small', {}, m.help)))));
    const placeField = h('div', { class: 'field' }, h('label', { for: 'v' }, 'Place'), venueSel);
    const go = h('button', { class: 'btn big', type: 'submit' }, icon('scan'), 'Start & show QR');
    const idem = uuid();

    function paint() {
      for (const [k, b] of Object.entries(segBtns)) b.setAttribute('aria-pressed', String(k === mode));
      placeField.hidden = mode !== 'strict';
      mount(note);
      if (mode === 'strict') {
        const v = venues.items.find((x) => x.id === venueSel.value);
        note.append(h('div', { class: 'hint' }, v
          ? (v.reference_count
              ? `Attendees share their location and photograph the screen. Compared with ${v.reference_count} saved photo${v.reference_count === 1 ? '' : 's'} of ${v.name} and today’s photo.`
              : `${v.name} has no saved photos yet. After starting, take today’s photo of the room from where attendees sit.`)
          : 'Your location marks the place. After starting, take today’s photo of the room from where attendees sit.'));
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
              toast('Getting your location…');
              const p = await getPosition();
              req.anchor = { lat: p.lat, lon: p.lon, accuracy_m: p.accuracy_m };
            }
          }
          const s = await post(`/v1/orgs/${id}/sessions`, req, { 'Idempotency-Key': idem });
          navigate(`#/o/${id}/s/${s.id}`);
        } catch (err) { fail(err); go.disabled = false; }
      },
    },
    h('h2', {}, 'Start attendance'),
    h('div', { class: 'field' }, h('label', { for: 't' }, 'What is it for?'), title),
    seg, placeField, note, go);
  }

  function sessionsList(first) {
    const list = h('div', { class: 'list' });
    const more = h('button', { class: 'btn secondary', type: 'button' }, 'Show more');
    let cursor = first.next_cursor;
    const render = (items) => items.forEach((s) => list.append(
      h('a', { class: 'item', href: `#/o/${id}/s/${s.id}` },
        h('div', { class: 'grow' }, h('div', { class: 'title' }, s.title),
          h('div', { class: 'small muted' }, `${fmtWhen(s.starts_at)} · ${modeLabel(s.verification_mode)}`)),
        h('div', { class: 'row' },
          s.needs_review_count > 0 && h('span', { class: 'chip warn' }, `${s.needs_review_count} to review`),
          h('span', { class: 'chip ok' }, `${s.verified_count} in`),
          s.status === 'active' && h('span', { class: 'chip brand' }, 'Live')))));
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
    return h('section', { class: 'stack-sm' }, h('h2', {}, 'Sessions'),
      first.items.length ? list : h('div', { class: 'empty' }, 'No sessions yet. Start one above.'), more);
  }
}
