# ADR-0025: Appointment lifecycle — the domain decides, one change at a time, the deadline kept with the booking

- Status: Accepted · Date: 2026-10-01 · Builds on [ADR-0015](0015-business-tenancy-and-authorization.md) and [ADR-0024](0024-booking-an-appointment.md)

## Context
M5.4 lets the customer cancel a booking, and lets the shop confirm, reject, cancel, complete or
mark a no-show (the diagram in domain-model.md §3.5). Four questions:

- who may do what;
- what happens when two people act at once (the shop confirms as the customer cancels);
- which cancellation window applies after the branch changes it;
- how the shop finds the appointments to act on.

## Decision
- **The aggregate owns the rules.** `Confirm`, `Reject`, `CancelByCustomer`, `CancelByStaff`,
  `Complete` and `MarkNoShow` each check the current status and time, then either change the
  appointment or return why not:
  - `*TransitionError` → `409 invalid_transition`, naming what the appointment is now;
  - `ErrTooLateToCancel` → `409 cancellation_window_passed`;
  - `ErrNotStarted` → `409 appointment_not_started`.

  A pending booking past its `pending_until` counts as expired, even before the expiry job (M5.5)
  writes it down. Every change bumps the version and records a `StatusChanged` event. A test runs
  every action on every status at four kinds of moment, and checks that a change only ever follows
  an edge of the diagram.
- **Customers cancel within the deadline they booked under.**
  - **Pending:** until it starts — the shop hasn't taken it on.
  - **Confirmed:** until `cancellable_until`, which is the start minus the branch's cancellation
    window, stored when the appointment is booked (migration 00019).
  - A later change to the window doesn't move existing bookings, like a price change doesn't (ADR-0024).
  - The app shows the deadline, so the customer knows it when booking.
- **The shop:**
  - **Confirm / reject:** a pending booking.
  - **Cancel:** a pending or confirmed one, at any time, with an optional reason (≤ 300 characters).
  - **Complete / no-show:** a confirmed one, once it has started.
- **Who may act** follows business's membership-first rule (ADR-0015):
  - not active staff of the business → `404`, whatever the appointment ID;
  - another business's appointment → `404`;
  - staff of another branch → `403`;
  - a barber acting on a colleague's appointment → `403`.

  booking learns the caller's role and branches from `business.MemberOf`, through its ACL.
- **One change at a time.** The repository loads the appointment `FOR UPDATE`, lets the use case
  apply the change, saves it and publishes its event in the same transaction. Two people acting at
  once take turns: the second sees what the first did, and the domain refuses an action that no
  longer applies. The update also checks the version, a second guard behind the lock.
- **Events:** `booking.appointment_{confirmed,rejected,cancelled,completed,no_show}` share one
  payload (`AppointmentStatusChanged`) with the status before and after, and for a cancellation who
  cancelled and why. Notifications use them (M7).
- **The shop's day.** `GET …/branches/{id}/appointments?date=` lists the appointments starting
  that day in the branch's time zone, in every status. A barber sees their own; the owner and the
  branch's managers see everyone's. It shows the customer's ID only: there is no customer name yet
  (iam has no endpoint that sets one), and booking doesn't depend on iam.

## Consequences
- Cancelling frees the barber's time at once: the exclusion constraint only counts pending and
  confirmed appointments (ADR-0008).
- A booking made inside the window can't be cancelled by the customer once it is confirmed. The
  shop still can.
- Rejection takes no reason in v1. The column for one is the cancellation's.
- Staff bookings (walk-ins) and the pending-expiry job are M5.5.
