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
| **Tone** | **The Skillture identity ([skillture.club](https://skillture.club/)), used for a utility.** Cyan on ink, friendly rounded headings, plain controls. Calm and exact: the boarding-pass idea stays, in Skillture's colors and type. |
| **Memorable detail** | **The stamped pass.** A successful check-in is a ticket with a perforated edge and an ink stamp that lands ("HERE"). It is the one moment the product celebrates, and the mark (a pin with a check, drawn as one cyan line on ink) repeats it. On home, the **cyan card** (Start attendance) and the **ink band** (Check in) are the only two filled surfaces, so the two actions cannot be missed. |
| **Constraints** | No build step and no framework (plain ES modules). Works from 320 px wide to a projector. Right-to-left ready (logical CSS properties only). Self-hosted fonts (no third-party requests: privacy, and Iraq's networks). Every text pair passes WCAG AA in both themes. |
| **Primary screens** | Home (composer), Live session (QR), Check-in result (pass), Sign-up with a held check-in, Reports, Landing. |
| **Content states** | Every data view has loading (spinner in place), empty (dashed box that says what to do next), error (a banner with the next step), partial (list with "load more" or a review count) and success. Permission states (camera, location) explain why before the browser asks. |

## 2. Color

The palette is Skillture's, read from its stylesheet. **Cyan `#00CCCC` on ink `#050909`** is the signature: the logo,
the top bar, the highlighter and the Start card. Cyan cannot carry text on a light page (1.9:1 on white), so text-sized
brand color is the readable teal `#007A7A`, and the primary button is ink, as on skillture.club. Success green is
deliberately **not** the brand color, so "checked in" never looks like decoration.

| Token | Light | Dark | Use | Contrast |
|---|---|---|---|---|
| `--brand` | `#007A7A` | `#00CCCC` | Links, active tab, focus ring, icons | 5.17:1 on white · 10.0:1 on dark bg |
| `--brand-strong` | `#005F5F` | `#26E0E0` | Hover and pressed | |
| `--brand-soft` | `#EAF6F6` | `#092B2B` | Selected chips, context boxes, the active tab | brand on soft 4.7:1 / 7.5:1 |
| `--accent` / `--accent-ink` | `#00CCCC` / `#050909` | same | The Start card, the highlighter, the timer, the avatar | ink on cyan 10.0:1 |
| `--btn` / `--btn-ink` | `#050909` / `#FFFFFF` | `#00CCCC` / `#050909` | The primary button | 20.4:1 / 10.0:1 |
| `--ink` / `--on-ink` / `--on-ink-muted` | `#050909` / `#F1F8F8` / `#B0C4C4` | `#111B1B` / `#F1F8F8` / `#A9C0C0` | Top bar, Check in band, promise band | 18.6:1 / 11:1 on ink |
| `--bg` | `#F3FAFA` | `#050909` | App pages (the landing page is white, like skillture.club) | |
| `--surface` / `--surface-2` | `#FFFFFF` / `#E6F4F4` | `#0A1111` / `#141F1F` | Cards / quiet fills | |
| `--line` / `--line-strong` | `#CFE3E3` / `#6F9A9A` | `#233636` / `#4A6B6B` | Card borders / the edge of inputs, switches, secondary buttons (3:1) | |
| `--text` | `#050909` | `#F1F8F8` | Body text | 18.9:1 / 18.6:1 on bg |
| `--muted` | `#435B5B` | `#88A2A2` | Secondary text | 6.9:1 / 7.4:1 on bg; 6.5:1 on surface-2 |
| `--ok` / `--warn` / `--bad` | `#0B7A4B` / `#8A5300` / `#B42318` | `#2FD58A` / `#FBBF24` / `#FF4040` | Status text, stamps, dots | on their `-soft` fills: 4.7:1 lowest (ok, light; bad, dark) |
| `--on-status` | `#FFFFFF` | `#050909` | Text on a filled ok/bad button or toast | 5.4:1 lowest light · 5.8:1 lowest dark |

Rules: no gradients except the highlighter; no color carries meaning alone (status always has a word or an icon too);
the QR code is always on a white box (scanners need it); the Start card is cyan with ink text in both themes.

## 3. Type

**Readex Pro** for text and **Baloo Bhaijaan 2** for headings and big numbers: Skillture's pair. Both are SIL Open Font
License, self-hosted in `/fonts` as variable fonts (one file per script, every weight), and both have Arabic, so the
identity holds in both scripts. The Arabic files are only downloaded by a page that shows Arabic text.

| Role | Family | Size | Weight | Notes |
|---|---|---|---|---|
| Landing headline | Baloo Bhaijaan 2 | clamp(2.7rem → 4.6rem) | 700 | -0.02em tracking, line-height 1.04 |
| h1 (page) | Baloo Bhaijaan 2 | clamp(1.85rem → 2.4rem) | 700 | -0.015em; no tracking and line-height 1.3 in right-to-left |
| h2 / h3 | Baloo Bhaijaan 2 | 1.4rem / 1.15rem | 700 | |
| Body | Readex Pro | 16px / 1.55 | 400 | Never below 16px in inputs (no zoom on iOS) |
| Small / hint | Readex Pro | .875rem / .85rem | 400 | |
| Eyebrow | Readex Pro | .75rem, uppercase, +.08em | 600 | Section labels, pass labels |
| Numbers | Baloo Bhaijaan 2 | 2.8rem (counts) | 700 | `font-variant-numeric: tabular-nums`, -0.03em |

## 4. Space, shape, depth, motion, motifs

- **Spacing:** a 4-px base; layouts use 4, 8, 12, 16, 20/22, 28 (the `.stack-*` and `.row` helpers). Touch targets are at least 48 px (buttons 48, inputs 50, primary actions 56, small buttons 40).
- **Radius:** 16 px for cards and dialogs, 8 px for buttons, inputs and rows (Skillture's control radius), 6 px for chips inside rows. Pills only for chips and switches.
- **Depth:** borders first. One resting shadow (barely there) and one raised shadow for menus, dialogs, the pass, the Start card and the landing demo.
- **Motion:** only where it tells something: the timer bar (the code is about to change), the stamp landing (you are in), new names rising into the live list. All of it is switched off under `prefers-reduced-motion`.
- **The highlighter (`.mark`):** one phrase per screen, marked in cyan the way a pen would. On dark surfaces it is a solid cyan label with ink text.
- **Line motifs:** Skillture draws a column on a maze path; this product draws its own object in the same line: **a pin with a check, on a path**. `assets/pattern.svg` (sign-in pages, the promise band) and `assets/frieze.svg` (the footer rule) are masks, colored in CSS (`.pattern`, `.frieze`). The Skillture logo itself is not used.

## 5. Components

Defined once in [`app.css`](../apps/web/public/app.css), grouped by area:

| Area | Components |
|---|---|
| Identity | `.mark` (highlighter), `.on-ink` (ink band), `.pattern`, `.frieze` |
| Shell | `.topbar` (ink) with `.brand` and the account `.menu`; `.tabbar` (phones); `.notice` (one reminder strip) |
| Surfaces | `.card` (`.flat`, `.soft`, `.hero`), `.list` + `.item`, `.empty`, `.page-head`, `.tabs`, `.kv` |
| Actions | `.btn` (`.secondary`, `.ghost`, `.danger`, `.ok`, `.big`, `.small`), `.linkish`, `.chip-btn`, `.seg`, `.switch` |
| Feedback | `.chip` (`.ok/.warn/.bad/.brand`), `.banner`, `.toast`, `dialog`, `.spinner`, `.big-icon` |
| Home | `.composer` (the cyan card: title + Start + Strict switch), `.checkin-cta` (the ink band), `.org-grid` / `.org-card` |
| Live session | `.qr-card`, `.qr-box`, `.timer`, `.stats`, `.who`, `.dot`; `body.present` for the projector |
| Check-in | `.steps`, `.camera`, `.result`, and the **pass**: `.pass`, `.pass-top`, `.pass-cut`, `.pass-bottom`, `.stamp` |
| Reports | `.table-wrap`, `table.table`, `.rate` (bar + percentage, `.low` under 75%) |
| Account | `.auth`, `.auth-card`, `.auth-context` (what you are signing up for), `.auth-foot` |
| Landing | `.land-hero`, `.demo` (a real rotating QR and a live feed), `.land-section`, the grids, `.promise`, `.land-foot` |

## 6. Generic-pattern check (ECC "AI slop" list)

| Pattern | Status |
|---|---|
| Gradients on everything / purple-to-blue | None. Flat color; the only gradient is the hard-edged highlighter. |
| Glass-morphism without purpose | None. The top bar is solid ink. |
| Rounded corners on everything | Three radii by role; tables and the projector QR stay square-ish. |
| Scroll animations | None. Motion is tied to state (timer, stamp, arrivals). |
| Centered hero text over a stock gradient | Left-aligned hero next to a working product demo (a real rotating code). |
| Sans-serif with no personality | Baloo Bhaijaan 2 headings over Readex Pro, both with Arabic. |
| Cards inside cards, icon-in-a-circle feature grids | Sections use plain grids; icons sit inline with text, not in decorative circles. |
| Emoji as icons | None; one consistent stroke icon set (`ui.js`). |

## 7. Audit (10 dimensions)

Re-checked after the move to the Skillture identity: all 26 renders, keyboard focus and reduced motion pass. Scored after the rendered check (`apps/web/e2e/design.mjs`: phone 390×844 and desktop 1440×900, light and dark;
screenshots and the measured report in [`design-evidence/`](design-evidence/), accepted by the ECC
`check-evidence.js`). The first run found four real problems, fixed before scoring: a 20-px-tall "Send the link again"
link, report filters 27 px wider than a phone, the amber words of the promise band at 1.14:1 on the dark-mode teal
(now ink with an amber underline), and white text on light status colors in dark mode (the `--on-status` token).
Those four fixes carry over (the promise band is now ink, so its highlighted words are ink on cyan at 10:1).
Each score names what holds it back.

| Dimension | Score | Notes |
|---|---|---|
| Color consistency | 9 | Every color is a token; the only literals are the white QR box and the white field inside the cyan Start card. |
| Typography hierarchy | 9 | Two families by role (headings and numbers / text), five sizes; tabular figures for every number. |
| Spacing rhythm | 8 | A 4-px scale; a few component paddings (22 px) sit between steps on purpose. |
| Component consistency | 9 | One button, one row and one card style across all screens. |
| Responsive behavior | 9 | Checked at 320, 390, 768 and 1440 px; no horizontal scroll; tab bar on phones, top navigation on desktop. |
| Dark mode | 9 | Full token set, checked for contrast; the QR stays on white. |
| Animation | 9 | Three purposeful motions, all disabled under reduced motion. |
| Accessibility | 9 | AA contrast in both themes, visible focus, 44-px targets, labelled controls, live regions for check-in results. |
| Information density | 8 | Home shows two actions; reports are dense by design (it is a table). |
| Polish | 8 | Loading, empty, error and success states on every data view. Arabic type is still to come. |
