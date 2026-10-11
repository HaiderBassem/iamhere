import { h, mount, loadScript } from '../ui.js';
import { openCamera, watchForCode, setPendingCapture, tokenFrom } from '../lib.js';
import { t } from '../i18n.js';

export default async function scan({ page, navigate, onCleanup, setTitle }) {
  setTitle(t('Check in'));
  const video = h('video', { autoplay: true, playsinline: true, muted: true });
  const msg = h('p', { class: 'muted center' }, t('Point your camera at the code on the screen.'));
  mount(page, h('div', { class: 'stack' },
    h('div', { class: 'page-head' }, h('h1', {}, t('Scan to check in'))),
    h('div', { class: 'camera scan' }, video, h('div', { class: 'frame' })),
    msg,
    h('p', { class: 'hint center' }, t('Tip: your phone’s normal camera app works too. Scan the code and tap the link.'))));

  let stop = () => {}, watch = null, alive = true;
  onCleanup(() => { alive = false; watch?.stop(); stop(); });
  try {
    await loadScript('/vendor/jsQR.js');
    const release = await openCamera(video, 'environment', { hd: true });
    if (!alive) return release();
    stop = release;
  } catch (err) {
    return mount(page, h('div', { class: 'stack' }, h('h1', {}, t('Scan to check in')), h('div', { class: 'banner bad' }, err.message),
      h('p', { class: 'muted' }, t('You can still check in: open your phone’s camera app, scan the code on the screen, and tap the link.'))));
  }

  let warned = 0;
  watch = watchForCode(video, {
    accept: (text) => {
      if (tokenFrom(text)) return true;
      if (Date.now() - warned > 3000) { warned = Date.now(); msg.textContent = t('That is not an attendance code. Look for the one on the organizer’s screen.'); }
      return false;
    },
  });
  const found = await watch.promise;
  if (!found || !alive) return;
  const token = tokenFrom(found.text);
  // keep the frame: in strict mode it is the photo of the screen, no second step needed
  setPendingCapture({ token, photo: found.photo, hint: found.hint });
  stop();
  navigator.vibrate?.(60);
  navigate(`#/c/${token}`);
}
