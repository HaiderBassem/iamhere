import { h, mount, icon, loading, fmtDay } from '../ui.js';
import { get, download } from '../api.js';
import { can, fail } from '../lib.js';
import { orgHeader } from './org.js';
import { t, tn } from '../i18n.js';

const LOW = 0.75; // the usual attendance requirement; below it is highlighted

// Attendance per person over a period: "who missed too many?"
export default async function report({ page, params: [id], orgById, refreshOrgs, setTitle, stale }) {
  let o = orgById(id);
  if (!o) { await refreshOrgs(); o = orgById(id); }
  if (!o) throw new Error(t('Organization not found.'));
  setTitle(t('Reports') + ' · ' + o.name);

  const q = { from: '', to: '', title: '' };
  let sort = 'name', onlyLow = false, rep = null;
  const from = h('input', { class: 'input', type: 'date', id: 'r-from' });
  const to = h('input', { class: 'input', type: 'date', id: 'r-to' });
  const title = h('select', { class: 'input', id: 'r-title' });
  const body = h('div', { class: 'stack' }, loading());
  const chip = (label, on, fn) => h('button', { type: 'button', class: 'chip-btn', 'aria-pressed': String(on), onClick: fn }, label);
  const tools = h('div', { class: 'row wrap between' });

  mount(page, orgHeader(o, '/report'), h('div', { class: 'stack' },
    h('form', { class: 'card flat stack-sm', onSubmit: (e) => { e.preventDefault(); load(); } },
      h('div', { class: 'report-filters' },
        h('div', { class: 'field' }, h('label', { for: 'r-from' }, t('From')), from),
        h('div', { class: 'field' }, h('label', { for: 'r-to' }, t('To')), to),
        h('div', { class: 'field' }, h('label', { for: 'r-title' }, t('Title')), title))),
    tools, body));
  [from, to, title].forEach((el) => el.addEventListener('change', load));

  async function load() {
    Object.assign(q, { from: from.value, to: to.value, title: title.value });
    mount(body, loading());
    try {
      rep = await get(`/v1/orgs/${id}/report?` + new URLSearchParams(q));
      if (stale()) return;
      from.value = rep.from; to.value = rep.to;
      mount(title, h('option', { value: '' }, t('All sessions')), rep.titles.map((x) => h('option', { value: x, selected: x === rep.title }, x)));
      paint();
    } catch (err) {
      if (stale()) return;
      mount(body, h('div', { class: 'banner bad', role: 'alert' }, err.message));
    }
  }

  function paint() {
    const low = rep.people.filter((p) => p.rate < LOW).length;
    mount(tools,
      h('p', { class: 'muted' }, tn('{n} session', '{n} sessions', rep.sessions), ' · ', tn('{n} person', '{n} people', rep.people.length),
        low > 0 && rep.sessions > 0 ? [' · ', h('b', {}, tn('{n} below 75%', '{n} below 75%', low))] : ''),
      h('div', { class: 'row wrap', style: 'gap:8px' },
        chip(t('Below 75%'), onlyLow, () => { onlyLow = !onlyLow; paint(); }),
        chip(t('Lowest first'), sort === 'rate', () => { sort = sort === 'rate' ? 'name' : 'rate'; paint(); }),
        can.export(o.role) && h('button', { class: 'btn secondary small', type: 'button', onClick: csv }, icon('download'), t('Download'))));
    if (rep.people.length === 0) {
      return mount(body, h('div', { class: 'empty' }, icon('calendar'),
        h('p', {}, rep.sessions ? t('Nobody checked in during this period.') : t('No sessions in this period.'))));
    }
    let rows = rep.people.slice();
    if (onlyLow) rows = rows.filter((p) => p.rate < LOW);
    if (sort === 'rate') rows.sort((a, b) => a.rate - b.rate || a.full_name.localeCompare(b.full_name));
    const pct = (r) => Math.round(r * 100);
    mount(body,
      rows.length === 0 ? h('div', { class: 'empty' }, t('Everyone is at 75% or more.')) : h('div', { class: 'table-wrap' }, h('table', { class: 'table' },
        h('thead', {}, h('tr', {},
          h('th', { scope: 'col' }, t('Name')),
          h('th', { scope: 'col', class: 'num' }, t('Attended')), h('th', { scope: 'col', class: 'num' }, t('Rate')),
          h('th', { scope: 'col', class: 'num' }, t('To review')), h('th', { scope: 'col' }, t('Last check-in')))),
        h('tbody', {}, rows.map((p) => h('tr', {},
          h('td', { class: 'name' }, h('div', {}, p.full_name), p.email && h('div', { class: 'small muted', style: 'font-weight:400' }, p.email)),
          h('td', { class: 'num' }, `${p.attended} / ${rep.sessions}`),
          h('td', { class: 'num' }, h('span', { class: 'rate' + (p.rate < LOW ? ' low' : '') },
            h('i', { 'aria-hidden': 'true' }, h('i', { style: `inline-size:${pct(p.rate)}%` })), `${pct(p.rate)}%`)),
          h('td', { class: 'num' }, p.to_review || ''),
          h('td', {}, fmtDay(p.last_seen))))))),
      h('p', { class: 'hint' }, t('Everyone who checked in at least once in this period. Added-by-hand check-ins count; those waiting for review do not count yet.')),
      rep.truncated && h('div', { class: 'banner' }, t('Showing the first 5,000 people. Download the list or choose a shorter period.')));
  }

  async function csv() {
    try { await download(`/v1/orgs/${id}/report.csv?` + new URLSearchParams(q), `attendance-${rep.from}-${rep.to}.csv`); } catch (err) { fail(err); }
  }

  load();
}
