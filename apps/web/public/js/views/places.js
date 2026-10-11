import { h, mount, icon, toast, confirmDialog, loading } from '../ui.js';
import { get, post, del } from '../api.js';
import { can, fail, getPosition } from '../lib.js';
import { photoPanel, cameraShot } from '../photos.js';
import { orgHeader } from './org.js';

export default async function places({ page, params: [id], orgById, navigate, setTitle, onCleanup, stale }) {
  const o = orgById(id);
  if (!o) throw new Error('Organization not found.');
  if (!can.manage(o.role)) return navigate(`#/o/${id}`, { replace: true });
  setTitle(`${o.name} · Places`);
  const body = h('div', { class: 'stack' }, loading());
  mount(page, orgHeader(o, '/places'), body);

  // ---- "test from a seat": does a photo taken here match the saved photos? -----
  async function testFromSeat(v) {
    const shot = await cameraShot({ title: `Test ${v.name}`, hint: 'Sit where attendees sit and take a photo of the front of the room.' });
    if (!shot) return;
    try {
      const r = await post(`/v1/orgs/${id}/venues/${v.id}/test`, { photo: shot });
      const strong = r.matched && r.score >= 2 * r.threshold;
      const pct = Math.min(100, Math.round((100 * r.score) / (2 * r.threshold)));
      const color = r.matched ? 'var(--ok)' : 'var(--warn)';
      await confirmDialog({
        title: r.matched ? (strong ? 'Strong match ✓' : 'Match ✓') : 'Not recognized',
        body: r.matched
          ? (strong ? 'Photos from this seat will be recognized.' : 'Recognized, but only just. Add a photo taken from around this seat to make it reliable.')
          : 'A photo from this seat does not match the saved photos. Add a photo taken from around here.',
        confirm: 'OK', cancel: null,
        extra: h('div', { class: 'meter', 'aria-hidden': 'true' }, h('i', { style: `inline-size:${pct}%;background:${color}` })),
      });
    } catch (err) { fail(err); }
  }

  // ---- add-a-place form -----------------------------------------------------
  function addForm() {
    let pos = null;
    const name = h('input', { class: 'input', id: 'pn', required: true, maxlength: 200, placeholder: 'e.g. Room 204, Main hall' });
    const where = h('p', { class: 'hint' }, 'Stand inside the place, then tap the button.');
    const locate = h('button', { class: 'btn secondary', type: 'button' }, icon('pin'), 'Use my current location');
    const radius = h('select', { class: 'input', id: 'pr' }, [20, 30, 50, 100, 200].map((r) => h('option', { value: r, selected: r === 30 }, `${r} meters`)));
    const save = h('button', { class: 'btn', type: 'submit' }, 'Save place');
    locate.addEventListener('click', async () => {
      locate.disabled = true; where.textContent = 'Finding you…';
      try { pos = await getPosition(); where.textContent = `✓ Got it (accurate to about ${pos.accuracy_m} m).`; }
      catch (err) { where.textContent = err.message; }
      locate.disabled = false;
    });
    return h('form', {
      class: 'card stack',
      onSubmit: async (e) => {
        e.preventDefault();
        if (!pos) { where.textContent = 'Tap “Use my current location” first.'; return; }
        save.disabled = true;
        try {
          await post(`/v1/orgs/${id}/venues`, { name: name.value.trim(), lat: pos.lat, lon: pos.lon, radius_m: Number(radius.value) });
          toast('Place saved. Now add a photo of it.', 'ok');
          paint();
        } catch (err) { fail(err); save.disabled = false; }
      },
    },
    h('h2', {}, 'Add a place'),
    h('div', { class: 'field' }, h('label', { for: 'pn' }, 'Name'), name),
    h('div', { class: 'field' }, h('span', { class: 'label' }, 'Where is it?'), locate, where),
    h('div', { class: 'field' }, h('label', { for: 'pr' }, 'How far from the spot still counts?'), radius), save);
  }

  async function paint() {
    const venues = await get(`/v1/orgs/${id}/venues`);
    if (stale()) return;
    const cards = venues.items.map((v) => {
      const chip = h('span', { class: 'chip' });
      // 3+ photos from different seats is what makes recognition reliable (docs/VISION.md)
      const setCount = (n) => {
        chip.className = 'chip ' + (n >= 3 ? 'ok' : 'warn');
        chip.textContent = !n ? 'No photos' : n >= 3 ? `${n} photos` : `${n} photo${n === 1 ? '' : 's'} · add ${3 - n} more`;
        testBtn.hidden = !n;
      };
      const testBtn = h('button', { class: 'btn secondary small', type: 'button', hidden: true, onClick: () => testFromSeat(v) }, 'Test from a seat');
      const panel = photoPanel({
        base: `/v1/orgs/${id}/venues/${v.id}/references`, orgId: id, max: 12, canEdit: true,
        cameraTitle: `Photo of ${v.name}`,
        cameraHint: 'Stand where attendees sit and photograph the front: the screen, the board and the walls around them. Take 3–4 from different seats.',
        empty: 'Strict mode recognizes the room from these. Add 3–4 photos of the front, from different seats.',
        onChange: setCount,
      });
      return h('div', { class: 'card stack-sm' },
        h('div', { class: 'row between' }, h('div', {}, h('h3', {}, v.name), h('div', { class: 'small muted' }, `Within ${v.radius_m} m`)), chip),
        panel,
        h('div', { class: 'row wrap' }, testBtn,
          h('button', {
            class: 'btn ghost small', type: 'button',
            onClick: async () => {
              if (!(await confirmDialog({ title: `Delete ${v.name}?`, body: 'Past sessions keep their records.', confirm: 'Delete', danger: true }))) return;
              try { await del(`/v1/orgs/${id}/venues/${v.id}`); paint(); } catch (err) { fail(err); }
            },
          }, 'Delete place')));
    });
    mount(body,
      h('p', { class: 'muted' }, 'Places are only needed for Strict mode. Strict mode checks attendees against the room itself, using the photos you save here (and any photo you take when a session starts).'),
      cards.length ? h('div', { class: 'stack' }, cards) : h('div', { class: 'empty' }, 'No places yet.'),
      addForm());
  }
  await paint();
}
