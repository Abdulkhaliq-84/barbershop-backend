# ADR-0003: Idiomatic Go stack — net/http + chi, pgx + sqlc, goose

- Status: Accepted · Date: 2026-09-23

## Context
Coming from Express + Prisma, the tempting path is Gin/Echo + GORM. ORMs tend to leak persistence
concerns into the domain (tags, lazy loading, hooks) and hide the SQL we need (PostGIS, `tstzrange`,
exclusion constraints). The goal is also to learn *idiomatic* Go.

## Decision
- HTTP: stdlib `net/http` with **chi** (route groups, middleware; fully net/http compatible).
- DB: **pgx v5** driver + **sqlc** (write SQL, generate type-safe Go). No ORM.
- Migrations: **goose** (plain SQL).
- Logging: **log/slog**. Config: env vars → typed struct (**caarlos0/env**).
- Tests: stdlib `testing`, **go-cmp**, **testcontainers-go**. Lint: **golangci-lint**.

## Consequences
- Domain types stay pure; mapping rows ↔ aggregates is explicit (a little more code, much more clarity).
- Full access to Postgres features.
- Generated code must be regenerated and committed; CI checks it's in sync.
