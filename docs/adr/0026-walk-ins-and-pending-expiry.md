# ADR-0026: Walk-ins and pending expiry — the shop books by name, a scheduled task expires what wasn't answered

- Status: Accepted · Date: 2026-10-01 · Builds on [ADR-0009](0009-domain-events-outbox-river.md), [ADR-0024](0024-booking-an-appointment.md) and [ADR-0025](0025-appointment-lifecycle.md)

## Context
M5.5 finishes the booking core with two things:

- **The shop books for people without the app.** Someone walks in, or phones. They have no
  account, the shop knows which barber is free, and the haircut may already have started when
  someone records it.
- **Pending bookings the shop never answers.** Since M5.4 a pending booking past its
  `pending_until` *reads* as expired, but its row still says `pending`, so the exclusion
  constraint keeps the barber's time blocked and no `appointment_expired` event goes out.

The second needs something the platform didn't have yet: a task that runs on a schedule.

## Decision
- **Walk-ins:** `POST /v1/businesses/{id}/branches/{id}/appointments` with an `Idempotency-Key`
  and `{starts_at, service_ids, barber_id, customer_name, note?}`.
  - **Who:** the owner, the branch's managers, and a barber for themselves. Not staff of the
    business → `404`; staff of another branch, or a barber booking a colleague → `403`
    (ADR-0015's membership-first rule, as in ADR-0025).
  - **No account, a name:** `customer_id` is empty and `customer_name` is required (trimmed,
    1–100 characters). The database holds the same rule: an appointment has a customer, or is a
    staff booking with a name (migration 00020).
  - **The shop's rules for the start:**
    - any whole minute, not just the slot grid;
    - from 15 minutes ago (`WalkInGrace`: the customer may already be in the chair);
    - up to the end of the booking horizon;
    - no lead time and no limit on bookings.
  - **What still applies:** the branch is published (`409 branch_not_bookable` if not), the
    barber works there and offers every service, the time fits inside their working hours and
    is free. The exclusion constraint has the last word, as for customers (ADR-0024).
  - **Always confirmed:** the shop booked it itself, so there is nothing to answer.
  - **A named barber:** there is no "any barber" for walk-ins; the shop knows who is free.
- **An idempotency key belongs to whoever asked.** The key table's `customer_id` becomes
  `requester_id`: the customer for app bookings, the staff member for walk-ins. A retry finds the
  appointment by ID, because a walk-in has no customer to find it by. Customer and staff requests
  hash differently (`staff|…`), so one person's key can't replay the other kind of request.
- **Scheduled tasks in the outbox:** `Bus.Every(name, interval, task)` registers a River periodic
  job (kind `scheduled_task`, with the task's name).
  - It runs when a worker starts, then every interval.
  - It is scheduled at most once per interval even with several workers (River's unique jobs,
    by name and period). A run that outlasts its interval can overlap the next, so a task must
    be safe to run twice at once; expiry is, thanks to `SKIP LOCKED`.
  - It gets one attempt: a failed run isn't retried, because the next run does the same work.
  - A task this release doesn't know (during a rolling deploy) is skipped with a warning.
- **Expiry:** `booking.expire_pending` runs every minute.
  - In batches of 100, each batch one transaction: pending bookings with
    `pending_until <= now`, oldest first, `FOR UPDATE SKIP LOCKED`.
  - The domain's `Expire(now)` decides for each one, and `booking.appointment_expired` is
    published in the same transaction.
  - It loops while a batch comes back full.
  - A partial index on `pending_until` (pending rows only) keeps the minutely query cheap.
  - `SKIP LOCKED`: a booking the shop is confirming right now is skipped, not waited for. If it
    is still pending afterwards, the next run expires it.

## Consequences
- Walk-in customers get no notifications: they have no account or device. Their name is visible
  only to the shop.
- The barber's time comes free up to a minute after a booking expires (longer if no worker is
  running). Reads already show it as expired (ADR-0025).
- Rolling back migration 00020 deletes walk-ins, which the old schema can't hold.
- Other modules can use `Bus.Every` for their own scheduled work (reminders in M7).
