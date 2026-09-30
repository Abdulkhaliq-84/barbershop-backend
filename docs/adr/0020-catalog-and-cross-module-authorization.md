# ADR-0020: Catalog as its own module; other modules ask business who may work on a branch

- Status: Accepted · Date: 2026-09-30 · Builds on [ADR-0015](0015-business-tenancy-and-authorization.md)

## Context
M4.1 adds the service menu: what each branch sells, for how long and at what price. Services
belong to branches, and branches and staff belong to `business`. This is the first module
outside `business` that keeps tenant data. It needs the same membership-first authorization
without copying staff data or reading `business`'s tables.

## Decision
- **`catalog` is its own module** with its own schema (`catalog.services`).
  - It stores `business_id` and `branch_id` without foreign keys into `business` (ADR-0015),
    and every query is scoped by both.
  - Categories (haircut, beard, shave, kids, skin care, colour, packages) are reference data in
    code, like plans. Platform admins will manage them later.
- **`business.Module.AuthorizeBranch(actor, business, branch, role)`** is the one question other
  modules ask. It checks three things, in this order:
  1. The caller is active staff of the business with the role. Otherwise `ErrNotFound`, so
     strangers learn nothing; a role that is too small gets `ErrForbidden`.
  2. The branch is the business's. Otherwise `ErrNotFound`. Without this, an owner (who "works
     at every branch") could write into another shop's branch by putting its ID under their own
     business. A mutation test shows exactly that.
  3. The caller works at the branch (the owner works at all of them). Otherwise `ErrForbidden`.
- `catalog` calls it through `adapters/acl`, translating `business`'s errors into its own.
  Lint rules forbid `catalog` from importing `business` internals, and forbid `business` from
  importing `catalog` (that would be an import cycle).
- **Who may do what:** anyone working at the branch lists its services. The owner, or a manager
  of that branch, adds and edits them.
- **Services are never deleted**, only deactivated (`active: false`): appointments will point
  at them. Edits use the same `If-Match` version check as branches.
- **Rules:**
  - duration 5–480 minutes, in 5-minute steps;
  - price 0–100,000 SAR, in halalas, VAT-inclusive, SAR only;
  - Arabic name required; name ≤ 80 characters, description ≤ 500;
  - sort order 0–1000.

  The database repeats each rule as a `CHECK`.

## Consequences
- Every catalog request makes one extra call into `business` (two indexed reads). That is
  cheap, and it keeps one source of truth for who works where.
- Later, `business` will need to know whether a branch has active services before it can be
  published. It can't import `catalog`, so that will come through an event or a function wired
  in `main`, like billing's trial (ADR-0019).
- `scheduling` (M4.3) and barber offerings (M4.2) use the same `AuthorizeBranch`.
- No catalog events yet (`ServiceCreated`…). They arrive with their first subscriber, discovery
  (M6).
