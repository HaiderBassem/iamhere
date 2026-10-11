import { h, mount, icon, fmtWhen, uuid } from '../ui.js';
import { get, post } from '../api.js';
import { STATUS, ROLE_LABEL, can, fail, getPosition, defaultTitle } from '../lib.js';
import { t, tn } from '../i18n.js';

const LAST_ORG = 'iah.lastOrg';
const remembered = () => { try { return localStorage.getItem(LAST_ORG); } catch { return null; } };
const remember = (id) => { try { localStorage.setItem(LAST_ORG, id); } catch { /* private mode */ } };

// Home is two actions: start attendance (one tap; the title is already filled
// in) and check in. Everything else (organizations, history) sits below.
export default async function home({ page, state, refreshOrgs, navigate, setTitle, stale }) {
  setTitle(t('Home'));
  const [orgs, hist] = await Promise.all([refreshOrgs(), get('/v1/me/attendance?limit=20')]);
  const startable = orgs.filter((o) => can.start(o.role));
  // sessions still running, so an organizer who looked away finds their code again
  const live = (await Promise.all(startable.slice(0, 4).map((o) =>
    get(`/v1/orgs/${o.id}/sessions?limit=5`).then((r) => r.items.filter((s) => s.status === 'active').map((s) => ({ ...s, org: o })))
      .catch(() => [])))).flat();
  if (stale()) return;

  const first = state.me.full_name.split(/\s+/)[0];
  const today = new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long' }).format(new Date());
  const orgCount = new Set(hist.items.map((r) => r.organization_name)).size;

  mount(page, h('div', { class: 'stack-lg' },
    h('div', { class: 'stack-xs' }, h('span', { class: 'eyebrow' }, today), h('h1', {}, t('Hi {name}', { name: first }))),
    h('div', { class: 'stack' },
      composer(),
      h('a', { class: 'checkin-cta', href: '#/scan' },
        h('span', { class: 'feature-icon' }, icon('scan')),
        h('span', { class: 'stack-xs grow' }, h('b', {}, t('Check in')), h('span', { class: 'small muted' }, t('Scan the code on the organizer’s screen.'))),
        icon('arrow'))),
    live.length > 0 && h('section', { class: 'stack-sm' },
      h('h2', {}, t('Live now')),
      h('div', { class: 'list' }, live.map((s) => h('a', { class: 'item', href: `#/o/${s.org.id}/s/${s.id}` },
        h('span', { class: 'dot ok' }),
        h('div', { class: 'grow' }, h('div', { class: 'title' }, s.title), h('div', { class: 'small muted' }, `${s.org.name} · ${fmtWhen(s.starts_at)}`)),
        h('span', { class: 'chip ok' }, tn('{n} in', '{n} in', s.verified_count)))))),
    orgs.length > 0 && h('section', { class: 'stack-sm' },
      h('div', { class: 'row between' }, h('h2', {}, t('Your organizations')),
        h('a', { class: 'btn ghost small', href: '#/new-org' }, icon('plus'), t('New'))),
      h('div', { class: 'org-grid' }, orgs.map((o) => h('a', { class: 'org-card org-link', href: `#/o/${o.id}` },
        h('span', { class: 'org-mark', 'aria-hidden': 'true' }, [...o.name.trim()][0]?.toUpperCase() || '?'),
        h('span', { class: 'stack-xs' }, h('b', {}, o.name), h('span', { class: 'small muted' }, t(ROLE_LABEL[o.role]))))))),
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

  // Start attendance: a title that is already filled in, and one button. The
  // first Start of someone without an organization makes their own workspace.
  function composer() {
    const title = h('input', { class: 'input', id: 'start-title', value: defaultTitle(), maxlength: 200, autocomplete: 'off', 'aria-label': t('What is it for?') });
    const strict = h('input', { type: 'checkbox', id: 'start-strict' });
    const pick = startable.find((o) => o.id === remembered()) || startable.find((o) => o.role === 'owner') || startable[0];
    const where = startable.length > 1 && h('select', { class: 'input', id: 'start-org' },
      startable.map((o) => h('option', { value: o.id, selected: o.id === pick.id }, o.name)));
    const go = h('button', { class: 'btn', type: 'submit' }, icon('qr'), t('Start'));
    const idem = uuid();

    return h('form', {
      class: 'card composer',
      onSubmit: async (e) => {
        e.preventDefault();
        go.disabled = true;
        try {
          let orgId = where ? where.value : pick?.id;
          if (!orgId) orgId = (await post('/v1/workspace', { timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC' })).id;
          remember(orgId);
          const req = { title: title.value.trim() || defaultTitle(), mode: strict.checked ? 'strict' : 'standard' };
          if (strict.checked) {
            // a saved place if there is exactly one; otherwise here, where the organizer stands
            const venues = (await get(`/v1/orgs/${orgId}/venues`)).items;
            if (venues.length === 1) req.venue_id = venues[0].id;
            else {
              const p = await getPosition();
              req.anchor = { lat: p.lat, lon: p.lon, accuracy_m: p.accuracy_m };
            }
          }
          const s = await post(`/v1/orgs/${orgId}/sessions`, req, { 'Idempotency-Key': idem });
          await refreshOrgs();
          navigate(`#/o/${orgId}/s/${s.id}`);
        } catch (err) { fail(err); go.disabled = false; }
      },
    },
    h('h2', {}, t('Start attendance')),
    h('div', { class: 'composer-row' }, title, go),
    h('div', { class: 'composer-meta' },
      h('label', { class: 'switch', for: 'start-strict' }, strict,
        h('span', {}, h('b', {}, t('Strict')), ' ', h('span', { class: 'small muted' }, t('location + a photo of the screen')))),
      where && h('label', { class: 'composer-org small muted' }, t('In'), where)));
  }
}
