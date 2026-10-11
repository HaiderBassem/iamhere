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
  js/              router, API client, accounts, translation (i18n.js), views/
e2e/               end-to-end tests (Playwright); not served
```

## Run it
```sh
cd apps/api
IAH_MAIL_DIR=/tmp/iah-mail IAH_WEB_DIR=../web/public IAH_DATABASE_URL='postgres://...' go run ./cmd/api
# open http://localhost:8080 and create an account (emailed links land in /tmp/iah-mail)
```
Camera and location need HTTPS, except on `localhost`. Production: Caddy serves `public/` (see `infra/`).

## Re-skin (for example to a new brand)
1. `public/theme.css`: change the variables under `:root` (and the dark-mode block). Nothing else holds a color.
2. `public/brand.js`: product name and tagline. `public/assets/logo.svg`: the logo (also the favicon and app icon).
3. `public/manifest.webmanifest` and the `theme-color` meta tag in `public/index.html`: the brand color on phones.

## Languages
All text goes through `t()` / `tn()` in `public/js/i18n.js` (gettext style: the English text is the key, plurals follow
CLDR rules, dates and numbers use the browser's conventions, `dir="rtl"` is set for right-to-left languages). The app
ships in English. To add a language, add a dictionary from English to it in `CATALOGS` (and the language to `RTL` if it
is written right to left); the browser's preferred language is chosen automatically. Messages that come from the API
are English for now.

## What it does
- **Everyone**: one account (name, email, password) for every organization, class, workshop and event. A start page
  for visitors; sign up and sign in; "forgot password" by email; a short welcome that sets up an organization for
  people who run sessions; one attendance history across all organizations.
- **Attendees**: scan the QR code on the screen with the phone's own camera (it opens the app) or with the in-app
  scanner. *Normal* sessions check in at once. *Strict* sessions ask for location and a photo of the screen with the code, taken by itself when the code is
  in view; it must show the current code and the room (see `docs/VISION.md`).
- **Organizers**: create an organization with just a name, tap *Start & show QR*, watch the count grow live, show the QR
  full screen on a projector, approve or reject flagged check-ins, download the list.
- **Admins**: invite people with a link, save places with 3–4 reference photos (and test them from a seat), set the default mode.

## Test
```sh
cd apps/web && npm ci && npx playwright install chromium
BASE_URL=http://localhost:8080 npm run e2e     # against a running API started as above
```
Every person in the test creates a real account through the sign-up page. The API must be started with
`IAH_MAIL_DIR` and the test given the same folder as `MAIL_DIR` (it follows the emailed confirmation and reset links).
The strict-mode part needs the vision service (`apps/vision`, see `docs/VISION.md`) running and the API started
with `IAH_VISION_URL=http://localhost:8090`; the fake camera feeds are generated with its Python
(`VISION_PYTHON=/path/to/python`, default `python3`).
The tests drive real Chromium: Chromium's fake camera plays generated "rooms", GPS is mocked per person, and the QR is
decoded from the organizer's screen the way a phone would. Screenshots are written to `$SHOTS`.

## Notes
- Sign-in is OpenID Connect (authorization code + PKCE, no client secret, no cookies); tokens live in `sessionStorage`.
- The interface is English only for now. Layout uses logical CSS properties, so adding right-to-left languages needs
  translations, not a redesign.
