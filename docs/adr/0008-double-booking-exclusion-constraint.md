# ADR-0008: Prevent double booking with a Postgres exclusion constraint

- Status: Accepted · Date: 2026-09-23

## Context
Two customers can pick the same slot at the same time. Checking availability in Go and then inserting
is a race. Alternatives: a "barber-day" aggregate locked on every booking (contention, complexity),
distributed locks (Redis), or serializable transactions with retries.

## Decision
Store each appointment's time as a `tstzrange` and add
`EXCLUDE USING gist (barber_id WITH =, during WITH &&) WHERE (status IN ('pending','confirmed'))`.
The application computes availability (advisory), inserts, and translates the exclusion violation
(SQLSTATE `23P01`) into the domain error `ErrSlotUnavailable` → HTTP `409 slot_unavailable`.
For "any barber", the next free barber is tried before failing.

## Consequences
- The most important invariant is guaranteed by the database, even with many API instances.
- The invariant lives partly outside the Go domain model — documented here and covered by an
  integration test that races concurrent bookings.
- Requires the `btree_gist` extension.
