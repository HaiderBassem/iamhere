import { h, mount, loadScript } from '../ui.js';
import { openCamera } from '../lib.js';

// Pulls the check-in token out of a scanned QR: our link, or a bare token.
export function tokenFrom(text) {
  const m = /#\/c\/([A-Za-z0-9_-]{40,80})\s*$/.exec(text) || /^([A-Za-z0-9_-]{40,80})$/.exec(text.trim());
  return m ? m[1] : null;
}

export default async function scan({ page, navigate, onCleanup, setTitle }) {
  setTitle('Check in');
  const video = h('video', { autoplay: true, playsinline: true, muted: true });
  const msg = h('p', { class: 'muted center' }, 'Point your camera at the code on the screen.');
  mount(page, h('div', { class: 'stack' },
    h('div', { class: 'page-head' }, h('h1', {}, 'Scan to check in')),
    h('div', { class: 'camera scan' }, video, h('div', { class: 'frame' })),
    msg,
    h('p', { class: 'hint center' }, 'Tip: your phone’s normal camera app works too. Scan the code and tap the link.')));

  let stop = () => {}, alive = true;
  onCleanup(() => { alive = false; stop(); });
  try { await loadScript('/vendor/jsQR.js'); stop = await openCamera(video, 'environment'); } catch (err) {
    return mount(page, h('div', { class: 'stack' }, h('h1', {}, 'Scan to check in'), h('div', { class: 'banner bad' }, err.message),
      h('p', { class: 'muted' }, 'You can still check in: open your phone’s camera app, scan the code on the screen, and tap the link.')));
  }
  if (!alive) return stop();

  const canvas = document.createElement('canvas');
  const ctx = canvas.getContext('2d', { willReadFrequently: true });
  let last = 0, warned = 0;
  const loop = (now) => {
    if (!alive) return;
    requestAnimationFrame(loop);
    if (now - last < 100 || video.readyState < 2 || !video.videoWidth) return;   // ~10 scans per second
    last = now;
    const scale = Math.min(1, 640 / video.videoWidth);
    canvas.width = Math.round(video.videoWidth * scale); canvas.height = Math.round(video.videoHeight * scale);
    ctx.drawImage(video, 0, 0, canvas.width, canvas.height);
    const code = window.jsQR(ctx.getImageData(0, 0, canvas.width, canvas.height).data, canvas.width, canvas.height, { inversionAttempts: 'dontInvert' });
    if (!code) return;
    const token = tokenFrom(code.data);
    if (token) {
      alive = false; stop();
      navigator.vibrate?.(60);
      navigate(`#/c/${token}`);
    } else if (now - warned > 3000) {
      warned = now; msg.textContent = 'That is not an attendance code. Look for the one on the organizer’s screen.';
    }
  };
  requestAnimationFrame(loop);
}
