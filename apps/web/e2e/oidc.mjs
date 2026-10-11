// End-to-end test of the production sign-in path (OpenID Connect, code + PKCE)
// against a mock identity provider. Starts the provider, then the API binary
// configured for OIDC, then drives real Chromium.
//
//   API_BIN=/path/to/api DATABASE_URL=postgres://... node e2e/oidc.mjs
import { chromium } from 'playwright';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { startMockIdP } from './mock-idp.mjs';

const API_BIN = process.env.API_BIN;
const DATABASE_URL = process.env.DATABASE_URL;
if (!API_BIN || !DATABASE_URL) { console.error('set API_BIN and DATABASE_URL'); process.exit(2); }
const APP_PORT = 8091, IDP_PORT = 9411;
const APP = `http://localhost:${APP_PORT}`;
const ACCESS_TTL = 40;   // the app refreshes 30 s before expiry, so a refresh happens after ~10 s

let failed = 0;
const check = (cond, msg) => { console.log(`${cond ? '  PASS' : '  FAIL'}  ${msg}`); if (!cond) failed++; };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const idp = await startMockIdP({ port: IDP_PORT, clientId: 'iamhere-web', appOrigin: APP, accessTtlS: ACCESS_TTL });
const api = spawn(API_BIN, [], {
  env: {
    ...process.env, IAH_ENV: 'production', IAH_LISTEN: `:${APP_PORT}`, IAH_DATABASE_URL: DATABASE_URL,
    IAH_OIDC_ISSUER: idp.issuer, IAH_OIDC_CLIENT_ID: 'iamhere-web',
    IAH_WEB_DIR: fileURLToPath(new URL('../public', import.meta.url)),
  },
  stdio: ['ignore', 'ignore', 'inherit'],
});
let browser;
try {
  for (let i = 0; ; i++) {
    try { if ((await fetch(`${APP}/readyz`)).ok) break; } catch { /* starting */ }
    if (i > 40) throw new Error('API did not start');
    await sleep(250);
  }
  const cfg = await (await fetch(`${APP}/v1/public-config`)).json();
  check(cfg.auth === 'oidc' && cfg.issuer === idp.issuer, 'API in production mode advertises OIDC sign-in');
  check((await fetch(`${APP}/v1/me`, { headers: { Authorization: 'Bearer dev:x:Mallory' } })).status === 401, 'dev tokens are refused in production mode');

  browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 390, height: 844 } });
  const page = await ctx.newPage();
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('console', (m) => { if (m.type() === 'error' && !/Failed to load resource/.test(m.text())) errors.push(m.text()); });
  const signIn = async (p, name) => {
    await p.waitForURL((u) => u.origin === idp.issuer && u.pathname === '/auth', { timeout: 15000 });
    await p.getByLabel('Full name').fill(name);
    await p.getByRole('button', { name: 'Sign in' }).click();
  };

  console.log('\n== Sign in (authorization code + PKCE)');
  await page.goto(APP + '/');
  await signIn(page, 'Olivia Organizer');
  await page.getByRole('heading', { name: /Hi Olivia/ }).waitFor();
  check(true, 'signed in through the identity provider and landed on the app');
  check(!page.url().includes('code='), 'the authorization code is removed from the address bar');
  check(idp.stats.codeExchanges === 1 && idp.stats.pkceFailures === 0, 'the code was exchanged once, with a valid PKCE verifier');

  console.log('\n== Organizer works normally with real tokens');
  await page.getByRole('link', { name: 'Set up your organization' }).click();
  await page.getByLabel('Organization name').fill('OIDC School');
  await page.getByRole('button', { name: 'Create' }).click();
  await page.getByRole('button', { name: /Start & show QR/ }).click();
  await page.locator('.qr-box:not(.stale) svg').waitFor();
  const sessionUrl = page.url();
  check(true, 'created an organization and started a session');

  console.log('\n== A signed-out attendee opens a check-in link');
  // the attendee is sent to sign in, then straight back to the check-in they opened
  const actx = await browser.newContext({ viewport: { width: 390, height: 844 } });
  const apage = await actx.newPage();
  const [, orgId, sid] = /#\/o\/([\w-]+)\/s\/([\w-]+)/.exec(sessionUrl);
  const token = await page.evaluate(async (path) => {
    const r = await fetch(path, { headers: { Authorization: 'Bearer ' + JSON.parse(sessionStorage.getItem('iah.auth')).access } });
    return (await r.json()).token;
  }, `/v1/orgs/${orgId}/sessions/${sid}/qr`);
  await apage.goto(`${APP}/#/c/${token}`);
  await signIn(apage, 'Sam Student');
  await apage.getByRole('heading', { name: /You’re checked in/ }).waitFor();
  check(apage.url().includes('#/c/' + token), 'returned to the exact check-in link after signing in');
  check(true, 'and was checked in (a fresh code survives the sign-in round trip)');
  await page.goto(sessionUrl);
  await page.getByText('Sam Student').waitFor();
  check(true, 'the organizer sees the attendee under the name from the identity provider');

  console.log('\n== Token refresh');
  const before = idp.stats.refreshes;
  await sleep((ACCESS_TTL - 30 + 3) * 1000);   // past the refresh point
  await page.goto(APP + '/#/');
  await page.getByRole('heading', { name: /Hi Olivia/ }).waitFor();
  check(idp.stats.refreshes > before, 'an expiring access token is refreshed silently');
  check(new URL(page.url()).origin === APP, 'no new sign-in was needed');

  console.log('\n== A rejected token sends the person to sign in again');
  await page.evaluate(() => {
    const t = JSON.parse(sessionStorage.getItem('iah.auth'));
    sessionStorage.setItem('iah.auth', JSON.stringify({ ...t, access: 'not-a-valid-token', exp: Date.now() + 3600e3, refresh: undefined }));
  });
  await page.goto(APP + '/#/me');
  await page.reload();
  await page.waitForURL((u) => u.origin === idp.issuer, { timeout: 15000 });
  check(true, 'a 401 from the API leads back to the identity provider (no broken page)');
  await page.getByLabel('Full name').fill('Olivia Organizer');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await page.getByRole('heading', { name: 'Olivia Organizer' }).waitFor();
  check(page.url().endsWith('#/me'), 'and returns to the page they were on');

  console.log('\n== Sign out');
  await page.getByRole('button', { name: 'Sign out' }).click();
  await page.waitForURL((u) => u.origin === idp.issuer && u.pathname === '/auth', { timeout: 15000 });
  const q = idp.stats.lastLogoutQuery || {};
  check(idp.stats.logouts === 1 && q.id_token_hint && q.post_logout_redirect_uri === APP + '/', 'signed out at the identity provider (with id_token_hint) and came back');
  check(await page.evaluate(() => sessionStorage.getItem('iah.auth')) === null, 'no tokens are left in the browser');

  check(errors.length === 0, 'no JavaScript errors' + (errors.length ? ': ' + errors.join(' | ') : ''));
} catch (err) {
  failed++;
  console.error('\nUNEXPECTED FAILURE:', err);
} finally {
  await browser?.close();
  api.kill();
  idp.close();
  console.log(failed ? `\n${failed} check(s) FAILED` : '\nAll checks passed');
  process.exit(failed ? 1 : 0);
}
