# Node.js → Go: the mindset shift

A living guide written while building this project. Each milestone adds the idioms it introduced.

## 1. The big shifts

| In Node.js you… | In Go you… | Why it matters here |
|---|---|---|
| `throw` / `try…catch` | return `error` as the last value and handle it right there | Every failure path in booking is visible in the code — no surprise crash in the middle of a transaction |
| Write classes with `this` | Write `struct`s with methods; composition, not inheritance | Aggregates are structs with unexported fields + behaviour methods |
| `implements Interface` explicitly | Satisfy interfaces **implicitly** (just have the methods) | Define tiny interfaces in the consumer (`app/ports.go`); adapters satisfy them without knowing |
| Rely on the event loop, `async/await` | Write blocking code; each HTTP request already runs in its own goroutine | Straight-line code, no promise chains; concurrency only where you ask for it |
| `Promise.all` | `errgroup.Group` | Loading windows for several barbers in parallel |
| `AbortController` / timeouts per library | `context.Context` passed everywhere | Request cancelled → DB query cancelled automatically |
| DI containers / decorators (Nest) | Plain constructors wired by hand in `main` | You can read `main.go` and see the whole app |
| `export` / `private` keywords | Capitalised names are exported, lowercase are package-private | Domain fields are lowercase → only methods can change state |
| One file = one module | One **directory** = one package | Folder structure *is* the architecture |
| `npm install` a package per tiny need | Standard library first (`net/http`, `log/slog`, `crypto`, `time`, `testing`) | Fewer dependencies to trust and update |
| `undefined` / `null` everywhere | Zero values (`""`, `0`, `nil`, empty struct) — design types so zero is useful or invalid-by-construction | Constructors return `(T, error)` so invalid values can't exist |
| Runtime type checks (zod) | The compiler checks types; validation is for business rules | Value objects: `NewPhoneNumber`, `NewMoney` |
| Jest + mocks everywhere | `go test`, table-driven tests, small hand-written fakes | Domain tests need no mocks at all |
| Prisma generates a client from a schema | sqlc generates Go from **your SQL** | You keep full control of queries (PostGIS, exclusion constraints) |

## 2. Idioms you will meet first (M1–M2)

```go
// Errors: wrap with context, check with errors.Is / errors.As
if err := repo.Save(ctx, user); err != nil {
    return fmt.Errorf("register user: %w", err)
}
if errors.Is(err, domain.ErrOTPExpired) { /* map to 400 otp_expired */ }

// Constructors enforce invariants; fields stay unexported
type PhoneNumber struct{ e164 string }
func NewPhoneNumber(raw string) (PhoneNumber, error) { /* parse, validate, normalise */ }
func (p PhoneNumber) String() string { return p.e164 }

// Interfaces are small and live where they're used
type OTPSender interface {
    SendOTP(ctx context.Context, to PhoneNumber, code string) error
}

// Table-driven tests
func TestNewPhoneNumber(t *testing.T) {
    tests := []struct {
        name    string
        in      string
        wantErr bool
    }{
        {"valid saudi mobile", "+966512345678", false},
        {"local format", "0512345678", false},
        {"landline", "+966112345678", true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := NewPhoneNumber(tt.in)
            if (err != nil) != tt.wantErr {
                t.Fatalf("NewPhoneNumber(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
            }
        })
    }
}
```

## 2b. What M1 introduced (read the code next to this)

| Idiom | Where | Node.js equivalent |
|---|---|---|
| `main` stays tiny; `run(ctx, args, environ, stdout) error` does the work | `cmd/server/main.go` | `main()` wrapped in `try/catch` + `process.exit(1)` — but testable |
| `signal.NotifyContext` → a context cancelled on SIGTERM | `cmd/server/main.go` | `process.on('SIGTERM', …)` |
| Graceful shutdown: `srv.Shutdown(ctx)` with a deadline | `internal/platform/httpx/server.go` | `server.close()` + a timeout |
| A goroutine + buffered channel to run the server, `select` to wait | `httpx/server.go` | a Promise you `await` alongside a signal |
| Middleware = `func(http.Handler) http.Handler` | `httpx/middleware.go` | Express `(req, res, next)` |
| Values in `context.Context` with an unexported key type | `httpx/middleware.go` (request ID) | `res.locals` / AsyncLocalStorage |
| `defer` + `recover()` turns a panic into a 500 | `httpx/middleware.go` | an Express error handler |
| Interface declared where it's used, satisfied implicitly (`Pinger` ← `*pgxpool.Pool`) | `httpx/health.go` | duck typing — but checked by the compiler |
| Hand-written fakes instead of a mocking library | `httpx/httpx_test.go` (`fakePinger`) | `jest.fn()` |
| Typed errors: `errors.As(err, &target)` | `config/config.go` (`withEnvNames`) | `err instanceof ParseError` |
| `errors.Join` to report several problems at once | `config/config.go` | `AggregateError` |
| Struct tags drive parsing (`env:"HTTP_ADDR" envDefault:":8080"`) | `config/config.go` | decorators / zod schemas |
| `//go:embed *.sql` bakes files into the binary | `migrations/migrations.go` | bundling assets with a build step |
| Generics for small helpers: `decode[T any]` | `httpx/httpx_test.go` | TypeScript generics |
| `httptest.NewRecorder` + `router.ServeHTTP` | `httpx/httpx_test.go` | supertest |
| `t.Skip` when an integration dependency is missing | `database/database_test.go` | `describe.skip` / `test.skipIf` |
| `t.Parallel()` + `-race` | all tests | jest workers — plus a data-race detector Node doesn't have |

Try this: run `make test-all`, then break something on purpose (e.g. remove `defer cancel()` in
`health.go`) and see which linter or test catches it.

## 2c. What M1.5 introduced (delivery)

| Idiom | Where | Node.js equivalent |
|---|---|---|
| A Go binary is static: the runtime image needs no Go, no libc | `Dockerfile` (distroless static) | `node:alpine` must ship the Node runtime and `node_modules` |
| Cross-compiling with `GOOS`/`GOARCH` (no emulator) | `Dockerfile` (`--platform=$BUILDPLATFORM`) | prebuilt native addons per platform (node-gyp) |
| Stamping values at link time: `-ldflags "-X main.version=v0.3.0"` | `Makefile`, `Dockerfile`, `cmd/server/main.go` | `process.env.npm_package_version` / a bundler `define` |
| Multi-stage build: compile stage + tiny runtime stage | `Dockerfile` | `npm ci && npm run build` then copy `dist/` into a slim image |
| `govulncheck` reports only vulnerabilities in code you actually call | `security.yml` | `npm audit` (reports everything in the tree) |
| release-please: Conventional Commits → SemVer + changelog | `cd.yml`, `release-please-config.json` | semantic-release / changesets |
| Build once, promote by digest | `cd.yml` (`promote` job) | re-running the build per environment — which we avoid |

Try this: after the first release, run `gh attestation verify` on the image and read which workflow and
commit produced it — that's supply-chain provenance you can check yourself.

## 2d. What M2 introduces — part 1: the shared kernel

| Idiom | Where | Node.js equivalent |
|---|---|---|
| **Value objects**: unexported fields + validating constructor = a value that is valid by construction | `internal/shared/*.go` | a class with a private constructor + zod parse |
| Sentinel errors checked with `errors.Is` | `ErrInvalidPhoneNumber`, `ErrCurrencyMismatch` … | `err.code === 'INVALID_PHONE'` |
| Generics with a **phantom type**: `ID[UserTag]` and `ID[BranchTag]` can't be mixed up | `internal/shared/id.go` | TypeScript "branded" types |
| `slog.LogValuer`: a type decides how it appears in logs (phone numbers log masked) | `internal/shared/phone.go` | a custom `toJSON()` / pino redaction |
| Integers for money, with overflow checks | `internal/shared/money.go` | `dinero.js` / integer cents |
| Runes vs bytes: `for _, r := range s` walks Unicode characters (Arabic digits) | `NewPhoneNumber` | `for (const ch of str)` |
| **Fuzzing** built into `go test` | `FuzzNewPhoneNumber`, `FuzzIntervalOverlaps` | fast-check (a library in JS) |
| **Example tests**: documentation whose output is verified | `internal/shared/example_test.go` | doctests (not built into Node) |
| A `Clock` interface so tests control time | `internal/platform/clock` | `jest.useFakeTimers()` |

Try this: `go test ./internal/shared -fuzz=FuzzNewPhoneNumber -fuzztime=1m`, then `go doc ./internal/shared PhoneNumber`.

## 2e. What M2 introduces — part 2: phone OTP login

| Idiom | Where | Node.js equivalent |
|---|---|---|
| **Contract first**: write `api/openapi.yaml`, generate types + a server interface, implement it; the compiler says what's missing | `api/`, `internal/apigen`, `adapters/httpapi` | tsoa / zod-openapi, but generated *from* the spec |
| Request validation middleware driven by the same spec | `httpx.MountAPI` (kin-openapi) | express-openapi-validator |
| **sqlc**: write SQL, get typed Go functions — no ORM, no query builder | `adapters/postgres/queries.sql` → `sqlcgen/` | Prisma's typed client, but from your SQL |
| Hexagonal **ports**: the use case needs "something that sends an SMS", not Twilio | `app/ports.go`, `adapters/sms` | dependency injection with interfaces (NestJS providers) |
| **Update-function pattern**: `repo.UpdateLatest(ctx, phone, func(c *OTPChallenge) error {…})` — lock, change, save in one transaction | `domain/repository.go`, `adapters/postgres` | `prisma.$transaction(async tx => …)` |
| `SELECT … FOR UPDATE` against parallel guesses — and a test that fails without it | `repository_test.go` | the same SQL; Node devs often skip the test |
| `crypto/subtle.ConstantTimeCompare`, HMAC-SHA256, `crypto/rand` | `domain/otp.go`, `adapters/otpcode` | `crypto.timingSafeEqual`, `createHmac`, `randomInt` |
| Error types carrying data: `*RetryLaterError` + `errors.As` → `Retry-After` header | `domain/errors.go`, `httpapi/handlers.go` | `class RetryLaterError extends Error { after }` + `instanceof` |
| In-memory **fakes** of ports: use-case tests in microseconds | `app/app_test.go` | hand-written fakes instead of `jest.mock` |
| A throwaway database per test; `httptest.Server` for end-to-end | `dbtest`, `internal/iam/iam_test.go` | supertest + a test DB per worker |

Try this: `make run`, then
`curl -s localhost:8080/v1/auth/otp/request -H 'content-type: application/json' -d '{"phone":"0551234567"}'`,
read the code from the log, and verify it with `/v1/auth/otp/verify`. Then guess wrong six times and read the errors.

## 2f. What M2 introduces — part 3: tokens and sessions

| Idiom | Where | Node.js equivalent |
|---|---|---|
| **Auth declared in the contract**: a global `bearerAuth` security scheme; public operations opt out with `security: []` | `api/openapi.yaml`, `httpx.MountAPI` | a route-level `passport.authenticate()` you must remember to add; here forgetting it fails closed |
| Values in `context.Context` with an unexported key type, read back with a typed getter | `internal/platform/auth` | `req.user` set by middleware (typed, and impossible to collide with) |
| JWT with a **pinned algorithm** (`WithValidMethods`) against `alg: none` / key-confusion attacks | `adapters/tokens/access.go` | `jwt.verify(token, key, { algorithms: ['EdDSA'] })` |
| Ed25519 from the standard library (`crypto/ed25519`) | same | `crypto.generateKeyPairSync('ed25519')` |
| Update-function repository where **a refusal must still commit** (reuse revokes the session) | `app/sessions.go` `RefreshHandler` | a Prisma transaction that writes, then throws *after* commit |
| `SELECT … FOR UPDATE OF t, s` locks two joined rows, **and a test that fails without it** | `adapters/postgres` `TestSessionsParallelRefreshesTakeTurns` | same SQL; the test is the part people skip |
| Forged-token table test: none-alg, HS256-with-public-key, tampered claims, wrong `kid`/`iss`/`aud` | `adapters/tokens/tokens_test.go` | jest cases per attack |

Try this: sign in, then call `GET /v1/me` with the token, refresh twice with the *same* refresh token and
watch the second call end the session (`refresh_token_reused`).

## 2g. What M3 introduces — part 1: a second module and authorization

| Idiom | Where | Node.js equivalent |
|---|---|---|
| **Authorize first, in the use case**: load the caller's membership, check the role, then load data | `internal/business/app/policy.go` | a `canAccess(user, shopId)` guard at the top of each service method, not only in Express middleware |
| **404 for strangers, 403 for staff**, so IDs can't be probed | same | returning 404 instead of 403 in a NestJS guard |
| A factory that returns **two objects created together** (business + owner) and one transaction that saves both | `domain/business.go` `RegisterBusiness`, `adapters/postgres` `Register` | `prisma.$transaction([createShop, createOwner])` |
| **Let the database decide duplicates**: map a unique-index violation (`23505` + constraint name) to a domain error | `adapters/postgres/repository.go` | catching Prisma `P2002` |
| **Partial unique index**: unique only for some statuses | `migrations/00005_business_onboarding.sql` | the same SQL; ORMs rarely express it |
| **Read model**: a query-shaped struct for one screen, never saved back | `app.MembershipView`, `ForUser` | a hand-written `SELECT … JOIN` next to the ORM models |
| **Embedding two types with the same name** needs aliases (`type iamAPI = iamhttp.Handlers`) | `cmd/server/main.go` | mixing two classes into one object with `Object.assign` |
| Module boundaries enforced by the linter (depguard) | `.golangci.yml` | `eslint-plugin-boundaries` |
| A whole-server test through HTTP only | `cmd/server/api_test.go` | supertest against the real `app` |

| **Optimistic concurrency**: `If-Match: <version>`, a row lock, and `UPDATE … WHERE version = $n` (412 on a stale version) | `adapters/postgres` `Update`, `TestStoreParallelUpdatesOfTheSameVersion` | Mongoose's `versionKey` / `__v` check |
| A **value object** compared with `==` (`BookingPolicy`, `BranchProfile`): all fields are comparable values | `domain/booking_policy.go` | deep-equal on a frozen object |
| **Struct conversion** between two types with identical fields (`sqlcgen.InsertBranchParams(row)`), checked by the compiler | `adapters/postgres/branches.go` | a spread `{...row}` that nothing checks |
| An error type carrying a safe message (`*PolicyError`) matched with `errors.As` | `domain/booking_policy.go` | `class PolicyError extends Error` + `instanceof` |

| **Streaming an upload** through `io.TeeReader` + `io.LimitReader` into storage while hashing and counting — never the whole file in memory | `internal/media/app/app.go` `Store` | piping a `req` stream through a hash transform into `fs.createWriteStream` |
| `os.Root`: file access that **cannot escape a directory** (no `..`, no symlinks out) | `internal/media/adapters/disk` | hand-rolled `path.resolve` checks |
| **HMAC-signed links** checked with `hmac.Equal` (constant time) | `internal/media/adapters/signer` | `crypto.timingSafeEqual` |
| An **anti-corruption layer**: business's own port, implemented over another module's public API | `internal/business/adapters/acl/media.go` | a wrapper service that maps another team's SDK to your types |

| **Keyset pagination**: `WHERE (submitted_at, id) > ($1, $2) ORDER BY submitted_at, id LIMIT n+1` and an opaque cursor | `adapters/postgres` `ReviewPage`, `app.ReviewHandlers.Queue` | cursor pagination in a GraphQL connection; never `OFFSET` |
| A local closure `fail := func(err error) (…)` to keep every error return one line | `adapters/httpapi/review.go` | a small `sendError(res, err)` helper |
| A **state machine** as methods on the aggregate (`Submit`, `Approve`, `Reject`) that refuse illegal moves | `domain/review.go` | xstate guards |

| **Random tokens, stored as a hash**: `crypto/rand` → base64url, keep only `sha256` | `business/adapters/invites` | `crypto.randomBytes(32).toString('base64url')` + `createHash('sha256')` |
| A **method value** as a callback: `(*domain.Invitation).Revoke` is a `func(*Invitation) error` | `app.StaffHandlers.Revoke` | passing `Invitation.prototype.revoke.call` — but type-checked |
| **Postgres arrays** (`uuid[]`) and `array_agg(...) FILTER (WHERE ...)` mapped to `[]uuid.UUID` by sqlc | `queries.sql` `StaffByBusiness` | `pg` returning arrays; no ORM join table needed to *read* |
| **Composite foreign keys** `(business_id, branch_id)` so the database refuses cross-tenant rows | `migrations/00010` | a check you would otherwise write in every service |

Try this: register a business, then call `GET /v1/businesses/{id}` with another user's token and
compare the answer with an ID that doesn't exist. They should be identical.

## 3. Pointers vs values (the question everyone asks)

- Use **values** for small immutable things: value objects (`Money`, `PhoneNumber`, `Interval`).
- Use **pointers** for aggregates you mutate (`*Appointment`) and for large structs.
- Be consistent per type: if one method needs a pointer receiver, give all methods pointer receivers.

## 4. Things that will feel strange (and are fine)

- `if err != nil` everywhere — it's the point: every failure is handled where it happens.
- No generics-heavy abstractions — use generics for small utilities, not to rebuild inheritance.
- No "repository base class" — a bit of repetition beats a clever abstraction.
- `gofmt` decides formatting — no style debates.

## 5. Reading list

- *Effective Go* and *Go Code Review Comments* (go.dev) — the idioms reviewers expect.
- *Go with the Domain* (Three Dots Labs) + the **Wild Workouts** example repo — DDD in Go.
- *100 Go Mistakes and How to Avoid Them* — Teiva Harsanyi.
- *Learn Go with Tests* — Chris James.
- sqlc, pgx and River documentation — the libraries we use daily.
