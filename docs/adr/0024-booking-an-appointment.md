# ADR-0024: Booking — the key first, the database decides, one lock per barber shared with scheduling

- Status: Accepted · Date: 2026-09-30 · Builds on [ADR-0008](0008-double-booking-exclusion-constraint.md), [ADR-0021](0021-scheduling-time-model.md) and [ADR-0023](0023-availability.md)

## Context
M5.3 lets a signed-in customer book: `POST /v1/appointments` with a branch, a start time, 1–5
services and, optionally, a barber. Between showing a slot and booking it, a lot can change:
- someone else books the same barber;
- the barber's hours change or they add time off;
- the customer's app times out and sends the request again.

Four questions follow: how a retry gets the first answer rather than a second appointment, who
has the last word on double booking, how a schedule change can't slip in while a booking checks
the hours, and how "any barber" picks one.

## Decision
- **An `Idempotency-Key` header is required**, a UUID the app makes per booking attempt. The
  table `booking.idempotency_keys` keeps `(customer, key)` with a SHA-256 of what was asked, and
  the appointment it made.
  - **The key is looked up first**, before any rule: a retry gets its appointment (`201`,
    `Idempotent-Replayed: true`) even though the time is now taken — by that very appointment —
    or the lead time has passed meanwhile.
  - The same key with a different request is `422 idempotency_key_reused`.
  - Inside the booking transaction the key is claimed with `INSERT … ON CONFLICT DO NOTHING`.
    Two requests with the same key at once: the second waits for the first. If it committed, the
    second replays it; if it rolled back, the second goes ahead. A failed booking keeps no key,
    so the app can retry it.
- **The database has the last word** (ADR-0008). The exclusion constraint refuses two active
  appointments of one barber that overlap: `409 slot_unavailable`, and the app asks for the
  availability again. The use case checks the rules first to fail early and explain why, but the
  constraint decides.
- **One lock per barber, shared with scheduling**: `database.LockStaff`, a transaction-scoped
  advisory lock on the staff id. A booking takes it, re-reads the barber's working windows
  (`StillWorking`), then inserts. Changing a schedule and adding time off take the same lock. So
  hours can't be taken away between the check and the insert: one of them waits for the other.
- **"Any barber"** tries the free barbers in order: fewest booked minutes that day first (spreading
  the work), then by id so the order is stable. Each try runs in a **savepoint**: if the
  constraint refuses that barber, or they no longer work then, the savepoint is rolled back —
  their lock with it — and the next barber is tried. One transaction, one lock held at a time.
- **A limit on upcoming bookings** (`max_active_bookings` from the branch policy): counted under a
  per-customer-per-branch advisory lock, so two requests at once can't both take the last place
  (`409 booking_limit_reached`).
- **At most half the pool books at once.** `StillWorking` runs inside the booking's transaction
  but reads on another connection. If bookings held every connection, each would wait for a
  second one forever; a semaphore of half the pool (at least 1) keeps one free.
- **The appointment is a snapshot**: each item keeps the service's name, and the barber's duration
  and price at booking time. Later menu changes don't change a booked price.
- **Status**: `confirmed` if the branch auto-confirms, otherwise `pending` until
  `now + pending_expiry` (expiry itself is M5.5).
- **`booking.appointment_booked` is published in the same transaction** (the outbox): the
  appointment and its event commit together or not at all.

## Consequences
- A customer's key is theirs alone: another customer's request with the same UUID is a new
  request.
- Keys are kept for now. Pruning old ones is a scheduled job, once M5.5 brings scheduled jobs.
- Error codes: `409 slot_unavailable`, `409 booking_limit_reached`, `422 invalid_start` (off the
  clock grid, inside the lead time, past the horizon), `422 idempotency_key_reused`, and the
  availability ones (`service_unavailable`, `barber_unavailable`).
- A retry after the first request failed is a new attempt: it may book a different barber, or
  none.
