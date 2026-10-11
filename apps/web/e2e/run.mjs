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
import { chromium } from 'playwright';
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
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

/** A signed-in person on a phone-sized screen. */
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
  await page.goto(BASE + '/');
  await page.getByLabel('What’s your name?').fill(name);
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByRole('heading', { name: new RegExp('Hi ' + name.split(' ')[0]) }).waitFor();
  return { name, ctx, page };
}

const liveTracks = (p) => p.page.evaluate(() => window.__streams.flatMap((st) => st.getTracks()).filter((t) => t.readyState === 'live').length);

/** Reads the QR on screen the way a phone camera would. */
async function readQR(page) {
  await page.locator('.qr-box:not(.stale) svg').waitFor();
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
  section('Organizer sets up in seconds');
  const prof = await person('Prof Ada Lovelace', { desktop: true });
  await prof.page.getByRole('link', { name: 'Set up your organization' }).click();
  await prof.page.getByLabel('Organization name').fill('Engineering School ' + RUN);
  await prof.page.getByRole('button', { name: 'Create' }).click();
  await prof.page.getByRole('heading', { name: 'Start attendance' }).waitFor();
  check(true, 'organization created with just a name; lands on the Start screen');
  await shot(prof, '10-org-start');
  const orgUrl = prof.page.url();

  // ---------------------------------------------------------------------------
  section('Normal mode: QR only, rotating every 5 seconds');
  await prof.page.getByRole('button', { name: /^Normal/ }).click();
  await prof.page.getByLabel('What is it for?').fill('Software Engineering');
  await prof.page.getByRole('button', { name: /Start & show QR/ }).click();
  const url1 = await readQR(prof.page);
  check(url1 && url1.startsWith(BASE + '/#/c/'), 'the QR on screen decodes to a check-in link: ' + (url1 || '').slice(0, 48) + '…');
  await shot(prof, '20-live-normal');
  const sessionUrl = prof.page.url();

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
  await prof.page.getByText('ئاوات کوردی').first().waitFor();
  check(true, 'and the Kurdish name displays correctly in the organizer\'s list');
  await prof.page.getByText('Ben Student').first().waitFor();
  check(true, 'and the name appears in the list');

  await sleep(5600);
  const url2 = await readQR(prof.page);
  check(url2 && url2 !== url1, 'the QR changed after ~5 seconds');

  const ben2 = await ben.page.goto(url2).then(() => ben.page.getByText(/already checked in/).waitFor().then(() => true));
  check(ben2, 'scanning again says "already checked in" (one record per person)');

  await sleep(12000);
  const latecomer = await person('Late Lena');
  await latecomer.page.goto(url1);
  await latecomer.page.getByRole('heading', { name: 'That code has changed' }).waitFor();
  check(true, 'a code that is >2 rotations old is refused with a friendly "scan again"');
  await shot(latecomer, '22-expired');
  await latecomer.page.getByRole('link', { name: 'Scan again' }).click();
  await latecomer.page.getByRole('heading', { name: 'Scan to check in' }).waitFor();
  check(true, 'and offers the scanner');
  await shot(latecomer, '23-scanner');

  check(latecomer.page.errors.length === 0, 'scanner page opened the camera without errors');

  // The in-app scanner must read a real QR from the camera: point a fake camera at
  // the code currently on the organizer's screen and check it jumps to that check-in.
  const live = await readQR(prof.page);
  writeQRVideo(join(work, 'qr.y4m'), live);
  browsers.qr = await chromium.launch({ args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream', `--use-file-for-fake-video-capture=${join(work, 'qr.y4m')}`] });
  const qctx = await browsers.qr.newContext({ viewport: { width: 390, height: 844 }, permissions: ['camera'] });
  const qpage = await qctx.newPage();
  await qpage.addInitScript(() => localStorage.setItem('iah.dev', JSON.stringify({ sub: 'scanner-1', name: 'Scan Tester' })));
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

  // organizer sees the three flagged people and approves one
  await prof.page.locator('.stat.warn b').filter({ hasText: '3' }).waitFor({ timeout: 8000 });
  check(true, 'organizer sees 3 to review live');
  await prof.page.locator('.item', { hasText: 'Dan Elsewhere' }).getByRole('button', { name: 'Approve' }).click();
  await prof.page.locator('.stat.warn b').filter({ hasText: '2' }).waitFor({ timeout: 8000 });
  await prof.page.locator('.stat.ok b').filter({ hasText: '2' }).waitFor({ timeout: 8000 });
  check(true, 'approving moves them to checked in (2 in, 2 to review)');
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
  await prof.page.locator('.stat.ok b').filter({ hasText: '3' }).waitFor({ timeout: 8000 });
  check(true, 'the organizer\'s count follows (3 in)');

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
  await rev.page.getByRole('heading', { name: /Engineering School/ }).waitFor();
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
  section('Home & errors');
  await shot(carla, '50-home-student');
  await carla.page.goto(BASE + '/#/');
  await carla.page.getByText('Your recent check-ins').waitFor();
  check(await carla.page.getByText('Strict exam').count() > 0, 'student home lists their recent check-ins');
  await prof.page.goto(BASE + '/#/');
  await shot(prof, '51-home-organizer');

  const allErrors = [prof, ben, carla, dan, eve, fay, hal, gus, rev].flatMap((p) => p.page.errors);
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
