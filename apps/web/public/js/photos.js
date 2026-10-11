// Reference photos of a room (a saved place's, or one session's "today's
// photo"): thumbnails, add from the camera or a file, delete.
import { h, mount, icon, toast, confirmDialog } from './ui.js';
import { get, post, del, imageURL } from './api.js';
import { fail, openCamera, captureJPEG, fileToJPEG } from './lib.js';
import { t } from './i18n.js';

/** Opens a camera dialog; resolves with a JPEG data URL, or null if closed. */
export function cameraShot({ title, hint }) {
  return new Promise((resolve) => {
    const video = h('video', { autoplay: true, playsinline: true, muted: true });
    const shoot = h('button', { class: 'btn big', type: 'button', disabled: true }, icon('camera'), t('Take photo'));
    const dlg = h('dialog', { 'aria-label': title, style: 'inline-size:min(560px,calc(100vw - 24px))' },
      h('div', { class: 'stack' }, h('h2', {}, title), h('div', { class: 'camera' }, video),
        hint && h('p', { class: 'hint center' }, hint), shoot,
        h('button', { class: 'btn secondary', type: 'button', onClick: () => dlg.close() }, t('Cancel'))));
    let stop = () => {}, closed = false, result = null;
    dlg.addEventListener('close', () => { closed = true; stop(); dlg.remove(); resolve(result); });
    document.body.append(dlg);
    dlg.showModal();
    openCamera(video, 'environment', { hd: true }).then((release) => {
      if (closed) return release();            // closed while the camera was starting
      stop = release; shoot.disabled = false;
    }).catch((err) => { if (!closed) dlg.close(); fail(err); });
    shoot.addEventListener('click', () => {
      try { result = captureJPEG(video).dataURL; } catch (err) { return fail(err); }
      dlg.close();
    });
  });
}

/** Lets people pick a photo file; resolves with a JPEG data URL or null. */
function pickFile() {
  return new Promise((resolve) => {
    const input = h('input', { type: 'file', accept: 'image/*', style: 'display:none' });
    input.addEventListener('change', async () => {
      const f = input.files?.[0];
      input.remove();
      if (!f) return resolve(null);
      try { resolve(await fileToJPEG(f)); } catch { toast(t('That file could not be read as a photo.'), 'bad'); resolve(null); }
    });
    document.body.append(input);
    input.click();
  });
}

/**
 * A self-refreshing panel of reference photos.
 *   base:    API path of the list (GET) / add (POST); items at `${base}/${id}`
 *   orgId:   for image URLs
 *   max:     how many are allowed
 *   canEdit: whether add/remove are offered
 *   onChange(count): called after every change
 */
export function photoPanel({ base, orgId, max, canEdit, cameraTitle, cameraHint, empty, onChange }) {
  const grid = h('div', { class: 'thumbs' });
  const status = h('p', { class: 'hint' });
  const add = h('button', { class: 'btn small', type: 'button' }, icon('camera'), t('Take photo'));
  const upload = h('button', { class: 'btn secondary small', type: 'button' }, t('Upload'));
  const urls = [];
  const panel = h('div', { class: 'stack-sm' }, grid, status, canEdit && h('div', { class: 'row wrap' }, add, upload));

  async function save(dataURL) {
    if (!dataURL) return;
    status.textContent = t('Checking the photo…');
    add.disabled = upload.disabled = true;
    try {
      await post(base, { photo: dataURL });
      status.textContent = t('✓ Saved.');
    } catch (err) { status.textContent = err.message; }
    add.disabled = upload.disabled = false;
    refresh();
  }
  add.addEventListener('click', async () => save(await cameraShot({ title: cameraTitle, hint: cameraHint })));
  upload.addEventListener('click', async () => save(await pickFile()));

  async function refresh() {
    let items = [];
    try { items = (await get(base)).items; } catch (err) { status.textContent = err.message; return; }
    urls.splice(0).forEach((u) => URL.revokeObjectURL(u));
    mount(grid, items.length === 0 ? h('p', { class: 'muted small' }, empty) : items.map((r) => {
      const img = h('img', { alt: 'Reference photo', loading: 'lazy' });
      imageURL(`/v1/orgs/${orgId}/references/${r.id}/image`).then((u) => { urls.push(u); img.src = u; }).catch(() => {});
      return h('figure', { class: 'thumb' }, img,
        canEdit && h('button', {
          class: 'thumb-del', type: 'button', 'aria-label': t('Remove this photo'),
          onClick: async () => {
            if (!(await confirmDialog({ title: t('Remove this photo?'), confirm: t('Remove'), danger: true }))) return;
            try { await del(`${base}/${r.id}`); refresh(); } catch (err) { fail(err); }
          },
        }, icon('x')));
    }));
    add.hidden = upload.hidden = items.length >= max;
    onChange?.(items.length);
  }
  refresh();
  panel.refresh = refresh;
  panel.dispose = () => urls.splice(0).forEach((u) => URL.revokeObjectURL(u));
  return panel;
}
