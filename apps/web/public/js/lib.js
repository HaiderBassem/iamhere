// Shared helpers: roles, human wording for server reason codes, camera, location.
import { toast } from './ui.js';
import { t } from './i18n.js';

// ---- roles (mirror internal/app rolePerms) ---------------------------------
export const can = {
  manage: (r) => r === 'owner' || r === 'admin',
  start: (r) => ['owner', 'admin', 'organizer'].includes(r),
  review: (r) => ['owner', 'admin', 'reviewer'].includes(r),
  export: (r) => ['owner', 'admin', 'reporter'].includes(r),
};
export const ROLE_LABEL = { owner: t('Owner'), admin: t('Admin'), organizer: t('Organizer'), reviewer: t('Reviewer'), reporter: t('Reporter') };
export const ROLE_HELP = {
  admin: t('Manages the team, places and settings.'),
  organizer: t('Starts attendance and sees who checked in.'),
  reviewer: t('Approves or rejects flagged check-ins.'),
  reporter: t('Views and exports attendance.'),
};

// ---- modes -----------------------------------------------------------------
export const MODES = {
  standard: { label: t('Normal'), help: t('Scan the QR code. That is all.') },
  strict: { label: t('Strict'), help: t('QR code + location + a live photo of the room.') },
};
export const modeLabel = (m) => (m === 'standard' ? t('Normal') : t('Strict'));

// ---- statuses & reasons ----------------------------------------------------
export const STATUS = {
  verified: { label: t('Checked in'), cls: 'ok' },
  needs_review: { label: t('Needs review'), cls: 'warn' },
  rejected: { label: t('Rejected'), cls: 'bad' },
  revoked: { label: t('Revoked'), cls: 'bad' },
  pending: { label: t('Pending'), cls: '' },
};
const REASON = {
  gps_unavailable: t('No location was shared'),
  gps_stale: t('Location reading was too old'),
  gps_accuracy_too_low: t('Location was too imprecise'),
  within_uncertainty_of_radius: t('Near the edge of the place'),
  outside_radius: t('Outside the place'),
  no_session_anchor: t('This session has no known location'),
  visual_check_required: t('Photo missing or did not match the room'),
  visual_not_configured: t('No reference photos for this place yet'),
  photo_without_current_code: t('The photo did not show the current code'),
  visual_check_unavailable: t('The photo check was unavailable at that moment'),
  qr_only: '',
  gps_within_radius: '',
};
export const reasonText = (codes = []) => codes.map((c) => REASON[c]).filter(Boolean).join(' · ');

// ---- errors ----------------------------------------------------------------
export function fail(err) {
  console.error(err);
  toast(err?.message || t('Something went wrong.'), 'bad');
}

// ---- location --------------------------------------------------------------
/** Resolves {lat, lon, accuracy_m, fix_age_ms} or rejects with a friendly Error. */
export function getPosition({ timeout = 15000 } = {}) {
  return new Promise((resolve, reject) => {
    if (!navigator.geolocation) return reject(new Error(t('This device cannot share its location.')));
    navigator.geolocation.getCurrentPosition(
      (p) => resolve({
        lat: p.coords.latitude, lon: p.coords.longitude, accuracy_m: Math.round(p.coords.accuracy),
        fix_age_ms: Math.max(0, Date.now() - p.timestamp),
      }),
      (e) => reject(new Error(e.code === 1
        ? t('Location is blocked. Allow location for this site in your browser settings, then try again.')
        : t('Could not get your location. Move near a window and try again.'))),
      { enableHighAccuracy: true, timeout, maximumAge: 0 },
    );
  });
}

// ---- camera ----------------------------------------------------------------
/**
 * Opens the camera into a <video>. facing: 'environment' (back) or 'user'.
 * hd asks for the highest practical resolution: a far-away projected code needs
 * enough pixels to be read again on the server.
 */
export async function openCamera(video, facing = 'environment', { hd = false } = {}) {
  if (!navigator.mediaDevices?.getUserMedia) throw new Error(t('This browser cannot use the camera.'));
  let stream;
  try {
    const size = hd ? { width: { ideal: 2560 }, height: { ideal: 1440 } } : { width: { ideal: 1280 } };
    stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: facing }, ...size }, audio: false });
  } catch (e) {
    throw new Error(e.name === 'NotAllowedError'
      ? t('Camera is blocked. Allow the camera for this site in your browser settings, then try again.')
      : t('No camera found on this device.'));
  }
  video.srcObject = stream;
  video.setAttribute('playsinline', '');
  video.muted = true;
  await video.play().catch(() => {});
  return () => stream.getTracks().forEach((t) => t.stop());
}

/** Draws an image source scaled so its long side is at most maxSide; returns the canvas. */
function scaled(src, w, h, maxSide) {
  const s = Math.min(1, maxSide / Math.max(w, h));
  const c = document.createElement('canvas');
  c.width = Math.round(w * s); c.height = Math.round(h * s);
  c.getContext('2d').drawImage(src, 0, 0, c.width, c.height);
  return { canvas: c, scale: s };
}

/** The current camera frame as a JPEG data URL (whole frame, long side <= maxSide). */
export function captureJPEG(video, maxSide = 1600, quality = 0.85) {
  if (!video.videoWidth) throw new Error(t('The camera is not ready yet.'));
  const { canvas, scale } = scaled(video, video.videoWidth, video.videoHeight, maxSide);
  return { dataURL: canvas.toDataURL('image/jpeg', quality), scale };
}

/** A chosen photo file, re-encoded as a JPEG data URL with long side <= maxSide. */
export async function fileToJPEG(file, maxSide = 1600, quality = 0.85) {
  const bmp = await createImageBitmap(file);
  const { canvas } = scaled(bmp, bmp.width, bmp.height, maxSide);
  bmp.close?.();
  return canvas.toDataURL('image/jpeg', quality);
}

/**
 * Watches a live <video> for the check-in QR code and resolves with a photo of
 * the frame it was found in, plus the code's corners in that photo:
 * { photo, hint: [[x,y]x4], text }. The photo proves the camera saw the live
 * screen; the corners help the server read the code again. Call stop() to give up.
 */
export function watchForCode(video, { maxSide = 1600, accept = () => true } = {}) {
  let alive = true, timer, settle;
  const work = document.createElement('canvas');
  const ctx = work.getContext('2d', { willReadFrequently: true });
  const promise = new Promise((resolve) => {
    settle = resolve;
    const tick = () => {
      if (!alive) return;
      if (video.readyState >= 2 && video.videoWidth && window.jsQR) {
        const s = Math.min(1, 960 / video.videoWidth);
        work.width = Math.round(video.videoWidth * s); work.height = Math.round(video.videoHeight * s);
        ctx.drawImage(video, 0, 0, work.width, work.height);
        const code = window.jsQR(ctx.getImageData(0, 0, work.width, work.height).data, work.width, work.height, { inversionAttempts: 'dontInvert' });
        if (code && accept(code.data)) {
          const shot = captureJPEG(video, maxSide);
          const k = shot.scale / s;                 // detection canvas -> photo pixels
          const L = code.location;
          const hint = [L.topLeftCorner, L.topRightCorner, L.bottomRightCorner, L.bottomLeftCorner].map((p) => [p.x * k, p.y * k]);
          alive = false;
          return resolve({ photo: shot.dataURL, hint, text: code.data });
        }
      }
      timer = setTimeout(tick, 120);
    };
    tick();
  });
  // stopping resolves with null, so whoever awaits the promise is not left hanging
  return { promise, stop: () => { alive = false; clearTimeout(timer); settle(null); } };
}

/** Pulls the check-in token out of a scanned QR: our link, or a bare token. */
export function tokenFrom(text) {
  const m = /#\/c\/([A-Za-z0-9_-]{40,80})\s*$/.exec(text) || /^([A-Za-z0-9_-]{40,80})$/.exec(text.trim());
  return m ? m[1] : null;
}

/** Whether two check-in tokens belong to the same session (they start with its id). */
export const sameSession = (a, b) => !!a && !!b && a.slice(0, 21) === b.slice(0, 21);

// A photo taken by the in-app scanner at the moment it read the code, handed to
// the check-in page so strict mode does not need a second photo.
let pendingCapture = null;
export function setPendingCapture(c) { pendingCapture = c ? { ...c, at: Date.now() } : null; }
export function takePendingCapture(token) {
  const c = pendingCapture;
  pendingCapture = null;
  return c && c.token === token && Date.now() - c.at < 20000 ? c : null;
}
