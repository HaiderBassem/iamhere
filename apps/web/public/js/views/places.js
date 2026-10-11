import { h, mount, icon, toast, confirmDialog, loading, spinner } from '../ui.js';
import { get, post, del } from '../api.js';
import { can, fail, getPosition, openCamera, captureFrame } from '../lib.js';
import { orgHeader } from './org.js';

export default async function places({ page, params: [id], orgById, navigate, setTitle, onCleanup, stale }) {
  const o = orgById(id);
  if (!o) throw new Error('Organization not found.');
  if (!can.manage(o.role)) return navigate(`#/o/${id}`, { replace: true });
  setTitle(`${o.name} · Places`);
  const body = h('div', { class: 'stack' }, loading());
  mount(page, orgHeader(o, '/places'), body);

  // ---- camera dialog for reference photos ----------------------------------
  async function photoDialog(venue) {
    const video = h('video', { autoplay: true, playsinline: true, muted: true });
    const status = h('p', { class: 'hint center' }, `Stand in the room and take 2–3 photos from different spots. ${venue.reference_count} so far.`);
    const shoot = h('button', { class: 'btn big', type: 'button' }, icon('camera'), 'Take photo');
    const dlg = h('dialog', { 'aria-label': `Photos of ${venue.name}`, style: 'inline-size:min(520px,calc(100vw - 24px))' },
      h('div', { class: 'stack' }, h('h2', {}, `Photos of ${venue.name}`), h('div', { class: 'camera' }, video), status, shoot,
        h('button', { class: 'btn secondary', type: 'button', onClick: () => dlg.close() }, 'Done')));
    let stop = () => {};
    dlg.addEventListener('close', () => { stop(); dlg.remove(); paint(); });
    document.body.append(dlg);
    dlg.showModal();
    try { stop = await openCamera(video, 'environment'); } catch (err) { dlg.close(); return fail(err); }
    let count = venue.reference_count;
    shoot.addEventListener('click', async () => {
      shoot.disabled = true;
      try {
        await post(`/v1/orgs/${id}/venues/${venue.id}/references`, { photo: captureFrame(video) });
        count++;
        status.textContent = `✓ Saved. ${count} photo${count === 1 ? '' : 's'} for this place. Add another angle, or tap Done.`;
      } catch (err) { status.textContent = err.message; }
      shoot.disabled = false;
    });
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
    const cards = venues.items.map((v) => h('div', { class: 'card stack-sm' },
      h('div', { class: 'row between' }, h('div', {}, h('h3', {}, v.name), h('div', { class: 'small muted' }, `Within ${v.radius_m} m`)),
        v.reference_count > 0 ? h('span', { class: 'chip ok' }, `${v.reference_count} photo${v.reference_count === 1 ? '' : 's'}`) : h('span', { class: 'chip warn' }, 'No photos')),
      v.reference_count === 0 && h('p', { class: 'hint' }, 'Strict mode compares attendees’ live photos with these. Add one to turn it on for this place.'),
      h('div', { class: 'row wrap' },
        h('button', { class: 'btn small', type: 'button', onClick: () => photoDialog(v) }, icon('camera'), 'Add photo'),
        v.reference_count > 0 && h('button', {
          class: 'btn secondary small', type: 'button',
          onClick: async () => {
            const refs = (await get(`/v1/orgs/${id}/venues/${v.id}/references`)).items;
            const last = refs[refs.length - 1];
            if (!last) return;
            if (!(await confirmDialog({ title: 'Remove the latest photo?', confirm: 'Remove', danger: true }))) return;
            try { await del(`/v1/orgs/${id}/venues/${v.id}/references/${last.id}`); paint(); } catch (err) { fail(err); }
          },
        }, 'Remove last photo'),
        h('button', {
          class: 'btn ghost small', type: 'button',
          onClick: async () => {
            if (!(await confirmDialog({ title: `Delete ${v.name}?`, body: 'Past sessions keep their records.', confirm: 'Delete', danger: true }))) return;
            try { await del(`/v1/orgs/${id}/venues/${v.id}`); paint(); } catch (err) { fail(err); }
          },
        }, 'Delete'))));
    mount(body,
      h('p', { class: 'muted' }, 'Places are only needed for Strict mode. A saved place lets attendees be checked against the room itself.'),
      cards.length ? h('div', { class: 'stack' }, cards) : h('div', { class: 'empty' }, 'No places yet.'),
      addForm());
  }
  await paint();
}
