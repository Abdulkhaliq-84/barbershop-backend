---
name: scaffold-module
description: Recipe and checklists for adding code to this Go DDD modular monolith the way the architecture requires — a new bounded-context module, an aggregate or value object, a command/query use case, an HTTP endpoint (OpenAPI-first), a repository with sqlc, a migration, a domain event + handler, or a background job. Use whenever implementing a roadmap milestone step or any backend feature here, even if the user just says "add the endpoint", "implement booking", "next step", "scaffold", or names a module (iam, business, catalog, scheduling, booking, discovery, billing, notification, media).
---

# Scaffolding a slice in barbershop-backend

Follow `CLAUDE.md` (rules) and `docs/architecture/overview.md` (layout). This skill is the **order of
work** and the **checklists**. The owner is learning Go: build the first instance of a pattern, explain
it in the PR, and leave the next instance as the "Your turn" exercise.

## 0. Before writing code

1. Find the roadmap step in `docs/PLAN.md` §5 and the rules in `docs/architecture/domain-model.md` for the module.
2. Name things with the ubiquitous language (glossary in the domain model).
3. Decide the slice: one use case (+ its endpoint, persistence, tests) per PR.
4. If a decision is new or changes an ADR → write the ADR in the same PR.

## 1. Order of work (outside-in contract, inside-out code)

1. **Contract** — add/modify the operation in `api/openapi.yaml` (shared schemas: `Problem`, `Money`,
   `LocalizedText`, cursor page). Regenerate (`make generate`).
2. **Domain** (`internal/<module>/domain/`) — value objects, aggregate, domain errors, events, repository
   interface. Pure Go: stdlib + `internal/shared` only. **Write table-driven tests first** for invariants and state transitions.
3. **Application** (`internal/<module>/app/command|query/`) — handler struct with dependencies as small
   interfaces (`ports.go`), authorization policy check **first** (membership for business routes), then load →
   call aggregate behaviour → save via update-function. Test with in-memory fakes + fake clock.
4. **Migration** (`migrations/NNNN_<module>_<what>.sql`, goose) — tables in the module's schema,
   `business_id` on tenant-owned rows, constraints that protect invariants, indexes for the queries.
5. **Persistence** (`internal/<module>/adapters/postgres/`) — `queries.sql` → sqlc (`sqlcgen/`), repository
   mapping rows ↔ domain via `Rehydrate`; events inserted to the outbox (River `InsertTx`) in the same tx.
   Integration test with testcontainers (PostGIS image).
6. **HTTP adapter** (`internal/<module>/adapters/http/`) — implement the generated strict-server interface;
   map DTO → command, domain errors → problem+json codes; no business logic here.
7. **Cross-module needs** — call the other module's **root package** API through an ACL adapter in
   `adapters/acl/` that implements this module's port. Never import its internals.
8. **Wiring** — `internal/<module>/module.go` (`New(deps)`, routes, event subscriptions) and `cmd/server`.
9. **Docs** — update `docs/api/overview.md` / domain model if behaviour changed; README roadmap checkbox when a milestone completes.

## 2. Shapes to copy

```go
// domain: aggregate with invariants and recorded events
type Branch struct {
    id       BranchID
    business shared.BusinessID
    name     shared.LocalizedText
    status   BranchStatus
    events   []Event
}

func NewBranch(id BranchID, biz shared.BusinessID, name shared.LocalizedText, now time.Time) (*Branch, error) {
    if name.Ar() == "" {
        return nil, ErrArabicNameRequired
    }
    b := &Branch{id: id, business: biz, name: name, status: BranchDraft}
    b.record(BranchCreated{BranchID: id, BusinessID: biz, At: now})
    return b, nil
}

// domain: repository port (update-function pattern keeps the transaction in the adapter)
type BranchRepository interface {
    Add(ctx context.Context, b *Branch) error
    Update(ctx context.Context, biz shared.BusinessID, id BranchID, fn func(*Branch) error) error
}

// app: command handler — authorize first, then domain behaviour
type PublishBranchHandler struct {
    branches domain.BranchRepository
    members  MembershipChecker // small port, implemented by an adapter
    clock    clock.Clock
}

func (h PublishBranchHandler) Handle(ctx context.Context, cmd PublishBranch) error {
    if err := h.members.Require(ctx, cmd.Actor, cmd.BusinessID, RoleOwner); err != nil {
        return err
    }
    return h.branches.Update(ctx, cmd.BusinessID, cmd.BranchID, func(b *domain.Branch) error {
        return b.Publish(h.clock.Now())
    })
}
```

## 3. Checklists

**Domain**
- [ ] Unexported fields; constructor returns `(T, error)`; behaviour methods enforce invariants.
- [ ] No `time.Now()`, no pgx/http/json imports, no other modules.
- [ ] Sentinel/typed errors with stable names (they map to API `code`s).
- [ ] Events recorded for every state change other modules care about.
- [ ] Table-driven tests for every invariant and transition (fuzz for algorithms like availability).

**Application**
- [ ] Authorization (BOLA) checked first; tenant `BusinessID` passed explicitly.
- [ ] Ports are small interfaces defined here; one transaction per command.
- [ ] Queries read optimised SQL / read models, not aggregates.

**Persistence**
- [ ] Every tenant-scoped query filters by `business_id` (a required parameter).
- [ ] Invariants that must survive concurrency are DB constraints (e.g. the booking `EXCLUDE`).
- [ ] Money `bigint` halalas, instants `timestamptz`, IDs UUIDv7 from Go.
- [ ] Migration is forward-only, reversible in intent (expand → migrate → contract), tested on a fresh DB in CI.

**HTTP**
- [ ] Spec updated first; generated code untouched by hand.
- [ ] Errors → RFC 9457 with stable `code`; `Idempotency-Key` on creates; cursor pagination on lists.
- [ ] `Accept-Language` respected for localised text and messages.

**PR** (template in `.github/pull_request_template.md`)
- [ ] Conventional Commit title (`feat(<module>): …`).
- [ ] "Go concepts introduced (Node → Go)" filled; "Your turn" exercise set.
- [ ] `make check` green locally; no new dependency without a reason.

## 4. Events and jobs

- Define the event in the producing module's `domain/events.go` (past tense: `AppointmentBooked`).
- Publish via the outbox in the same transaction; subscribers register in their `module.go`.
- Handlers are **idempotent** (upsert, dedupe by event ID, re-check state) — delivery is at-least-once.
- Scheduled work (reminders, expiries) = River jobs; the job re-reads current state before acting.
