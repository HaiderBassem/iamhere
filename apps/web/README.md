# I Am Here: web app

Plain HTML, CSS and ES modules. **No framework, no build step, no runtime dependencies**: the browser loads
`public/` as is. Two small libraries are vendored in `public/vendor/` (QR drawing: `qrcode-generator`, MIT; QR reading:
`jsQR`, Apache-2.0).

```
public/            everything the browser loads (this is the only folder that is served)
  theme.css        colors, fonts, radii (tokens; same values in design-tokens.json)  ← re-skin here
  fonts/           IBM Plex Sans (OFL), self-hosted
  design-preview.html  every token and component, rendered
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
  for visitors; sign up and sign in; "forgot password" by email; one attendance history across all organizations;
  a privacy page (`#/privacy`). Home is two actions: **Start attendance** (the title is already filled in: one tap) and
  **Check in**, then sessions that are still live.
- **Attendees**: scan the QR code on the screen with the phone's own camera (it opens the app) or with the in-app
  scanner. Without an account, the scan is held for 5 minutes while they sign up, so they are checked in without
  scanning again. Success is a pass with a "Here" stamp. *Normal* sessions check in at once. *Strict* sessions ask for location and a photo of the screen with the code, taken by itself when the code is
  in view; it must show the current code and the room (see `docs/VISION.md`).
- **Organizers**: press Start on home (the first Start creates a personal workspace named after them; no setup), watch
  the count grow live, show the QR full screen on a projector, **Add someone** by email when a phone fails, approve or
  reject flagged check-ins, download the list. **Reports**: attendance per person over a period, by title, with the
  people under 75% counted, as a table or a spreadsheet. Organizations (teams, places, settings) are optional.
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

Design check (the rendered finish gate of `docs/DESIGN.md`): `BASE_URL=http://localhost:8080 OUT=../../docs/design-evidence
node e2e/design.mjs` renders the main screens at 390×844 and 1440×900 in light and dark, produces every content state
(loading, empty, error, partial, success, permission) and measures overflow, contrast, labels, target sizes, keyboard
focus and reduced motion. It writes the screenshots and `report.json`.

## Notes
- Sign-in uses the built-in accounts: an opaque bearer token in `localStorage`, sent in the `Authorization` header (no cookies).
- The interface is English only for now. Layout uses logical CSS properties, so adding right-to-left languages needs
  translations, not a redesign.
