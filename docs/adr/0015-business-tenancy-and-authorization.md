# ADR-0015: Business tenancy — membership-first authorization, CR claimed on submission, no cross-module foreign keys

- Status: Accepted · Date: 2026-09-27 · Builds on [ADR-0004](0004-multi-tenancy-shared-schema.md) and [ADR-0014](0014-sessions-and-access-tokens.md)

## Context
M3.1 adds the second module, `business`: an owner registers a business (the tenant) and becomes its
first staff member. Four questions come with it:

- how a business-mode request decides whether the caller may act on the business in its path
- what the answer looks like when they may not
- when a Commercial Registration (CR) number becomes unique
- how two modules' tables relate

## Decision
- **Membership first.** Every business-mode use case starts with `authorize`
  (`internal/business/app/policy.go`). It loads the caller's staff record for the business named in
  the path and checks the role (`owner` ⊃ `manager` ⊃ `barber`). The check runs before anything
  else is loaded, in the application layer, never only in HTTP handlers.
  - The caller comes from the access token (`auth.PrincipalFrom`), never from the request body.
  - Business roles are not in the JWT (ADR-0014). They are read per request, so a role change or
    a removed barber takes effect on the next request, not when a token expires.
  - Platform admins get no shortcut here: they act through `/v1/admin/*` routes (later in M3).
- **404 for strangers, 403 for staff.** A caller who is not active staff of the business gets
  `404 not_found`, exactly as if it didn't exist, so IDs can't be probed. Staff whose role is too
  small get `403 forbidden`; they already know the business exists.
- **Business and owner are created together.** `RegisterBusiness` returns both, and one transaction
  saves both. The database keeps "at most one owner per business" (a partial unique index).
- **The CR number is claimed on submission, not registration.**
  - A draft checks the format only: 10 digits, with Arabic-Indic digits and spaces accepted.
  - A partial unique index makes the number unique among `pending_review`, `active` and `suspended`
    businesses. Nobody can block a real shop by registering its number as a draft, and the reviewer
    checks the CR document before approving.
  - `(owner_user_id, cr_number)` is unique. A retried registration gets
    `409 business_already_registered` instead of a second draft. That makes creating a business
    safe to retry without an `Idempotency-Key` store; that store arrives with bookings (M5).
- **No foreign keys across module schemas.** `business.*.user_id` references `iam.users` by value
  only. Modules meet through their root packages, never through each other's tables, so either can
  change or move its storage alone. The user IDs come from verified access tokens.
- **Module boundaries are linted.** depguard rules forbid `business` from importing `iam`'s
  internals and the reverse. Tests that need several modules together live in `cmd/server`, which
  wires the real server.

## Consequences
- The same `authorize` call opens every later business-mode use case (branches, staff, services,
  schedules, bookings). Forgetting it is the bug to look for in review; app tests check that a
  refused caller causes no load.
- Two drafts can share a CR number. The second to submit gets a conflict at submission (M3.4), which
  is where a human reviewer is involved anyway.
- The business module can't rely on the database to reject an unknown user ID. It doesn't need to:
  IDs come only from tokens signed by iam.
- Orphans are possible if a user is ever hard-deleted. Account deletion (PDPL) will anonymise users
  instead, and will be designed with its own event.
