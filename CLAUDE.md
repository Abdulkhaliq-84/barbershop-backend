# CLAUDE.md — barbershop-backend

Context and rules for every AI session in this repository. Read this first; go to the linked docs for depth.

## 1. What this project is

Go backend for a **multi-tenant barbershop booking SaaS** in **Saudi Arabia / GCC** (Arabic-first, RTL,
SAR, `Asia/Riyadh`). A **business** (tenant) registers, is verified by the platform (Commercial
Registration), and runs **branches** on a map, each with **services** and **barbers** who have their own
schedules. **Customers** find branches nearby or by city and book a specific barber or "any barber",
one or more services, and pay at the shop. Businesses pay a **subscription plan**. The mobile app is
**Flutter** (separate repo, later) and consumes this API; owners/barbers use a "Business mode" in the same app.

**Status**: the current milestone is the first unchecked item in the README roadmap. M1 delivered
the walking skeleton (config, logging, HTTP server, health checks, migrations) and CI; M1.5 the
delivery pipeline (Docker image, security scans, GHCR publishing, release-please releases).

## 2. Where things are

| You need | Read |
|---|---|
| Decisions, scope, roadmap, workflow | `docs/PLAN.md` |
| Layers, folder layout, module rules, Go conventions, stack | `docs/architecture/overview.md` |
| Bounded contexts, aggregates, invariants, events, availability algorithm, authorization | `docs/architecture/domain-model.md` |
| Tenancy, schemas, constraints, time & money | `docs/architecture/persistence.md` |
| API conventions and endpoint inventory | `docs/api/overview.md` (contract: `api/openapi.yaml` from M2) |
| Pipeline lifecycle, workflows, releases, future deploy | `docs/operations/ci-cd.md` |
| Why a decision was made | `docs/adr/` (0001–0012) |
| Mobile design system (colours, IBM Plex, components, RTL, screens) | `docs/design/design-system.md`, `docs/design/tokens.json`, `docs/design/mockups/` |
| Node.js → Go idioms for the owner | `docs/learning/node-to-go.md` |

Project skills (`.claude/skills/`): **`design-system`** — any UI, Figma, Flutter theme or mockup work;
**`scaffold-module`** — adding a module, aggregate, use case, endpoint or migration.

## 3. Locked decisions (don't change without a new ADR)

- DDD **modular monolith**, one binary (`cmd/server`: `api` · `worker` · `migrate`), hexagonal layers per module.
- Go stack: `net/http` + **chi**, **pgx v5 + sqlc** (no ORM), **goose**, **log/slog**, **River** (jobs + outbox), **oapi-codegen** (OpenAPI-first).
- **PostgreSQL + PostGIS** only stateful dependency; one Postgres **schema per module**; **shared tables with `business_id`** for tenancy.
- Auth: **phone OTP** + Ed25519 **JWT** (15 min) + rotating refresh tokens; business roles checked per request, not in the JWT.
- Double booking prevented by a Postgres **`EXCLUDE` constraint** on `(barber_id, tstzrange)`.
- **Pay at the shop** in v1; subscription plans with entitlements; manual admin approval of businesses.
- Notifications: FCM push + SMS (OTP) behind ports; WhatsApp/e-mail later.
- Hosting: **localhost** (Docker Compose) for now. CI/CD: GitHub Actions → GHCR images + release-please releases; deploy stage later.

## 4. Domain in one screen

| Module | Owns | Key rules |
|---|---|---|
| `iam` | users, OTP challenges, sessions | OTP hashed, 5 min TTL, 5 attempts; refresh reuse revokes the family |
| `business` | businesses, branches, staff, invitations | Draft → PendingReview → Active/Rejected → Suspended; branch publishable only when complete; limits from billing |
| `catalog` | categories, services, offerings | price VAT-inclusive halalas; duration 5–480 min in 5-min steps |
| `scheduling` ★ | opening hours, closures, barber schedules, time off | wall-clock templates in branch TZ; **shifts may cross midnight** |
| `booking` ★ | appointments | no overlap per barber (DB); item price/name **snapshots**; lead time, horizon, cancellation window |
| `discovery` | search read model | built only from events; PostGIS nearby; Arabic-normalised search |
| `billing` | plans, subscriptions | `Entitlements(businessID)`; expiry unpublishes over-limit branches |
| `notification` | device tokens, deliveries | idempotent handlers; reminders re-check status when fired |
| `media` | uploads | CR documents private (signed URLs only) |

Rule of thumb: **business = who & where · catalog = what · scheduling = when possible · booking = when committed.**
Use the ubiquitous language (EN ↔ AR glossary in the domain model) in code, API and UI copy.

## 5. Working with the owner — guided scaffolding

The owner is moving from **Node.js to Go** and wants to learn, not just receive code.
- Build the **first instance** of a pattern and explain it; leave the **next instance** as the PR's "Your turn" exercise unless told otherwise.
- Every PR fills **"Go concepts introduced (Node → Go)"**. Comment the *why* of an idiom the first time it appears.
- Review the owner's code like a senior Go reviewer: idioms, errors, naming, tests, boundaries — short explanations.
- Plan before large changes; ask when a choice is genuinely the owner's (product rules, scope, cost).
- Small PRs, one concept each.

## 6. Architecture rules (hard)

- Layers: `domain` (stdlib + `internal/shared` only) ← `app` ← `adapters`. Never the reverse.
- Other modules only via their **root package** public API; never another module's `domain`/`app`/`adapters`; never SQL on another module's schema.
- Every tenant-scoped repository method takes `BusinessID`; every business-mode use case checks **membership first** (BOLA is the #1 risk).
- Domain events are written to the outbox **in the same transaction** as the state change; handlers are idempotent.
- API changes start in `api/openapi.yaml`; generated code (sqlc, oapi-codegen) is never edited by hand.
- A new architectural decision needs a new ADR in the same PR.

## 7. Go conventions

- Errors are values: wrap with `%w` + context; domain sentinels via `errors.Is/As`; problem+json only at the HTTP edge; no `panic` for control flow.
- `ctx context.Context` first for I/O; never stored in structs.
- Small interfaces defined where consumed; constructor injection wired in `main`; no globals, no `init()` side effects.
- Aggregates: unexported fields, constructors returning `(T, error)`, behaviour methods, `Rehydrate` for loading, update-function pattern for saves.
- Domain never calls `time.Now()` (use `Clock`/`now`). Instants UTC; schedules branch-local wall-clock.
- Money = `int64` halalas + currency, never floats. IDs = UUIDv7, typed per aggregate.
- `log/slog` structured logs; never log OTPs, tokens or full phone numbers.
- Short lowercase package names by responsibility; no `utils`/`common`/`helpers`.
- Tests: table-driven, fakes over mocks, a throwaway database per test (`dbtest.NewDatabase`) for repositories and HTTP flows, `-race`; fuzz the availability calculator.

## 8. API rules

`/v1`, JSON `snake_case`, RFC 3339 UTC timestamps, money as `{amount (halalas), currency}`, cursor
pagination, RFC 9457 problem+json with stable `code`, `Accept-Language` (`ar` default), business routes
under `/v1/businesses/{business_id}/…`, `Idempotency-Key` on creates, `version` + `If-Match` on edits.

## 9. Pipeline rules

- PR titles use **Conventional Commits** (`feat(booking): …`, `fix(iam): …`); squash merge; the title drives SemVer.
- CI must be green before merge. Never skip, disable or quarantine a test to get green; fix the cause.
- Workflows: actions pinned by commit SHA, minimal `permissions:`, no secrets in the repo or images.
- **Build once, promote**: releases re-tag the image built for that commit; never rebuild or overwrite `:sha-*` / `:vX.Y.Z`.
- Migrations are forward-only once merged and backward compatible (expand → migrate → contract).
- Run `make check` before pushing (see §11).

## 10. Design system — quick reference (full: `.claude/skills/design-system/SKILL.md`)

- Style: **modern heritage barbershop**, Arabic-first RTL, Material 3 customised in Flutter.
- Fonts: **IBM Plex Sans Arabic** (Arabic, default) + **IBM Plex Sans** (Latin) + IBM Plex Mono (codes).
- Colours: brand green `#127A56` (actions), brand navy `#13203F` (ink, headers), white surfaces, canvas `#FAFBFC`.
  Variant A = green · navy · white (default); variant B = green · white. Tokens in `docs/design/tokens.json`.
- Signature: barber-pole stripe (green · white · navy, 45°), ≤ 5 % of a screen.
- Shapes: pill buttons (56 px), cards 16–24 radius, bento tiles 28–32, floating pill bottom nav.
- Latin digits for times/prices/phones; phone/OTP/prices stay LTR inside RTL; directional icons mirror.
- Every text colour pair must meet WCAG AA (checked pairs are in the design doc).

## 11. Commands

Go 1.27+ and Docker are required; `make tools` installs the pinned golangci-lint and air.

| Command | Does |
|---|---|
| `make db-up` / `make db-down` / `make db-reset` | Start / stop / wipe local PostGIS (`compose.yaml`) |
| `make migrate` | Apply migrations (`server migrate`) |
| `make migration name=<module>_<what>` | Create the next numbered goose file in `migrations/` |
| `make dev` / `make run` | API with live reload / once, on `:8080` |
| `make generate` | Regenerate `internal/apigen` (oapi-codegen, from `api/openapi.yaml`) and `*/sqlcgen` (sqlc, from `migrations/` + `queries.sql`); commit the output — CI fails on drift |
| `make fmt` · `make lint` | Format · lint (incl. depguard architecture rules) |
| `make test` · `make test-all` | Unit tests · all tests incl. database (`TEST_DATABASE_URL`) |
| `make check` | **What CI runs** — generate, tidy, lint, all tests, build. Run before every push. |
| `make docker-build` · `make app-up BARBERSHOP_IMAGE=…` · `make app-down` | Build the production image · run an image (local or a GHCR release) with Compose |

Binary roles: `server api` (default), `server migrate`; `worker` arrives in M3. Config is env vars only — see `.env.example`.
`main.version` is stamped at build time (`-ldflags -X`); releases are cut by merging release-please's Release PR — never tag by hand.
Database tests skip unless `TEST_DATABASE_URL` is set (in CI a missing URL fails); each test gets its own database (ADR-0012).
Login locally: `make run`, `POST /v1/auth/otp/request`, read the code from the API log (`SMS_PROVIDER=console`), `POST /v1/auth/otp/verify`.

## 12. Don'ts

No ORM · no cross-module imports of internals · no float money · no `time.Now()` in domain · no
secrets in code/logs/images · no hand-edited generated code · no edited merged migrations · no new
dependency without a reason in the PR · no skipping tests to pass CI.
