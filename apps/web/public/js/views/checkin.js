import { h, mount, icon, loading, fmtTime } from '../ui.js';
import { post } from '../api.js';
import { getPosition, openCamera, captureFrame, reasonText } from '../lib.js';

export default async function checkin({ page, params: [token], navigate, onCleanup, setTitle, stale }) {
  setTitle('Check in');
  let stopCam = () => {};
  onCleanup(() => stopCam());
  const show = (...kids) => { stopCam(); stopCam = () => {}; mount(page, h('div', { class: 'stack' }, kids)); };
  const working = (text) => show(h('div', { class: 'loading stack-sm center' }, h('span', { class: 'spinner' }), h('p', { class: 'muted' }, text)));

  working('Checking the code…');
  let scan;
  try { scan = await post('/v1/checkins/scan', { token }); } catch (err) { return problem(err); }
  if (stale()) return;
  setTitle(scan.session_title);
  if (scan.already_recorded) return result({ status: scan.status, reasons: [], already_recorded: true });

  const need = { gps: scan.needs_gps, photo: scan.needs_photo };
  if (!need.gps && !need.photo) return submit({});

  // ---- strict: location, then photo ---------------------------------------------
  const evidence = {};
  const steps = [need.gps && 'gps', need.photo && 'photo'].filter(Boolean);
  const stepsList = (current) => h('div', { class: 'steps' }, steps.map((s, i) => {
    const done = steps.indexOf(current) > i;
    return h('div', { class: 'step ' + (done ? 'done' : s === current ? 'now' : '') },
      h('span', { class: 'n' }, done ? icon('check') : i + 1),
      h('span', {}, s === 'gps' ? 'Share your location' : 'Take a photo of the room'));
  }));
  const header = h('div', { class: 'page-head' }, h('h1', {}, scan.session_title), h('p', { class: 'muted' }, `${scan.organization_name} · this check-in needs a couple of quick steps`));

  const next = (from) => {
    const nxt = steps[steps.indexOf(from) + 1];
    if (nxt === 'photo') return photoStep();
    return submit(evidence);
  };

  function locationStep(error) {
    const btn = h('button', { class: 'btn big', type: 'button' }, icon('pin'), 'Share my location');
    btn.addEventListener('click', async () => {
      btn.disabled = true; btn.textContent = 'Finding you…';
      try { evidence.gps = await getPosition(); next('gps'); } catch (err) { locationStep(err.message); }
    });
    show(header, stepsList('gps'), error && h('div', { class: 'banner' }, error), btn,
      error && h('button', { class: 'btn ghost', type: 'button', onClick: () => next('gps') }, 'Continue without location (your organizer will review)'),
      h('p', { class: 'hint center' }, 'Only used to confirm you are at the place. Your exact position is not saved.'));
  }

  async function photoStep(error) {
    const video = h('video', { autoplay: true, playsinline: true, muted: true });
    const shoot = h('button', { class: 'btn big', type: 'button' }, icon('camera'), 'Take photo');
    show(header, stepsList('photo'), error && h('div', { class: 'banner' }, error),
      h('div', { class: 'camera' }, video, h('div', { class: 'shutter-hint' }, 'Point at the room, then tap')), shoot,
      h('button', { class: 'btn ghost', type: 'button', onClick: () => next('photo') }, 'Skip (your organizer will review)'),
      h('p', { class: 'hint center' }, 'The photo is only compared with the room and is not kept.'));
    try { stopCam = await openCamera(video, 'environment'); } catch (err) { return photoFailed(err.message); }
    shoot.addEventListener('click', () => {
      try { evidence.photo = captureFrame(video); } catch (err) { return photoFailed(err.message); }
      stopCam(); stopCam = () => {};
      next('photo');
    });
  }
  function photoFailed(message) {
    show(header, stepsList('photo'), h('div', { class: 'banner bad' }, message),
      h('button', { class: 'btn big', type: 'button', onClick: () => photoStep() }, 'Try again'),
      h('button', { class: 'btn ghost', type: 'button', onClick: () => submit(evidence) }, 'Continue without a photo (your organizer will review)'));
  }

  async function submit(ev) {
    working('Checking you in…');
    try {
      const body = { ticket: scan.ticket };
      if (ev.gps) body.gps = ev.gps;
      if (ev.photo) body.photo = ev.photo;
      result(await post('/v1/checkins', body));
    } catch (err) {
      if (stale()) return;
      if (err.code === 'photo_unusable' || err.code === 'photo_invalid' || err.code === 'photo_too_large') {
        delete evidence.photo; return photoStep(err.message);
      }
      problem(err);
    }
  }

  function result(r) {
    if (stale()) return;
    const cfg = {
      verified: ['ok', 'check', r.already_recorded ? 'You are already checked in' : 'You’re checked in'],
      needs_review: ['warn', 'warn', 'Sent to your organizer to review'],
      rejected: ['bad', 'x', 'You seem to be outside the place'],
      revoked: ['bad', 'x', 'Your check-in was removed by the organizer'],
    }[r.status] || ['warn', 'warn', 'Done'];
    const why = r.status === 'needs_review' ? reasonText(r.reasons) : '';
    show(h('div', { class: 'result' },
      h('div', { class: 'big-icon ' + cfg[0] }, icon(cfg[1])),
      h('h1', {}, cfg[2]),
      h('p', { class: 'muted' }, [scan.session_title, ' · ', scan.organization_name]),
      why && h('div', { class: 'banner' }, why),
      r.status === 'needs_review' && h('p', { class: 'muted small' }, 'You do not need to do anything else. They can approve you from their screen.'),
      r.status === 'rejected' && h('p', { class: 'muted small' }, 'If you are in the room, move closer to the organizer and scan again.'),
      h('p', { class: 'small muted' }, fmtTime(new Date())),
      h('div', { class: 'stack-sm', style: 'inline-size:100%;max-inline-size:360px' },
        h('a', { class: 'btn big', href: '#/' }, 'Done'),
        r.status === 'rejected' && h('a', { class: 'btn secondary big', href: '#/scan' }, 'Scan again'))));
  }

  function problem(err) {
    if (stale()) return;
    const again = ['qr_expired', 'ticket_expired', 'invalid_qr'].includes(err.code);
    show(h('div', { class: 'result' },
      h('div', { class: 'big-icon warn' }, icon(err.code === 'session_ended' ? 'x' : 'warn')),
      h('h1', {}, err.code === 'session_ended' ? 'This session has ended' : again ? 'That code has changed' : 'Could not check in'),
      h('p', { class: 'muted' }, err.message),
      h('div', { class: 'stack-sm', style: 'inline-size:100%;max-inline-size:360px' },
        again && h('a', { class: 'btn big', href: '#/scan' }, 'Scan again'),
        h('a', { class: `btn ${again ? 'secondary' : ''} big`, href: '#/' }, 'Back to home'))));
  }

  if (steps[0] === 'gps') locationStep(); else photoStep();
}
