# iamhere

"I Am Here": QR- and GPS-based attendance for organizations. The backend is a Go modular monolith on
PostgreSQL + PostGIS (`apps/api`), a framework-free web app (`apps/web`), a computer-vision service for strict mode
(`apps/vision`, Python, CPU only), deployed with Docker Compose + Caddy (`infra`).
One account per person, used everywhere (built-in sign-up, sign-in, password reset). Two modes: **Normal** (a QR code
that changes every 5 seconds) and **Strict** (QR + GPS + a photo of the screen that must show the current code and the
room; each room is learned better with use).

- Backend design, API reference, status and known limits: [`docs/BACKEND.md`](docs/BACKEND.md)
- Web app (organizers and attendees, no build step), re-skinning guide: [`apps/web/README.md`](apps/web/README.md)
- Strict mode's photo check (method, measured accuracy, limits): [`docs/VISION.md`](docs/VISION.md)
- Quick start: `cd apps/api && go test -race ./...` (needs PostGIS, see the doc), then run with `IAH_DEV_AUTH=1`
- CI (`.github/workflows/ci.yml`): gofmt, `go mod tidy`, vet, staticcheck, govulncheck, race tests against PostGIS,
  vision service tests, image builds, Compose and Caddyfile validation, and browser end-to-end tests of the web app. Missing test database fails CI instead of skipping.
