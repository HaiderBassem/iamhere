// A scan held through sign-up. Someone without an account scans the code; the
// code changes every few seconds, but creating an account takes about twenty.
// So the fresh scan is exchanged for a claim (valid 5 minutes) before the
// sign-up page opens, and the sign-up sends it along: the person is checked in
// without scanning again. Kept in sessionStorage: this tab only, never a URL.
import { publicPost } from './api.js';

const KEY = 'iah.held';

const read = () => { try { return JSON.parse(sessionStorage.getItem(KEY) || 'null'); } catch { return null; } };
const write = (v) => { try { v ? sessionStorage.setItem(KEY, JSON.stringify(v)) : sessionStorage.removeItem(KEY); } catch { /* private mode */ } };

/** Exchanges a scanned code for a claim. Best effort: on failure nothing is held. */
export async function holdScan(token) {
  try {
    const c = await publicPost('/v1/checkins/claim', { token });
    write({ claim: c.claim, title: c.session_title, org: c.organization_name, mode: c.mode, until: Date.now() + c.expires_in_s * 1000 });
  } catch { write(null); }
}

/** The held scan, if it is still good. */
export function heldScan() {
  const h = read();
  if (h && h.claim && h.until > Date.now()) return h;
  return null;
}

/** After sign-up: what the server made of the claim ({checkin} or {checkin_error}). */
export function setHeldResult(res) { write({ result: res }); }
export function takeHeldResult() {
  const h = read();
  write(null);
  return h?.result || null;
}
export const dropHeld = () => write(null);
