# ADR-0012: Integration tests get a throwaway database on a shared PostGIS server

- Status: Accepted · Date: 2026-09-23 · Amends the test tooling in [ADR-0003](0003-go-stack.md)

## Context
ADR-0003 planned testcontainers-go: each test package starts its own PostGIS container. In practice:
- CI already runs PostGIS as a job **service container**, and developers already run the same image
  with `make db-up`, so a second container per package only adds start-up time (seconds each, much
  more under emulation on Apple Silicon, since `postgis/postgis` is amd64-only).
- Tests would need access to the Docker daemon, which some CI and sandboxed environments don't give.
- But `go test ./...` runs packages **in parallel**: sharing one database, one package's migration
  round-trip drops tables under another package's tests.

## Decision
- Database tests connect to the server named by `TEST_DATABASE_URL` (compose locally, the service
  container in CI — the same `postgis/postgis` tag in both).
- `dbtest.NewDatabase(t)` creates a fresh database `test_<random>` for **each test**, returns a pool to
  it, and drops it (`WITH (FORCE)`) when the test ends. Tests then run migrations or fixtures themselves.
- Without `TEST_DATABASE_URL` the test is **skipped** (plain `go test ./...` needs no Postgres) — except
  when `CI` is set, where a missing URL **fails**, so a misconfigured pipeline can't silently skip them.
- testcontainers-go is not a dependency.

## Consequences
- Isolated, parallel-safe tests (`t.Parallel()` is fine) with ~1 s per test database including migrations.
- One place pins the Postgres/PostGIS version for tests: keep `compose.yaml` and `ci.yml` in step.
- The test role needs `CREATEDB` (the image's `POSTGRES_USER` is a superuser; so is the local dev role).
