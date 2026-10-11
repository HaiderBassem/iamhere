// A minimal OpenID Connect provider that behaves like Keycloak for the parts the
// app uses: discovery, JWKS, authorization code + PKCE (S256 only), refresh
// tokens, RS256-signed tokens with `azp`, and RP-initiated logout. Test use only.
import http from 'node:http';
import { createHash, generateKeyPairSync, randomBytes, sign } from 'node:crypto';

const b64url = (buf) => Buffer.from(buf).toString('base64url');

export function startMockIdP({ port, clientId, appOrigin, accessTtlS = 60 }) {
  const issuer = `http://localhost:${port}`;
  const { publicKey, privateKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
  const jwk = { ...publicKey.export({ format: 'jwk' }), kid: 'k1', alg: 'RS256', use: 'sig' };
  const codes = new Map(), refresh = new Map();
  const stats = { logins: 0, codeExchanges: 0, refreshes: 0, logouts: 0, pkceFailures: 0, lastLogoutQuery: null };

  const jwt = (claims) => {
    const head = b64url(JSON.stringify({ alg: 'RS256', typ: 'JWT', kid: 'k1' }));
    const body = b64url(JSON.stringify(claims));
    return `${head}.${body}.${b64url(sign('RSA-SHA256', Buffer.from(`${head}.${body}`), privateKey))}`;
  };
  const tokens = (user) => {
    const now = Math.floor(Date.now() / 1000);
    const base = { iss: issuer, sub: user.sub, iat: now, exp: now + accessTtlS, azp: clientId, name: user.name, preferred_username: user.sub };
    const rt = b64url(randomBytes(24));
    refresh.set(rt, user);
    return {
      access_token: jwt({ ...base, aud: 'account', typ: 'Bearer' }),
      id_token: jwt({ ...base, aud: clientId, typ: 'ID' }),
      refresh_token: rt, token_type: 'Bearer', expires_in: accessTtlS,
    };
  };
  const send = (res, status, body, headers = {}) => {
    res.writeHead(status, { 'Access-Control-Allow-Origin': '*', 'Cache-Control': 'no-store', ...headers });
    res.end(typeof body === 'string' ? body : JSON.stringify(body));
  };
  const readBody = (req) => new Promise((r) => { let d = ''; req.on('data', (c) => (d += c)); req.on('end', () => r(new URLSearchParams(d))); });
  const okRedirect = (u) => typeof u === 'string' && u.startsWith(appOrigin + '/');

  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, issuer);
    if (req.method === 'OPTIONS') return send(res, 204, '', { 'Access-Control-Allow-Headers': 'Content-Type', 'Access-Control-Allow-Methods': 'GET, POST' });

    if (url.pathname === '/.well-known/openid-configuration') {
      return send(res, 200, {
        issuer, authorization_endpoint: `${issuer}/auth`, token_endpoint: `${issuer}/token`, jwks_uri: `${issuer}/jwks`,
        end_session_endpoint: `${issuer}/logout`, id_token_signing_alg_values_supported: ['RS256'],
        code_challenge_methods_supported: ['S256'], response_types_supported: ['code'],
      }, { 'Content-Type': 'application/json' });
    }
    if (url.pathname === '/jwks') return send(res, 200, { keys: [jwk] }, { 'Content-Type': 'application/json' });
    if (url.pathname === '/stats') return send(res, 200, stats, { 'Content-Type': 'application/json' });

    if (url.pathname === '/auth' && req.method === 'GET') {
      const q = url.searchParams;
      if (q.get('client_id') !== clientId || q.get('response_type') !== 'code' || q.get('code_challenge_method') !== 'S256' ||
          !q.get('code_challenge') || !q.get('state') || !okRedirect(q.get('redirect_uri'))) {
        return send(res, 400, 'invalid authorization request', { 'Content-Type': 'text/plain' });
      }
      // a login page, like Keycloak's: the person types their name and signs in
      const hidden = [...q].map(([k, v]) => `<input type="hidden" name="${k}" value="${v.replace(/"/g, '&quot;')}">`).join('');
      return send(res, 200, `<!doctype html><title>Mock sign-in</title><h1>Sign in to Mock IdP</h1>
        <form method="post" action="/auth">${hidden}<label>Full name <input name="name" required></label><button>Sign in</button></form>`,
      { 'Content-Type': 'text/html' });
    }
    if (url.pathname === '/auth' && req.method === 'POST') {
      const f = await readBody(req);
      stats.logins++;
      const code = b64url(randomBytes(18));
      const name = f.get('name');
      codes.set(code, { challenge: f.get('code_challenge'), redirect: f.get('redirect_uri'), user: { sub: 'u-' + createHash('sha256').update(name).digest('hex').slice(0, 12), name } });
      const back = new URL(f.get('redirect_uri'));
      back.searchParams.set('code', code);
      back.searchParams.set('state', f.get('state'));
      res.writeHead(302, { Location: back.toString() });
      return res.end();
    }
    if (url.pathname === '/token' && req.method === 'POST') {
      const f = await readBody(req);
      if (f.get('client_id') !== clientId) return send(res, 401, { error: 'invalid_client' });
      if (f.get('grant_type') === 'authorization_code') {
        const c = codes.get(f.get('code'));
        codes.delete(f.get('code'));                      // single use
        if (!c || c.redirect !== f.get('redirect_uri')) return send(res, 400, { error: 'invalid_grant' });
        const expect = b64url(createHash('sha256').update(f.get('code_verifier') || '').digest());
        if (expect !== c.challenge) { stats.pkceFailures++; return send(res, 400, { error: 'invalid_grant', error_description: 'PKCE verification failed' }); }
        stats.codeExchanges++;
        return send(res, 200, tokens(c.user), { 'Content-Type': 'application/json' });
      }
      if (f.get('grant_type') === 'refresh_token') {
        const user = refresh.get(f.get('refresh_token'));
        refresh.delete(f.get('refresh_token'));           // rotate
        if (!user) return send(res, 400, { error: 'invalid_grant' });
        stats.refreshes++;
        return send(res, 200, tokens(user), { 'Content-Type': 'application/json' });
      }
      return send(res, 400, { error: 'unsupported_grant_type' });
    }
    if (url.pathname === '/logout') {
      stats.logouts++;
      stats.lastLogoutQuery = Object.fromEntries(url.searchParams);
      const to = url.searchParams.get('post_logout_redirect_uri');
      if (!okRedirect(to)) return send(res, 400, 'invalid post_logout_redirect_uri', { 'Content-Type': 'text/plain' });
      res.writeHead(302, { Location: to });
      return res.end();
    }
    send(res, 404, 'not found', { 'Content-Type': 'text/plain' });
  });
  return new Promise((resolve) => server.listen(port, () => resolve({ issuer, stats, close: () => server.close() })));
}
