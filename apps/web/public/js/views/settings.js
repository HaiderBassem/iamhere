import { h, mount, toast, loading } from '../ui.js';
import { get, put } from '../api.js';
import { can, MODES, fail } from '../lib.js';
import { orgHeader } from './org.js';
import { t, tn } from '../i18n.js';

export default async function settings({ page, params: [id], orgById, navigate, setTitle }) {
  const o = orgById(id);
  if (!o) throw new Error(t('Organization not found.'));
  if (!can.manage(o.role)) return navigate(`#/o/${id}`, { replace: true });
  setTitle(`${o.name} · ${t('Settings')}`);
  mount(page, orgHeader(o, '/settings'), loading());
  const st = await get(`/v1/orgs/${id}/settings`);
  let mode = st.verification_mode === 'standard' ? 'standard' : 'strict';

  const btns = {};
  const seg = h('div', { class: 'seg', role: 'group', 'aria-label': t('Default mode') },
    Object.entries(MODES).map(([k, m]) => (btns[k] = h('button', { type: 'button', onClick: () => { mode = k; paint(); } }, m.label, h('small', {}, m.help)))));
  const paint = () => Object.entries(btns).forEach(([k, b]) => b.setAttribute('aria-pressed', String(k === mode)));
  paint();

  const mins = Math.round(st.default_duration_s / 60);
  const options = [...new Set([15, 30, 45, 60, 90, 120, 180, mins])].sort((a, b) => a - b);
  const dur = h('select', { class: 'input', id: 'd' }, options.map((m) => h('option', { value: m, selected: m === mins }, m >= 60 && m % 60 === 0 ? tn('{n} hour', '{n} hours', m / 60) : tn('{n} minute', '{n} minutes', m))));
  const save = h('button', { class: 'btn', type: 'submit' }, t('Save'));

  mount(page, orgHeader(o, '/settings'), h('form', {
    class: 'card stack',
    onSubmit: async (e) => {
      e.preventDefault(); save.disabled = true;
      try {
        await put(`/v1/orgs/${id}/settings`, { ...st, verification_mode: mode, default_duration_s: Number(dur.value) * 60 });
        toast(t('Saved. New sessions will use this.'), 'ok');
      } catch (err) { fail(err); }
      save.disabled = false;
    },
  },
  h('div', { class: 'field' }, h('span', { class: 'label' }, t('Default mode for new sessions')), seg,
    h('p', { class: 'hint' }, t('You can still choose a different mode each time you start attendance.'))),
  h('div', { class: 'field' }, h('label', { for: 'd' }, t('A session stays open for')), dur),
  h('p', { class: 'hint' }, t('The QR code changes every 5 seconds so a photo of it cannot be shared around.')),
  save));
}
