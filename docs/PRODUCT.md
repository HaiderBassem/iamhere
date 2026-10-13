# I Am Here — Product brief

*One Identity. Every Attendance. Everywhere.*

Written with the ECC `product-lens` method (diagnostic, journey audit, prioritisation) from the business concept.
Every recommendation ends in something built, or in a numbered item on the roadmap below.

## 1. Diagnostic

| Question | Answer |
|---|---|
| **Who is it for?** | The **organizer**: a lecturer, trainer, HR officer or event host who takes attendance every week and has no technical help. The **attendee**: a student, employee or guest who has 20 seconds at the door, a phone and no wish to install anything. |
| **What is the pain?** | Paper lists take 5–15 minutes of a 60-minute lecture, are signed for absent friends, and end up in a drawer. Static QR codes get photographed and shared in group chats within seconds. |
| **What do they do today?** | Paper sheets, a Google Form behind a static QR code (shareable, no identity), or calling names out loud. |
| **Why now?** | Every attendee already carries a camera and GPS. Phone browsers open QR links natively, so no app is needed. The verification that used to need hardware (card readers, fingerprint terminals) can run in software. |
| **10-star version** | The organizer says "start" and walks away. Every honest attendee is in within one scan. Nobody can be checked in from outside the room. Reports reach the registrar on their own. |
| **MVP (this product today)** | A title, then a rotating code. Normal mode (a scan is enough) and Strict mode (location + a photo of the screen, recognised against the room). Live list, review of doubtful check-ins, CSV export, teams. |
| **Anti-goals** | No hardware. No native app. No timetables or LMS in the core. No feature that adds a step for the attendee in Normal mode. |
| **Is it working?** | **Time to first session** for a new organizer (target: under 60 s from landing). **Scan-to-checked-in** for an attendee (target: under 5 s signed in, under 30 s without an account). **Share of check-ins needing review** in Strict mode (target: under 5%). **Weekly active organizers**, the growth metric. |

**Go.** The problem is frequent (weekly), painful (minutes lost every class) and poorly served (forms and paper).
The risk is not demand. It is friction: every extra step loses organizers at the first try and attendees at the door.

## 2. Journey audit (time to value)

Measured in the real app with the browser end-to-end setup: actions = taps + fields to fill.

### Organizer, first time: landing → code on screen

| | Before this round | After |
|---|---|---|
| Screens | Landing → sign up → "what brings you here?" → name your organization → organization page → live code | Landing → sign up → **home: title + Start** → live code |
| Actions | 9 (3 fields + 6 taps) | **6** (3 fields + 3 taps; the title is pre-filled and can be kept) |
| Decisions to understand | Organization, kind of organization, mode, place | **None.** A personal workspace is created on the first Start. Strict mode is one switch, off by default. |

### Organizer, every other time

Before: home → organization → Start: 3 taps. **After: Start on home: 1 tap** (2 with a typed title).

### Attendee without an account (the most common first contact)

| | Before | After |
|---|---|---|
| Path | Scan → sign up → "the code moved on, scan once more" → scan again → checked in | Scan → sign up → **checked in** |
| Actions | 3 fields + 3 taps + a second scan | **3 fields + 1 tap** |
| Why it changed | The 5-second code expired during sign-up | The scan is held for 5 minutes while the account is created (see below) |

### Attendee with an account

Scan → checked in: no change (one scan, about 1 s). That path was already as short as it can be.

## 3. Assumptions challenged

1. **"Enter only a title."** Even a title is a decision. → The field is **pre-filled** ("Session – Sun, Oct 11") and Start works without typing. A real title is still one tap away, because reports read better with one.
2. **"Organizers create an organization."** A lecturer does not think in organizations. → **A personal workspace is created on the first Start**, named after the person. Organizations (teams, places, shared reports) stay available and are offered when they are needed (inviting someone, saving a place).
3. **"No app means no account."** Trustworthy attendance needs to know who is checking in. → One account per person (name, email, password), created once, in about 20 seconds, **during the first check-in** without losing it. After that it is a single scan forever, in any organization.
4. **"GPS proves presence."** Indoors, phone GPS is accurate to 10–50 m, cannot tell floors apart, and can be faked by a determined student. → GPS is one signal, never the only one. Strict mode adds a photo of the screen that must show the **current** code and **this** room. Doubtful cases go to the organizer, who has the last word; nobody is rejected by a machine for a bad GPS fix.
5. **"One QR for thousands."** True for the code. But a lecture hall behind one school network looks like one internet address. → Rate limits are sized for a class behind one address, and abuse is limited per account. Tested: 300 concurrent check-ins on one code.
6. **"Verification can be fully automatic."** It cannot be perfect without hardware. A forwarded code is useless after seconds, but someone in the room can still stream the screen. → Be honest in the product: "verified" means the evidence is consistent with presence. Review is a first-class, two-tap action.
7. **"Paid plans from day one."** For a new brand in a market used to paper, the first barrier is trust, not price. → **Free for now** (the owner's decision). Usage limits are not built into the product. The pricing structure below is kept for later, so nothing built now blocks it.

## 4. Missing business requirements

| # | Requirement | Why it matters | Status |
|---|---|---|---|
| R1 | **Attendance report per person over many sessions** (rate per attendee, export) | The registrar's question is "who missed more than 25%?", not "who came today" | **Built this round** (organization → Reports) |
| R2 | **Add someone by hand** (dead phone, no phone) | Happens every week; without it the organizer goes back to paper | **Built this round** (live session → "Add someone" by email) |
| R3 | No re-scan after signing up | Lost first check-ins = lost trust at the door | **Built this round** (new accounts; someone signing in to an existing account scans once more) |
| R4 | Arabic and Kurdish (Sorani), right to left | The launch market is Iraq | Ready in the code (`t()`, RTL); **needs translations** |
| R5 | Privacy notice and terms | Location and photos are sensitive; institutions will ask before adopting | Privacy page **built this round** (what is stored and for how long); terms need a lawyer |
| R6 | Recurring sessions / courses ("Software Engineering, every Sunday") | Reports per course instead of per organization | Roadmap: R1 groups by session title meanwhile |
| R7 | Guests without accounts at public events | Conferences may not want sign-ups | Roadmap: an organizer option for Normal mode only (name, no account, marked as "guest") |
| R8 | Plans and billing | Revenue | Deferred (free for now); structure below |
| R9 | Institution features: single sign-on (Google, Microsoft), departments, LMS export | Needed to sell to universities at scale | Roadmap |
| R10 | Offline fallback for the organizer's screen | Lecture halls with bad internet | Roadmap: the code is computed on the server; a cached offline mode needs a signed code computed in the browser |

## 5. Prioritised roadmap (ICE: impact × confidence ÷ effort, 1–5 each)

| Item | I | C | E | ICE | When |
|---|---|---|---|---|---|
| One-title start from home, personal workspace (R-journey) | 5 | 5 | 2 | 12.5 | **Now** |
| Keep the scan through sign-up (R3) | 5 | 5 | 2 | 12.5 | **Now** |
| Attendance report per person (R1) | 5 | 4 | 2 | 10 | **Now** |
| Add someone by hand (R2) | 4 | 5 | 2 | 10 | **Now** |
| Privacy page (R5) | 3 | 5 | 1 | 15 | **Now** |
| Arabic + Kurdish translations (R4) | 5 | 5 | 3 | 8.3 | Next (needs a native reviewer) |
| Courses / recurring sessions (R6) | 4 | 4 | 3 | 5.3 | Next |
| Sign in with Google / Microsoft (R9) | 4 | 3 | 3 | 4 | Later |
| Guests without accounts (R7) | 3 | 3 | 2 | 4.5 | Later, behind an organizer option |
| Plans & billing (R8) | 3 | 3 | 4 | 2.3 | When usage shows who pays |

## 6. Monetisation, for later

The structure from the concept holds, with one change: limits must never touch the attendee. An attendee is never
refused because of an organizer's plan; the organizer sees the upgrade prompt, after the session.

| Plan | For | Natural limit (what grows with value) |
|---|---|---|
| Free | Individuals, small events | Attendance history kept 90 days; one organizer |
| Professional | Lecturers, trainers | Unlimited history, reports, Strict mode with room recognition |
| Organization | Schools, companies | Several organizers, departments, shared places, analytics |
| Enterprise | Universities, ministries | Single sign-on, integrations, branding, self-hosting, support |

In Iraq, institutions pay by invoice or bank transfer more often than by card. Start with manual activation of
paid plans after payment, and add an online provider (ZainCash, Qi Card or FastPay locally; Stripe abroad) when
the volume justifies it.

## 7. Principles the product is held to

1. **The first screen is the product.** A signed-in person sees "Start attendance" and "Check in", nothing to read first.
2. **Defaults over questions.** Every question the product can answer itself (title, workspace, mode, place) is answered, and can be changed after.
3. **No dead ends.** Every error says what happened and offers the next step (scan again, take the photo again, ask the organizer).
4. **The attendee's path never grows.** New features add steps for the organizer, never for the attendee in Normal mode.
5. **Honest verification.** The product says "checked in" only when the evidence supports it, and "your organizer will review" otherwise.
