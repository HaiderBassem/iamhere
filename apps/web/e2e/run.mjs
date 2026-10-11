// End-to-end tests in real Chromium against a running API + web app (dev auth).
//
//   BASE_URL=http://localhost:8080 node e2e/run.mjs
//
// Chromium's fake camera plays generated rooms (apps/vision/tests/camfeed.py,
// the same scenes the vision service is tested on), geolocation is mocked per
// person, and the QR code on the organizer's screen is decoded from the page
// itself with jsQR, exactly as an attendee's camera would read it. Strict mode
// needs the vision service running (IAH_VISION_URL on the API).
//
//   VISION_PYTHON  python with apps/vision/requirements-dev.txt (default python3)
//   MAIL_DIR       where the API writes its emails (IAH_MAIL_DIR), to follow emailed links
import { chromium } from 'playwright';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { writeQRVideo } from './rooms.mjs';

const BASE = process.env.BASE_URL || 'http://localhost:8080';
const SHOTS = process.env.SHOTS || join(tmpdir(), 'iah-shots');
mkdirSync(SHOTS, { recursive: true });
const work = join(tmpdir(), 'iah-rooms');
mkdirSync(work, { recursive: true });
const CAMFEED = fileURLToPath(new URL('../../vision/tests/camfeed.py', import.meta.url));
/** Writes a camera feed: room `room` seen from `seat` (>= 100: straight on), with
 *  `code` on its screen when given. Chromium reads the file when the camera opens. */
function camFeed(name, room, seat, code) {
  const out = join(work, name + '.y4m');
  execFileSync(process.env.VISION_PYTHON || 'python3', [CAMFEED, out, '--room', String(room), '--seat', String(seat), ...(code ? ['--code', code] : [])]);
  return name;
}
camFeed('a', 1, 100);   // the venue, as the organizer photographs it
camFeed('b', 9, 100);   // somewhere else

const VENUE = { latitude: 36.19, longitude: 44.01, accuracy: 6 };
const FAR = { latitude: 36.35, longitude: 44.2, accuracy: 6 };

let failed = 0;
const check = (cond, msg) => { console.log(`${cond ? '  PASS' : '  FAIL'}  ${msg}`); if (!cond) failed++; };
const section = (t) => console.log(`\n== ${t}`);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const browsers = {};
async function browserFor(room) {
  browsers[room] ??= await chromium.launch({
    args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream', `--use-file-for-fake-video-capture=${join(work, room + '.y4m')}`],
  });
  return browsers[room];
}

const RUN = Date.now().toString(36);
const PASSWORD = 'blue-river-seven';
let people = 0;
const emailFor = (name) => `${RUN}.${++people}.${name.normalize('NFKD').replace(/[^a-z]/gi, '').toLowerCase() || 'p'}@example.org`;

/** The newest email sent to `to`, with its link of the given kind (#/verify/… or #/reset/…). */
async function emailLink(to, kind) {
  const dir = process.env.MAIL_DIR;
  if (!dir) throw new Error('MAIL_DIR is not set');
  for (let i = 0; i < 100; i++) {
    const files = (() => { try { return readdirSync(dir).filter((f) => f.endsWith('.eml')).sort().reverse(); } catch { return []; } })();
    for (const f of files) {
      const m = readFileSync(join(dir, f), 'utf8').replace(/=\r\n/g, '');
      if (!m.includes('To: ' + to)) continue;
      const link = m.match(new RegExp('https?://[^\\s"<]+#/' + kind + '/(iah_l_[A-Za-z0-9_-]+)'));
      if (link) return '/#/' + kind + '/' + link[1];
    }
    await sleep(100);
  }
  throw new Error(`no ${kind} email for ${to}`);
}

/** Creates an account through the sign-up page; it lands on home. */
async function signUp(page, name, email) {
  await page.goto(BASE + '/#/signup');
  await page.getByLabel('Full name').fill(name);
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await page.getByRole('button', { name: 'Create account' }).click();
}

/** A person with their own account, signed in on a phone-sized screen. */
async function person(name, { room = 'a', geo = VENUE, desktop = false } = {}) {
  const b = await browserFor(room);
  const ctx = await b.newContext({
    viewport: desktop ? { width: 1180, height: 820 } : { width: 390, height: 844 },
    isMobile: !desktop, hasTouch: !desktop, deviceScaleFactor: 2, acceptDownloads: true,
    permissions: ['camera', 'geolocation'], geolocation: geo, locale: 'en-US',
  });
  // record every camera stream, so tests can check the camera is always released
  await ctx.addInitScript(() => {
    window.__streams = [];
    const gum = navigator.mediaDevices?.getUserMedia?.bind(navigator.mediaDevices);
    if (gum) navigator.mediaDevices.getUserMedia = async (c) => { const st = await gum(c); window.__streams.push(st); return st; };
  });
  const page = await ctx.newPage();
  page.errors = [];
  page.on('pageerror', (e) => page.errors.push(e.message));
  page.on('console', (m) => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) page.errors.push(m.text()); });
  const email = emailFor(name);
  await signUp(page, name, email);
  await page.getByRole('heading', { name: new RegExp('Hi ' + name.split(' ')[0]) }).waitFor();
  return { name, email, ctx, page };
}

const liveTracks = (p) => p.page.evaluate(() => window.__streams.flatMap((st) => st.getTracks()).filter((t) => t.readyState === 'live').length);

/** Waits until the organizer's screen shows a new code (the old one has just stopped working). */
async function nextCode(page) {
  await page.locator('.qr-box:not(.stale) svg').waitFor();
  const before = await page.evaluate(() => document.querySelector('.qr-box svg').outerHTML);
  await page.waitForFunction((b) => document.querySelector('.qr-box svg')?.outerHTML !== b, before, { timeout: 15000 });
}

/** Reads the QR on screen the way a phone camera would. A code works only while it
 *  is on the screen, so by default this waits for a fresh one (a full rotation left). */
async function readQR(page, { fresh = true } = {}) {
  await page.locator('.qr-box:not(.stale) svg').waitFor();
  if (fresh) await nextCode(page);
  if (!(await page.evaluate(() => !!window.jsQR))) await page.addScriptTag({ url: '/vendor/jsQR.js' });
  return page.evaluate(async () => {
    const xml = new XMLSerializer().serializeToString(document.querySelector('.qr-box svg'));
    const img = new Image();
    img.src = 'data:image/svg+xml;charset=utf-8,' + encodeURIComponent(xml);
    await img.decode();
    const c = document.createElement('canvas'); c.width = c.height = 480;
    const x = c.getContext('2d');
    x.fillStyle = '#fff'; x.fillRect(0, 0, 480, 480); x.drawImage(img, 40, 40, 400, 400);
    const d = x.getImageData(0, 0, 480, 480);
    return window.jsQR(d.data, 480, 480)?.data || null;
  });
}

const shot = (p, name) => p.page.screenshot({ path: join(SHOTS, name + '.png') });
const heading = (p) => p.page.locator('main h1').first();

try {
  // ---------------------------------------------------------------------------
  section('Organizer: sign up, one title, Start');
  const prof = await person('Prof Ada Lovelace', { desktop: true });
  check(await prof.page.getByLabel('What is it for?').inputValue() !== '', 'home opens with "Start attendance" and a title already filled in');
  await shot(prof, '10-home-composer');
  await prof.page.getByLabel('What is it for?').fill('Software Engineering');
  await prof.page.getByRole('button', { name: 'Start', exact: true }).click();
  const url1 = await readQR(prof.page);
  check(url1 && url1.startsWith(BASE + '/#/c/'), 'sign up -> type a title -> Start: a rotating code is on screen (no organization to set up): ' + (url1 || '').slice(0, 40) + '…');
  check(await prof.page.locator('.back').first().textContent() === 'Prof Ada Lovelace', 'the session went into a personal workspace named after the organizer');
  await shot(prof, '20-live-normal');
  const sessionUrl = prof.page.url();
  const orgUrl = sessionUrl.replace(/\/s\/[\w-]+$/, '');

  const awat = await person('ئاوات کوردی');                 // a Kurdish name must work end to end
  await awat.page.goto(url1);
  await awat.page.getByRole('heading', { name: /You’re checked in/ }).waitFor();
  check(true, 'a Kurdish-named student checks in (non-Latin names work through the whole stack)');

  const ben = await person('Ben Student', { geo: FAR });   // location is irrelevant in normal mode
  await ben.page.goto(url1);
  await ben.page.getByRole('heading', { name: /You’re checked in/ }).waitFor();
  check(true, 'student scans -> checked in with no extra steps (even far away: normal mode ignores location)');
  await shot(ben, '21-normal-success');
  await prof.page.locator('.stat.ok b').filter({ hasText: '2' }).waitFor({ timeout: 8000 });
  check(true, 'the organizer\'s counter went to 2 live');
  check(await ben.page.locator('.pass .stamp').isVisible(), 'the attendee gets a pass with a "Here" stamp');
  await prof.page.getByText('ئاوات کوردی').first().waitFor();
  check(true, 'and the Kurdish name displays correctly in the organizer\'s list');
  await prof.page.getByText('Ben Student').first().waitFor();
  check(true, 'and the name appears in the list');

  await sleep(5600);
  const url2 = await readQR(prof.page);
  check(url2 && url2 !== url1, 'the QR changed after ~5 seconds');

  const ben2 = await ben.page.goto(url2).then(() => ben.page.getByText(/already checked in/).waitFor().then(() => true));
  check(ben2, 'scanning again says "already checked in" (one record per person)');

  const latecomer = await person('Late Lena');
  const replaced = await readQR(prof.page);
  await nextCode(prof.page);                      // the screen moves on: that code must stop working at once
  await sleep(1200);                              // (beyond the 1 s allowance for a request already on its way)
  await latecomer.page.goto(replaced);
  await latecomer.page.getByRole('heading', { name: 'That code has changed' }).waitFor();
  check(true, 'a code is refused as soon as the next one is on the screen, with a friendly "scan again"');
  await shot(latecomer, '22-expired');
  await latecomer.page.getByRole('link', { name: 'Scan again' }).click();
  await latecomer.page.getByRole('heading', { name: 'Scan to check in' }).waitFor();
  check(true, 'and offers the scanner');
  await shot(latecomer, '23-scanner');

  check(latecomer.page.errors.length === 0, 'scanner page opened the camera without errors');

  // Lena's phone gave up: the organizer adds her by her email
  await prof.page.getByRole('button', { name: 'Add someone' }).click();
  await prof.page.getByRole('dialog').getByLabel('Their email').fill('nobody-' + RUN + '@example.org');
  await prof.page.getByRole('dialog').getByRole('button', { name: 'Add', exact: true }).click();
  await prof.page.getByRole('dialog').getByRole('alert').filter({ hasText: /No account uses that email/ }).waitFor();
  check(true, 'adding an email without an account says what to do');
  await prof.page.getByRole('dialog').getByLabel('Their email').fill(latecomer.email);
  await prof.page.getByRole('dialog').getByRole('button', { name: 'No phone with them' }).click();
  await shot(prof, '25-add-someone');
  await prof.page.getByRole('dialog').getByRole('button', { name: 'Add', exact: true }).click();
  await prof.page.locator('.item', { hasText: 'Late Lena' }).getByText(/added by hand/).waitFor({ timeout: 8000 });
  check(true, '"Add someone" by email puts them on the list, marked as added by hand');

  // The in-app scanner must read a real QR from the camera: point a fake camera at
  // the code currently on the organizer's screen and check it jumps to that check-in.
  const live = await readQR(prof.page);
  writeQRVideo(join(work, 'qr.y4m'), live);
  browsers.qr = await chromium.launch({ args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream', `--use-file-for-fake-video-capture=${join(work, 'qr.y4m')}`] });
  const qctx = await browsers.qr.newContext({ viewport: { width: 390, height: 844 }, permissions: ['camera'] });
  const qpage = await qctx.newPage();
  const scanner = await (await fetch(BASE + '/v1/auth/signup', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: 'Scan Tester', email: emailFor('scanner'), password: PASSWORD }),
  })).json();
  await qpage.addInitScript((tok) => localStorage.setItem('iah.session', tok), scanner.token);
  await qpage.goto(BASE + '/#/scan');
  const token = live.split('/#/c/')[1];
  await qpage.waitForURL((u) => u.hash === '#/c/' + token, { timeout: 15000 });
  check(true, 'the in-app scanner decoded the QR from the camera and opened that exact check-in');
  await qctx.close();

  // ---------------------------------------------------------------------------
  section('Projector mode');
  await prof.page.getByRole('button', { name: 'Show on projector' }).click();
  await prof.page.waitForFunction(() => document.body.classList.contains('present'));
  await shot(prof, '24-present');
  await prof.page.keyboard.press('Escape');
  await prof.page.waitForFunction(() => !document.body.classList.contains('present'));
  check(true, 'projector mode shows and exits with Escape');

  const lastUrl = await readQR(prof.page);   // the final code, taken just before the end
  await prof.page.getByRole('button', { name: 'End session' }).click();
  await prof.page.getByRole('dialog').getByRole('button', { name: 'End session' }).click();
  await prof.page.getByRole('heading', { name: 'This session has ended' }).waitFor();
  check(true, 'ending the session works and the QR disappears');
  const late2 = await person('Too Late');
  await late2.page.goto(lastUrl);
  await late2.page.getByRole('heading', { name: 'This session has ended' }).waitFor();
  check(true, 'check-in after the end is refused');

  const [dl] = await Promise.all([prof.page.waitForEvent('download'), prof.page.getByRole('button', { name: 'Download list' }).click()]);
  const csv = readFileSync(await dl.path(), 'utf8');
  check(csv.includes('Ben Student') && csv.startsWith('﻿') && csv.includes('Full Name'), 'CSV download has the attendee (UTF-8 with BOM)');

  // ---------------------------------------------------------------------------
  section('Strict mode: save the place + reference photos');
  await prof.page.goto(orgUrl);
  await prof.page.getByRole('link', { name: 'Places' }).click();
  await prof.page.getByLabel('Name').fill('Room 204');
  await prof.page.getByRole('button', { name: 'Use my current location' }).click();
  await prof.page.getByText(/Got it/).waitFor();
  await prof.page.getByRole('button', { name: 'Save place' }).click();
  await prof.page.getByRole('heading', { name: 'Room 204' }).waitFor();
  check(true, 'place saved from the organizer\'s current location');
  await prof.page.getByText('No photos').waitFor();
  const dialog = prof.page.getByRole('dialog');
  await prof.page.getByRole('button', { name: 'Take photo' }).click();
  await dialog.getByRole('button', { name: 'Cancel' }).click();               // close before the camera is even up
  await sleep(1500);
  check((await liveTracks(prof)) === 0, 'closing the photo dialog straight away still turns the camera off');
  for (const n of [1, 2, 3]) {
    await prof.page.getByRole('button', { name: 'Take photo' }).click();
    await dialog.getByRole('button', { name: 'Take photo' }).and(prof.page.locator(':enabled')).waitFor();
    await sleep(700);   // let the fake camera deliver frames
    if (n === 1) await shot(prof, '30-reference-camera');
    await dialog.getByRole('button', { name: 'Take photo' }).click();
    await prof.page.getByText(n === 3 ? '3 photos' : `${n} photo${n === 1 ? '' : 's'} · add ${3 - n} more`).waitFor({ timeout: 20000 });
  }
  check(await prof.page.locator('.thumb img').count() === 3, 'three photos of the place saved, checked by the vision service, shown as thumbnails');
  await shot(prof, '30-reference-photos');
  await sleep(1000);
  check((await liveTracks(prof)) === 0, 'the camera is off once the photos are taken');

  await prof.page.getByRole('button', { name: 'Test from a seat' }).click();
  await dialog.getByRole('button', { name: 'Take photo' }).and(prof.page.locator(':enabled')).waitFor();
  await sleep(700);
  await dialog.getByRole('button', { name: 'Take photo' }).click();
  await prof.page.getByRole('heading', { name: /Strong match/ }).waitFor({ timeout: 20000 });
  check(true, '"Test from a seat" confirms photos from here will be recognized');
  await shot(prof, '30-test-from-a-seat');
  await dialog.getByRole('button', { name: 'OK' }).click();

  section('Strict mode: start, then attendees photograph the screen');
  await prof.page.goto(orgUrl);
  await prof.page.getByRole('button', { name: /^Strict/ }).click();
  await prof.page.getByLabel('What is it for?').fill('Strict exam');
  await shot(prof, '31-start-strict');
  await prof.page.getByRole('button', { name: /Start & show QR/ }).click();
  const strictUrl = await readQR(prof.page);
  check(!!strictUrl, 'strict session shows a rotating QR too');
  const strictSession = prof.page.url();

  // today's photo of the room, taken from the live session screen
  await prof.page.getByRole('heading', { name: 'Today’s photo' }).waitFor();
  await prof.page.getByRole('button', { name: 'Take photo' }).click();
  await dialog.getByRole('button', { name: 'Take photo' }).and(prof.page.locator(':enabled')).waitFor();
  await sleep(700);
  await dialog.getByRole('button', { name: 'Take photo' }).click();
  await prof.page.getByText(/compared with this and the place’s saved photos/).waitFor({ timeout: 20000 });
  check(true, 'today\'s photo added from the live session; attendees are compared with it and the saved photos');

  /** An attendee whose camera sees `room` from `seat`, with the code live on the
   *  organizer's screen right now (or no code); opens that code's link. */
  async function attendee(name, { room, seat, geo, withCode = true }) {
    const key = 'cam-' + name.split(' ')[0].toLowerCase();
    camFeed(key, room, seat);
    const p = await person(name, { room: key, geo });
    const url = await readQR(prof.page);
    camFeed(key, room, seat, withCode ? url : undefined);   // the screen as it is now
    await p.page.goto(url);
    return p;
  }
  const photoStep = (p) => p.page.getByText('Point at the screen with the code').waitFor();

  // 1. right room, at the venue -> the photo is taken by itself when the code is in view -> verified
  const carla = await attendee('Carla Inside', { room: 1, seat: 2, geo: VENUE });
  await carla.page.getByRole('button', { name: 'Share my location' }).click();
  await shot(carla, '32-strict-photo-step');
  await carla.page.getByRole('heading', { name: /You’re checked in/ }).waitFor({ timeout: 30000 });
  check(true, 'right place + photo of this room showing the current code -> checked in, no button to press');
  await shot(carla, '33-strict-success');
  await sleep(1000);
  check((await liveTracks(carla)) === 0, 'the camera is off after the photo');

  // 2. at the venue, the current code, but a different room around it -> review
  const dan = await attendee('Dan Elsewhere', { room: 9, seat: 2, geo: VENUE });
  await dan.page.getByRole('button', { name: 'Share my location' }).click();
  await dan.page.getByRole('heading', { name: 'Sent to your organizer to review' }).waitFor({ timeout: 30000 });
  check(await dan.page.getByText(/did not match the room/).isVisible(), 'the code shown in another room -> sent for review, with a plain-language reason');
  check(await dan.page.getByRole('button', { name: 'Take the photo again' }).isVisible(), 'and is offered to retake the photo');
  await shot(dan, '34-strict-review');

  // 3. right room but located far away -> rejected
  const eve = await attendee('Eve Faraway', { room: 1, seat: 3, geo: FAR });
  await eve.page.getByRole('button', { name: 'Share my location' }).click();
  await eve.page.getByRole('heading', { name: 'You seem to be outside the place' }).waitFor({ timeout: 30000 });
  check(true, 'confidently outside the radius -> rejected');
  await shot(eve, '35-strict-rejected');

  // 4. refuses to share location -> can continue; goes to review
  const fay = await attendee('Fay NoGps', { room: 1, seat: 4, geo: VENUE });
  await fay.ctx.clearPermissions();
  await fay.ctx.grantPermissions(['camera']);
  await fay.page.getByRole('button', { name: 'Share my location' }).click();
  await fay.page.getByText(/Location is blocked/).waitFor();
  await shot(fay, '36-strict-location-blocked');
  await fay.page.getByRole('button', { name: /Continue without location/ }).click();
  await fay.page.getByRole('heading', { name: 'Sent to your organizer to review' }).waitFor({ timeout: 30000 });
  check(true, 'location denied -> still can finish; goes to review (never silently accepted)');

  // 5. a photo of the room with no code on the screen (e.g. an old photo) -> review
  const hal = await attendee('Hal NoCode', { room: 1, seat: 5, geo: VENUE, withCode: false });
  await hal.page.getByRole('button', { name: 'Share my location' }).click();
  await photoStep(hal);
  await hal.page.getByRole('button', { name: 'Take photo anyway' }).click({ timeout: 15000 });
  await hal.page.getByRole('heading', { name: 'Sent to your organizer to review' }).waitFor({ timeout: 30000 });
  check(await hal.page.getByText(/did not show the current code/).isVisible(), 'a room photo without the current code -> review, never verified');
  // ...and can retake it straight away, this time with the code in view
  camFeed('cam-hal', 1, 5, await readQR(prof.page));
  await hal.page.getByRole('button', { name: 'Take the photo again' }).click();
  await hal.page.getByRole('heading', { name: /You’re checked in/ }).waitFor({ timeout: 30000 });
  check(true, 'retaking the photo from the review screen verifies them');

  // organizer sees the two flagged people and approves one
  await prof.page.locator('.stat.warn b').filter({ hasText: '2' }).waitFor({ timeout: 8000 });
  check(true, 'organizer sees 2 to review live');
  await prof.page.locator('.item', { hasText: 'Dan Elsewhere' }).getByRole('button', { name: 'Approve' }).click();
  await prof.page.locator('.stat.warn b').filter({ hasText: '1' }).waitFor({ timeout: 8000 });
  await prof.page.locator('.stat.ok b').filter({ hasText: '3' }).waitFor({ timeout: 8000 });
  check(true, 'approving moves them to checked in (3 in, 1 to review)');
  await shot(prof, '37-live-strict-review');
  check(!(await prof.page.getByText('Eve Faraway').count()), 'a rejected attempt never becomes an attendance record');

  await prof.page.getByRole('button', { name: 'Download list' }).focus();
  await sleep(2500);
  check(await prof.page.evaluate(() => document.activeElement?.textContent === 'Download list'),
        'keyboard focus stays put on the live screen while it updates every second');

  // someone in review can try again and get verified straight away
  const gus = await attendee('Gus Retry', { room: 1, seat: 8, geo: VENUE });
  await gus.page.getByRole('button', { name: 'Share my location' }).click();
  await gus.page.getByRole('button', { name: /^Skip/ }).click();           // skips the photo while the camera starts
  await gus.page.getByRole('heading', { name: 'Sent to your organizer to review' }).waitFor();
  await sleep(1000);
  check((await liveTracks(gus)) === 0, 'skipping the photo turns the camera off');
  await gus.page.goto(BASE + '/#/');                                        // comes back later, fresh from the camera app
  await gus.page.getByRole('heading', { name: /Hi Gus/ }).waitFor();
  const gusUrl = await readQR(prof.page);
  camFeed('cam-gus', 1, 8, gusUrl);
  await gus.page.goto(gusUrl);
  await gus.page.getByText(/waiting for your organizer’s review/).waitFor();
  await gus.page.getByRole('button', { name: 'Share my location' }).click();
  await gus.page.getByRole('heading', { name: /You’re checked in/ }).waitFor({ timeout: 30000 });
  check(true, 'a person in review can retry and is verified by a good attempt');
  await prof.page.locator('.stat.ok b').filter({ hasText: '4' }).waitFor({ timeout: 8000 });
  check(true, 'the organizer\'s count follows (4 in)');

  // ---------------------------------------------------------------------------
  section('Reports: attendance per person');
  await prof.page.goto(orgUrl + '/report');
  await prof.page.locator('table.table').waitFor();
  const benRow = prof.page.locator('tr', { hasText: 'Ben Student' });
  check((await benRow.textContent()).includes('1 / 2') && (await benRow.textContent()).includes('50%'), 'Ben: 1 of 2 sessions, 50%');
  check((await prof.page.locator('tr', { hasText: 'Late Lena' }).textContent()).includes('1 / 2'), 'someone added by hand counts as attended');
  check(await prof.page.getByText(/below 75%/).first().isVisible(), 'people below 75% are counted at the top');
  await shot(prof, '38-report');
  await prof.page.getByLabel('Title').selectOption('Strict exam');
  await prof.page.locator('tr', { hasText: 'Carla Inside' }).filter({ hasText: '1 / 1' }).waitFor();
  check(!(await prof.page.locator('tr', { hasText: 'Ben Student' }).count()), 'choosing one title reports on those sessions only');
  const [rdl] = await Promise.all([prof.page.waitForEvent('download'), prof.page.getByRole('button', { name: 'Download' }).click()]);
  const rcsv = readFileSync(await rdl.path(), 'utf8');
  check(rcsv.includes('Carla Inside') && rcsv.includes('Rate (%)'), 'the report downloads as a spreadsheet');

  // ---------------------------------------------------------------------------
  section('Team: invite by link');
  await prof.page.goto(orgUrl);
  await prof.page.getByRole('link', { name: 'Team' }).click();
  await prof.page.getByLabel('They can').selectOption('reviewer');
  await prof.page.getByRole('button', { name: 'Create invite link' }).click();
  const link = await prof.page.getByLabel('Invite link').inputValue();
  check(link.startsWith(BASE + '/#/join/iah_inv_'), 'invite link created');
  await shot(prof, '40-team-invite');
  const rev = await person('Rita Reviewer');
  await rev.page.goto(link);
  await rev.page.getByRole('heading', { name: 'Prof Ada Lovelace' }).waitFor();
  check(!(await rev.page.getByRole('heading', { name: 'Start attendance' }).count()), 'invited reviewer joins; no "Start attendance" for their role');
  const again = await person('Sam Second');
  await again.page.goto(link);
  await again.page.getByRole('heading', { name: 'This invitation does not work' }).waitFor();
  check(true, 'the invite link works only once');
  await rev.page.goto(strictSession);
  await rev.page.getByText('Dan Elsewhere').waitFor();
  check((await rev.page.getByText('Live now').count()) === 1 && (await rev.page.locator('.qr-box').count()) === 0,
        'a reviewer sees the live list but not the QR');

  // ---------------------------------------------------------------------------
  section('Accounts: one identity, everywhere');
  const vctx = await (await browserFor('a')).newContext({ viewport: { width: 390, height: 844 }, locale: 'en-US' });
  const v = { name: 'Vera Visitor', email: emailFor('vera'), ctx: vctx, page: await vctx.newPage() };
  v.page.errors = [];
  v.page.on('pageerror', (e) => v.page.errors.push(e.message));
  await v.page.goto(BASE + '/');
  await v.page.getByRole('heading', { level: 1, name: /One title\. One QR code\./ }).waitFor();
  check(await v.page.getByRole('link', { name: 'Start free' }).first().isVisible(), 'a visitor sees the start page, with sign up and sign in');
  await v.page.locator('.demo-qr svg').waitFor();
  check(true, 'the landing page shows a live, rotating QR demo');
  await shot(v, '60-landing');

  // a check-in link opened without an account: the scan is held while the account is created
  await prof.page.goto(BASE + '/#/');
  await prof.page.getByLabel('What is it for?').fill('Open Day');
  await prof.page.getByRole('button', { name: 'Start', exact: true }).click();
  await v.page.goto(await readQR(prof.page));
  await v.page.getByRole('heading', { name: 'Create your account' }).waitFor();
  check(await v.page.locator('.auth-context').getByText('Open Day').isVisible(), 'a check-in link without an account asks to create one, and shows which session it is for');
  await v.page.getByLabel('Full name').fill(v.name);
  await v.page.getByLabel('Email').fill(carla.email);
  await v.page.getByLabel('Password', { exact: true }).fill('password123');
  await v.page.getByRole('button', { name: 'Create account' }).click();
  await v.page.getByRole('alert').filter({ hasText: /too common/ }).waitFor();
  check(true, 'a well-known password is refused with a clear reason');
  await v.page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await v.page.getByRole('button', { name: 'Create account' }).click();
  await v.page.getByRole('alert').filter({ hasText: /already an account/ }).waitFor();
  check(true, 'an email that already has an account is pointed to sign in instead');
  await shot(v, '61-signup-errors');
  await sleep(11000);                                                       // the code on the screen moves on meanwhile
  await v.page.getByLabel('Email').fill(v.email);
  await v.page.getByRole('button', { name: 'Create account' }).click();
  await v.page.getByRole('heading', { name: /You’re checked in/ }).waitFor();
  check(true, 'after signing up from a check-in link, they are checked in without scanning again (the code had moved on)');
  await shot(v, '64-checked-in-after-signup');
  await prof.page.getByText('Vera Visitor').first().waitFor({ timeout: 8000 });
  check(true, 'and the organizer sees them arrive');

  // the organizer finds the running session again from home
  await prof.page.goto(BASE + '/#/');
  await prof.page.getByRole('heading', { name: 'Live now' }).waitFor();
  check(await prof.page.locator('.item', { hasText: 'Open Day' }).isVisible(), 'home lists sessions that are still live');

  // confirm the email from the link that was emailed
  await v.page.goto(BASE + (await emailLink(v.email, 'verify')));
  await v.page.getByRole('heading', { name: 'Email confirmed' }).waitFor();
  check(true, 'the emailed confirmation link confirms the address');

  // sign out, wrong password, right password
  await v.page.goto(BASE + '/#/me');
  await v.page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await v.page.getByRole('heading', { level: 1, name: /One title\. One QR code\./ }).waitFor();
  await v.page.goto(BASE + '/#/signin');
  await v.page.getByRole('heading', { name: 'Welcome back' }).waitFor();
  await v.page.getByLabel('Email').fill(v.email);
  await v.page.getByLabel('Password', { exact: true }).fill('not-my-password');
  await v.page.getByRole('button', { name: 'Sign in' }).click();
  await v.page.getByRole('alert').filter({ hasText: /don’t match/ }).waitFor();
  check(true, 'a wrong password says so, without telling whether the email exists');
  await v.page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await v.page.getByRole('button', { name: 'Sign in' }).click();
  await v.page.getByRole('heading', { name: /Hi Vera/ }).waitFor();
  check(true, 'signing in with the right password lands on home');

  // forgot password -> emailed link -> new password -> signed in; the old one stops working
  await v.page.goto(BASE + '/#/me');
  await v.page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await v.page.getByRole('heading', { level: 1, name: /One title\. One QR code\./ }).waitFor();
  await v.page.goto(BASE + '/#/signin');
  await v.page.getByRole('link', { name: 'Forgot password?' }).click();
  await v.page.getByRole('heading', { name: 'Reset your password' }).waitFor();
  await v.page.getByLabel('Email').fill(v.email);
  await v.page.getByRole('button', { name: 'Send reset link' }).click();
  await v.page.getByRole('heading', { name: 'Check your inbox' }).waitFor();
  await v.page.goto(BASE + (await emailLink(v.email, 'reset')));
  await v.page.getByLabel('New password').fill('green-hill-eight');
  await shot(v, '62-reset');
  await v.page.getByRole('button', { name: 'Save and sign in' }).click();
  await v.page.getByRole('heading', { name: /Hi Vera/ }).waitFor();
  check(true, 'forgot password: the emailed link sets a new password and signs in');

  // the organizer signs out from the account menu and back in, and lands where they were going
  await prof.page.getByRole('button', { name: 'Your account' }).click();
  await shot(prof, '63-account-menu');
  await prof.page.getByRole('menuitem', { name: 'Sign out' }).click();
  await prof.page.getByRole('heading', { level: 1, name: /One title\. One QR code\./ }).waitFor();
  await prof.page.goto(strictSession);
  await prof.page.getByRole('heading', { name: 'Welcome back' }).waitFor();
  await prof.page.getByLabel('Email').fill(prof.email);
  await prof.page.getByLabel('Password', { exact: true }).fill(PASSWORD);
  await prof.page.getByRole('button', { name: 'Sign in' }).click();
  await prof.page.locator('.qr-box:not(.stale) svg').waitFor();
  check(prof.page.url() === strictSession, 'signing in returns to the page that was asked for (the live session, its code showing)');

  // ---------------------------------------------------------------------------
  section('Home & errors');
  await shot(carla, '50-home-student');
  await carla.page.goto(BASE + '/#/');
  await carla.page.getByRole('heading', { name: 'Your attendance' }).waitFor();
  check(await carla.page.getByText('Strict exam').count() > 0, 'student home lists their check-ins, from every organization');
  await prof.page.goto(BASE + '/#/');
  await shot(prof, '51-home-organizer');

  const allErrors = [prof, ben, carla, dan, eve, fay, hal, gus, rev, v].flatMap((p) => p.page.errors);
  check(allErrors.length === 0, 'no JavaScript errors in any browser' + (allErrors.length ? ': ' + allErrors.join(' | ') : ''));
} catch (err) {
  failed++;
  console.error('\nUNEXPECTED FAILURE:', err);
} finally {
  await Promise.all(Object.values(browsers).map((b) => b.close()));
  console.log(failed ? `\n${failed} check(s) FAILED` : '\nAll checks passed');
  console.log('screenshots:', SHOTS);
  process.exit(failed ? 1 : 0);
}
