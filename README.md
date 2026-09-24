# barbershop-backend

[![CI](https://github.com/Abdulkhaliq-84/barbershop-backend/actions/workflows/ci.yml/badge.svg)](https://github.com/Abdulkhaliq-84/barbershop-backend/actions/workflows/ci.yml)
[![CD](https://github.com/Abdulkhaliq-84/barbershop-backend/actions/workflows/cd.yml/badge.svg)](https://github.com/Abdulkhaliq-84/barbershop-backend/actions/workflows/cd.yml)
[![Security](https://github.com/Abdulkhaliq-84/barbershop-backend/actions/workflows/security.yml/badge.svg)](https://github.com/Abdulkhaliq-84/barbershop-backend/actions/workflows/security.yml)

Multi-tenant **barbershop booking SaaS** backend in **Go** — built with Domain-Driven Design as a
modular monolith, PostgreSQL + PostGIS for map search, and phone-OTP authentication.
The mobile app (Flutter, Arabic-first) will consume this API.

> **Status: M1.5 — CD & releases.** Config, logging, HTTP server with graceful shutdown, health checks,
> embedded migrations, CI, security scanning, and a delivery pipeline that publishes scanned, attested
> multi-arch images to GHCR with SemVer releases. Business modules start in M2.
> The architecture, domain model and roadmap are in [`docs/`](docs/PLAN.md).

## Getting started

Requires **Go 1.27+** and **Docker**.

```bash
make tools     # install pinned golangci-lint and air
make db-up     # PostgreSQL 18 + PostGIS in Docker
make migrate   # apply migrations
make dev       # API on http://localhost:8080 with live reload

curl localhost:8080/healthz   # {"status":"ok"}
curl localhost:8080/readyz    # {"status":"ok","checks":{"database":"ok"}}

make check     # everything CI checks — run before pushing
make           # list all commands
```

Run the production image instead (distroless, non-root, ~6 MB):

```bash
make docker-build && make app-up BARBERSHOP_IMAGE=barbershop-backend:local
make app-up BARBERSHOP_IMAGE=ghcr.io/abdulkhaliq-84/barbershop-backend:latest   # latest release
make app-down
```

## What it does

- **Barbershops (tenants)** register, get verified (Commercial Registration), and manage multiple
  **branches** — each with its own map location, opening hours, services, prices and **barbers**.
- **Barbers** have individual schedules (split shifts, shifts past midnight, time off).
- **Customers** find branches **nearby on the map** or **by city**, choose services, a specific barber
  or **"any available barber"**, and book a slot. Pay at the shop.
- **Business mode** in the same app: today's timeline per barber, bookings, services, team, schedules.
- **Subscription plans** per business with enforced limits.

## Architecture

```mermaid
flowchart LR
  APP[Flutter app] --> EDGE[HTTP edge · OpenAPI · auth]
  EDGE --> IAM[[iam]]
  EDGE --> BIZ[business]
  EDGE --> CAT[catalog]
  EDGE --> SCH[scheduling ★]
  EDGE --> BOOK[booking ★]
  EDGE --> DISC[discovery]
  EDGE --> BILL[billing]
  BOOK -. events .-> NOTIF[[notification]]
  BIZ -. events .-> DISC
  CAT -. events .-> DISC
  SCH -. events .-> DISC
  BOOK --> SCH
  BOOK --> CAT
```

- **Modular monolith**: one binary, one module per bounded context, strict boundaries checked in CI.
- **Hexagonal layers** per module: pure `domain` → `app` use cases → `adapters` (HTTP, Postgres, providers).
- **Double booking is impossible by construction**: a Postgres exclusion constraint on each barber's time ranges.
- **Transactional outbox** (River) for reliable domain events and scheduled reminders.
- **Pipeline as code**: CI on every PR; every merge publishes a scanned, attested image — build once, promote on release.

## Tech stack

Go · net/http + chi · PostgreSQL + PostGIS · pgx + sqlc · goose · River · OpenAPI (oapi-codegen) ·
JWT (EdDSA) · log/slog · golangci-lint · Docker Compose ·
GitHub Actions · GHCR · release-please · Trivy · CodeQL

## Documentation

| | |
|---|---|
| [Master plan & roadmap](docs/PLAN.md) | decisions, scope, milestones, workflow |
| [Architecture overview](docs/architecture/overview.md) | layers, folder layout, Go conventions |
| [Domain model](docs/architecture/domain-model.md) | bounded contexts, aggregates, invariants, availability algorithm |
| [Persistence](docs/architecture/persistence.md) | tenancy, schemas, constraints, time & money |
| [API overview](docs/api/overview.md) | conventions and v1 endpoints |
| [Design system](docs/design/design-system.md) | mobile design direction (IBM Plex, green · navy · white, RTL) |
| [CI/CD pipeline](docs/operations/ci-cd.md) | lifecycle from commit to released image (and later, deploy) |
| [ADRs](docs/adr/) | why each major decision was made |
| [Node → Go guide](docs/learning/node-to-go.md) | the mindset shift, written along the way |

## Roadmap

- [x] M0 — Planning: domain model, architecture, ADRs, design direction
- [x] M1 — Walking skeleton + CI: server, config, Postgres/PostGIS, migrations, lint/test/build on every PR
- [x] M1.5 — CD & releases: scanned, attested Docker images on GHCR, SemVer releases
- [ ] M2 — Shared kernel + IAM: phone OTP, JWT, refresh rotation
- [ ] M3 — Business onboarding: verification, branches, staff, plans
- [ ] M4 — Catalog + scheduling: services, opening hours, barber schedules
- [ ] M5 — Booking core: availability engine, booking lifecycle
- [ ] M6 — Discovery: map / city / text search
- [ ] M7 — Notifications: push, SMS, reminders
- [ ] M8 — Hardening + Flutter hand-off

## About

Built by [Abdulkhaliq](https://github.com/Abdulkhaliq-84) while moving from Node.js to Go — with an
AI pair programmer used as a *guided scaffolder*: it builds the first instance of each pattern, I build
the next one, and every PR explains the Go idioms it introduces.
