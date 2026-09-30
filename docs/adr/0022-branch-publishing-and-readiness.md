# ADR-0022: Publishing a branch asks catalog and scheduling through main

- Status: Accepted · Date: 2026-09-30 · Builds on [ADR-0020](0020-catalog-and-cross-module-authorization.md) and [ADR-0021](0021-scheduling-time-model.md)

## Context
M5 starts customer booking, and customers must only see branches that can take a booking. Publishing
a branch is `business`'s decision (the branch is its aggregate). But whether the branch is ready
depends on two other modules:
- `catalog` knows its services and who performs them;
- `scheduling` knows its opening hours and the barbers' schedules.

Both of those modules already depend on `business` (they ask it who may work on a branch). If
`business` imported them, Go would refuse the import cycle. And it would be the wrong direction
anyway: the core tenancy module shouldn't know about menus or calendars.

## Decision
- **`business` declares what it needs, as a port.** `business.ReadinessChecker` answers
  `business.Readiness` for a branch:
  - `OpeningHours`: the branch has opening hours.
  - `OfferedService`: an active service that at least one barber performs.
  - `BookableBarber`: one of those barbers has a weekly schedule at the branch.
- **`main` implements the port** (`branchReadiness` in `cmd/server`). It asks
  `catalog.PerformingStaff`, then `scheduling.Readiness` for those staff. `main` already sees every
  module, so it is the one place that can put the answer together. The struct is created empty,
  given to `business.New`, and filled in once `catalog` and `scheduling` exist.
- **Publishing** (`POST …/branches/{id}/publish`, owner, `If-Match`):
  - the business must be `active` (`409 business_not_active`);
  - the branch must be ready (`409 branch_not_ready`; `detail` lists what to do, in our own words);
  - `draft` or `unpublished` → `published`.
- **Unpublishing** (`published` → `unpublished`) hides the branch. Bookings already made stay.
- Both record an event (`business.branch_published`, `business.branch_unpublished`), published in
  the same transaction as the change. Discovery (M6) will list and delist branches from them.
- **Readiness is a gate, not an invariant.** It is read before the branch row is locked, because it
  lives in other modules. A schedule removed a minute after publishing leaves a published branch
  with no free slots, and customers simply see none. Availability (M5.2) always computes from the
  live data.

## Consequences
- New cross-module questions follow the same shape: the module that decides declares a port, and
  `main` answers it from the modules that know. No import cycles, and dependencies stay one-way.
- The readiness reads authorize nobody (`PerformingStaff`, `Readiness`). They are only reachable
  from `main`'s adapter, after `business` has checked that the caller is the owner.
- Plan limits on *published* branches (a business back on Free with more branches than it allows)
  come with the unpublishing job described in ADR-0019.
