# I Am Here — Backend

Go modular monolith (`apps/api`): chi, pgx, PostgreSQL 17 + PostGIS. No Redis, no queue, no paid services.

## Run and test

```sh
docker run -d --name iah-pg -e POSTGRES_PASSWORD=dev -p 55432:5432 imresamu/postgis:17-3.5-bookworm   # arm64 dev
cd apps/api && go test -race ./...                         # integration tests create throwaway DBs
# other Postgres (needs the PostGIS extension): TEST_PG_ADMIN_URL='postgres://postgres:pw@host:5432/postgres?sslmode=disable' go test ./...
IAH_MAIL_DIR=/tmp/iah-mail IAH_WEB_DIR=../web/public IAH_DATABASE_URL='postgres://postgres:dev@localhost:55432/iamhere?sslmode=disable' go run ./cmd/api
# then open http://localhost:8080 and create an account; emails (confirm, reset) land in /tmp/iah-mail as .eml files
```
For tests and scripts, `IAH_DEV_AUTH=1` also accepts `Authorization: Bearer dev:<subject>:<Full Name>` (refused when
`IAH_ENV=production`; the name may be percent-encoded, as browsers only send Latin-1 in headers).
Production: `infra/` (Compose + Caddy). Copy `.env.example` to `.env`, then `docker compose up -d --build`.
Configuration is environment-only (`IAH_*`, see `internal/config`): `IAH_ENV` (`development`|`production`, anything else
refuses to start), `IAH_LISTEN`, `IAH_DATABASE_URL`, `IAH_PUBLIC_URL` (the web app's https address, for emailed links;
required in production), `IAH_SMTP_URL` + `IAH_MAIL_FROM` (account emails; optional), `IAH_MAIL_DIR` (development: write
emails to a folder), `IAH_TRUST_PROXY` (client IP from the proxy's `X-Forwarded-For`, for rate limits), `IAH_DEV_AUTH`,
`IAH_QR_GRACE_SLOTS`, `IAH_VISION_URL` (strict mode's photo check, see `docs/VISION.md`), `IAH_WEB_DIR` (serve the web app; production uses Caddy), `IAH_CORS_ORIGINS` (comma-separated exact origins; only needed when the web app is on a different
origin than the API). `api healthcheck` probes `/readyz` (used by the Compose healthcheck; the image has no shell/curl).
`api reset-link <email>` prints a one-time password reset link (for servers without email:
`docker compose exec api /api reset-link someone@example.org`).

## Layout
`internal/qr` rotating tokens and check-in tickets · `internal/vision` client of the vision service (`apps/vision`) · `internal/attendance` pure decision policy · `internal/app` authorization,
tenancy, transactions · `internal/httpapi` routes, rate limits, SSE · `internal/db` migrations · `internal/auth` password hashing, tokens, dev identities · `internal/mail` SMTP / mail folder.

## API (all under `/v1`, bearer token, JSON; errors `{error, message}`)
| | |
|---|---|
| `POST /auth/signup` `{name, email, password}`, `POST /auth/signin` `{email, password}` (no session needed) | create an account / sign in; returns `{token, user}`. 400 `invalid_name`/`invalid_email`/`weak_password`, 409 `email_taken`, 401 `wrong_credentials` (same answer for an unknown email), 429 `too_many_attempts` |
| `POST /auth/password/forgot` `{email}`, `POST /auth/password/reset` `{token, password}`, `POST /auth/email/confirm` `{token}` (no session needed) | emailed one-time links: reset (1 h; signs out every other device, signs this one in) and address confirmation (3 days); 410 `link_expired` |
| `GET /auth/account`, `POST /auth/signout`, `POST /auth/signout-others`, `POST /auth/password` `{current, new}`, `POST /auth/email/resend` | the signed-in account: email and whether it is confirmed, sign out here / everywhere else, change password |
| `GET/PATCH/DELETE /me`, `GET /me/attendance` | profile (name, E.164 phone), history across every organization, account deletion (anonymizes records, deletes sign-in details) |
| `POST /checkins/scan` `{token}` | validates a fresh QR, returns a short-lived `ticket` plus what is still needed (`needs_gps`, `needs_photo`) and whether the person is `already_recorded` |
| `POST /checkins` `{ticket \| token, gps?, photo?, photo_hint?}` | finishes a check-in; `photo` is a base64 JPEG/PNG of the screen showing the code (data URL ok, ≤ 1.5 MB), `photo_hint` the code's four corners as the browser saw them. Idempotent per (session, user); 410 `qr_expired`/`ticket_expired`/`session_ended`; 400 `photo_unusable` (retake; message says why); 429 rate limit |
| `GET /public-config` (no auth) | `{auth: "local", email}`: built-in accounts, and whether emailed links (reset, confirm) work on this server |
| `GET/POST /orgs`, `GET/PUT /orgs/{o}/settings`, `GET /members`, `PUT/DELETE /members/{u}` | orgs, policy, RBAC |
| `GET/POST /orgs/{o}/invites`, `DELETE .../{i}`, `POST /invites/accept` `{token}` | single-use join links (token shown once, only its hash is stored; 7 d default, 30 d max); owners are never created by invite; only owners mint admin invites |
| `GET/POST /orgs/{o}/venues`, `DELETE .../{v}` | registered places `{name, lat, lon, radius_m}` with `reference_count`; admin/owner manage, members read |
| `GET/POST /orgs/{o}/venues/{v}/references`, `DELETE .../{r}` | a place's reference photos for strict mode (max 12; checked for light, sharpness and detail on upload; 409 `reference_limit`). Admin/owner manage, organizers list |
| `POST /orgs/{o}/venues/{v}/test` `{photo}` | "test from a seat": `{matched, score, threshold, references}` for a photo against the place's photos |
| `GET /orgs/{o}/venues/{v}/photo-stats`, `DELETE .../learned` | how the photo check did at the place (30 days: photos checked, recognised, code read; learned views), and forgetting the learned views |
| `GET/POST /orgs/{o}/sessions/{s}/references`, `DELETE .../{r}` | the session's "today's photo" (max 4; the creator or a manager, while the session is live; deleted 30 days after the session ends) |
| `GET /orgs/{o}/references/{r}/image` | a reference photo's image (organizers only) |
| `POST/GET /orgs/{o}/sessions` (`Idempotency-Key` header), `GET .../{s}`, `/qr`, `POST /close` | creation takes a `title` and optionally `mode` (`standard`\|`strict`; default = organization setting), `anchor` or `venue_id` |
| `GET .../{s}/attendance` (rows carry `reasons`), `/attendance.csv`, `POST .../attendance/manual`, `POST /orgs/{o}/attendance/{r}/review` | records, export, fallback, review |
| `GET .../{s}/events` | SSE live counts (use fetch streaming; EventSource cannot send Authorization) |

## Key decisions
1. **Two modes.** *Normal* (`standard`): the rotating QR is the only check; location and photo are neither requested,
   evaluated nor stored, and a valid scan is always `verified`. *Strict*: QR + GPS + a live photo of the room (below).
   The legacy value `visual` behaves like strict. The mode is chosen per session (organization setting = default).
2. **Stateless HMAC QR** (per-session secret, **5 s** slots). Not single-use, so unlimited scanners. It proves a code was
   recently on the organizer's screen; a forwarded screenshot works only inside the grace window: 2 slots in normal
   mode, 1 slot in strict. With codes that short, the QR is checked **once, at scan time** (`/checkins/scan`), which
   returns a signed ticket bound to (session, user) valid for 120 s, so people can finish the location and camera
   steps. A ticket can only be minted from a fresh scan.
3. **One effective record** per (session, user): `UNIQUE` constraint plus a single upsert; tested with 50 concurrent
   requests from one account and 300 attendees on one QR.
4. **Session location** (`location_source`), first match wins: explicit `venue_id` (venue centre + radius) → organizer
   `anchor` → the organization's only venue (`sole_venue`, so title-only sessions still get GPS checks) → `none`.
   With several venues and no choice the location stays `none`; nothing is guessed.
   **Strict decision** (`attendance.Decide`): reject only when `distance − accuracy > radius`; poor accuracy, a GPS fix
   older than 30 s, missing GPS, a missing session anchor, or a missing/non-matching photo go to `needs_review`, never
   silent rejection or silent acceptance. Matching GPS **and** photo → `verified`.
   Anchor = organizer device position at creation (optional). Without it, every check-in is `needs_review`
   (`no_session_anchor`). Raw attendee coordinates are not stored, only derived distance/accuracy.
5. **Photo check** (`apps/vision`, called through `internal/vision`; details and measurements in `docs/VISION.md`).
   The attendee photographs the screen; the photo must contain this session's code from the last 3 rotations (proof
   it was taken live) **and** show the room of the reference photos: the place's saved photos plus the session's
   "today's photo". Recognition: XFeat features, fast and LighterGlue matching, MAGSAC++ geometric verification,
   scored by distinct consistent places; 0 false matches over 1601 pairs of different places. Attendee photos are
   never stored; the attempt keeps the score, the two verdicts and the model version. A non-match, a missing code
   or an unavailable service goes to review, never to rejection or silent acceptance. Strict mode with no reference
   photos at all cannot verify anyone and says so (`visual_not_configured`).
6. **Policy snapshot per session**: later settings changes never alter a running session.
7. **Tenant isolation**: every query is scoped by org id; non-members get 404, not 403.
8. **Plain pgx instead of sqlc** (fewer tools); revisit if the query count grows.
9. **Built-in accounts, one per person, used everywhere.** Name + email + password; the same account checks in to any
   organization's sessions and runs any number of organizations. Passwords: Argon2id (OWASP parameters, at most 4
   hashes at once), 8–128 characters, well-known passwords refused (NIST SP 800-63B). Sessions: opaque random bearer
   tokens (only their SHA-256 is stored), 30 days from last use, revocable (sign out, sign out elsewhere, password
   change/reset). **Bearer header, no cookies**, so no CSRF surface. Sign-in never reveals whether an email has an
   account (same message, same work); 10 wrong passwords pause an account for 15 minutes; per-IP limits on sign-in,
   sign-up and emails. Email confirmation is encouraged (a reminder), not required, so nobody is blocked in class.
10. Attendance status machine: `needs_review→verified|rejected`, `verified→revoked`, `rejected|revoked→verified`; all audited with a reason.

## Status
| Area | State |
|---|---|
| Schema, migrations, QR, policy, sessions, check-in, RBAC, members, invitations, venues, settings, manual, review, CSV, history, deletion, SSE, rate limits, CORS | Implemented and tested (`go test -race ./...` against real PostGIS) |
| HTTP routes | Tested end to end via `httptest` |
| Compose config, Caddy routing | Compose config validates (API healthcheck gates Caddy). Image build and the full stack were **not** run in the last session (no Docker daemon). |
| Accounts | Tested over HTTP (sign up, validation, confirmation link, sign in/out, wrong passwords, lockout, change and reset password, sessions revoked, deletion frees the email, one identity across organizations) and in Chromium end to end (sign up from a check-in link, onboarding, emailed links, account menu). SMTP delivery tested against a local server (refuses to authenticate without TLS); **not yet run against a real mail provider.** |
| Live-binary run | Earlier build: migrations incl. upgrade, 300 concurrent attendees on one QR over HTTP → 300 verified/0 errors, ~0.4 s wall, p95 ≈ 160 ms (one dev machine; indicative only). Not repeated for the two-step strict flow (it is covered by a 60-way concurrent test) |
| Web app (`apps/web`: organizer and attendee, no build step) | Tested in real Chromium end to end (`npm run e2e`, also in CI): normal and strict flows with a fake camera playing generated rooms with the live code on their screen, the real vision service, mocked GPS, QR decoded from the screen, in-app scanner, review, CSV, invites, Kurdish names |
| Photo check (`apps/vision`) | Implemented; calibrated on 60 real photos of 20 places (0 false matches in 1601 different-place pairs) and tested in CI (`pytest`). **Not yet measured on photos of real classrooms taken with the app**; see `docs/VISION.md` before changing thresholds |
| PDF reports, background job queue | **Not implemented** (CSV is synchronous streaming) |
| Load tests (100/500/1000), backups/restore, monitoring, audit-log API | **Not implemented**; no benchmark numbers exist |
| Manual attendance and direct member add still take a user UUID (invitations cover onboarding); no platform-admin role | Open product decisions |

## Known limitations and risks
- A browser cannot prove physical presence. GPS is client-reported and spoofable; the QR can be relayed; a photo of
  the room with the live code on it can be streamed to someone elsewhere. A `verified` record means "evidence
  consistent with presence", not proof. Strict raises the cost of cheating (5 s codes, 1-slot grace, fresh GPS, a
  photo that must show the current code and the room); it does not make it impossible.
- Normal mode deliberately checks nothing but the QR, so a code sent to a friend within ~10 s counts.
- With 5-second codes, a first-time user who must sign in or register after scanning will usually be told the code
  changed and asked to scan again (the sign-in round trip outlasts the code). Signed-in users are not affected.
- Strict mode without reference photos (no saved place photos and no today's photo) cannot verify anyone (everyone
  goes to review); the web app warns before such a session is started and offers to take today's photo.
- Strict mode needs the vision service (one CPU core per check in progress, ~0.1 s per matching photo, up to ~2 s
  otherwise). If it is down, strict check-ins go to review.
- `verification_attempts` (one row per attempt) and `audit_logs` grow without a retention policy. Decide how long to
  keep attempts (e.g. delete after 90 days) before large-scale use.
- PostgreSQL 15 or newer is required (migration 0003 uses `ON DELETE SET NULL (column)`); Compose ships 17.
- Rate limits and the per-account wrong-password pause are in memory per process (fine for one server; they reset on
  restart). A classroom behind one NAT shares one IP: the per-IP sign-in and sign-up limits are sized for that.
- Without email (`IAH_SMTP_URL` empty) people cannot reset a forgotten password themselves; the operator makes a
  link with `api reset-link`.
- User profile lookups happen on every request (one indexed query); add a short cache if profiling shows need.
- Account deletion anonymizes attendance snapshots, so historical reports lose names (privacy chosen over history).
- Single server = single point of failure. Backups must go off-machine (`pg_dump` to remote storage); not automated yet.
