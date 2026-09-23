# CLAUDE.md — rules for AI sessions in this repository

## Project
Go backend for a multi-tenant barbershop booking SaaS (Saudi/GCC, Arabic-first). DDD modular
monolith. Start with `docs/PLAN.md`; check the relevant ADR in `docs/adr/` before changing anything
architectural. Current milestone is tracked in the README roadmap.

## The owner is learning Go (coming from Node.js)
- Work in **guided scaffolding** mode: build the first instance of a pattern, explain it, and leave the
  next instance as an exercise ("Your turn") unless told otherwise.
- Every PR description fills the template's **"Go concepts introduced (Node → Go)"** section.
- Prefer clear, idiomatic code over clever code. Comment the *why* of a Go idiom the first time it appears.
- When reviewing the owner's code, review like a senior Go reviewer: idioms, error handling, naming,
  tests, boundaries — explain each point briefly.

## Architecture rules (hard)
- Layers: `domain` (pure Go, stdlib + `internal/shared` only) ← `app` ← `adapters`. Never the other way.
- Modules import other modules **only via their root package** public API; never another module's
  `domain`, `app` or `adapters`. No SQL against another module's schema.
- Every tenant-scoped repository method takes `BusinessID`; every business-mode use case checks
  membership first (BOLA is the #1 risk in this codebase).
- The API contract changes in `api/openapi.yaml` first; generated code is never edited by hand.
- New architectural decision → new ADR in the same PR.

## Go conventions
- Errors are values: wrap with `%w` and context; domain sentinels checked with `errors.Is/As`;
  map to problem+json only at the HTTP edge. No `panic` for control flow.
- `ctx context.Context` first parameter for I/O; never stored in structs.
- Small interfaces defined where consumed. Constructor injection wired in `main`. No globals, no `init()` side effects.
- Domain never calls `time.Now()` — use `Clock` / pass `now`. Instants UTC; schedules in branch-local wall-clock.
- Money is `int64` halalas + currency. Never floats.
- IDs are UUIDv7, typed per aggregate.
- `log/slog` structured logs; never log OTPs, tokens, or full phone numbers.
- Package names short, lowercase, by responsibility. No `utils` / `common` / `helpers`.
- Tests: table-driven, `t.Parallel()` where safe, fakes over mocks, testcontainers for repositories,
  `go test -race`. Fuzz the availability calculator.

## Workflow
- Small PRs, one concept each, following the PR template.
- Migrations are forward-only once merged — add a new one, never edit an old one.
- Before pushing: format, lint, test (commands are added to this file in M1).
- Do not add a dependency without stating why in the PR.
