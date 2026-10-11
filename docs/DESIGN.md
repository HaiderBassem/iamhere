# I Am Here — Design system

The visual identity and the interface rules, written with the ECC `design-system` and
`frontend-design-direction` skills. The tokens live in [`apps/web/public/theme.css`](../apps/web/public/theme.css)
(used by the app) and [`apps/web/public/design-tokens.json`](../apps/web/public/design-tokens.json) (the same values,
for other tools). Open [`/design-preview.html`](../apps/web/public/design-preview.html) in the running app to see every
token and component in light and dark mode. The voice and the brand story are in [`BRAND.md`](BRAND.md).

## 1. Design contract

| | |
|---|---|
| **Purpose** | Take attendance in one scan. The organizer starts in one tap; the attendee is checked in with one scan. |
| **Audience** | A lecturer in front of 60 people (often on a projector), and an attendee at a door on a cheap phone, often in bright light, often on 3G. |
| **Tone** | **Civic utility, like a boarding pass.** Calm, exact, official enough to trust, never corporate. Not "SaaS dashboard", not "playful startup". |
| **Memorable detail** | **The stamped pass.** A successful check-in is a ticket with a perforated edge and an ink stamp that lands ("HERE"). It is the one moment the product celebrates, and the mark (a pin with a check) repeats it. |
| **Constraints** | No build step and no framework (plain ES modules). Works from 320 px wide to a projector. Right-to-left ready (logical CSS properties only). Self-hosted fonts (no third-party requests: privacy, and Iraq's networks). Every text pair passes WCAG AA in both themes. |
| **Primary screens** | Home (composer), Live session (QR), Check-in result (pass), Sign-up with a held check-in, Reports, Landing. |
| **Content states** | Every data view has loading (spinner in place), empty (dashed box that says what to do next), error (a banner with the next step), partial (list with "load more" or a review count) and success. Permission states (camera, location) explain why before the browser asks. |

## 2. Color

Petrol teal is the brand: it reads as institutional and trustworthy (like transit and civic signage) without the
blue of every SaaS product, and it keeps its character in dark mode. Amber is the "you are here" color, used for
one thing per screen (the timer, the spot under the pin, the active tab underline). Success green is deliberately
**not** the brand color, so "checked in" never looks like decoration.

| Token | Light | Dark | Use | Contrast (text on it / it on bg) |
|---|---|---|---|---|
| `--brand` | `#0E6B73` | `#5CC6C9` | Primary buttons, links, focus | white on brand 6.23:1 · dark ink on brand 8.21:1 |
| `--brand-strong` | `#0A545B` | `#86D7D9` | Hover and pressed | |
| `--brand-soft` | `#E1EFEF` | `#123236` | Selected chips, context boxes | brand on soft 5.28:1 / 6.76:1 |
| `--accent` | `#F2A516` | `#F5B83D` | Timer, the "here" spot, one highlight | ink on accent 7.92:1 (never text on bg) |
| `--bg` | `#F5F3EE` | `#0C1517` | Page: warm paper, not clinical white | |
| `--surface` / `--surface-2` | `#FFFFFF` / `#EDEAE2` | `#132124` / `#1A2C30` | Cards / quiet fills | |
| `--line` | `#DFDACE` | `#26393F` | Borders (they do the work shadows usually do) | |
| `--text` | `#10222A` | `#E7EEEE` | Body text | 14.75:1 / 15.73:1 on bg |
| `--muted` | `#4E5E63` | `#9FB2B4` | Secondary text | 6.10:1 / 8.37:1 on bg; 5.63:1 on surface-2 |
| `--ok` / `--warn` / `--bad` | `#1C7A47` / `#9A5B00` / `#B5332A` | `#4CC982` / `#F0B657` / `#FF8A7D` | Status text, stamps, dots | on their `-soft` fills: 4.62:1 lowest (ok, light) |

| `--on-status` | `#FFFFFF` | `#0C1517` | Text on a filled ok/bad button or toast | 5.35:1 lowest light · 8.09:1 lowest dark |

Rules: no gradients; no color carries meaning alone (status always has a word or an icon too); the projector view
uses the same tokens (a white QR box on any theme, because scanners need it).

## 3. Type

**IBM Plex Sans** (SIL Open Font License, self-hosted in `/fonts`, Latin subset, weights 400/500/600/700). It is
engineered and neutral but not anonymous, has tabular figures for counts and times, and has an Arabic family (IBM
Plex Sans Arabic) for the Iraq roadmap, so the identity holds in both scripts.

| Role | Size | Weight | Notes |
|---|---|---|---|
| Landing headline | clamp(2.4rem → 4.1rem) | 700 | -0.035em tracking, line-height 1.02 |
| h1 (page) | clamp(1.55rem → 2rem) | 650 | |
| h2 / h3 | 1.2rem / 1rem | 650 | |
| Body | 16px / 1.55 | 400 | Never below 16px in inputs (no zoom on iOS) |
| Small / hint | .875rem / .85rem | 400 | |
| Eyebrow | .75rem, uppercase, +.08em | 600 | Section labels, pass labels |
| Numbers | — | 650 | `font-variant-numeric: tabular-nums` for counts, timers, rates |

## 4. Space, shape, depth, motion

- **Spacing:** a 4-px base; layouts use 4, 8, 12, 16, 20/22, 28 (the `.stack-*` and `.row` helpers). Touch targets are at least 44 px (buttons 44, inputs 48, primary actions 54).
- **Radius:** 14 px for cards and dialogs, 10 px for buttons, inputs and rows, 6 px for chips inside rows. Crisp, not bubbly. Pills only for chips and switches.
- **Depth:** borders first. One resting shadow (barely there) and one raised shadow for menus, dialogs, the pass and the landing demo.
- **Motion:** only where it tells something: the timer bar (the code is about to change), the stamp landing (you are in), new names rising into the live list. All of it is switched off under `prefers-reduced-motion`.

## 5. Components

Defined once in [`app.css`](../apps/web/public/app.css), grouped by area:

| Area | Components |
|---|---|
| Shell | `.topbar` with `.brand` and the account `.menu`; `.tabbar` (phones); `.notice` (one reminder strip) |
| Surfaces | `.card` (`.flat`, `.soft`, `.hero`), `.list` + `.item`, `.empty`, `.page-head`, `.tabs`, `.kv` |
| Actions | `.btn` (`.secondary`, `.ghost`, `.danger`, `.ok`, `.big`, `.small`), `.linkish`, `.chip-btn`, `.seg`, `.switch` |
| Feedback | `.chip` (`.ok/.warn/.bad/.brand`), `.banner`, `.toast`, `dialog`, `.spinner`, `.big-icon` |
| Home | `.composer` (title + Start + Strict switch), `.checkin-cta`, `.org-grid` / `.org-card` |
| Live session | `.qr-card`, `.qr-box`, `.timer`, `.stats`, `.who`, `.dot`; `body.present` for the projector |
| Check-in | `.steps`, `.camera`, `.result`, and the **pass**: `.pass`, `.pass-top`, `.pass-cut`, `.pass-bottom`, `.stamp` |
| Reports | `.table-wrap`, `table.table`, `.rate` (bar + percentage, `.low` under 75%) |
| Account | `.auth`, `.auth-card`, `.auth-context` (what you are signing up for), `.auth-foot` |
| Landing | `.land-hero`, `.demo` (a real rotating QR and a live feed), `.land-section`, the grids, `.promise`, `.land-foot` |

## 6. Generic-pattern check (ECC "AI slop" list)

| Pattern | Status |
|---|---|
| Gradients on everything / purple-to-blue | None. Flat brand color; the only "fill" moment is the promise band. |
| Glass-morphism without purpose | The sticky top bar blurs what scrolls under it (legibility); nothing else. |
| Rounded corners on everything | Three radii by role; tables and the projector QR stay square-ish. |
| Scroll animations | None. Motion is tied to state (timer, stamp, arrivals). |
| Centered hero text over a stock gradient | Left-aligned hero next to a working product demo (a real rotating code). |
| Sans-serif with no personality | IBM Plex Sans with tabular figures, chosen for its Arabic family. |
| Cards inside cards, icon-in-a-circle feature grids | Sections use plain grids; icons sit inline with text, not in decorative circles. |
| Emoji as icons | None; one consistent stroke icon set (`ui.js`). |

## 7. Audit (10 dimensions)

Scored after the rendered check (`apps/web/e2e/design.mjs`: phone 390×844 and desktop 1440×900, light and dark;
screenshots and the measured report in [`design-evidence/`](design-evidence/), accepted by the ECC
`check-evidence.js`). The first run found four real problems, fixed before scoring: a 20-px-tall "Send the link again"
link, report filters 27 px wider than a phone, the amber words of the promise band at 1.14:1 on the dark-mode teal
(now ink with an amber underline), and white text on light status colors in dark mode (the `--on-status` token).
Each score names what holds it back.

| Dimension | Score | Notes |
|---|---|---|
| Color consistency | 9 | Every color is a token; the only literals are the white QR box and white text on status buttons. |
| Typography hierarchy | 9 | One family, five sizes, weights by role; tabular figures for every number. |
| Spacing rhythm | 8 | A 4-px scale; a few component paddings (22 px) sit between steps on purpose. |
| Component consistency | 9 | One button, one row and one card style across all screens. |
| Responsive behavior | 9 | Checked at 320, 390, 768 and 1440 px; no horizontal scroll; tab bar on phones, top navigation on desktop. |
| Dark mode | 9 | Full token set, checked for contrast; the QR stays on white. |
| Animation | 9 | Three purposeful motions, all disabled under reduced motion. |
| Accessibility | 9 | AA contrast in both themes, visible focus, 44-px targets, labelled controls, live regions for check-in results. |
| Information density | 8 | Home shows two actions; reports are dense by design (it is a table). |
| Polish | 8 | Loading, empty, error and success states on every data view. Arabic type is still to come. |
