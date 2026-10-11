# iamhere

"I Am Here": QR- and GPS-based attendance for organizations. The backend is a Go modular monolith on
PostgreSQL + PostGIS (`apps/api`), a framework-free web app (`apps/web`), deployed with Docker Compose + Caddy (`infra`).
Two modes: **Normal** (a QR code that changes every 5 seconds) and **Strict** (QR + GPS + a live photo of the room).

- Backend design, API reference, status and known limits: [`docs/BACKEND.md`](docs/BACKEND.md)
- Web app (organizers and attendees, no build step), re-skinning guide: [`apps/web/README.md`](apps/web/README.md)
- Quick start: `cd apps/api && go test -race ./...` (needs PostGIS, see the doc), then run with `IAH_DEV_AUTH=1`
- CI (`.github/workflows/ci.yml`): gofmt, `go mod tidy`, vet, staticcheck, govulncheck, race tests against PostGIS,
  image build, Compose and Caddyfile validation, and browser end-to-end tests of the web app. Missing test database fails CI instead of skipping.
