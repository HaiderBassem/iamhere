import { h, mount, icon, fmtWhen, loadScript } from '../ui.js';
import { post } from '../api.js';
import { getPosition, openCamera, captureJPEG, watchForCode, takePendingCapture, reasonText, tokenFrom, sameSession } from '../lib.js';
import { t } from '../i18n.js';
import { arrivedFromSignIn } from '../auth.js';
import { takeHeldResult } from '../held.js';

// #/c/<code> is a scanned code; #/c/held continues a check-in held through sign-up.
export default async function checkin({ page, params: [token], navigate, onCleanup, setTitle, stale, state }) {
  setTitle(t('Check in'));
  let stopCam = () => {};
  onCleanup(() => stopCam());
  const show = (...kids) => { stopCam(); stopCam = () => {}; mount(page, h('div', { class: 'stack' }, kids)); };
  const working = (text) => show(h('div', { class: 'loading stack-sm center' }, h('span', { class: 'spinner' }), h('p', { class: 'muted' }, text)));

  let scan;
  if (token === 'held') {
    const held = takeHeldResult();
    if (!held) return navigate('#/', { replace: true });  // reloaded: nothing held any more
    if (!held.checkin) return problem({ code: held.error?.error, message: held.error?.message || t('Something went wrong.') });
    scan = held.checkin;
  } else {
    working(t('Checking the code…'));
    try { scan = await post('/v1/checkins/scan', { token }); } catch (err) { return problem(err); }
    if (stale()) return;
  }
  // identifies the session in a photographed code (a ticket starts like the code it came from)
  const ref = token === 'held' ? scan.ticket : token;
  setTitle(scan.session_title);
  const need = { gps: scan.needs_gps, photo: scan.needs_photo };
  // Someone still waiting for review may try again (a good attempt verifies
  // them); anyone else who is already recorded just sees where they stand.
  const retry = scan.already_recorded && scan.status === 'needs_review' && (need.gps || need.photo);
  if (scan.already_recorded && !retry) return result({ status: scan.status, reasons: [] }, true);
  if (!need.gps && !need.photo) return submit({});

  // ---- strict: location, then a photo of the screen ------------------------------
  const evidence = {};
  const scanned = takePendingCapture(token);      // the in-app scanner already took it
  if (scanned && need.photo) { evidence.photo = scanned.photo; evidence.hint = scanned.hint; need.photo = false; }
  const steps = [need.gps && 'gps', need.photo && 'photo'].filter(Boolean);
  const stepsList = (current) => h('div', { class: 'steps' }, steps.map((s, i) => {
    const done = steps.indexOf(current) > i;
    return h('div', { class: 'step ' + (done ? 'done' : s === current ? 'now' : '') },
      h('span', { class: 'n' }, done ? icon('check') : i + 1),
      h('span', {}, s === 'gps' ? t('Share your location') : t('Photograph the screen with the code')));
  }));
  const header = h('div', { class: 'stack-sm' },
    h('div', { class: 'page-head' }, h('h1', {}, scan.session_title), h('p', { class: 'muted' }, t('{org} · this check-in needs a couple of quick steps', { org: scan.organization_name }))),
    retry && h('div', { class: 'banner' }, t('You are waiting for your organizer’s review. You can try again now to check in straight away.')));

  const next = (from) => {
    const nxt = steps[steps.indexOf(from) + 1];
    if (nxt === 'photo') return photoStep();
    return submit(evidence);
  };
  // everything above is declared before this, so a retake (below) works in every path
  if (!need.gps && !need.photo) return submit(evidence);

  function locationStep(error) {
    const btn = h('button', { class: 'btn big', type: 'button' }, icon('pin'), t('Share my location'));
    btn.addEventListener('click', async () => {
      btn.disabled = true; btn.textContent = t('Finding you…');
      try { evidence.gps = await getPosition(); next('gps'); } catch (err) { locationStep(err.message); }
    });
    show(header, stepsList('gps'), error && h('div', { class: 'banner' }, error), btn,
      error && h('button', { class: 'btn ghost', type: 'button', onClick: () => next('gps') }, t('Continue without location (your organizer will review)')),
      h('p', { class: 'hint center' }, t('Only used to confirm you are at the place. Your exact position is not saved.')));
  }

  async function photoStep(error) {
    const video = h('video', { autoplay: true, playsinline: true, muted: true });
    const status = h('div', { class: 'shutter-hint' }, t('Point at the screen with the code'));
    const manual = h('button', { class: 'btn secondary', type: 'button', hidden: true }, icon('camera'), t('Take photo anyway'));
    show(header, stepsList('photo'), error && h('div', { class: 'banner' }, error),
      h('div', { class: 'camera' }, video, status),
      h('p', { class: 'hint center' }, t('Include the screen and the wall around it. The photo is taken by itself when the code is in view, compared with the room, and not kept.')),
      manual,
      h('button', { class: 'btn ghost', type: 'button', onClick: () => next('photo') }, t('Skip (your organizer will review)')));
    let watch = null;
    try {
      await loadScript('/vendor/jsQR.js');
      const release = await openCamera(video, 'environment', { hd: true });
      // left the page, or moved on (Skip) while the camera was starting
      if (stale() || !video.isConnected) return release();
      stopCam = () => { watch?.stop(); release(); };
    } catch (err) { return photoFailed(err.message); }
    // after a while, let people take the photo themselves (e.g. the code is too far away)
    const reveal = setTimeout(() => { manual.hidden = false; status.textContent = t('Move closer if the code is too small'); }, 8000);
    manual.addEventListener('click', () => {
      try { evidence.photo = captureJPEG(video).dataURL; } catch (err) { return photoFailed(err.message); }
      clearTimeout(reveal); stopCam(); stopCam = () => {}; next('photo');
    });
    // only this session's code proves anything (another session's screen may be in view)
    watch = watchForCode(video, { accept: (text) => sameSession(tokenFrom(text), ref) });
    const found = await watch.promise;
    clearTimeout(reveal);
    if (stale() || !video.isConnected || !found) return;
    navigator.vibrate?.(40);
    evidence.photo = found.photo; evidence.hint = found.hint;
    stopCam(); stopCam = () => {};
    next('photo');
  }
  function photoFailed(message) {
    show(header, stepsList('photo'), h('div', { class: 'banner bad' }, message),
      h('button', { class: 'btn big', type: 'button', onClick: () => photoStep() }, t('Try again')),
      h('button', { class: 'btn ghost', type: 'button', onClick: () => submit(evidence) }, t('Continue without a photo (your organizer will review)')));
  }

  async function submit(ev) {
    working(t('Checking you in…'));
    try {
      const body = { ticket: scan.ticket };
      if (ev.gps) body.gps = ev.gps;
      if (ev.photo) body.photo = ev.photo;
      if (ev.photo && ev.hint) body.photo_hint = ev.hint;
      result(await post('/v1/checkins', body), false);
    } catch (err) {
      if (stale()) return;
      if (err.code === 'photo_unusable' || err.code === 'photo_invalid' || err.code === 'photo_too_large') {
        delete evidence.photo; delete evidence.hint; return photoStep(err.message);
      }
      problem(err);
    }
  }

  // fromScan: the person was already recorded before this visit.
  // Checked in (or waiting for review) is shown as a pass with a stamp: the
  // one moment the product celebrates. Anything else says what to do next.
  function result(r, fromScan) {
    if (stale()) return;
    const title = {
      verified: fromScan ? t('You are already checked in') : t('You’re checked in'),
      needs_review: fromScan ? t('Still waiting for your organizer’s review') : t('Sent to your organizer to review'),
      rejected: fromScan ? t('Your organizer did not accept this check-in') : t('You seem to be outside the place'),
      revoked: t('Your check-in was removed by the organizer'),
    }[r.status] || t('Done');
    const why = r.status === 'needs_review' ? reasonText(r.reasons) : '';
    const done = h('div', { class: 'stack-sm', style: 'inline-size:100%;max-inline-size:380px' },
      retakeable(r, fromScan) && h('button', { class: 'btn big', type: 'button', onClick: retake }, icon('camera'), t('Take the photo again')),
      h('a', { class: `btn big ${retakeable(r, fromScan) ? 'secondary' : ''}`, href: '#/' }, t('Done')),
      r.status === 'rejected' && !fromScan && h('a', { class: 'btn secondary big', href: '#/scan' }, t('Scan again')));

    if (r.status === 'verified' || r.status === 'needs_review') {
      const ok = r.status === 'verified';
      return show(h('div', { class: 'result', role: 'status' },
        h('h1', {}, title),
        h('div', { class: 'pass' },
          h('div', { class: 'pass-top' },
            h('span', { class: 'eyebrow' }, scan.organization_name),
            h('h2', {}, scan.session_title),
            h('span', { class: 'small muted' }, state.me?.full_name || '')),
          h('div', { class: 'pass-cut', 'aria-hidden': 'true' }),
          h('div', { class: 'pass-bottom' },
            h('div', { class: 'stack-xs' },
              h('span', {}, ok ? t('Checked in') : t('Waiting for review')),
              h('b', {}, fromScan ? t('Already on the list') : fmtWhen(new Date()))),
            h('div', { class: 'stamp ' + (ok ? 'ok' : 'warn'), 'aria-hidden': 'true' }, icon(ok ? 'check' : 'warn'), ok ? t('Here') : t('Review')))),
        why && h('div', { class: 'banner', style: 'max-inline-size:380px' }, why),
        r.status === 'needs_review' && h('p', { class: 'muted small' }, t('You do not need to do anything else. They can approve you from their screen.')),
        done));
    }
    show(h('div', { class: 'result', role: 'status' },
      h('div', { class: 'big-icon bad' }, icon('x')),
      h('h1', {}, title),
      h('p', { class: 'muted' }, [scan.session_title, ' · ', scan.organization_name]),
      r.status === 'rejected' && !fromScan && h('p', { class: 'muted small' }, t('If you are in the room, move closer to the organizer and scan again.')),
      fromScan && h('p', { class: 'muted small' }, t('If this is a mistake, talk to your organizer.')),
      done));
  }

  // A photo that did not match the room or missed the code can be retaken at
  // once (the ticket from the scan is still good for a couple of minutes).
  function retakeable(r, fromScan) {
    return r.status === 'needs_review' && scan.needs_photo && !fromScan
      && (r.reasons || []).some((c) => c === 'visual_check_required' || c === 'photo_without_current_code');
  }
  function retake() {
    delete evidence.photo; delete evidence.hint;
    if (!steps.includes('photo')) steps.push('photo');
    photoStep();
  }

  function problem(err) {
    if (stale()) return;
    const again = ['qr_expired', 'ticket_expired', 'claim_expired', 'invalid_qr'].includes(err.code);
    const fresh = err.code === 'qr_expired' && arrivedFromSignIn('#/c/' + token);
    show(h('div', { class: 'result' },
      h('div', { class: 'big-icon warn' }, icon(err.code === 'session_ended' ? 'x' : 'warn')),
      h('h1', {}, err.code === 'session_ended' ? t('This session has ended') : err.code === 'invalid_qr' ? t('That code did not work')
        : again ? (fresh ? t('You’re signed in. Now scan once more') : t('That code has changed')) : t('Could not check in')),
      h('p', { class: 'muted' }, again && fresh ? t('The code on the screen changes every few seconds, so the one you opened while signing up has expired. Scan the screen again and you’re in.') : err.message),
      h('div', { class: 'stack-sm', style: 'inline-size:100%;max-inline-size:360px' },
        again && h('a', { class: 'btn big', href: '#/scan' }, t('Scan again')),
        h('a', { class: `btn ${again ? 'secondary' : ''} big`, href: '#/' }, t('Back to home')))));
  }

  if (steps[0] === 'gps') locationStep(); else photoStep();
}
