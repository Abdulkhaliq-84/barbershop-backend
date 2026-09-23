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
