# Architecture Decision Records

Short documents capturing *why* a significant decision was made. Format: Context → Decision →
Consequences. An ADR is never edited after acceptance — a new ADR supersedes it.

| # | Decision | Status |
|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | Accepted |
| [0002](0002-modular-monolith.md) | DDD modular monolith | Accepted |
| [0003](0003-go-stack.md) | Idiomatic Go stack: net/http + chi, pgx + sqlc, goose | Accepted (test tooling amended by 0012) |
| [0004](0004-multi-tenancy-shared-schema.md) | Shared-schema multi-tenancy with `business_id` | Accepted |
| [0005](0005-postgres-postgis.md) | PostgreSQL + PostGIS as the only stateful dependency | Accepted |
| [0006](0006-openapi-first.md) | OpenAPI-first REST contract | Accepted |
| [0007](0007-auth-phone-otp-jwt.md) | Own auth: phone OTP + JWT + rotating refresh tokens | Accepted |
| [0008](0008-double-booking-exclusion-constraint.md) | Prevent double booking with a Postgres exclusion constraint | Accepted |
| [0009](0009-domain-events-outbox-river.md) | Domain events via transactional outbox on River | Accepted |
| [0010](0010-pay-at-shop-first.md) | Pay at the shop in v1 | Accepted |
| [0011](0011-ci-cd-github-actions.md) | CI/CD on GitHub Actions — build once, publish versioned images, deploy later | Accepted |
| [0012](0012-test-database-per-test.md) | Integration tests get a throwaway database on a shared PostGIS server | Accepted |
