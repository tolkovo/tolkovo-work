# Tolkovo

Open-source live chat for support and sales: Telegram (a bot or a personal account) and a website widget in one inbox, an AI agent on the first line that hands over to a human, support lines L1/L2/L3, shifts and roles. Runs as multi-tenant SaaS or self-hosted with `docker compose`.

> **Status:** early development, Phase 0 (Foundation). Nothing is usable yet. The plan lives in [docs/](docs/README.md).

## Repository

```
apps/server     Go modular monolith: one binary, roles api | realtime | worker | all
apps/panel      operator, supervisor and admin panel (React)          — not started
apps/widget     chat widget for sites and Electron apps, npm @tolkovo/widget (Preact, MIT)
apps/landing    landing page and docs (Astro)                        — not started
api/            OpenAPI and WebSocket schemas: the source of truth   — not started
deploy/compose  local dependencies and self-host compose files
design/         local HTML prototypes (never published)
docs/           plan, ADRs, research
```

## Develop

Requirements: Go 1.26+, Docker (for `make dev`), GNU Make.

```sh
make dev     # PostgreSQL, Redis, Garage (S3), Mailpit in Docker → migrate → run the server on :8080
make check   # generated code is fresh, lint, tests — run before every PR
make help    # all targets
```

Integration tests need a PostgreSQL 16 role that can create databases and roles; each test gets its own throwaway database:

```sh
TOLKOVO_TEST_DATABASE_URL=postgres://postgres@localhost:5432/postgres?sslmode=disable make test
```

Without the variable integration tests are skipped locally and fail in CI.

Configuration: `TOLKOVO_*` environment variables, all listed in [deploy/compose/.env.example](deploy/compose/.env.example).

## Contributing

Read [CONTRIBUTING.md](CONTRIBUTING.md) and the engineering standard [docs/00-engineering-rules.md](docs/00-engineering-rules.md). Security issues: [SECURITY.md](SECURITY.md).

## License

The server and panel are licensed under [AGPL-3.0-or-later](LICENSE). The widget ([apps/widget](apps/widget/LICENSE)) and SDKs are MIT-licensed so they can be embedded in any site or app ([ADR-0006](docs/adr/0006-licensing.md)). Documentation: CC BY 4.0.
