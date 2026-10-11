import { h, mount, icon, toast, confirmDialog, loading } from '../ui.js';
import { get, post, del } from '../api.js';
import { can, fail, getPosition } from '../lib.js';
import { photoPanel, cameraShot } from '../photos.js';
import { orgHeader } from './org.js';
import { t, tn } from '../i18n.js';

export default async function places({ page, params: [id], orgById, navigate, setTitle, stale }) {
  const o = orgById(id);
  if (!o) throw new Error(t('Organization not found.'));
  if (!can.manage(o.role)) return navigate(`#/o/${id}`, { replace: true });
  setTitle(`${o.name} · ${t('Places')}`);
  const body = h('div', { class: 'stack' }, loading());
  mount(page, orgHeader(o, '/places'), body);

  // ---- "test from a seat": does a photo taken here match the saved photos? -----
  async function testFromSeat(v) {
    const shot = await cameraShot({ title: t('Test {place}', { place: v.name }), hint: t('Sit where attendees sit and take a photo of the front of the room.') });
    if (!shot) return;
    try {
      const r = await post(`/v1/orgs/${id}/venues/${v.id}/test`, { photo: shot });
      const strong = r.matched && r.score >= 2 * r.threshold;
      const pct = Math.min(100, Math.round((100 * r.score) / (2 * r.threshold)));
      const color = r.matched ? 'var(--ok)' : 'var(--warn)';
      await confirmDialog({
        title: r.matched ? (strong ? t('Strong match ✓') : t('Match ✓')) : t('Not recognized'),
        body: r.matched
          ? (strong ? t('Photos from this seat will be recognized.') : t('Recognized, but only just. Add a photo taken from around this seat to make it reliable.'))
          : t('A photo from this seat does not match the saved photos. Add a photo taken from around here.'),
        confirm: t('OK'), cancel: null,
        extra: h('div', { class: 'meter', 'aria-hidden': 'true' }, h('i', { style: `inline-size:${pct}%;background:${color}` })),
      });
    } catch (err) { fail(err); }
  }

  // ---- how the photo check has done here (last 30 days) ------------------------------
  function statsLine(v) {
    const line = h('div', { class: 'small muted row wrap' });
    get(`/v1/orgs/${id}/venues/${v.id}/photo-stats`).then((st) => {
      if (stale()) return;
      const parts = [];
      if (st.checks) {
        parts.push(t('{pct}% of {n} attendee photos recognized in the last 30 days', {
          pct: Math.round((100 * st.recognized) / st.checks), n: st.checks,
        }));
      } else parts.push(t('No attendee photos checked here yet.'));
      if (st.learned) parts.push(tn('learned {n} extra view from check-ins', 'learned {n} extra views from check-ins', st.learned));
      mount(line, icon('sparkle'), h('span', {}, parts.join(' · ')),
        st.learned > 0 && h('button', {
          class: 'linkish', type: 'button',
          onClick: async () => {
            if (!(await confirmDialog({
              title: t('Forget what was learned?'),
              body: t('Use this after the room was rearranged. Recognition goes back to the saved photos and learns again from new check-ins.'),
              confirm: t('Forget'), cancel: t('Cancel'),
            }))) return;
            try { await del(`/v1/orgs/${id}/venues/${v.id}/learned`); toast(t('Done'), 'ok'); paint(); } catch (err) { fail(err); }
          },
        }, t('Forget')));
    }).catch(() => {});
    return line;
  }

  // ---- add-a-place form -----------------------------------------------------
  function addForm() {
    let pos = null;
    const name = h('input', { class: 'input', id: 'pn', required: true, maxlength: 200, placeholder: t('e.g. Room 204, Main hall') });
    const where = h('p', { class: 'hint' }, t('Stand inside the place, then tap the button.'));
    const locate = h('button', { class: 'btn secondary', type: 'button' }, icon('pin'), t('Use my current location'));
    const radius = h('select', { class: 'input', id: 'pr' }, [20, 30, 50, 100, 200].map((r) => h('option', { value: r, selected: r === 30 }, t('{n} meters', { n: r }))));
    const save = h('button', { class: 'btn', type: 'submit' }, t('Save place'));
    locate.addEventListener('click', async () => {
      locate.disabled = true; where.textContent = t('Finding you…');
      try { pos = await getPosition(); where.textContent = t('✓ Got it (accurate to about {m} m).', { m: pos.accuracy_m }); }
      catch (err) { where.textContent = err.message; }
      locate.disabled = false;
    });
    return h('form', {
      class: 'card stack',
      onSubmit: async (e) => {
        e.preventDefault();
        if (!pos) { where.textContent = t('Tap “Use my current location” first.'); return; }
        save.disabled = true;
        try {
          await post(`/v1/orgs/${id}/venues`, { name: name.value.trim(), lat: pos.lat, lon: pos.lon, radius_m: Number(radius.value) });
          toast(t('Place saved. Now add a photo of it.'), 'ok');
          paint();
        } catch (err) { fail(err); save.disabled = false; }
      },
    },
    h('h2', {}, t('Add a place')),
    h('div', { class: 'field' }, h('label', { for: 'pn' }, t('Name')), name),
    h('div', { class: 'field' }, h('span', { class: 'label' }, t('Where is it?')), locate, where),
    h('div', { class: 'field' }, h('label', { for: 'pr' }, t('How far from the spot still counts?')), radius), save);
  }

  async function paint() {
    const venues = await get(`/v1/orgs/${id}/venues`);
    if (stale()) return;
    const cards = venues.items.map((v) => {
      const chip = h('span', { class: 'chip' });
      // 3+ photos from different seats is what makes recognition reliable (docs/VISION.md)
      const setCount = (n) => {
        chip.className = 'chip ' + (n >= 3 ? 'ok' : 'warn');
        chip.textContent = !n ? t('No photos') : n >= 3 ? tn('{n} photo', '{n} photos', n) : t('{photos} · add {more} more', { photos: tn('{n} photo', '{n} photos', n), more: 3 - n });
        testBtn.hidden = !n;
      };
      const testBtn = h('button', { class: 'btn secondary small', type: 'button', hidden: true, onClick: () => testFromSeat(v) }, t('Test from a seat'));
      const panel = photoPanel({
        base: `/v1/orgs/${id}/venues/${v.id}/references`, orgId: id, max: 12, canEdit: true,
        cameraTitle: t('Photo of {place}', { place: v.name }),
        cameraHint: t('Stand where attendees sit and photograph the front: the screen, the board and the walls around them. Take 3–4 from different seats.'),
        empty: t('Strict mode recognizes the room from these. Add 3–4 photos of the front, from different seats.'),
        onChange: setCount,
      });
      return h('div', { class: 'card stack-sm' },
        h('div', { class: 'row between' }, h('div', {}, h('h3', {}, v.name), h('div', { class: 'small muted' }, t('Within {m} m', { m: v.radius_m }))), chip),
        panel,
        statsLine(v),
        h('div', { class: 'row wrap' }, testBtn,
          h('button', {
            class: 'btn ghost small', type: 'button',
            onClick: async () => {
              if (!(await confirmDialog({ title: t('Delete {place}?', { place: v.name }), body: t('Past sessions keep their records.'), confirm: t('Delete'), cancel: t('Cancel'), danger: true }))) return;
              try { await del(`/v1/orgs/${id}/venues/${v.id}`); paint(); } catch (err) { fail(err); }
            },
          }, t('Delete place'))));
    });
    mount(body,
      h('p', { class: 'muted' }, t('Places are only needed for Strict mode. Strict mode checks attendees against the room itself, using the photos you save here (and any photo you take when a session starts). It also learns each room from verified check-ins.')),
      cards.length ? h('div', { class: 'stack' }, cards) : h('div', { class: 'empty' }, t('No places yet.')),
      addForm());
  }
  await paint();
}
