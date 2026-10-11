// Shared helpers: roles, human wording for server reason codes, camera, location.
import { toast } from './ui.js';

// ---- roles (mirror internal/app rolePerms) ---------------------------------
export const can = {
  manage: (r) => r === 'owner' || r === 'admin',
  start: (r) => ['owner', 'admin', 'organizer'].includes(r),
  review: (r) => ['owner', 'admin', 'reviewer'].includes(r),
  export: (r) => ['owner', 'admin', 'reporter'].includes(r),
};
export const ROLE_LABEL = { owner: 'Owner', admin: 'Admin', organizer: 'Organizer', reviewer: 'Reviewer', reporter: 'Reporter' };
export const ROLE_HELP = {
  admin: 'Manages the team, places and settings.',
  organizer: 'Starts attendance and sees who checked in.',
  reviewer: 'Approves or rejects flagged check-ins.',
  reporter: 'Views and exports attendance.',
};

// ---- modes -----------------------------------------------------------------
export const MODES = {
  standard: { label: 'Normal', help: 'Scan the QR code. That is all.' },
  strict: { label: 'Strict', help: 'QR code + location + a live photo of the room.' },
};
export const modeLabel = (m) => (m === 'standard' ? 'Normal' : 'Strict');

// ---- statuses & reasons ----------------------------------------------------
export const STATUS = {
  verified: { label: 'Checked in', cls: 'ok' },
  needs_review: { label: 'Needs review', cls: 'warn' },
  rejected: { label: 'Rejected', cls: 'bad' },
  revoked: { label: 'Revoked', cls: 'bad' },
  pending: { label: 'Pending', cls: '' },
};
const REASON = {
  gps_unavailable: 'No location was shared',
  gps_stale: 'Location reading was too old',
  gps_accuracy_too_low: 'Location was too imprecise',
  within_uncertainty_of_radius: 'Near the edge of the place',
  outside_radius: 'Outside the place',
  no_session_anchor: 'This session has no known location',
  visual_check_required: 'Photo missing or did not match the room',
  visual_not_configured: 'No reference photos for this place yet',
  qr_only: '',
  gps_within_radius: '',
};
export const reasonText = (codes = []) => codes.map((c) => REASON[c]).filter(Boolean).join(' · ');

// ---- errors ----------------------------------------------------------------
export function fail(err) {
  console.error(err);
  toast(err?.message || 'Something went wrong.', 'bad');
}

// ---- location --------------------------------------------------------------
/** Resolves {lat, lon, accuracy_m, fix_age_ms} or rejects with a friendly Error. */
export function getPosition({ timeout = 15000 } = {}) {
  return new Promise((resolve, reject) => {
    if (!navigator.geolocation) return reject(new Error('This device cannot share its location.'));
    navigator.geolocation.getCurrentPosition(
      (p) => resolve({
        lat: p.coords.latitude, lon: p.coords.longitude, accuracy_m: Math.round(p.coords.accuracy),
        fix_age_ms: Math.max(0, Date.now() - p.timestamp),
      }),
      (e) => reject(new Error(e.code === 1
        ? 'Location is blocked. Allow location for this site in your browser settings, then try again.'
        : 'Could not get your location. Move near a window and try again.')),
      { enableHighAccuracy: true, timeout, maximumAge: 0 },
    );
  });
}

// ---- camera ----------------------------------------------------------------
/** Opens the camera into a <video>. facing: 'environment' (back) or 'user'. */
export async function openCamera(video, facing = 'environment') {
  if (!navigator.mediaDevices?.getUserMedia) throw new Error('This browser cannot use the camera.');
  let stream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: facing }, width: { ideal: 1280 } }, audio: false });
  } catch (e) {
    throw new Error(e.name === 'NotAllowedError'
      ? 'Camera is blocked. Allow the camera for this site in your browser settings, then try again.'
      : 'No camera found on this device.');
  }
  video.srcObject = stream;
  video.setAttribute('playsinline', '');
  video.muted = true;
  await video.play().catch(() => {});
  return () => stream.getTracks().forEach((t) => t.stop());
}

/**
 * Captures the current frame as a small JPEG data URL, center-cropped to 4:3 so
 * every photo (reference and live) has the same framing. Photos are only ever
 * taken live from the camera, never picked from a gallery.
 */
export function captureFrame(video, w = 320, h = 240) {
  const c = document.createElement('canvas');
  c.width = w; c.height = h;
  const vw = video.videoWidth, vh = video.videoHeight;
  if (!vw || !vh) throw new Error('The camera is not ready yet.');
  const target = w / h;
  let sw = vw, sh = vh;
  if (vw / vh > target) sw = vh * target; else sh = vw / target;
  c.getContext('2d').drawImage(video, (vw - sw) / 2, (vh - sh) / 2, sw, sh, 0, 0, w, h);
  return c.toDataURL('image/jpeg', 0.72);
}
