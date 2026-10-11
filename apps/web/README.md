# I Am Here: web app

Plain HTML, CSS and ES modules. **No framework, no build step, no runtime dependencies**: the browser loads
`public/` as is. Two small libraries are vendored in `public/vendor/` (QR drawing: `qrcode-generator`, MIT; QR reading:
`jsQR`, Apache-2.0).

```
public/            everything the browser loads (this is the only folder that is served)
  theme.css        colors, fonts, radii  ← re-skin here
  brand.js         name, tagline, logo path  ← and here
  assets/logo.svg  the logo  ← and here
  app.css          components (use the theme variables)
  js/              router, API client, sign-in, views/
e2e/               end-to-end tests (Playwright); not served
```

## Run it
```sh
cd apps/api
IAH_DEV_AUTH=1 IAH_WEB_DIR=../web/public IAH_DATABASE_URL='postgres://...' go run ./cmd/api
# open http://localhost:8080 (dev sign-in asks only for a name)
```
Camera and location need HTTPS, except on `localhost`. Production: Caddy serves `public/` (see `infra/`).

## Re-skin (for example to a new brand)
1. `public/theme.css`: change the variables under `:root` (and the dark-mode block). Nothing else holds a color.
2. `public/brand.js`: product name and tagline. `public/assets/logo.svg`: the logo (also the favicon and app icon).
3. `public/manifest.webmanifest` and the `theme-color` meta tag in `public/index.html`: the brand color on phones.

## What it does
- **Attendees**: scan the QR code on the screen with the phone's own camera (it opens the app) or with the in-app
  scanner. *Normal* sessions check in at once. *Strict* sessions ask for location and a live photo of the room.
- **Organizers**: create an organization with just a name, tap *Start & show QR*, watch the count grow live, show the QR
  full screen on a projector, approve or reject flagged check-ins, download the list.
- **Admins**: invite people with a link, save places with reference photos, set the default mode.

## Test
```sh
cd apps/web && npm ci && npx playwright install chromium
BASE_URL=http://localhost:8080 npm run e2e     # against a running API started as above
```
Sign-in (OpenID Connect) has its own test, which starts a mock identity provider and the API in production mode:
```sh
API_BIN=/path/to/built/api DATABASE_URL='postgres://...' npm run e2e:oidc
```
The tests drive real Chromium: Chromium's fake camera plays generated "rooms", GPS is mocked per person, and the QR is
decoded from the organizer's screen the way a phone would. Screenshots are written to `$SHOTS`.

## Notes
- Sign-in is OpenID Connect (authorization code + PKCE, no client secret, no cookies); tokens live in `sessionStorage`.
- The interface is English only for now. Layout uses logical CSS properties, so adding right-to-left languages needs
  translations, not a redesign.
