# I Am Here — Backend

Go modular monolith (`apps/api`): chi, pgx, PostgreSQL 17 + PostGIS. No Redis, no queue, no paid services.

## Run and test

```sh
docker run -d --name iah-pg -e POSTGRES_PASSWORD=dev -p 55432:5432 imresamu/postgis:17-3.5-bookworm   # arm64 dev
cd apps/api && go test -race ./...                         # integration tests create throwaway DBs
# other Postgres (needs the PostGIS extension): TEST_PG_ADMIN_URL='postgres://postgres:pw@host:5432/postgres?sslmode=disable' go test ./...
IAH_DEV_AUTH=1 IAH_WEB_DIR=../web/public IAH_DATABASE_URL='postgres://postgres:dev@localhost:55432/iamhere?sslmode=disable' go run ./cmd/api
# then open http://localhost:8080 : the web app (apps/web) is served by the API itself in development
```
Dev login: `Authorization: Bearer dev:<subject>:<Full Name>` (refused when `IAH_ENV=production`). The name may be
percent-encoded: browsers can only send Latin-1 in headers, so the web app encodes it (needed for Arabic/Kurdish names).
Production: `infra/` (Compose + Caddy). Copy `.env.example` to `.env`, then `docker compose up -d --build`.
Configuration is environment-only (`IAH_*`, see `internal/config`): `IAH_ENV` (`development`|`production`, anything else
refuses to start), `IAH_LISTEN`, `IAH_DATABASE_URL`, `IAH_OIDC_ISSUER`, `IAH_OIDC_CLIENT_ID`, `IAH_DEV_AUTH`,
`IAH_QR_GRACE_SLOTS`, `IAH_WEB_DIR` (serve the web app; production uses Caddy), `IAH_CORS_ORIGINS` (comma-separated exact origins; only needed when the web app is on a different
origin than the API). `api healthcheck` probes `/readyz` (used by the Compose healthcheck; the image has no shell/curl).

## Layout
`internal/qr` rotating tokens and check-in tickets · `internal/vision` photo fingerprints · `internal/attendance` pure decision policy · `internal/app` authorization,
tenancy, transactions · `internal/httpapi` routes, rate limits, SSE · `internal/db` migrations · `internal/auth` OIDC/dev.

## API (all under `/v1`, bearer token, JSON; errors `{error, message}`)
| | |
|---|---|
| `GET/PATCH/DELETE /me`, `GET /me/attendance` | profile (name, E.164 phone), history, anonymizing account deletion |
| `POST /checkins/scan` `{token}` | validates a fresh QR, returns a short-lived `ticket` plus what is still needed (`needs_gps`, `needs_photo`) and whether the person is `already_recorded` |
| `POST /checkins` `{ticket \| token, gps?, photo?}` | finishes a check-in; `photo` is a base64 JPEG/PNG (data URL ok, ≤ 512 KB). Idempotent per (session, user); 410 `qr_expired`/`ticket_expired`/`session_ended`; 400 `photo_unusable`/`photo_invalid` (retryable); 429 rate limit |
| `GET /public-config` (no auth) | `{auth: "oidc"\|"dev", issuer, client_id}` so the web app knows how to sign in |
| `GET/POST /orgs`, `GET/PUT /orgs/{o}/settings`, `GET /members`, `PUT/DELETE /members/{u}` | orgs, policy, RBAC |
| `GET/POST /orgs/{o}/invites`, `DELETE .../{i}`, `POST /invites/accept` `{token}` | single-use join links (token shown once, only its hash is stored; 7 d default, 30 d max); owners are never created by invite; only owners mint admin invites |
| `GET/POST /orgs/{o}/venues`, `DELETE .../{v}` | registered places `{name, lat, lon, radius_m}` with `reference_count`; admin/owner manage, members read |
| `GET/POST /orgs/{o}/venues/{v}/references`, `DELETE .../{r}` | reference photos for the visual check (max 8 per place). Only the 64-byte fingerprint is stored, never the photo. Admin/owner only |
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
5. **Visual presence check** (`internal/vision`, standard library only). A venue has up to 8 reference photos taken live
   on site. Each photo becomes a 256-bit luminance difference hash plus a per-bit confidence mask (comparisons between
   near-equal regions such as plain walls are noise and are ignored). A live photo matches when the normalised Hamming
   distance to any reference is ≤ 64/256. It is insensitive to exposure, noise, recompression and small reframing,
   and **not** to a large change of viewpoint, so enrol 2–3 angles. Photos are never stored (fingerprint and distance
   only), are only ever taken from the live camera by the web app, and a non-match never rejects anyone.
   Strict mode at a place with no reference photos cannot verify anyone and says so (`visual_not_configured`).
6. **Policy snapshot per session**: later settings changes never alter a running session.
7. **Tenant isolation**: every query is scoped by org id; non-members get 404, not 403.
8. **Plain pgx instead of sqlc** (fewer tools); revisit if the query count grows.
9. **Bearer tokens, no cookies**, so no CSRF surface. Keycloak (OIDC) verified via JWKS; accounts need only a name.
10. Attendance status machine: `needs_review→verified|rejected`, `verified→revoked`, `rejected|revoked→verified`; all audited with a reason.

## Status
| Area | State |
|---|---|
| Schema, migrations, QR, policy, sessions, check-in, RBAC, members, invitations, venues, settings, manual, review, CSV, history, deletion, SSE, rate limits, CORS | Implemented and tested (`go test -race ./...` against real PostGIS) |
| HTTP routes | Tested end to end via `httptest` |
| Compose config, Caddy routing | Compose config validates (API healthcheck gates Caddy). Image build and the full stack were **not** run in the last session (no Docker daemon). |
| OIDC | Verified against a local mock provider (valid token, wrong client, expired, wrong issuer, forged signature). **Not yet run against a real Keycloak.** |
| Live-binary run | Earlier build: migrations incl. upgrade, 300 concurrent attendees on one QR over HTTP → 300 verified/0 errors, ~0.4 s wall, p95 ≈ 160 ms (one dev machine; indicative only). Not repeated for the two-step strict flow (it is covered by a 60-way concurrent test) |
| Web app (`apps/web`: organizer and attendee, no build step) | Tested in real Chromium end to end (`npm run e2e`, also in CI): normal and strict flows with a fake camera playing real scenes, mocked GPS, QR decoded from the screen, in-app scanner, review, CSV, invites, Kurdish names |
| Visual presence check | Implemented and measured on synthetic scenes and through the browser camera pipeline. **The threshold is not calibrated on real classroom photos**; expect to tune `vision.MatchMaxDistance` once real data exists |
| PDF reports, background job queue | **Not implemented** (CSV is synchronous streaming) |
| Load tests (100/500/1000), backups/restore, monitoring, audit-log API | **Not implemented**; no benchmark numbers exist |
| Manual attendance and direct member add still take a user UUID (invitations cover onboarding); no platform-admin role | Open product decisions |

## Known limitations and risks
- A browser cannot prove physical presence. GPS is client-reported and spoofable; the QR can be relayed; a photo of
  the room can be forwarded and re-shot from a screen. A `verified` record means "evidence consistent with presence",
  not proof. Strict raises the cost of cheating (5 s codes, 1-slot grace, fresh GPS, live photo); it does not make it
  impossible.
- Normal mode deliberately checks nothing but the QR, so a code sent to a friend within ~10 s counts.
- Rate limits are in memory per process (fine for one server); there is no per-IP limit on unauthenticated traffic
  beyond Caddy/OS defaults.
- User profile lookups happen on every request (one indexed query); add a short cache if profiling shows need.
- Account deletion anonymizes attendance snapshots, so historical reports lose names (privacy chosen over history).
- Single server = single point of failure. Backups must go off-machine (`pg_dump` to remote storage); not automated yet.
