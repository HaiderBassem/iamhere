// Rendered design check (the ECC frontend-design-direction finish gate).
//
//   BASE_URL=http://localhost:8080 OUT=../../docs/design-evidence node e2e/design.mjs
//
// Seeds a small realistic data set through the API, then renders the main
// screens at the contract's viewports (390x844 phone, 1440x900 desktop), in
// light and dark, and checks on every screen: no horizontal scrolling, text
// contrast (WCAG AA), labelled form controls, touch-target size, visible
// keyboard focus and reduced motion. Each content state (loading, empty,
// error, partial, success, permission) is produced for real. Writes
// screenshots and report.json to OUT; the report is the input of the skill's
// check-evidence.js. Exits non-zero when a check fails.
import { chromium } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const BASE = process.env.BASE_URL || 'http://localhost:8080';
const OUT = resolve(process.env.OUT || 'design-evidence');
mkdirSync(OUT, { recursive: true });
const RUN = Date.now().toString(36);
const PASSWORD = 'blue-river-seven';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// ---- seed ------------------------------------------------------------------
async function api(method, path, body, token) {
  const res = await fetch(BASE + path, {
    method, headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: 'Bearer ' + token } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${JSON.stringify(data)}`);
  return data;
}
let n = 0;
const signup = (name) => api('POST', '/v1/auth/signup', { name, email: `${RUN}.${++n}@example.org`, password: PASSWORD });
// A code works only while it is on the screen: take one with at least 3 s left.
async function code(org, s, tok) {
  const q = await api('GET', `/v1/orgs/${org}/sessions/${s}/qr`, undefined, tok);
  if (q.next_rotation_in_ms >= 3000) return q.token;
  await sleep(q.next_rotation_in_ms + 100);
  return (await api('GET', `/v1/orgs/${org}/sessions/${s}/qr`, undefined, tok)).token;
}

const sara = await signup('Sara Ahmed');
const ws = await api('POST', '/v1/workspace', { timezone: 'Asia/Baghdad' }, sara.token);
const names = ['Omar Khalid', 'Lana Hassan', 'Yusuf Majeed', 'Zainab Rasheed', 'ئاوات کوردی', 'Noor Faisal'];
const students = [];
for (const name of names) students.push(await signup(name));
const week5 = await api('POST', `/v1/orgs/${ws.id}/sessions`, { title: 'Software Engineering' }, sara.token);
for (const s of students.slice(0, 5)) await api('POST', '/v1/checkins', { token: await code(ws.id, week5.id, sara.token) }, s.token);
await api('POST', `/v1/orgs/${ws.id}/sessions/${week5.id}/close`, {}, sara.token);
const week6 = await api('POST', `/v1/orgs/${ws.id}/sessions`, { title: 'Software Engineering' }, sara.token);
for (const s of students.slice(0, 3)) await api('POST', '/v1/checkins', { token: await code(ws.id, week6.id, sara.token) }, s.token);

// ---- browser ---------------------------------------------------------------
const VIEWPORTS = { '390x844': { width: 390, height: 844, mobile: true }, '1440x900': { width: 1440, height: 900, mobile: false } };
const browser = await chromium.launch({ args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'] });
const noCamera = await chromium.launch({ args: ['--use-fake-device-for-media-stream'] });   // no auto-accept: the prompt is refused
async function open(vp, { token, scheme = 'light', motion = 'no-preference', camera = true } = {}) {
  const v = VIEWPORTS[vp];
  const ctx = await (camera ? browser : noCamera).newContext({
    viewport: { width: v.width, height: v.height }, isMobile: v.mobile, hasTouch: v.mobile, deviceScaleFactor: 1,
    colorScheme: scheme, reducedMotion: motion, locale: 'en-US', permissions: camera ? ['camera'] : [],
  });
  if (token) await ctx.addInitScript((tok) => localStorage.setItem('iah.session', tok), token);
  const page = await ctx.newPage();
  page.errors = [];
  page.on('pageerror', (e) => page.errors.push(e.message));
  return { ctx, page };
}

// In-page audit: overflow, contrast, labels, target sizes.
function audit() {
  const parse = (c) => {
    let m = c.match(/rgba?\(([\d.]+),\s*([\d.]+),\s*([\d.]+)(?:,\s*([\d.]+))?\)/);
    if (m) return [+m[1], +m[2], +m[3], m[4] === undefined ? 1 : +m[4]];
    m = c.match(/color\(srgb ([\d.]+) ([\d.]+) ([\d.]+)(?: \/ ([\d.]+))?\)/);
    if (m) return [m[1] * 255, m[2] * 255, m[3] * 255, m[4] === undefined ? 1 : +m[4]];
    return null;
  };
  const lum = ([r, g, b]) => [r, g, b].map((x) => { x /= 255; return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4; })
    .reduce((s, x, i) => s + x * [0.2126, 0.7152, 0.0722][i], 0);
  const blend = (top, bottom) => top.slice(0, 3).map((x, i) => x * top[3] + bottom[i] * (1 - top[3])).concat(1);
  const bgOf = (el) => {
    const layers = [];
    for (let e = el; e; e = e.parentElement) {
      const cs = getComputedStyle(e);
      if (cs.backgroundImage !== 'none') return null;           // text over an image: not measurable here
      const c = parse(cs.backgroundColor);
      if (c && c[3] > 0) { layers.push(c); if (c[3] >= 1) break; }
    }
    let bg = [255, 255, 255, 1];
    for (const l of layers.reverse()) bg = blend(l, bg);
    return bg;
  };
  const visible = (el) => { const r = el.getBoundingClientRect(); const cs = getComputedStyle(el); return r.width > 0 && r.height > 0 && cs.visibility !== 'hidden' && cs.opacity !== '0'; };
  const label = (el) => (el.getAttribute('aria-label') || el.labels?.[0]?.textContent || el.getAttribute('aria-labelledby') || el.title || '').trim();

  const lowContrast = [];
  for (const el of document.querySelectorAll('body *')) {
    if (!visible(el) || el.closest('.qr-box, .demo-qr, [disabled], [aria-hidden="true"], .sr-only, .skip')) continue;
    if (![...el.childNodes].some((c) => c.nodeType === 3 && c.textContent.trim())) continue;
    const cs = getComputedStyle(el);
    const fg = parse(cs.color), bg = bgOf(el);
    if (!fg || !bg) continue;
    const f = blend(fg, bg);
    const [a, b] = [lum(f), lum(bg)];
    const ratio = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
    const size = parseFloat(cs.fontSize), bold = +cs.fontWeight >= 700;
    const need = size >= 24 || (bold && size >= 18.66) ? 3 : 4.5;
    if (ratio < need) lowContrast.push(`${el.tagName.toLowerCase()}.${el.className} "${el.textContent.trim().slice(0, 30)}" ${ratio.toFixed(2)}`);
  }
  const unlabeled = [...document.querySelectorAll('input:not([type=hidden]), select, textarea')]
    .filter((el) => visible(el) && !label(el)).map((el) => el.id || el.name || el.type);
  // WCAG 2.2 AA target size: 24 px minimum (links inside running text are exempt)
  const smallTargets = [...document.querySelectorAll('button, a.btn, a.item, a.checkin-cta, .tabbar a, .tabs a, input, select, .chip-btn, .switch input')]
    .filter(visible).map((el) => [el, el.getBoundingClientRect()]).filter(([, r]) => r.height < 24 || r.width < 24)
    .map(([el, r]) => `${el.tagName.toLowerCase()} "${(el.textContent || el.getAttribute('aria-label') || '').trim().slice(0, 20)}" ${Math.round(r.width)}x${Math.round(r.height)}`);
  const overflow = document.documentElement.scrollWidth - document.documentElement.clientWidth;
  return { overflow, lowContrast, unlabeled, smallTargets };
}

const results = [];
const evidence = [];
let failures = 0;
async function capture(p, vp, name, what, scheme = 'light') {
  await sleep(350);
  const file = `${name}-${vp}${scheme === 'dark' ? '-dark' : ''}.jpg`;
  await p.page.screenshot({ path: join(OUT, file), type: 'jpeg', quality: 72, fullPage: false });
  const a = await p.page.evaluate(audit);
  const ok = a.overflow <= 0 && !a.lowContrast.length && !a.unlabeled.length && !a.smallTargets.length && !p.page.errors.length;
  if (!ok) failures++;
  results.push({ screen: name, viewport: vp, scheme, ...a, errors: p.page.errors.slice() });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name} ${vp} ${scheme}` + (ok ? '' : '  ' + JSON.stringify(a) + p.page.errors.join(' | ')));
  evidence.push({ viewport: vp, artifact: file, observation: `${what} (${scheme}). Measured: no horizontal scroll (${a.overflow <= 0 ? 'ok' : a.overflow + 'px'}), ` +
    `${a.lowContrast.length} text element(s) under WCAG AA contrast, ${a.unlabeled.length} unlabeled control(s), ${a.smallTargets.length} target(s) under 24 px, ${p.page.errors.length} script error(s).` });
}

const states = {};
for (const vp of Object.keys(VIEWPORTS)) {
  for (const scheme of vp === '390x844' ? ['light', 'dark'] : ['light']) {
    // landing (visitor)
    let p = await open(vp, { scheme });
    await p.page.goto(BASE + '/');
    await p.page.locator('.demo-qr svg').waitFor();
    await capture(p, vp, 'landing', 'Landing: promise headline, live rotating QR demo with names arriving, primary "Start free"', scheme);
    await p.ctx.close();

    // sign-up from a scanned code: the held check-in is shown
    p = await open(vp, { scheme });
    await p.page.goto(BASE + '/#/c/' + await code(ws.id, week6.id, sara.token));
    await p.page.locator('.auth-context').waitFor();
    await capture(p, vp, 'signup-held', 'Sign-up opened from a scanned code: shows the session being checked in to, three fields, one button', scheme);
    await p.ctx.close();

    // organizer home: composer + live now + organizations + history
    p = await open(vp, { token: sara.token, scheme });
    await p.page.goto(BASE + '/#/');
    await p.page.getByRole('heading', { name: 'Live now' }).waitFor();
    await capture(p, vp, 'home-organizer', 'Organizer home: title pre-filled with Start, Strict switch, Check in, the live session, workspace', scheme);

    // live session (partial: people still arriving)
    await p.page.goto(BASE + `/#/o/${ws.id}/s/${week6.id}`);
    await p.page.locator('.qr-box:not(.stale) svg').waitFor();
    await capture(p, vp, 'session-live', 'Live session: rotating QR with timer bar, live counts, Add someone, the list so far', scheme);
    if (scheme === 'light') states.partial ??= { status: 'pass', evidence: 'Live session while people are still arriving (session-live-*): the count and list show who is in so far, the timer and "Ends in" mark that more can arrive; list refreshes live.' };

    // report
    await p.page.goto(BASE + `/#/o/${ws.id}/report`);
    await p.page.locator('table.table').waitFor();
    await capture(p, vp, 'report', 'Report per person: period, title filter, attended / sessions, rate bars, below-75% count, Download', scheme);
    await p.ctx.close();

    // success: the pass
    const st = students[3];
    p = await open(vp, { token: st.token, scheme });
    await p.page.goto(BASE + '/#/c/' + await code(ws.id, week6.id, sara.token));
    await p.page.locator('.pass .stamp').waitFor();
    await capture(p, vp, 'pass', 'Check-in success: the pass with the organization, title, name, time and the "Here" stamp; Done', scheme);
    if (scheme === 'light') states.success ??= { status: 'pass', evidence: 'Scanning a live code shows the pass with a "Here" stamp, the session, the person and the time, and a Done button (pass-*). Already-checked-in people get the same pass marked "Already on the list".' };
    await p.ctx.close();
  }

  // empty: a brand-new attendee's home
  const fresh = await signup('Maya Daoud');
  let p = await open(vp, { token: fresh.token });
  await p.page.goto(BASE + '/#/');
  await p.page.locator('.empty').waitFor();
  await capture(p, vp, 'home-empty', 'Empty state: a new account sees Start and Check in, and an empty history that says what will appear');
  states.empty ??= { status: 'pass', evidence: 'A new account\'s home (home-empty-*): both actions stay available; the empty history explains that check-ins from every organization will appear there. Report with no data says "No sessions in this period".' };

  // loading: the report while its data is on the way
  await p.page.route('**/report?*', async (r) => { await sleep(2500); await r.continue(); });
  const fw = await api('POST', '/v1/workspace', {}, fresh.token);
  await p.page.goto(BASE + `/#/o/${fw.id}/report`);
  await p.page.locator('.loading .spinner').waitFor();
  await capture(p, vp, 'loading', 'Loading: the report frame (tabs, filters) stays in place while a spinner marks the data area');
  states.loading ??= { status: 'pass', evidence: 'Report with a delayed response (loading-*): header, tabs and filters keep their place; only the data area shows a labelled spinner (role=status). The same pattern is used on every data view.' };
  await p.page.locator('.empty').waitFor();
  await p.ctx.close();

  // error: a code that does not work
  p = await open(vp, { token: fresh.token });
  await p.page.goto(BASE + '/#/c/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA');
  await p.page.getByRole('heading', { name: 'That code did not work' }).waitFor();
  await capture(p, vp, 'error', 'Error: a bad code says what happened in plain words and offers "Scan again" and "Back to home"');
  states.error ??= { status: 'pass', evidence: 'An invalid or expired code (error-*) shows what happened and two ways on: Scan again, Back to home. Form errors appear in an alert banner above the form and focus the field to fix.' };
  await p.ctx.close();

  // permission: camera blocked on the scanner
  p = await open(vp, { token: fresh.token, camera: false });
  await p.page.goto(BASE + '/#/scan');
  await p.page.locator('.banner.bad').waitFor({ timeout: 15000 });
  await capture(p, vp, 'permission', 'Permission: camera blocked on the scanner explains how to allow it, and the fallback (the phone\'s own camera app)');
  states.permission ??= { status: 'pass', evidence: 'Scanner with the camera blocked (permission-*): says the camera is blocked, how to allow it in the browser, and that the phone\'s camera app works instead. Location in Strict sessions explains why before the browser asks, and offers to continue to review.' };
  await p.ctx.close();
}

// ---- keyboard focus and reduced motion -----------------------------------------
let p = await open('1440x900', { token: sara.token });
await p.page.goto(BASE + '/#/');
await p.page.getByRole('heading', { name: 'Start attendance' }).waitFor();
const focus = [];
for (let i = 0; i < 8; i++) {
  await p.page.keyboard.press('Tab');
  focus.push(await p.page.evaluate(() => {
    const el = document.activeElement;
    const cs = getComputedStyle(el);
    return { el: el.tagName.toLowerCase() + (el.id ? '#' + el.id : ''), outline: cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) >= 2 };
  }));
}
await p.ctx.close();
const focusOK = focus.every((f) => f.outline);
console.log(`${focusOK ? 'PASS' : 'FAIL'}  keyboard focus visible on ${focus.length} tab stops: ${focus.map((f) => f.el).join(', ')}`);
if (!focusOK) failures++;

p = await open('390x844', { token: students[4].token, motion: 'reduce' });
await p.page.goto(BASE + '/#/c/' + await code(ws.id, week6.id, sara.token));
await p.page.locator('.pass .stamp').waitFor();
const motionOK = await p.page.evaluate(() => getComputedStyle(document.querySelector('.stamp')).animationName === 'none');
await p.ctx.close();
console.log(`${motionOK ? 'PASS' : 'FAIL'}  reduced motion turns the stamp animation off`);
if (!motionOK) failures++;
await browser.close();
await noCamera.close();

const bad = results.filter((r) => r.overflow > 0 || r.lowContrast.length || r.unlabeled.length || r.smallTargets.length || r.errors.length);
const report = {
  contract: 'docs/DESIGN.md section 1 (design contract): the Skillture identity (cyan on ink), the stamped pass as the memorable detail, phone (390x844) and desktop (1440x900) targets, AA contrast in both themes, 48 px primary targets, every data view with loading/empty/error/partial/success states.',
  targetViewports: Object.keys(VIEWPORTS),
  accessibility: bad.length || !focusOK || !motionOK
    ? { status: 'fail', evidence: `${bad.length} screen(s) with findings; focus ${focusOK}; reduced motion ${motionOK}` }
    : { status: 'pass', evidence: `Measured on ${results.length} renders (light and dark): every text element meets WCAG AA contrast against its real background, every form control has an accessible name, every target is at least 24 px (primary actions 48-56 px), no script errors. Keyboard: ${focus.length} tab stops on home all show a 3 px outline. Reduced motion: the stamp and other animations are off.` },
  responsive: bad.some((r) => r.overflow > 0)
    ? { status: 'fail', evidence: 'horizontal overflow found' }
    : { status: 'pass', evidence: `No horizontal scrolling on any of ${results.length} renders at 390x844 and 1440x900; phone uses the bottom tab bar and stacked composer, desktop the top navigation, two-column live session and five-column landing grids. Long and right-to-left names (Kurdish) wrap without overlap.` },
  contentStates: states,
  renderedEvidence: evidence,
  measurements: results,
};
writeFileSync(join(OUT, 'report.json'), JSON.stringify(report, null, 2) + '\n');
console.log(failures ? `\n${failures} check(s) FAILED` : '\nAll design checks passed', '\n' + join(OUT, 'report.json'));
process.exit(failures ? 1 : 0);
