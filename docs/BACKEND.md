# I Am Here — Backend

Go modular monolith (`apps/api`): chi, pgx, PostgreSQL 17 + PostGIS. No Redis, no queue, no paid services.

## Run and test

```sh
docker run -d --name iah-pg -e POSTGRES_PASSWORD=dev -p 55432:5432 imresamu/postgis:17-3.5-bookworm   # arm64 dev
cd apps/api && go test -race ./...                         # integration tests create throwaway DBs
# other Postgres (needs the PostGIS extension): TEST_PG_ADMIN_URL='postgres://postgres:pw@host:5432/postgres?sslmode=disable' go test ./...
IAH_DEV_AUTH=1 IAH_DATABASE_URL='postgres://postgres:dev@localhost:55432/iamhere?sslmode=disable' go run ./cmd/api
```
Dev login: `Authorization: Bearer dev:<subject>:<Full Name>` (refused when `IAH_ENV=production`).
Production: `infra/` (Compose + Caddy). Copy `.env.example` to `.env`, then `docker compose up -d --build`.
Configuration is environment-only (`IAH_*`, see `internal/config`): `IAH_ENV` (`development`|`production`, anything else
refuses to start), `IAH_LISTEN`, `IAH_DATABASE_URL`, `IAH_OIDC_ISSUER`, `IAH_OIDC_CLIENT_ID`, `IAH_DEV_AUTH`,
`IAH_QR_GRACE_SLOTS`, `IAH_CORS_ORIGINS` (comma-separated exact origins; only needed when the web app is on a different
origin than the API). `api healthcheck` probes `/readyz` (used by the Compose healthcheck; the image has no shell/curl).

## Layout
`internal/qr` rotating tokens · `internal/attendance` pure decision policy · `internal/app` authorization,
tenancy, transactions · `internal/httpapi` routes, rate limits, SSE · `internal/db` migrations · `internal/auth` OIDC/dev.

## API (all under `/v1`, bearer token, JSON; errors `{error, message}`)
| | |
|---|---|
| `GET/PATCH/DELETE /me`, `GET /me/attendance` | profile (name, E.164 phone), history, anonymizing account deletion |
| `POST /checkins` `{token, gps?}` | idempotent per (session, user); 410 `qr_expired`/`session_ended`; 429 rate limit |
| `GET/POST /orgs`, `GET/PUT /orgs/{o}/settings`, `GET /members`, `PUT/DELETE /members/{u}` | orgs, policy, RBAC |
| `GET/POST /orgs/{o}/invites`, `DELETE .../{i}`, `POST /invites/accept` `{token}` | single-use join links (token shown once, only its hash is stored; 7 d default, 30 d max); owners are never created by invite; only owners mint admin invites |
| `GET/POST /orgs/{o}/venues`, `DELETE .../{v}` | registered places `{name, lat, lon, radius_m}`; admin/owner manage, members read |
| `POST/GET /orgs/{o}/sessions` (`Idempotency-Key` header), `GET .../{s}`, `/qr`, `POST /close` | title-only creation; optional `anchor` or `venue_id` |
| `GET .../{s}/attendance`, `/attendance.csv`, `POST .../attendance/manual`, `POST /orgs/{o}/attendance/{r}/review` | records, export, fallback, review |
| `GET .../{s}/events` | SSE live counts (use fetch streaming; EventSource cannot send Authorization) |

## Key decisions
1. **Stateless HMAC QR** (per-session secret, 10 s slots, 2-slot grace). Not single-use, so unlimited scanners.
   It proves a code was recently on the organizer's screen. Forwarded screenshots work within the grace window.
2. **One effective record** per (session, user): `UNIQUE` constraint plus a single upsert; tested with 50 concurrent
   requests from one account and 300 attendees on one QR.
3. **Session location** (`location_source`), first match wins: explicit `venue_id` (venue centre + radius) → organizer
   `anchor` → the organization's only venue (`sole_venue`, so title-only sessions still get GPS checks) → `none`.
   With several venues and no choice the location stays `none`; nothing is guessed.
   **GPS decision** (`attendance.Decide`): reject only when `distance − accuracy > radius`; poor accuracy, stale fix,
   missing GPS or missing session anchor go to `needs_review`, never silent rejection or silent acceptance.
   Anchor = organizer device position at creation (optional). Without it, every check-in is `needs_review`
   (`no_session_anchor`). Raw attendee coordinates are not stored, only derived distance/accuracy.
4. **Policy snapshot per session**: later settings changes never alter a running session.
5. **Tenant isolation**: every query is scoped by org id; non-members get 404, not 403.
6. **Plain pgx instead of sqlc** (fewer tools); revisit if the query count grows.
7. **Bearer tokens, no cookies**, so no CSRF surface. Keycloak (OIDC) verified via JWKS; accounts need only a name.
8. Attendance status machine: `needs_review→verified|rejected`, `verified→revoked`, `rejected|revoked→verified`; all audited with a reason.

## Status
| Area | State |
|---|---|
| Schema, migrations, QR, policy, sessions, check-in, RBAC, members, invitations, venues, settings, manual, review, CSV, history, deletion, SSE, rate limits, CORS | Implemented and tested (`go test -race ./...` against real PostGIS) |
| HTTP routes | Tested end to end via `httptest` |
| Compose config, Caddy routing | Compose config validates (API healthcheck gates Caddy). Image build and the full stack were **not** run in the last session (no Docker daemon). |
| OIDC | Verified against a local mock provider (valid token, wrong client, expired, wrong issuer, forged signature). **Not yet run against a real Keycloak.** |
| Live-binary run | Started the built binary on PostgreSQL 16 + PostGIS 3.4: migrations 1→3 incl. upgrade, 300 concurrent attendees on one QR over HTTP → 300 verified/0 errors, ~0.4 s wall, p95 ≈ 160 ms (one dev machine; indicative only, not a capacity claim) |
| Visual Presence Check (CV worker, venues, fingerprints) | **Not implemented.** Visual/strict modes route to review |
| PDF reports, background job queue | **Not implemented** (CSV is synchronous streaming) |
| Load tests (100/500/1000), backups/restore, monitoring, audit-log API | **Not implemented**; no benchmark numbers exist |
| Manual attendance and direct member add still take a user UUID (invitations cover onboarding); no platform-admin role | Open product decisions |

## Known limitations and risks
- A browser cannot prove physical presence. GPS is client-reported and spoofable; the QR can be relayed.
  A `verified` record means "evidence consistent with presence", not proof.
- Rate limits are in memory per process (fine for one server); there is no per-IP limit on unauthenticated traffic
  beyond Caddy/OS defaults.
- User profile lookups happen on every request (one indexed query); add a short cache if profiling shows need.
- Account deletion anonymizes attendance snapshots, so historical reports lose names (privacy chosen over history).
- Single server = single point of failure. Backups must go off-machine (`pg_dump` to remote storage); not automated yet.
